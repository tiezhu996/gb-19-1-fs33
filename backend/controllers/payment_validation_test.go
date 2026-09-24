package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edu-train/models"

	"github.com/gin-gonic/gin"
)

func callCreatePayment(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/payments", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	CreatePayment(c)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return resp
}

func TestCreatePaymentRejectsInvalidAmountAndMethod(t *testing.T) {
	db := setupTestDB(t)
	student := models.Student{Name: "学员A", Phone: "13900000001"}
	db.Create(&student)
	course := models.Course{Name: "课程A", Type: "small", PricePerHour: 100, TotalHours: 10, Status: 1}
	db.Create(&course)

	resp := callCreatePayment(t, map[string]any{
		"student_id": student.ID, "course_id": course.ID,
		"amount": 0, "payment_method": "cash", "payment_date": "2026-09-24",
	})
	if code, _ := resp["code"].(float64); code != 400 {
		t.Fatalf("amount=0 should be rejected, got %v", resp)
	}

	resp = callCreatePayment(t, map[string]any{
		"student_id": student.ID, "course_id": course.ID,
		"amount": 100, "payment_method": "cheque", "payment_date": "2026-09-24",
	})
	if code, _ := resp["code"].(float64); code != 400 {
		t.Fatalf("invalid method should be rejected, got %v", resp)
	}

	var n int64
	db.Model(&models.Payment{}).Count(&n)
	if n != 0 {
		t.Fatalf("no payment should be created, got %d", n)
	}
	db.Model(&models.StudentCourse{}).Count(&n)
	if n != 0 {
		t.Fatalf("no account should be created on rejected payment, got %d", n)
	}
}

func TestCreatePaymentStillWorks(t *testing.T) {
	db := setupTestDB(t)
	student := models.Student{Name: "学员B", Phone: "13900000002"}
	db.Create(&student)
	course := models.Course{Name: "课程B", Type: "small", PricePerHour: 100, TotalHours: 10, Status: 1}
	db.Create(&course)

	resp := callCreatePayment(t, map[string]any{
		"student_id": student.ID, "course_id": course.ID,
		"amount": 1000, "payment_method": "wechat", "payment_date": "2026-09-24",
		"type": "tuition",
	})
	if code, _ := resp["code"].(float64); code != 0 {
		t.Fatalf("valid payment should succeed, got %v", resp)
	}

	var account models.StudentCourse
	if err := db.Where("student_id = ? AND course_id = ?", student.ID, course.ID).First(&account).Error; err != nil {
		t.Fatalf("account should be auto-created: %v", err)
	}
	if account.TotalHours != 10 {
		t.Fatalf("account total hours = %d, want 10", account.TotalHours)
	}
}
