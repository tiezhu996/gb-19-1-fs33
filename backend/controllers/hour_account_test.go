package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edu-train/database"
	"edu-train/models"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 每个用例使用独立的内存库（cache=shared 配合唯一 DSN），避免数据互相干扰
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Lead{},
		&models.FollowUp{},
		&models.Student{},
		&models.StudentCourse{},
		&models.Course{},
		&models.Classroom{},
		&models.Teacher{},
		&models.Schedule{},
		&models.Attendance{},
		&models.Payment{},
		&models.Refund{},
		&models.Performance{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db
	gin.SetMode(gin.TestMode)
	return db
}

func seedStudentCourse(t *testing.T, db *gorm.DB, used, total int) (models.Student, models.Course, models.StudentCourse) {
	t.Helper()
	student := models.Student{Name: "学员张三", Phone: "13800000001"}
	if err := db.Create(&student).Error; err != nil {
		t.Fatalf("create student: %v", err)
	}
	course := models.Course{Name: "英语一对一", Type: "one_on_one", PricePerHour: 200, TotalHours: 48, Status: 1}
	if err := db.Create(&course).Error; err != nil {
		t.Fatalf("create course: %v", err)
	}
	account := models.StudentCourse{StudentID: student.ID, CourseID: course.ID, TotalHours: total, UsedHours: used, Status: 1}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	return student, course, account
}

func performJSON(t *testing.T, handler gin.HandlerFunc, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/hour-accounts/renew", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func renewBody(studentID, courseID uint, hours int, method string) map[string]any {
	return map[string]any{
		"student_id":     studentID,
		"course_id":      courseID,
		"hours":          hours,
		"payment_method": method,
		"payment_date":   time.Now().Format("2006-01-02"),
	}
}

// 续费成功：按单价生成学费记录并增加账户总课时
func TestRenewHourAccountSuccess(t *testing.T) {
	db := setupTestDB(t)
	student, course, account := seedStudentCourse(t, db, 45, 48) // 剩余 3 小时

	_, resp := performJSON(t, RenewHourAccount, renewBody(student.ID, course.ID, 10, "wechat"))
	if code, _ := resp["code"].(float64); code != 0 {
		t.Fatalf("expected success, got %v", resp)
	}

	var updated models.StudentCourse
	if err := db.First(&updated, account.ID).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if updated.TotalHours != 58 || updated.UsedHours != 45 {
		t.Fatalf("account not updated correctly: %+v", updated)
	}

	var payment models.Payment
	if err := db.Where("student_id = ? AND course_id = ?", student.ID, course.ID).First(&payment).Error; err != nil {
		t.Fatalf("payment not created: %v", err)
	}
	if payment.Amount != 2000.0 {
		t.Fatalf("amount should be 200*10=2000, got %v", payment.Amount)
	}
	if payment.PaymentMethod != "wechat" || payment.Type != "tuition" || payment.Status != "paid" {
		t.Fatalf("payment fields wrong: %+v", payment)
	}
	if payment.ReceiptNo == "" {
		t.Fatal("receipt number should be generated")
	}
}

// 无效收款方式：缴费与课时账户都不能改
func TestRenewInvalidMethodRollsBack(t *testing.T) {
	db := setupTestDB(t)
	student, course, account := seedStudentCourse(t, db, 45, 48)

	_, resp := performJSON(t, RenewHourAccount, renewBody(student.ID, course.ID, 10, "bitcoin"))
	if code, _ := resp["code"].(float64); code != 400 {
		t.Fatalf("expected 400, got %v", resp)
	}

	var unchanged models.StudentCourse
	db.First(&unchanged, account.ID)
	if unchanged.TotalHours != 48 {
		t.Fatalf("account changed despite invalid method: %+v", unchanged)
	}
	var n int64
	db.Model(&models.Payment{}).Count(&n)
	if n != 0 {
		t.Fatalf("payment created despite invalid method: %d", n)
	}
}

// 无效课时数（0）：两边都不能改
func TestRenewInvalidHoursRollsBack(t *testing.T) {
	db := setupTestDB(t)
	student, course, account := seedStudentCourse(t, db, 0, 10)

	_, resp := performJSON(t, RenewHourAccount, renewBody(student.ID, course.ID, 0, "cash"))
	if code, _ := resp["code"].(float64); code != 400 {
		t.Fatalf("expected 400, got %v", resp)
	}

	var unchanged models.StudentCourse
	db.First(&unchanged, account.ID)
	if unchanged.TotalHours != 10 {
		t.Fatalf("account changed despite invalid hours: %+v", unchanged)
	}
	var n int64
	db.Model(&models.Payment{}).Count(&n)
	if n != 0 {
		t.Fatalf("payment created despite invalid hours: %d", n)
	}
}

