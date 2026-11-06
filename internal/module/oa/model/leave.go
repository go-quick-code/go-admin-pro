// leave.go 承载请假域的持久化模型（issue #51，docs/oa-mvp-spec.md §3.5/§4.8）：
// 类型、额度、申请三表。额度算术语义在纯函数（service/leave_rule.go），
// 半天/时长槽位语义复用 #50 的 workday_rule.go（HalfAM/HalfPM），本文件只是
// 存储形态。申请表本工单只建 schema（写入由 #52 承接），模型先行供
// 「被引用类型不可删」的行数判定（CountLeaveRequestsByType）与 #52 复用。
package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 请假类型状态（与系统通用状态语义一致：0 启用 / 1 停用）。
const (
	LeaveTypeStatusEnabled  = int8(0)
	LeaveTypeStatusDisabled = int8(1)
)

// 请假申请状态机（spec §4.8：草稿 → 待审 → 通过/驳回；待审可撤回；
// 通过后可在开始日期前取消）。
const (
	LeaveRequestStatusDraft     = int8(0) // 草稿
	LeaveRequestStatusPending   = int8(1) // 待审
	LeaveRequestStatusApproved  = int8(2) // 通过
	LeaveRequestStatusRejected  = int8(3) // 驳回
	LeaveRequestStatusWithdrawn = int8(4) // 撤回
	LeaveRequestStatusCancelled = int8(5) // 已取消
)

// LeaveType 是请假类型（spec §3.5）：租户内编码与名称唯一
// （唯一键 uk_oa_leave_type_tenant_code / uk_oa_leave_type_tenant_name）。
// 被申请引用（oa_leave_request 有行，任意状态）时只能停用不能删除（spec §4.14）；
// 软删行仍占用编码/名称唯一键（与 bpm 定义的 key 语义一致，编码不复用）。
type LeaveType struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// Name 类型名称（租户内唯一）。
	Name string `gorm:"size:50;not null" json:"name"`
	// Code 类型编码（租户内唯一，小写字母/数字/连字符；创建后不可变）。
	Code string `gorm:"size:50;not null" json:"code"`
	// BalanceManaged 是否占用额度：false 不占（事假/病假）/ true 占
	//（年假/调休；提交请假时检查可用额度，spec §4.8）。
	BalanceManaged bool `gorm:"not null" json:"balanceManaged"`
	// AnnualQuota 年度额度（天，一位小数支持半天；balance_managed=true 时
	// 额度初始化的默认值）。
	AnnualQuota money.Money `gorm:"type:decimal(5,1);not null;default:0" json:"annualQuota"`
	// Status 状态：0 启用 / 1 停用。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// Sort 显示顺序（小者在前）。
	Sort int `gorm:"not null;default:0" json:"sort"`
	// Remark 备注。
	Remark string `gorm:"size:200" json:"remark"`
}

func (LeaveType) TableName() string { return "oa_leave_type" }

// LeaveBalance 是请假额度（spec §3.5）：(租户, 用户, 类型, 自然年) 一行
// （唯一键 uk_oa_leave_balance_row）。可用额度 = quota + adjustment - used
// （纯函数 service.AvailableDays）。quota 是初始化时类型年度额度的快照，
// 之后不随类型定义变化；管理员调整累计在 adjustment，留痕在 adjust_reason
// 与 updater/updated_at（spec §4.8）。used 由 #52 维护（通过扣减、取消返还）。
// 额度行不做软删（记账事实），租户隔离由插件兜底。
type LeaveBalance struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// UserID 员工用户 ID。
	UserID int64 `gorm:"not null" json:"userId"`
	// LeaveTypeID 请假类型 ID（balance_managed=true 的类型）。
	LeaveTypeID int64 `gorm:"not null" json:"leaveTypeId"`
	// Year 自然年（如 2026）。
	Year int `gorm:"not null" json:"year"`
	// Quota 初始额度（天）。
	Quota money.Money `gorm:"type:decimal(5,1);not null;default:0" json:"quota"`
	// Adjustment 管理员手工调整累计（天，可负）。
	Adjustment money.Money `gorm:"type:decimal(5,1);not null;default:0" json:"adjustment"`
	// Used 已使用（天）。
	Used money.Money `gorm:"type:decimal(5,1);not null;default:0" json:"used"`
	// AdjustReason 最近一次手工调整的原因（留痕；操作人=updater、时间=updatedAt）。
	AdjustReason string `gorm:"size:200" json:"adjustReason"`
}

func (LeaveBalance) TableName() string { return "oa_leave_balance" }

// LeaveRequest 是请假申请（spec §3.5/§4.8）。本工单（#51）只建 schema 与
// 引用计数读取，全部写入路径（创建/提交/审批回调/撤回/取消）由 #52 承接；
// 字段预齐：
//   - 起止日期 + 起止半天（0 上午 / 1 下午，service.HalfAM/HalfPM）；
//   - duration_days 时长快照（服务端按工作日历槽位计算）；
//   - status 状态机（LeaveRequestStatus* 常量）、reject_reason 驳回原因；
//   - approval_instance_id 关联 bpm 实例（提交时写入，草稿为 NULL）。
//   - 附件不建列：infra_file（biz_type='oa_leave'、biz_id=申请 ID）。
type LeaveRequest struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// UserID 申请人。
	UserID int64 `gorm:"not null" json:"userId"`
	// LeaveTypeID 请假类型 ID。
	LeaveTypeID int64 `gorm:"not null" json:"leaveTypeId"`
	// StartDate/EndDate 起止日期（date 列）。
	StartDate time.Time `gorm:"type:date;not null" json:"startDate"`
	EndDate   time.Time `gorm:"type:date;not null" json:"endDate"`
	// StartHalf/EndHalf 起止半天：0 上午 / 1 下午（HalfAM/HalfPM）。
	StartHalf int8 `gorm:"not null" json:"startHalf"`
	EndHalf   int8 `gorm:"not null" json:"endHalf"`
	// DurationDays 时长快照（工作日天数，服务端计算）。
	DurationDays money.Money `gorm:"type:decimal(5,1);not null;default:0" json:"durationDays"`
	// Reason 请假事由。
	Reason string `gorm:"size:500;not null" json:"reason"`
	// Status 状态机（LeaveRequestStatusDraft…Cancelled）。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// RejectReason 驳回原因（bpm 回调写入）。
	RejectReason string `gorm:"size:200" json:"rejectReason"`
	// ApprovalInstanceID bpm 实例 ID（提交时写入；草稿为 nil）。
	ApprovalInstanceID *int64 `gorm:"column:approval_instance_id" json:"approvalInstanceId"`
}

func (LeaveRequest) TableName() string { return "oa_leave_request" }
