package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// purchase.go 是采购三单两主两明加一主一明（issue #91，docs/erp-mvp-spec.md
// §3.3）：采购订单（CG）/采购入库（RK）/采购退货（RT）。审核状态沿用 #90 的
// 单据口径（10 未审核 / 20 已审核，只有审核动作落账，spec §4.1）——已审核
// 不可修改、不可删除，反审核退回 10；入库/退货的库存变动只走唯一入账入口
// ApplyStockChange（spec §4.2），本文件只定义表结构。
//
// 明细 product_price 为单价，total_price = 单价 × 数量（Money.MulQty，服务端
// 计算，客户端不采纳，spec §4.2 明细不变量）；订单主表不设 total_price
//（spec §3.3 未列，与杂项出入库同构），入库/退货主表 total_price = 明细
// 合计冗余（spec §3.3 明列）。订单明细 in_count/return_count 是进度回写列
//（只计已审核单，审核/反审核重算，spec §4.4）；入库 payment_price / 退货
// refund_price 是核销回写列（本票只落列，核销在 #94）。
//
// 订单编号列按 spec §3.3 字面为 order_no（入库/退货沿 00030 单据的 no）；单号由
// erp_sequence 事务内原子发放（spec §4.8），客户端提交不采纳——DTO 上就没有单号字段。

// 采购单据的库存流水 biz_type（spec §4.3/§4.8：与单据表名一致 + _CANCEL 对，
// 对齐 #90 的 stock_in/stock_out 处置；反审核新增 _CANCEL 反向流水，原流水保留）。
const (
	StockRecordBizPurchaseIn           = "purchase_in"
	StockRecordBizPurchaseInCancel     = "purchase_in_CANCEL"
	StockRecordBizPurchaseReturn       = "purchase_return"
	StockRecordBizPurchaseReturnCancel = "purchase_return_CANCEL"
)

// PurchaseOrder 是采购订单主表。
type PurchaseOrder struct {
	model.BaseEntity
	TenantID int64 `gorm:"index;not null;default:1" json:"tenantId"`
	// OrderNo 是订单号：CG + yyyyMMdd（业务时区）+ 至少 4 位序号，租户内唯一
	//（spec §3.3 字面列名 order_no），erp_sequence 事务内原子发放。
	OrderNo    string    `gorm:"column:order_no;size:32;not null" json:"orderNo"`
	SupplierID int64     `gorm:"not null" json:"supplierId"`
	OrderTime  time.Time `gorm:"not null" json:"orderTime"`
	Status     int8      `gorm:"not null;default:10" json:"status"`
	Remark     string    `gorm:"size:500" json:"remark"`

	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`

	// Items 是明细（Get/Create/Update 路径装载，列表分页不装载）。
	Items []PurchaseOrderItem `gorm:"foreignKey:PurchaseOrderID" json:"items,omitempty"`
}

func (PurchaseOrder) TableName() string { return "erp_purchase_order" }

// PurchaseOrderItem 是采购订单明细。InCount/ReturnCount 为进度回写列
// （服务端重算，创建/编辑入参不采纳）。
type PurchaseOrderItem struct {
	model.BaseEntity
	TenantID        int64       `gorm:"index;not null;default:1" json:"tenantId"`
	PurchaseOrderID int64       `gorm:"index;not null" json:"purchaseOrderId"`
	ProductID       int64       `gorm:"not null" json:"productId"`
	Count           qty.Qty     `gorm:"type:decimal(24,3);not null" json:"count"`
	ProductPrice    money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	TotalPrice      money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
	// InCount/ReturnCount 已入库/已退货数量回写（只计已审核单，spec §4.4）。
	InCount     qty.Qty `gorm:"type:decimal(24,3);not null;default:0" json:"inCount"`
	ReturnCount qty.Qty `gorm:"type:decimal(24,3);not null;default:0" json:"returnCount"`
}

func (PurchaseOrderItem) TableName() string { return "erp_purchase_order_items" }

// PurchaseIn 是采购入库单主表。
type PurchaseIn struct {
	model.BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	No       string `gorm:"size:32;not null" json:"no"`
	// SupplierID 必须与挂单订单的供应商一致（服务端校验）。
	SupplierID int64 `gorm:"not null" json:"supplierId"`
	// OrderID 必填且必须是已审核采购订单（spec §4.4）。
	OrderID     int64     `gorm:"not null" json:"orderId"`
	WarehouseID int64     `gorm:"not null" json:"warehouseId"`
	InTime      time.Time `gorm:"not null" json:"inTime"`
	Status      int8      `gorm:"not null;default:10" json:"status"`
	// TotalPrice 单据金额 = 明细行金额合计（服务端计算冗余）。
	TotalPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
	// PaymentPrice 已核销付款回写（本票只落列，核销在 #94）。
	PaymentPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"paymentPrice"`
	Remark       string      `gorm:"size:500" json:"remark"`

	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`

	Items []PurchaseInItem `gorm:"foreignKey:PurchaseInID" json:"items,omitempty"`
}

func (PurchaseIn) TableName() string { return "erp_purchase_in" }

// PurchaseInItem 是采购入库单明细。
type PurchaseInItem struct {
	model.BaseEntity
	TenantID     int64       `gorm:"index;not null;default:1" json:"tenantId"`
	PurchaseInID int64       `gorm:"index;not null" json:"purchaseInId"`
	ProductID    int64       `gorm:"not null" json:"productId"`
	Count        qty.Qty     `gorm:"type:decimal(24,3);not null" json:"count"`
	ProductPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	TotalPrice   money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
}

func (PurchaseInItem) TableName() string { return "erp_purchase_in_items" }

// PurchaseReturn 是采购退货单主表。WarehouseID 取自入库单（服务端回填，
// 入参不收，spec §3.3）。
type PurchaseReturn struct {
	model.BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	No       string `gorm:"size:32;not null" json:"no"`
	// SupplierID 必须与挂单入库单的供应商一致（服务端校验）。
	SupplierID int64 `gorm:"not null" json:"supplierId"`
	// InID 必填且必须是已审核采购入库单（spec §4.4）。
	InID        int64     `gorm:"not null" json:"inId"`
	WarehouseID int64     `gorm:"not null" json:"warehouseId"`
	ReturnTime  time.Time `gorm:"not null" json:"returnTime"`
	Status      int8      `gorm:"not null;default:10" json:"status"`
	// TotalPrice 单据金额 = 明细行金额合计（服务端计算冗余）。
	TotalPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
	// RefundPrice 已核销退款回写（本票只落列，核销在 #94）。
	RefundPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"refundPrice"`
	Remark      string      `gorm:"size:500" json:"remark"`

	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`

	Items []PurchaseReturnItem `gorm:"foreignKey:PurchaseReturnID" json:"items,omitempty"`
}

func (PurchaseReturn) TableName() string { return "erp_purchase_return" }

// PurchaseReturnItem 是采购退货单明细。
type PurchaseReturnItem struct {
	model.BaseEntity
	TenantID         int64       `gorm:"index;not null;default:1" json:"tenantId"`
	PurchaseReturnID int64       `gorm:"index;not null" json:"purchaseReturnId"`
	ProductID        int64       `gorm:"not null" json:"productId"`
	Count            qty.Qty     `gorm:"type:decimal(24,3);not null" json:"count"`
	ProductPrice     money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	TotalPrice       money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
}

func (PurchaseReturnItem) TableName() string { return "erp_purchase_return_items" }
