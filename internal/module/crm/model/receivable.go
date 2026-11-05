package model

import (
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/model"
	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// 回款计划派生状态枚举（docs/crm-mvp-spec.md §3：计划不存状态，列表用 SUM 派生）。
// 判定优先级（spec 与 issue #18）：已回款（已收 ≥ 计划额）→ 逾期（due_date < 今天）
// → 部分回款（已收 > 0）→ 待回款。逾期不覆盖已回款：钱已收齐的期次不再标红。
const (
	ReceivableStatusPending  int8 = 1 // 待回款
	ReceivableStatusPartial  int8 = 2 // 部分回款
	ReceivableStatusReceived int8 = 3 // 已回款
	ReceivableStatusOverdue  int8 = 4 // 逾期
)

// ValidReceivableStatus 报告回款计划派生状态枚举是否合法（筛选参数校验用）。
func ValidReceivableStatus(s int8) bool {
	return s >= ReceivableStatusPending && s <= ReceivableStatusOverdue
}

// ReceivablePlan 是回款计划（GLOSSARY：合同约定的一期应收款，含金额与到期日）。
// 不存状态与已收金额：列表查询用回款记录 SUM 子查询派生（ReceivedAmount 由仓储层
// 以 received_amount 别名扫描，Status/Overdue 由 service 按纯函数计算）。
// 数据权限不在本表上：可见性 join 合同后套用合同 owner_* 谓词（spec §4.1）。
type ReceivablePlan struct {
	model.BaseEntity
	TenantID   int64       `gorm:"index;not null;default:1" json:"tenantId"`
	ContractID int64       `gorm:"not null;index" json:"contractId"`
	PeriodNo   int         `gorm:"not null" json:"periodNo"`
	Amount     money.Money `gorm:"type:decimal(12,2);not null;default:0" json:"amount"`
	DueDate    time.Time   `gorm:"type:date;not null" json:"dueDate"`
	Remark     string      `gorm:"size:500" json:"remark"`

	// ReceivedAmount 已收金额：列表查询按 SUM(crm_receivable_record.amount) 别名
	// received_amount 扫描（只读列，创建/更新不写）。
	ReceivedAmount money.Money `gorm:"column:received_amount;->" json:"receivedAmount"`
	// Status/StatusName/Overdue 为派生展示字段，不落库（判定见 service.DeriveReceivableStatus）。
	Status     int8   `gorm:"-" json:"status"`
	StatusName string `gorm:"-" json:"statusName"`
	Overdue    bool   `gorm:"-" json:"overdue"`
	// 合同展示字段由 service 层批量补齐（列表不逐行查合同，避免 N+1）。
	ContractNo     string      `gorm:"-" json:"contractNo,omitempty"`
	ContractName   string      `gorm:"-" json:"contractName,omitempty"`
	ContractAmount money.Money `gorm:"-" json:"contractAmount"`
	CustomerName   string      `gorm:"-" json:"customerName,omitempty"`
	OwnerUserName  string      `gorm:"-" json:"ownerUserName,omitempty"`
}

func (ReceivablePlan) TableName() string { return "crm_receivable_plan" }

// ReceivableRecord 是回款记录（GLOSSARY：实际收到的一笔款，冲抵某个回款计划）。
// contract_id 由所属计划带出（冗余列：列表按合同筛选与数据权限 join 合同用）。
// 编号 HK+yyyyMMdd+至少 4 位序号，租户内唯一，由 crm_sequence 原子发放。
type ReceivableRecord struct {
	model.BaseEntity
	TenantID    int64       `gorm:"index;not null;default:1" json:"tenantId"`
	PlanID      int64       `gorm:"not null;index" json:"planId"`
	ContractID  int64       `gorm:"not null;index" json:"contractId"`
	RecordNo    string      `gorm:"size:32;not null" json:"recordNo"`
	Amount      money.Money `gorm:"type:decimal(12,2);not null;default:0" json:"amount"`
	ReceiptDate time.Time   `gorm:"type:date;not null" json:"receiptDate"`
	Method      string      `gorm:"size:32" json:"method"`
	Remark      string      `gorm:"size:500" json:"remark"`

	// 计划期次由列表查询以 plan_period_no 别名扫描（只读列），创建响应由 service
	// 按计划补齐；合同展示字段由 service 层批量补齐，不落库。
	PlanPeriodNo int    `gorm:"column:plan_period_no;->" json:"planPeriodNo,omitempty"`
	ContractNo   string `gorm:"-" json:"contractNo,omitempty"`
	ContractName string `gorm:"-" json:"contractName,omitempty"`
}

func (ReceivableRecord) TableName() string { return "crm_receivable_record" }
