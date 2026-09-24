package controllers

import (
	"errors"
	"math"
	"time"

	"edu-train/database"
	"edu-train/models"
	"edu-train/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// LowBalanceHours 剩余课时不超过该值时标记为待续费
const LowBalanceHours = 5

var validPaymentMethods = map[string]bool{
	"cash":   true,
	"wechat": true,
	"alipay": true,
	"bank":   true,
}

func isValidPaymentMethod(method string) bool {
	return validPaymentMethods[method]
}

// HourAccountView 课时账户视图
type HourAccountView struct {
	ID             uint    `json:"id"`
	StudentID      uint    `json:"student_id"`
	StudentName    string  `json:"student_name"`
	StudentPhone   string  `json:"student_phone"`
	CourseID       uint    `json:"course_id"`
	CourseName     string  `json:"course_name"`
	PricePerHour   float64 `json:"price_per_hour"`
	TotalHours     int     `json:"total_hours"`
	UsedHours      int     `json:"used_hours"`
	RemainingHours int     `json:"remaining_hours"`
	NeedRenew      bool    `json:"need_renew"`
	Status         int     `json:"status"`
	CreatedAt      string  `json:"created_at"`
}

func toHourAccountView(sc models.StudentCourse) HourAccountView {
	remaining := sc.TotalHours - sc.UsedHours
	view := HourAccountView{
		ID:             sc.ID,
		StudentID:      sc.StudentID,
		CourseID:       sc.CourseID,
		TotalHours:     sc.TotalHours,
		UsedHours:      sc.UsedHours,
		RemainingHours: remaining,
		NeedRenew:      remaining <= LowBalanceHours,
		Status:         sc.Status,
		CreatedAt:      sc.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if sc.Student != nil {
		view.StudentName = sc.Student.Name
		view.StudentPhone = sc.Student.Phone
	}
	if sc.Course != nil {
		view.CourseName = sc.Course.Name
		view.PricePerHour = sc.Course.PricePerHour
	}
	return view
}

// GetHourAccounts 课时账户列表：学员、课程、总课时、已用、剩余，低余额标记待续费
func GetHourAccounts(c *gin.Context) {
	studentID := c.Query("student_id")
	courseID := c.Query("course_id")
	keyword := c.Query("keyword")
	needRenew := c.Query("need_renew")

	query := database.DB.Model(&models.StudentCourse{}).
		Preload("Student").
		Preload("Course")

	if studentID != "" {
		query = query.Where("student_courses.student_id = ?", studentID)
	}
	if courseID != "" {
		query = query.Where("student_courses.course_id = ?", courseID)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Joins("LEFT JOIN students ON students.id = student_courses.student_id").
			Where("students.name LIKE ? OR students.phone LIKE ?", like, like)
	}
	if needRenew == "true" {
		query = query.Where("student_courses.total_hours - student_courses.used_hours <= ?", LowBalanceHours)
	}

	var accounts []models.StudentCourse
	if err := query.Order("(student_courses.total_hours - student_courses.used_hours) ASC, student_courses.updated_at DESC").
		Find(&accounts).Error; err != nil {
		utils.InternalServerError(c, "查询课时账户失败")
		return
	}

	list := make([]HourAccountView, 0, len(accounts))
	for _, sc := range accounts {
		list = append(list, toHourAccountView(sc))
	}

	utils.Success(c, gin.H{
		"list":  list,
		"total": len(list),
	})
}

// RenewHourAccount 续费：填写课时数和收款方式，按课程单价生成学费记录并增加账户总课时。
// 缴费记录与课时账户在同一事务内更新，任一环节失败则整体回滚，不会只改一边。
func RenewHourAccount(c *gin.Context) {
	var req struct {
		StudentID     uint   `json:"student_id" binding:"required"`
		CourseID      uint   `json:"course_id" binding:"required"`
		Hours         int    `json:"hours" binding:"required"`
		PaymentMethod string `json:"payment_method" binding:"required"`
		PaymentDate   string `json:"payment_date"`
		Remarks       string `json:"remarks"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	if req.Hours <= 0 {
		utils.BadRequest(c, "续费课时数必须大于0")
		return
	}

	if !isValidPaymentMethod(req.PaymentMethod) {
		utils.BadRequest(c, "收款方式无效")
		return
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
	if course.PricePerHour <= 0 {
		utils.BadRequest(c, "课程单价无效，无法计算学费")
		return
	}

	paymentDate := req.PaymentDate
	if paymentDate == "" {
		paymentDate = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", paymentDate); err != nil {
		utils.BadRequest(c, "缴费日期格式无效，应为 YYYY-MM-DD")
		return
	}

	// 金额由系统按课程单价计算，保留两位小数
	amount := math.Round(course.PricePerHour*float64(req.Hours)*100) / 100

	tx := database.DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 锁定该学员该课程的课时账户，避免并发续费导致课时丢失
	lockQuery := tx.Set("gorm:query_option", "FOR UPDATE")
	if database.DB.Dialector.Name() != "mysql" {
		lockQuery = tx
	}
	var account models.StudentCourse
	err := lockQuery.
		Where("student_id = ? AND course_id = ?", req.StudentID, req.CourseID).
		First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		account = models.StudentCourse{
			StudentID:  req.StudentID,
			CourseID:   req.CourseID,
			TotalHours: 0,
			UsedHours:  0,
			Status:     1,
		}
	} else if err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "查询课时账户失败")
		return
	}

	account.TotalHours += req.Hours
	if account.Status == 0 {
		account.Status = 1
	}
	if account.ID == 0 {
		if err := tx.Create(&account).Error; err != nil {
			tx.Rollback()
			utils.InternalServerError(c, "开通课时账户失败")
			return
		}
	} else {
		if err := tx.Save(&account).Error; err != nil {
			tx.Rollback()
			utils.InternalServerError(c, "更新课时账户失败")
			return
		}
	}

	courseID := req.CourseID
	payment := models.Payment{
		StudentID:     req.StudentID,
		CourseID:      &courseID,
		Amount:        amount,
		PaymentMethod: req.PaymentMethod,
		PaymentDate:   paymentDate,
		Type:          "tuition",
		Status:        "paid",
		ReceiptNo:     generateReceiptNo(),
		Remarks:       req.Remarks,
	}
	if err := tx.Create(&payment).Error; err != nil {
		tx.Rollback()
		utils.InternalServerError(c, "生成学费记录失败")
		return
	}

	if err := tx.Commit().Error; err != nil {
		utils.InternalServerError(c, "续费保存失败，缴费与课时均未生效")
		return
	}

	payment.Student = &student
	payment.Course = &course

	account.Student = &student
	account.Course = &course

	utils.Success(c, gin.H{
		"payment": payment,
		"account": toHourAccountView(account),
	})
}
