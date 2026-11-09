package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// finance.go 是财务域的模型（issue #94，docs/erp-mvp-spec.md §3.6/§4.7）：
// 结算账户（erp_finance_account，无余额列）+ 收款单/付款单两主两明。
//
// 核销口径（spec §4.7）：
//   - 收款单明细 biz_type=sale_out（销售出库，收款）或 sale_return（销售退货，
//     退款）；付款单镜像为 purchase_in / purchase_return。biz_type 取单据表名
//     字符串（与 #90 库存流水 / #91/#92 序列 biz_type 同口径，spec §4.8
//     「biz_type 与表名一致」）。
//   - 核销金额只在审核时生效：审核回写被核销单据的 receipt_price /
//     refund_price / payment_price，反审核重算释放额度（service 层事务内完成）。
//   - 结算账户余额不落库：列表的余额列 = 该账户已审核收付款净额实时汇总。
//
// 单据状态沿用 #90 口径（10 未审核 / 20 已审核）；单号 SK/FK 由 erp_sequence
// 事务内原子发放，客户端提交不采纳（DTO 无单号字段）。主表不设 total_price
//（spec §3.6 未列；列表/详情金额由明细 price 汇总，服务端计算）。

// 结算账户的默认标记（default_status）：1 = 默认账户，租户内唯一互斥。
const (
	// FinanceDefaultNo 非默认账户。
	FinanceDefaultNo int8 = 0
	// FinanceDefaultYes 默认账户（置默认时取消原默认，spec §3.6/§4.7）。
	FinanceDefaultYes int8 = 1
)

// ValidFinanceDefaultStatus 报告默认标记枚举是否合法（0 否 / 1 是）。
func ValidFinanceDefaultStatus(v int8) bool {
	return v == FinanceDefaultNo || v == FinanceDefaultYes
}

// 核销明细的业务类型（spec §3.6：收款挂销售出库/销售退货，付款镜像）；
// 取单据表名字符串，与库存流水 biz_type 同口径。
const (
	FinanceBizSaleOut        = "sale_out"
	FinanceBizSaleReturn     = "sale_return"
	FinanceBizPurchaseIn     = "purchase_in"
	FinanceBizPurchaseReturn = "purchase_return"
)

// ValidFinanceReceiptBizType 报告收款单明细 biz_type 是否合法。
func ValidFinanceReceiptBizType(v string) bool {
	return v == FinanceBizSaleOut || v == FinanceBizSaleReturn
}

// ValidFinancePaymentBizType 报告付款单明细 biz_type 是否合法。
func ValidFinancePaymentBizType(v string) bool {
	return v == FinanceBizPurchaseIn || v == FinanceBizPurchaseReturn
}

