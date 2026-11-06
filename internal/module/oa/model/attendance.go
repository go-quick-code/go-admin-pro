// attendance.go 承载考勤基础五表的持久化模型（issue #60，docs/oa-mvp-spec.md
// §3.9/§4.10）：班次、考勤组、组员、打卡、考勤结果。
// 班次时间为 time-of-day（HH:MM 字符串，见 service/attendance_rule.go 的严格
// 校验），不支持跨天；结果表（oa_attendance_result）本工单只建 schema 与模型，
// 派生/校正写入由 #61 承接。
package model

import (
	"fmt"
	"time"
)

// BaseEntity 是统一实体底座（internal/pkg/model），别名声明见 model/workday.go，
// 列定义见 migrations/00022_oa_attendance.sql。

// 考勤启用状态（班次/考勤组共用，与 system 模块 0 启用/1 停用约定一致）。
const (
	AttendanceStatusEnabled  = int8(0)
	AttendanceStatusDisabled = int8(1)
)

// AttendanceStatusName 返回启用状态的中文展示名。
func AttendanceStatusName(status int8) string {
	switch status {
	case AttendanceStatusEnabled:
		return "启用"
	case AttendanceStatusDisabled:
		return "停用"
	default:
		return fmt.Sprintf("未知状态(%d)", status)
	}
}

// 打卡类型与来源（spec §3.9：0 上班/1 下班；source 0 Web）。
const (
	PunchTypeIn  = int8(0)
	PunchTypeOut = int8(1)

	PunchSourceWeb = int8(0)
)

// PunchTypeName 返回打卡类型的中文展示名。
func PunchTypeName(t int8) string {
	switch t {
	case PunchTypeIn:
		return "上班"
	case PunchTypeOut:
		return "下班"
	default:
		return fmt.Sprintf("未知类型(%d)", t)
	}
}

// PunchSourceName 返回打卡来源的中文展示名。
func PunchSourceName(s int8) string {
	switch s {
	case PunchSourceWeb:
		return "Web"
	default:
		return fmt.Sprintf("未知来源(%d)", s)
	}
}

// AttendanceShift 是班次（spec §3.9）：一天一个时段，end_time > start_time，
// 不支持跨天。start/end 为 time-of-day「HH:MM」（24 小时制定宽字符串），
// 不含日期成分；grace_minutes 为迟到/早退宽限分钟。
type AttendanceShift struct {
	BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	// StartTime 上班时间，HH:MM（time-of-day，不支持跨天）。
	StartTime string `gorm:"size:5;not null" json:"startTime"`
	// EndTime 下班时间，HH:MM；必须晚于 StartTime（同日内）。
	EndTime string `gorm:"size:5;not null" json:"endTime"`
	// GraceMinutes 迟到/早退宽限分钟数（0–1440）。
	GraceMinutes int  `gorm:"not null;default:0" json:"graceMinutes"`
	Status       int8 `gorm:"not null;default:0" json:"status"`
	Sort         int  `gorm:"not null;default:0" json:"sort"`
}

func (AttendanceShift) TableName() string { return "oa_attendance_shift" }

// AttendanceGroup 是考勤组（spec §3.9）：绑定一个班次；启停影响组员能否打卡
// （组停用时打卡返回 400）。
type AttendanceGroup struct {
	BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	// ShiftID 绑定班次 ID（oa_attendance_shift.id）。
	ShiftID int64 `gorm:"index;not null" json:"shiftId"`
	Status  int8  `gorm:"not null;default:0" json:"status"`
}

func (AttendanceGroup) TableName() string { return "oa_attendance_group" }

// AttendanceGroupUser 是组员关系（按用户物化）：唯一键 (tenant_id, user_id)，
// 一个用户只属于一个组。软删行同样占用唯一键——移出后再次入组经 upsert
// 复活改绑，不产生第二行（repository.UpsertGroupUsers）。
type AttendanceGroupUser struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	GroupID  int64 `gorm:"index;not null" json:"groupId"`
	UserID   int64 `gorm:"not null" json:"userId"`
}

func (AttendanceGroupUser) TableName() string { return "oa_attendance_group_user" }