// 无账户的学员课程续费时自动开通账户
func TestRenewCreatesAccountIfMissing(t *testing.T) {
	db := setupTestDB(t)
	student := models.Student{Name: "新学员", Phone: "13800000002"}
	db.Create(&student)
	course := models.Course{Name: "数学小班课", Type: "small", PricePerHour: 120, TotalHours: 48, Status: 1}
	db.Create(&course)

	_, resp := performJSON(t, RenewHourAccount, renewBody(student.ID, course.ID, 20, "alipay"))
	if code, _ := resp["code"].(float64); code != 0 {
		t.Fatalf("expected success, got %v", resp)
	}

	var account models.StudentCourse
	if err := db.Where("student_id = ? AND course_id = ?", student.ID, course.ID).First(&account).Error; err != nil {
		t.Fatalf("account not created: %v", err)
	}
	if account.TotalHours != 20 || account.UsedHours != 0 {
		t.Fatalf("new account wrong: %+v", account)
	}
}

// 剩余 <=5 标记待续费，>5 正常
func TestHourAccountsLowBalanceFlag(t *testing.T) {
	db := setupTestDB(t)
	student, course, _ := seedStudentCourse(t, db, 45, 48) // 剩余 3
	student2 := models.Student{Name: "学员李四", Phone: "13800000003"}
	db.Create(&student2)
	account2 := models.StudentCourse{StudentID: student2.ID, CourseID: course.ID, TotalHours: 48, UsedHours: 10, Status: 1} // 剩余 38
	db.Create(&account2)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/hour-accounts", nil)
	GetHourAccounts(c)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			List []HourAccountView `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != 0 || len(resp.Data.List) != 2 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}

	flags := map[uint]bool{}
	for _, a := range resp.Data.List {
		flags[a.StudentID] = a.NeedRenew
		if a.StudentID == student.ID {
			if a.StudentName != "学员张三" || a.CourseName != "英语一对一" {
				t.Fatalf("joined fields missing: %+v", a)
			}
			if a.RemainingHours != 3 {
				t.Fatalf("remaining should be 3, got %d", a.RemainingHours)
			}
		}
	}
	if !flags[student.ID] {
		t.Fatal("3-hour account should be flagged need_renew")
	}
	if flags[student2.ID] {
		t.Fatal("38-hour account should not be flagged")
	}

	// need_renew 过滤只返回低余额账户
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/hour-accounts?need_renew=true", nil)
	GetHourAccounts(c2)
	var filtered struct {
		Data struct {
			List []HourAccountView `json:"list"`
		} `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &filtered)
	if len(filtered.Data.List) != 1 || filtered.Data.List[0].StudentID != student.ID {
		t.Fatalf("need_renew filter wrong: %s", w2.Body.String())
	}
}

// 续费与考勤扣课共存：续费增加总课时，考勤只增已用课时
func TestRenewCoexistsWithAttendanceConsumption(t *testing.T) {
	db := setupTestDB(t)
	student, course, account := seedStudentCourse(t, db, 48, 48) // 剩余 0

	// 先续费 10 小时
	performJSON(t, RenewHourAccount, renewBody(student.ID, course.ID, 10, "bank"))
	var afterRenew models.StudentCourse
	db.First(&afterRenew, account.ID)
	if afterRenew.TotalHours != 58 || afterRenew.UsedHours != 48 {
		t.Fatalf("after renew: %+v", afterRenew)
	}

	// 考勤扣 2 小时（与 attendance.go 中相同的 GORM 表达式）
	if err := db.Model(&models.StudentCourse{}).
		Where("student_id = ? AND course_id = ?", student.ID, course.ID).
		UpdateColumn("used_hours", gorm.Expr("used_hours + ?", 2)).Error; err != nil {
		t.Fatalf("attendance deduction: %v", err)
	}

	var afterAttendance models.StudentCourse
	db.First(&afterAttendance, account.ID)
	if afterAttendance.TotalHours != 58 || afterAttendance.UsedHours != 50 {
		t.Fatalf("after attendance: %+v", afterAttendance)
	}
	if afterAttendance.TotalHours-afterAttendance.UsedHours != 8 {
		t.Fatal("remaining should be 8")
	}
}
