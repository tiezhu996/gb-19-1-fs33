package controllers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edu-train/database"
	"edu-train/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func setupMockDB(t *testing.T) (sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open gorm: %v", err)
	}
	oldDB := database.DB
	database.DB = gormDB
	return mock, func() {
		database.DB = oldDB
		_ = sqlDB.Close()
	}
}

func performJSONRequest(handler gin.HandlerFunc, body interface{}) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/payments/renew", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	handler(ctx)
	return w
}

type apiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// 续费成功：缴费记录与课时账户在同一事务中提交
func TestRenewPayment_Success_CommitsPaymentAndHours(t *testing.T) {
	mock, teardown := setupMockDB(t)
	defer teardown()

	mock.ExpectQuery("SELECT .* FROM `students`").
		WithArgs(uint(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(uint(1), "张三"))
	mock.ExpectQuery("SELECT .* FROM `courses`").
		WithArgs(uint(2)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "name", "price_per_hour", "total_hours", "status"}).
			AddRow(uint(2), "英语一对一", 200.0, 48, 1))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `student_courses` .*FOR UPDATE").
		WithArgs(uint(1), uint(2)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "student_id", "course_id", "total_hours", "used_hours", "status",
		}).AddRow(uint(5), uint(1), uint(2), 10, 8, 1))
	mock.ExpectExec("INSERT INTO `payments`").
		WillReturnResult(sqlmock.NewResult(10, 1))
	mock.ExpectExec("UPDATE `student_courses` SET `total_hours`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := performJSONRequest(RenewPayment, map[string]interface{}{
		"student_id":     1,
		"course_id":      2,
		"hours":          10,
		"payment_method": "wechat",
	})

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("expected success, got code=%d message=%s", resp.Code, resp.Message)
	}
	var data struct {
		Amount         float64 `json:"amount"`
		AddedHours     int     `json:"added_hours"`
		TotalHours     int     `json:"total_hours"`
		RemainingHours int     `json:"remaining_hours"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("invalid data: %v", err)
	}
	if data.Amount != 2000.0 {
		t.Errorf("amount = %v, want 2000.00 (200 * 10)", data.Amount)
	}
	if data.AddedHours != 10 || data.TotalHours != 20 || data.RemainingHours != 12 {
		t.Errorf("hours result = %+v, want added=10 total=20 remaining=12", data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

// 续费成功但学员还没有该课程账户：在事务内先建账户，再建缴费、再加课时
func TestRenewPayment_NewAccount_CreatedInTransaction(t *testing.T) {
	mock, teardown := setupMockDB(t)
	defer teardown()

	mock.ExpectQuery("SELECT .* FROM `students`").
		WithArgs(uint(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(uint(3), "李四"))
	mock.ExpectQuery("SELECT .* FROM `courses`").
		WithArgs(uint(4)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "name", "price_per_hour", "total_hours", "status"}).
			AddRow(uint(4), "数学小班课", 120.0, 48, 1))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `student_courses` .*FOR UPDATE").
		WithArgs(uint(3), uint(4)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "student_id", "course_id", "total_hours", "used_hours", "status",
		}))
	mock.ExpectExec("INSERT INTO `student_courses`").
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectExec("INSERT INTO `payments`").
		WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectExec("UPDATE `student_courses` SET `total_hours`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := performJSONRequest(RenewPayment, map[string]interface{}{
		"student_id":     3,
		"course_id":      4,
		"hours":          5,
		"payment_method": "cash",
	})

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected success, got %s", resp.Message)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

// 保存失败（缴费记录插入报错）：必须回滚，课时账户也不能被改动
func TestRenewPayment_PaymentInsertFails_EntireTransactionRolledBack(t *testing.T) {
	mock, teardown := setupMockDB(t)
	defer teardown()

	mock.ExpectQuery("SELECT .* FROM `students`").
		WithArgs(uint(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(uint(1), "张三"))
	mock.ExpectQuery("SELECT .* FROM `courses`").
		WithArgs(uint(2)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "name", "price_per_hour", "total_hours", "status"}).
			AddRow(uint(2), "英语一对一", 200.0, 48, 1))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `student_courses` .*FOR UPDATE").
		WithArgs(uint(1), uint(2)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "student_id", "course_id", "total_hours", "used_hours", "status",
		}).AddRow(uint(5), uint(1), uint(2), 10, 8, 1))
	mock.ExpectExec("INSERT INTO `payments`").
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	w := performJSONRequest(RenewPayment, map[string]interface{}{
		"student_id":     1,
		"course_id":      2,
		"hours":          10,
		"payment_method": "wechat",
	})

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if resp.Code != 500 {
		t.Errorf("expected code 500, got %d (%s)", resp.Code, resp.Message)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

// 无效小时数：直接拒绝，不触碰数据库
func TestRenewPayment_InvalidHours_Rejected(t *testing.T) {
	mock, teardown := setupMockDB(t)
	defer teardown()

	for _, hours := range []int{0, -3} {
		w := performJSONRequest(RenewPayment, map[string]interface{}{
			"student_id":     1,
			"course_id":      2,
			"hours":          hours,
			"payment_method": "wechat",
		})
		var resp apiResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Code != 400 {
			t.Errorf("hours=%d: expected code 400, got %d", hours, resp.Code)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

// 无效收款方式：直接拒绝，不触碰数据库
func TestRenewPayment_InvalidMethod_Rejected(t *testing.T) {
	mock, teardown := setupMockDB(t)
	defer teardown()

	w := performJSONRequest(RenewPayment, map[string]interface{}{
		"student_id":     1,
		"course_id":      2,
		"hours":          10,
		"payment_method": "bitcoin",
	})
	var resp apiResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != 400 {
		t.Errorf("expected code 400, got %d", resp.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

// 普通新增缴费：金额/收款方式无效时不产生任何写入
func TestCreatePayment_InvalidInput_Rejected(t *testing.T) {
	mock, teardown := setupMockDB(t)
	defer teardown()

	cases := []map[string]interface{}{
		{"student_id": 1, "amount": 0, "payment_method": "cash", "payment_date": "2026-09-24"},
		{"student_id": 1, "amount": -1, "payment_method": "cash", "payment_date": "2026-09-24"},
		{"student_id": 1, "amount": 100, "payment_method": "card", "payment_date": "2026-09-24"},
		{"student_id": 1, "amount": 100, "payment_method": "cash", "payment_date": ""},
	}
	for _, body := range cases {
		w := performJSONRequest(CreatePayment, body)
		var resp apiResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Code != 400 {
			t.Errorf("body=%v: expected code 400, got %d", body, resp.Code)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}

// 课时账户视图：剩余不超过 5 小时标记待续费
func TestGetStudentCourseAccounts_LowBalanceFlagged(t *testing.T) {
	mock, teardown := setupMockDB(t)
	defer teardown()

	mock.ExpectQuery("SELECT sc.id .* FROM student_courses AS sc .*JOIN students.*JOIN courses").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "student_id", "student_name", "course_id", "course_name",
			"price_per_hour", "total_hours", "used_hours", "remaining_hours", "status",
		}).
			AddRow(uint(1), uint(1), "张三", uint(2), "英语一对一", 200.0, 48, 43, 5, 1).
			AddRow(uint(2), uint(2), "李四", uint(3), "数学小班课", 120.0, 48, 40, 8, 1).
			AddRow(uint(3), uint(3), "王五", uint(2), "英语一对一", 200.0, 48, 50, -2, 1))

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/student-courses", nil)
	GetStudentCourseAccounts(ctx)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			List []models.StudentCourseAccount `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("expected success code, got %d", resp.Code)
	}
	if len(resp.Data.List) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(resp.Data.List))
	}
	if !resp.Data.List[0].NeedRenew {
		t.Error("remaining=5 should be flagged need_renew")
	}
	if resp.Data.List[1].NeedRenew {
		t.Error("remaining=8 should not be flagged need_renew")
	}
	if !resp.Data.List[2].NeedRenew {
		t.Error("negative remaining should be flagged need_renew")
	}
	if resp.Data.List[0].StudentName != "张三" || resp.Data.List[0].CourseName != "英语一对一" {
		t.Errorf("joined names wrong: %+v", resp.Data.List[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sql expectations: %v", err)
	}
}