// AttendancePunch 是打卡记录（spec §3.9/§4.10）：同一天允许多次，全部保留
// （不覆盖），#61 派生结果时取最早上班与最晚下班。PunchDate 按 app.timezone
// 业务时区取当天；Source v1 只有 Web。
type AttendancePunch struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	UserID   int64 `gorm:"not null" json:"userId"`
	// PunchDate 打卡日期（业务时区当天；MySQL DATE，service 归一化为当日零点入库）。
	PunchDate time.Time `gorm:"type:date;not null" json:"punchDate"`
	// PunchTime 打卡时刻。
	PunchTime time.Time `gorm:"type:datetime(3);not null" json:"punchTime"`
	// Type 打卡类型：0 上班 / 1 下班。
	Type int8 `gorm:"not null" json:"type"`
	// Source 打卡来源：0 Web。
	Source int8   `gorm:"not null;default:0" json:"source"`
	IP     string `gorm:"size:64;not null;default:''" json:"ip"`
	// UserAgent 打卡端 User-Agent（超长截断到 512）。
	UserAgent string `gorm:"size:512" json:"userAgent"`
}

func (AttendancePunch) TableName() string { return "oa_attendance_punch" }

// AttendanceResult 是考勤结果（spec §3.9/§4.10）——issue #60 只建 schema 与模型，
// 派生/校正写入由 #61 承接；此处建模只为锁定列语义（列注释与迁移一致）。
// 唯一键 (tenant_id, user_id, work_date)：一人一天一条结果。
// 派生优先级（#61 实现）：非工作日 → 休息；leave_days >= 1 → 请假；缺上班卡或缺
// 下班卡 → 缺卡；迟到且早退 → 迟到且早退；仅迟到 → 迟到；仅早退 → 早退；否则正常。
// leave_days=0.5（半天，对齐 #50 HalfAM/HalfPM）不改变状态，只记录半天请假。
// override_* 是管理员校正位：校正结果优先于派生结果，不覆盖原始打卡。
type AttendanceResult struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	UserID   int64 `gorm:"not null" json:"userId"`
	// WorkDate 考勤日期（MySQL DATE）。
	WorkDate time.Time `gorm:"type:date;not null" json:"workDate"`
	// ShiftID 当日适用班次 ID（无组/组未绑班次为 NULL）。
	ShiftID *int64 `gorm:"" json:"shiftId"`
	// InTime 最早上班打卡时刻（无上班卡为 NULL）。
	InTime *time.Time `gorm:"type:datetime(3)" json:"inTime"`
	// OutTime 最晚下班打卡时刻（无下班卡为 NULL）。
	OutTime *time.Time `gorm:"type:datetime(3)" json:"outTime"`
	// LateMinutes 迟到分钟数（超过宽限的部分）。
	LateMinutes int `gorm:"not null;default:0" json:"lateMinutes"`
	// EarlyMinutes 早退分钟数（超过宽限的部分）。
	EarlyMinutes int `gorm:"not null;default:0" json:"earlyMinutes"`
	// MissingIn 缺上班卡：0 否 / 1 是。
	MissingIn int8 `gorm:"not null;default:0" json:"missingIn"`
	// MissingOut 缺下班卡：0 否 / 1 是。
	MissingOut int8 `gorm:"not null;default:0" json:"missingOut"`
	// LeaveDays 请假天数（0.5=半天；>=1 整天请假）。
	LeaveDays float64 `gorm:"type:decimal(3,1);not null;default:0.0" json:"leaveDays"`
	// Status 派生结果状态（取值语义由 #61 定义）。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// OverrideStatus 管理员校正状态（NULL=未校正；校正优先于派生）。
	OverrideStatus *int8 `gorm:"" json:"overrideStatus"`
	// OverrideRemark 校正备注（1–200 字）。
	OverrideRemark string `gorm:"size:200" json:"overrideRemark"`
	// CorrectedBy 校正人用户 ID。
	CorrectedBy *int64 `gorm:"" json:"correctedBy"`
	// CorrectedAt 校正时间。
	CorrectedAt *time.Time `gorm:"type:datetime(3)" json:"correctedAt"`
	// ComputedAt 最近一次派生计算时间。
	ComputedAt *time.Time `gorm:"type:datetime(3)" json:"computedAt"`
}

func (AttendanceResult) TableName() string { return "oa_attendance_result" }
