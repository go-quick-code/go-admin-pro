package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 合同状态枚举（docs/crm-mvp-spec.md §4.4 状态机）：
// 1 草稿 → 2 待审（提交）→ 3 已签（审批通过）；待审可驳回回草稿；
// 草稿/待审/已签均可作废（4），作废为终态。
const (
	ContractStatusDraft   int8 = 1 // 草稿
	ContractStatusPending int8 = 2 // 待审
	ContractStatusSigned  int8 = 3 // 已签
	ContractStatusVoid    int8 = 4 // 作废（终态）
)

// ValidContractStatus 报告合同状态枚举是否合法。
func ValidContractStatus(s int8) bool {
	return s >= ContractStatusDraft && s <= ContractStatusVoid
}

// IsTerminalContractStatus 报告状态是否终态（作废：进入后不再有任何流转）。
func IsTerminalContractStatus(s int8) bool {
	return s == ContractStatusVoid
}

// Contract 是合同（GLOSSARY：客户与己方签订的销售合同，是回款的依据；
// 可来自商机，也可直接创建）。
// owner-scoped 正体：owner_user_id/owner_dept_id 承载数据范围谓词（ADR 0004），
// 负责人必填（无公海形态），部门快照随负责人写入同步。
//
// 金额不变量（spec §4.3）：有行项目时 Amount 恒等于 SUM(行项目金额)，由服务端
// 按 数量 × 单价（money.Mul）计算后落库，客户端提交的金额忽略；无行项目时手填且 ≥ 0。
// 状态机唯一入口是 PUT /crm/contract/status（条件更新防并发，service 层校验合法性）。
type Contract struct {
	model.BaseEntity
	TenantID      int64       `gorm:"index;not null;default:1" json:"tenantId"`
	ContractNo    string      `gorm:"size:32;not null" json:"contractNo"`
	CustomerID    int64       `gorm:"not null;index" json:"customerId"`
	OpportunityID int64       `gorm:"index" json:"opportunityId"`
	Name          string      `gorm:"size:128;not null" json:"name"`
	Amount        money.Money `gorm:"type:decimal(12,2);not null;default:0" json:"amount"`
	SignDate      *time.Time  `gorm:"type:date" json:"signDate"`
	StartDate     *time.Time  `gorm:"type:date" json:"startDate"`
	EndDate       *time.Time  `gorm:"type:date" json:"endDate"`
	Status        int8        `gorm:"not null;default:1" json:"status"`
	// RejectReason 最近一次驳回原因：驳回回草稿时写入，重新提交审批时清空。
	RejectReason string `gorm:"size:500" json:"rejectReason"`
	OwnerUserID  int64  `gorm:"not null;index" json:"ownerUserId"`
	OwnerDeptID  int64  `gorm:"not null;index" json:"ownerDeptId"`
	Remark       string `gorm:"size:500" json:"remark"`

	// 非持久化字段：负责人昵称/部门名称/客户名称/来源商机名称由 service 层补齐供展示；
	// Products 与 TotalAmount 只在详情/创建返回时填充（指针 nil 表示未加载，
	// 列表行不携带行项目与合计，omitempty 生效不输出）。
	OwnerUserName   string            `gorm:"-" json:"ownerUserName,omitempty"`
	OwnerDeptName   string            `gorm:"-" json:"ownerDeptName,omitempty"`
	CustomerName    string            `gorm:"-" json:"customerName,omitempty"`
	OpportunityName string            `gorm:"-" json:"opportunityName,omitempty"`
	Products        []ContractProduct `gorm:"-" json:"products,omitempty"`
	TotalAmount     *money.Money      `gorm:"-" json:"totalAmount,omitempty"`
}

func (Contract) TableName() string { return "crm_contract" }

// ContractProduct 是合同产品行项目（关联表，ADR 0002：复合主键 + tenant_id，
// 不设软删除与审计列；变更随合同整体保存，spec §4.5 引用约束见 ProductRefCountChecker）。
type ContractProduct struct {
	ContractID int64       `gorm:"primaryKey" json:"contractId"`
	ProductID  int64       `gorm:"primaryKey" json:"productId"`
	TenantID   int64       `gorm:"not null;default:1" json:"tenantId"`
	Quantity   int64       `gorm:"not null" json:"quantity"`
	Price      money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	// Amount = Quantity × Price，服务端计算落库（客户端不提交金额）。
	Amount money.Money `gorm:"type:decimal(12,2);not null" json:"amount"`

	// 非持久化字段：产品名称，由 service 层批量补齐供展示。
	ProductName string `gorm:"-" json:"productName,omitempty"`
}

func (ContractProduct) TableName() string { return "crm_contract_product" }
