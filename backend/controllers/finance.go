package controllers

import (
	"fmt"
	"strconv"
	"time"

	"edu-train/database"
	"edu-train/models"
	"edu-train/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// validPaymentMethods 支持的收款方式
var validPaymentMethods = map[string]bool{
	"cash":   true,
	"wechat": true,
	"alipay": true,
	"bank":   true,
}

// LowBalanceThreshold 剩余课时不超过该值视为待续费（小时）
const LowBalanceThreshold = 5

func GetPayments(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	studentID := c.Query("student_id")
	paymentMethod := c.Query("payment_method")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	typeParam := c.Query("type")

	offset := (page - 1) * pageSize

	query := database.DB.Model(&models.Payment{}).Preload("Student").Preload("Course")

	if studentID != "" {
		query = query.Where("student_id = ?", studentID)
	}

	if paymentMethod != "" {
		query = query.Where("payment_method = ?", paymentMethod)
	}

	if startDate != "" {
		query = query.Where("payment_date >= ?", startDate)
	}

	if endDate != "" {
		query = query.Where("payment_date <= ?", endDate)
	}

	if typeParam != "" {
		query = query.Where("type = ?", typeParam)
	}

	var total int64
	query.Count(&total)

	var payments []models.Payment
	if err := query.Order("payment_date DESC, created_at DESC").Offset(offset).Limit(pageSize).Find(&payments).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, gin.H{
		"list":      payments,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func GetPayment(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var payment models.Payment
	if err := database.DB.Preload("Student").Preload("Course").First(&payment, id).Error; err != nil {
		utils.NotFound(c, "缴费记录不存在")
		return
	}

	utils.Success(c, payment)
}

// CreatePayment 新增缴费记录（原有功能，保留可用）。
// 仅生成缴费记录，不改动课时账户总课时；课时增加统一走续费接口 RenewPayment。
func CreatePayment(c *gin.Context) {
	var payment models.Payment
	if err := c.ShouldBindJSON(&payment); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	if payment.Amount <= 0 {
		utils.BadRequest(c, "金额必须大于0")
		return
	}

	if !validPaymentMethods[payment.PaymentMethod] {
		utils.BadRequest(c, "收款方式无效")
		return
	}

	if payment.PaymentDate == "" {
		utils.BadRequest(c, "缴费日期不能为空")
		return
	}

	if payment.Type == "" {
		payment.Type = "tuition"
	}

	payment.ReceiptNo = generateReceiptNo()
	payment.Status = "paid"

	// 校验学员是否存在，避免产生无主缴费记录
	var student models.Student
	if err := database.DB.First(&student, payment.StudentID).Error; err != nil {
		utils.BadRequest(c, "学员不存在")
		return
	}

	if payment.CourseID != nil {
		var course models.Course
		if err := database.DB.First(&course, *payment.CourseID).Error; err != nil {
			utils.BadRequest(c, "课程不存在")
			return
		}
	}

	tx := database.DB.Begin()
	if tx.Error != nil {
		utils.InternalServerError(c, "开启事务失败")
		return
	}

	if err := tx.Create(&payment).Error; err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "创建缴费记录失败")
		return
	}

	if err := tx.Commit().Error; err != nil {
		utils.InternalServerError(c, "保存失败")
		return
	}

	utils.Success(c, payment)
}

// RenewPayment 学员续费：填写小时数与收款方式，系统按课程单价计算金额生成学费记录，
// 同时增加该学员该课程课时账户的总课时。缴费记录与课时账户在同一事务内提交，
// 任何一步失败都会整体回滚，不会只改一边。
func RenewPayment(c *gin.Context) {
	var req struct {
		StudentID     uint   `json:"student_id" binding:"required"`
		CourseID      uint   `json:"course_id" binding:"required"`
		Hours         int    `json:"hours" binding:"required"`
		PaymentMethod string `json:"payment_method" binding:"required"`
		PaymentDate   string `json:"payment_date"`
		Remarks       string `json:"remarks"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "参数错误：请填写学员、课程、续费小时数和收款方式")
		return
	}

	if req.Hours <= 0 {
		utils.BadRequest(c, "续费小时数必须大于0")
		return
	}

	if !validPaymentMethods[req.PaymentMethod] {
		utils.BadRequest(c, "收款方式无效")
		return
	}

	paymentDate := req.PaymentDate
	if paymentDate == "" {
		paymentDate = time.Now().Format("2006-01-02")
	}

	var student models.Student
	if err := database.DB.First(&student, req.StudentID).Error; err != nil {
		utils.BadRequest(c, "学员不存在")
		return
	}

	var course models.Course
	if err := database.DB.First(&course, req.CourseID).Error; err != nil {
		utils.BadRequest(c, "课程不存在")
		return
	}

	if course.Status != 1 {
		utils.BadRequest(c, "该课程已停用，无法续费")
		return
	}

	amount := course.PricePerHour * float64(req.Hours)

	tx := database.DB.Begin()
	if tx.Error != nil {
		utils.InternalServerError(c, "开启事务失败")
		return
	}

	// 缴费记录与课时账户任一失败都整体回滚
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// 锁定课时账户行，避免并发续费/扣课造成课时不一致
	var studentCourse models.StudentCourse
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("student_id = ? AND course_id = ?", req.StudentID, req.CourseID).
		First(&studentCourse).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			now := time.Now()
			studentCourse = models.StudentCourse{
				StudentID:  req.StudentID,
				CourseID:   req.CourseID,
				TotalHours: 0,
				UsedHours:  0,
				StartDate:  &now,
				Status:     1,
			}
			if err := tx.Create(&studentCourse).Error; err != nil {
				utils.InternalServerError(c, "创建课时账户失败，续费已取消")
				return
			}
		} else {
			utils.InternalServerError(c, "查询课时账户失败，续费已取消")
			return
		}
	}

	payment := models.Payment{
		StudentID:     req.StudentID,
		CourseID:      &req.CourseID,
		Amount:        amount,
		PaymentMethod: req.PaymentMethod,
		PaymentDate:   paymentDate,
		Type:          "tuition",
		Status:        "paid",
		ReceiptNo:     generateReceiptNo(),
		Remarks:       req.Remarks,
	}
	if err := tx.Create(&payment).Error; err != nil {
		utils.InternalServerError(c, "生成学费记录失败，续费已取消")
		return
	}

	// 增加账户总课时（已用课时不变，剩余随之增加）
	if err := tx.Model(&models.StudentCourse{}).
		Where("id = ?", studentCourse.ID).
		UpdateColumn("total_hours", gorm.Expr("total_hours + ?", req.Hours)).Error; err != nil {
		utils.InternalServerError(c, "增加账户课时失败，续费已取消")
		return
	}

	if err := tx.Commit().Error; err != nil {
		utils.InternalServerError(c, "保存失败，缴费与课时均未生效")
		return
	}
	committed = true

	utils.Success(c, gin.H{
		"payment":         payment,
		"amount":          amount,
		"added_hours":     req.Hours,
		"total_hours":     studentCourse.TotalHours + req.Hours,
		"remaining_hours": studentCourse.TotalHours - studentCourse.UsedHours + req.Hours,
	})
}

func UpdatePayment(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var payment models.Payment
	if err := database.DB.First(&payment, id).Error; err != nil {
		utils.NotFound(c, "缴费记录不存在")
		return
	}

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	// 编辑缴费记录不允许直接改课时账户；金额与收款方式做合法性校验
	if amountVal, ok := updates["amount"]; ok {
		amount, _ := amountVal.(float64)
		if amount <= 0 {
			utils.BadRequest(c, "金额必须大于0")
			return
		}
	}
	if methodVal, ok := updates["payment_method"]; ok {
		method, _ := methodVal.(string)
		if !validPaymentMethods[method] {
			utils.BadRequest(c, "收款方式无效")
			return
		}
	}

	if err := database.DB.Model(&payment).Updates(updates).Error; err != nil {
		utils.InternalServerError(c, "更新失败")
		return
	}

	utils.Success(c, payment)
}

func DeletePayment(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	if err := database.DB.Delete(&models.Payment{}, id).Error; err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	utils.Success(c, nil)
}

// GetStudentCourseAccounts 课时账户视图：列出学员、课程、总课时、已用、剩余，
// 剩余课时不超过 LowBalanceThreshold 小时标记为待续费。
func GetStudentCourseAccounts(c *gin.Context) {
	studentID := c.Query("student_id")
	courseID := c.Query("course_id")
	keyword := c.Query("keyword")
	needRenew := c.Query("need_renew")

	query := database.DB.Table("student_courses AS sc").
		Select(`sc.id AS id,
			sc.student_id AS student_id,
			s.name AS student_name,
			sc.course_id AS course_id,
			c.name AS course_name,
			c.price_per_hour AS price_per_hour,
			sc.total_hours AS total_hours,
			sc.used_hours AS used_hours,
			(sc.total_hours - sc.used_hours) AS remaining_hours,
			sc.status AS status`).
		Joins("JOIN students AS s ON s.id = sc.student_id AND s.deleted_at IS NULL").
		Joins("JOIN courses AS c ON c.id = sc.course_id AND c.deleted_at IS NULL").
		Where("sc.deleted_at IS NULL")

	if studentID != "" {
		query = query.Where("sc.student_id = ?", studentID)
	}
	if courseID != "" {
		query = query.Where("sc.course_id = ?", courseID)
	}
	if keyword != "" {
		query = query.Where("s.name LIKE ? OR s.phone LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if needRenew == "1" || needRenew == "true" {
		query = query.Where("(sc.total_hours - sc.used_hours) <= ?", LowBalanceThreshold)
	}

	var accounts []models.StudentCourseAccount
	if err := query.Order("remaining_hours ASC, sc.updated_at DESC").Scan(&accounts).Error; err != nil {
		utils.InternalServerError(c, "查询课时账户失败")
		return
	}

	for i := range accounts {
		accounts[i].NeedRenew = accounts[i].RemainingHours <= LowBalanceThreshold
	}

	utils.Success(c, gin.H{
		"list":  accounts,
		"total": len(accounts),
	})
}

func CreateRefund(c *gin.Context) {
	var refund models.Refund
	if err := c.ShouldBindJSON(&refund); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	refund.Status = "pending"

	if err := database.DB.Create(&refund).Error; err != nil {
		utils.InternalServerError(c, "创建退费申请失败")
		return
	}

	utils.Success(c, refund)
}

func GetRefunds(c *gin.Context) {
	var refunds []models.Refund
	if err := database.DB.Order("created_at DESC").Find(&refunds).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, refunds)
}

func ProcessRefund(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID, _ := c.Get("user_id")

	var req struct {
		Status string `json:"status" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	var refund models.Refund
	if err := database.DB.First(&refund, id).Error; err != nil {
		utils.NotFound(c, "退费申请不存在")
		return
	}

	refund.Status = req.Status
	if req.Status == "approved" {
		today := time.Now().Format("2006-01-02")
		refund.RefundDate = &today
	}
	processedBy := userID.(uint)
	refund.ProcessedBy = &processedBy

	if err := database.DB.Save(&refund).Error; err != nil {
		utils.InternalServerError(c, "处理退费失败")
		return
	}

	utils.Success(c, refund)
}

func GetFinanceReports(c *gin.Context) {
	reportType := c.DefaultQuery("type", "daily")
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	var results []map[string]interface{}
	var groupBy string

	switch reportType {
	case "daily":
		groupBy = "DATE(payment_date)"
	case "monthly":
		groupBy = "SUBSTRING(payment_date, 1, 7)"
	case "yearly":
		groupBy = "SUBSTRING(payment_date, 1, 4)"
	default:
		groupBy = "DATE(payment_date)"
	}

	query := database.DB.Model(&models.Payment{}).
		Select(fmt.Sprintf("%s as period, SUM(amount) as total_income, COUNT(*) as payment_count, payment_method", groupBy)).
		Where("status = ?", "paid")

	if startDate != "" {
		query = query.Where("payment_date >= ?", startDate)
	}
	if endDate != "" {
		query = query.Where("payment_date <= ?", endDate)
	}

	if err := query.Group(groupBy + ", payment_method").Order("period DESC").Find(&results).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, results)
}

func generateReceiptNo() string {
	return fmt.Sprintf("R%s%06d", time.Now().Format("20060102150405"), time.Now().UnixNano()%1000000)
}