// FinanceAccount 是结算账户（纯信息 + 默认唯一互斥；无余额列，spec §3.6）。
// status 沿用主数据 0 启用 / 1 停用；default_status 1 = 默认；owner 语义同
// 主数据（owner = 创建人，spec §4.10）。
type FinanceAccount struct {
	model.BaseEntity
	TenantID int64 `gorm:"index;not null;default:1" json:"tenantId"`
	// Name 账户名称；No 账户编号（展示用文本，客户端提交，不做自动编号）。
	Name string `gorm:"size:64;not null" json:"name"`
	No   string `gorm:"size:32" json:"no"`
	// Remark 备注；Sort 排序（0 合法，恒显式写入）；Status 0 启用 / 1 停用。
	Remark string `gorm:"size:500" json:"remark"`
	Sort   int    `gorm:"not null" json:"sort"`
	Status int8   `gorm:"not null" json:"status"`
	// DefaultStatus 是否默认账户（1 是 / 0 否），租户内唯一互斥（置默认取消原默认，
	// 迁移层有生成列唯一键双保险）。
	DefaultStatus int8 `gorm:"not null;default:0" json:"defaultStatus"`

	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`
}

func (FinanceAccount) TableName() string { return "erp_finance_account" }

// FinanceReceipt 是收款单主表（明细挂已审核销售出库/销售退货，spec §3.6）。
type FinanceReceipt struct {
	model.BaseEntity
	DocBase `gorm:"embedded"`
	// No 是收款单号：SK + yyyyMMdd（业务时区）+ 至少 4 位序号，租户内唯一。
	No         string `gorm:"size:32;not null" json:"no"`
	CustomerID int64  `gorm:"index;not null" json:"customerId"`
	AccountID  int64  `gorm:"index;not null" json:"accountId"`
	// ReceiptUserID 经手人（纯记录字段；> 0 时校验用户存在）。
	ReceiptUserID int64     `gorm:"not null;default:0" json:"receiptUserId"`
	ReceiptTime   time.Time `gorm:"not null" json:"receiptTime"`
	Remark        string    `gorm:"size:500" json:"remark"`

	Items []FinanceReceiptItem `gorm:"foreignKey:ReceiptID" json:"items,omitempty"`
}

func (FinanceReceipt) TableName() string { return "erp_finance_receipt" }

// GetNo/SetNo/GetBizTime/SetBizTime 实现 doc.Header（业务时间列 receipt_time）。
func (d *FinanceReceipt) GetNo() string          { return d.No }
func (d *FinanceReceipt) SetNo(v string)         { d.No = v }
func (d *FinanceReceipt) GetBizTime() time.Time  { return d.ReceiptTime }
func (d *FinanceReceipt) SetBizTime(v time.Time) { d.ReceiptTime = v }

// FinanceReceiptItem 是收款单明细：biz_type + biz_id 指向已审核销售出库/销售
// 退货，price 是本次核销金额（审核时校验上限并回写）。
type FinanceReceiptItem struct {
	model.BaseEntity
	TenantID  int64       `gorm:"index;not null;default:1" json:"tenantId"`
	ReceiptID int64       `gorm:"index;not null" json:"receiptId"`
	BizType   string      `gorm:"size:32;not null" json:"bizType"`
	BizID     int64       `gorm:"not null" json:"bizId"`
	Price     money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"price"`
}

func (FinanceReceiptItem) TableName() string { return "erp_finance_receipt_items" }

// FinancePayment 是付款单主表（明细挂已审核采购入库/采购退货，收款单镜像）。
type FinancePayment struct {
	model.BaseEntity
	DocBase `gorm:"embedded"`
	// No 是付款单号：FK + yyyyMMdd（业务时区）+ 至少 4 位序号，租户内唯一。
	No         string `gorm:"size:32;not null" json:"no"`
	SupplierID int64  `gorm:"index;not null" json:"supplierId"`
	AccountID  int64  `gorm:"index;not null" json:"accountId"`
	// PaymentUserID 经手人（纯记录字段；> 0 时校验用户存在）。
	PaymentUserID int64     `gorm:"not null;default:0" json:"paymentUserId"`
	PaymentTime   time.Time `gorm:"not null" json:"paymentTime"`
	Remark        string    `gorm:"size:500" json:"remark"`

	Items []FinancePaymentItem `gorm:"foreignKey:PaymentID" json:"items,omitempty"`
}

func (FinancePayment) TableName() string { return "erp_finance_payment" }

// GetNo/SetNo/GetBizTime/SetBizTime 实现 doc.Header（业务时间列 payment_time）。
func (d *FinancePayment) GetNo() string          { return d.No }
func (d *FinancePayment) SetNo(v string)         { d.No = v }
func (d *FinancePayment) GetBizTime() time.Time  { return d.PaymentTime }
func (d *FinancePayment) SetBizTime(v time.Time) { d.PaymentTime = v }

// FinancePaymentItem 是付款单明细：biz_type + biz_id 指向已审核采购入库/采购
// 退货，price 是本次核销金额。
type FinancePaymentItem struct {
	model.BaseEntity
	TenantID  int64       `gorm:"index;not null;default:1" json:"tenantId"`
	PaymentID int64       `gorm:"index;not null" json:"paymentId"`
	BizType   string      `gorm:"size:32;not null" json:"bizType"`
	BizID     int64       `gorm:"not null" json:"bizId"`
	Price     money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"price"`
}

func (FinancePaymentItem) TableName() string { return "erp_finance_payment_items" }
