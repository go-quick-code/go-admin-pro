// expense.go 承载报销域的持久化模型（issue #54，docs/oa-mvp-spec.md
// §3.6/§4.9）：报销单（表头）与明细行两表。
//
// 本工单只承接草稿闭环：表头 total_amount 由明细行求和、服务端计算（请求里的
// 手填金额忽略），金额统一用 money.Money（int64 分 + decimal(12,2)），禁止
// 浮点。状态机与 approval_instance_id 列预齐，提交/审批回调（#55）在同一
// 状态机常量上继续（草稿 → 待审 → 通过/驳回；驳回回草稿可修改重提；
// 待审可撤回；通过即终态）。
//
// 附件不建列：infra_file（biz_type='oa_expense'、biz_id=报销单 ID）。
// 两表均登记 TenantTables（spec §4.6：跨租户业务 ID 一律 404）。
package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 报销单状态机（spec §4.9）：
//
//	0 草稿 → 1 待审 → 2 通过（终态）
//	                  ↘ 3 驳回（可修改重提）→ 1 待审
//	1 待审 → 4 撤回（本工单只做状态常量与守卫，迁移由 #55 的 bpm 回调完成）
const (
	ExpenseStatusDraft     = int8(0) // 草稿
	ExpenseStatusPending   = int8(1) // 待审
	ExpenseStatusApproved  = int8(2) // 通过（终态，不允许修改明细）
	ExpenseStatusRejected  = int8(3) // 驳回（回草稿可修改重提）
	ExpenseStatusWithdrawn = int8(4) // 撤回
)

// ExpenseClaim 是报销单表头（spec §3.6）：
//   - user_id 报销人；title 标题；reason 事由；
//   - total_amount 表头金额，由明细求和后由服务端写入（请求值忽略）；
//   - status 状态机（ExpenseStatus*）、reject_reason 驳回原因；
//   - approval_instance_id 关联 bpm 实例（提交时写入，草稿为 NULL，#55 承接）。
type ExpenseClaim struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// UserID 报销人（system_user，租户内）。
	UserID int64 `gorm:"not null" json:"userId"`
	// Title 报销标题。
	Title string `gorm:"size:200;not null" json:"title"`
	// TotalAmount 表头金额（= 明细合计，服务端计算；decimal(12,2) 分单位）。
	TotalAmount money.Money `gorm:"type:decimal(12,2);not null;default:0" json:"totalAmount"`
	// Reason 报销事由。
	Reason string `gorm:"size:500;not null" json:"reason"`
	// Status 状态机（ExpenseStatusDraft…Withdrawn）。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// RejectReason 驳回原因（bpm 回调写入，#55）。
	RejectReason string `gorm:"size:200" json:"rejectReason"`
	// ApprovalInstanceID bpm 实例 ID（提交时写入；草稿为 nil）。
	ApprovalInstanceID *int64 `gorm:"column:approval_instance_id" json:"approvalInstanceId"`
}

func (ExpenseClaim) TableName() string { return "oa_expense_claim" }

// ExpenseItem 是报销明细行（spec §3.6）：
//   - claim_id 所属报销单；category 费用类别（系统字典 oa_expense_category 的
//     启用值，纯函数 NormalizeExpenseCategory 校验）；
//   - amount 金额（必须 > 0，decimal(12,2) 分单位）；
//   - expense_date 发生日期（date 列）；remark 备注；sort 显示顺序（小者在前）。
//
// 明细的增删改排序通过草稿整体保存（POST/PUT /oa/expense/claim 携带全部行）
// 完成：服务端在事务内替换行集并重算表头合计，保证「表头 = 明细合计」原子一致。
type ExpenseItem struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// ClaimID 所属报销单 ID（oa_expense_claim）。
	ClaimID int64 `gorm:"not null" json:"claimId"`
	// Category 费用类别（oa_expense_category 字典值）。
	Category string `gorm:"size:50;not null" json:"category"`
	// Amount 金额（> 0；decimal(12,2)，分单位，禁止浮点）。
	Amount money.Money `gorm:"type:decimal(12,2);not null" json:"amount"`
	// ExpenseDate 发生日期（date 列，业务时区下的自然日）。
	ExpenseDate time.Time `gorm:"type:date;not null" json:"expenseDate"`
	// Remark 备注。
	Remark string `gorm:"size:200" json:"remark"`
	// Sort 显示顺序（小者在前；保存时按请求顺序归一化为 0..n-1）。
	Sort int `gorm:"not null;default:0" json:"sort"`
}

func (ExpenseItem) TableName() string { return "oa_expense_item" }
