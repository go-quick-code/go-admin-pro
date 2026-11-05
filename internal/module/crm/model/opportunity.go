package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// 商机阶段枚举（docs/crm-mvp-spec.md §3/§4.4）：1–4 为进行中，5 赢单、6 输单为终态。
const (
	OpportunityStageInitial   int8 = 1 // 初步
	OpportunityStageDemand    int8 = 2 // 需求
	OpportunityStageQuote     int8 = 3 // 报价
	OpportunityStageNegotiate int8 = 4 // 谈判
	OpportunityStageWon       int8 = 5 // 赢单（终态）
	OpportunityStageLost      int8 = 6 // 输单（终态）
)

// ValidOpportunityStage 报告阶段枚举是否合法。
func ValidOpportunityStage(s int8) bool {
	return s >= OpportunityStageInitial && s <= OpportunityStageLost
}

// IsTerminalOpportunityStage 报告阶段是否终态（赢单/输单，spec §4.4：进终态后不可再变更阶段）。
func IsTerminalOpportunityStage(s int8) bool {
	return s == OpportunityStageWon || s == OpportunityStageLost
}

// Opportunity 是商机（GLOSSARY：潜在的交易机会，勿称「订单」）。
// owner-scoped 正体：owner_user_id/owner_dept_id 承载数据范围谓词（ADR 0004），
// 负责人必填（无公海形态），部门快照随负责人写入同步。
type Opportunity struct {
	model.BaseEntity
	TenantID      int64  `gorm:"index;not null;default:1" json:"tenantId"`
	OpportunityNo string `gorm:"size:32;not null" json:"opportunityNo"`
	CustomerID    int64  `gorm:"not null;index" json:"customerId"`
	Name          string `gorm:"size:128;not null" json:"name"`
	Stage         int8   `gorm:"not null;default:1" json:"stage"`
	// ExpectedAmount 预计成交金额：销售手填的预估，与行项目合计无强制不变量
	//（规格 §4.3 的「合同金额 = SUM(行项目)」只约束合同，不约束商机）。
	ExpectedAmount    money.Money `gorm:"type:decimal(12,2);not null;default:0" json:"expectedAmount"`
	ExpectedCloseDate *time.Time  `gorm:"type:date" json:"expectedCloseDate"`
	OwnerUserID       int64       `gorm:"not null;index" json:"ownerUserId"`
	OwnerDeptID       int64       `gorm:"not null;index" json:"ownerDeptId"`
	Remark            string      `gorm:"size:500" json:"remark"`

	// 非持久化字段：负责人昵称/部门名称/客户名称由 service 层补齐供展示；
	// Products 与 TotalAmount 只在详情/创建返回时填充（指针 nil 表示未加载，
	// 列表行不携带行项目与合计，omitempty 生效不输出）。
	OwnerUserName string               `gorm:"-" json:"ownerUserName,omitempty"`
	OwnerDeptName string               `gorm:"-" json:"ownerDeptName,omitempty"`
	CustomerName  string               `gorm:"-" json:"customerName,omitempty"`
	Products      []OpportunityProduct `gorm:"-" json:"products,omitempty"`
	TotalAmount   *money.Money         `gorm:"-" json:"totalAmount,omitempty"`
}

func (Opportunity) TableName() string { return "crm_opportunity" }

// OpportunityProduct 是商机产品行项目（关联表，ADR 0002：复合主键 + tenant_id，
// 不设软删除与审计列；变更随商机整体保存，spec §4.5 引用约束见 ProductRefCountChecker）。
type OpportunityProduct struct {
	OpportunityID int64       `gorm:"primaryKey" json:"opportunityId"`
	ProductID     int64       `gorm:"primaryKey" json:"productId"`
	TenantID      int64       `gorm:"not null;default:1" json:"tenantId"`
	Quantity      int64       `gorm:"not null" json:"quantity"`
	Price         money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	// Amount = Quantity × Price，服务端计算落库（客户端不提交金额）。
	Amount money.Money `gorm:"type:decimal(12,2);not null" json:"amount"`

	// 非持久化字段：产品名称，由 service 层批量补齐供展示。
	ProductName string `gorm:"-" json:"productName,omitempty"`
}

func (OpportunityProduct) TableName() string { return "crm_opportunity_product" }
