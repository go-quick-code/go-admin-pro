package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// sale.go 是销售三单两主两明 + 订单（issue #92，docs/erp-mvp-spec.md §3.4）：
// 销售订单（XS）/ 销售出库（CK）/ 销售退货（ST），采购三单（#91）的镜像。
// 审核状态口径复用 #90 的 DocStatusUnaudited/DocStatusApproved（10/20，芋道
// erp_audit_status）；只有审核动作落账/回写进度（spec §4.1/§4.5）——出库审核
// 经唯一入账入口减库存，退货审核回库存，反审核生成 _CANCEL 反向流水回滚。
//
// 列名口径：订单编号列按 spec §3.4 字面为 order_no（对齐采购订单）；出库/退货
// 主表 spec 未列编号列名，沿 #90 单据的 no。receipt_price/refund_price 本票只
// 落列（DEFAULT 0），收付款核销回写在 #94（spec §4.7）。
// 明细 out_count/return_count 是进度回写列（只计已审核，审批/反审核重算）；
// total_price = 单价 × 数量（Money.MulQty 服务端权威，客户端不采纳）。
// 单号由 erp_sequence 事务内原子发放（spec §4.8），DTO 上无单号字段。

// SaleOrder 是销售订单主表。
type SaleOrder struct {
	model.BaseEntity
	DocBase `gorm:"embedded"`
	// OrderNo 是销售订单号：XS + yyyyMMdd（业务时区）+ 至少 4 位序号（spec §3.4
	// 字面列名 order_no），租户内唯一，erp_sequence 事务内原子发放。
	OrderNo    string `gorm:"size:32;not null" json:"orderNo"`
	CustomerID int64  `gorm:"index;not null" json:"customerId"`
	// SaleUserID 是销售员（纯记录字段，spec §3.4；0 = 未记录，不参与任何规则）。
	SaleUserID int64     `gorm:"not null;default:0" json:"saleUserId"`
	OrderTime  time.Time `gorm:"not null" json:"orderTime"`
	Remark     string    `gorm:"size:500" json:"remark"`

	// Items 是明细（Get/Create/Update 路径装载，列表分页不装载）。
	Items []SaleOrderItem `gorm:"foreignKey:SaleOrderID" json:"items,omitempty"`
}

func (SaleOrder) TableName() string { return "erp_sale_order" }

// GetNo/SetNo/GetBizTime/SetBizTime 实现 doc.Header（业务时间列 order_time）。
func (d *SaleOrder) GetNo() string          { return d.OrderNo }
func (d *SaleOrder) SetNo(v string)         { d.OrderNo = v }
func (d *SaleOrder) GetBizTime() time.Time  { return d.OrderTime }
func (d *SaleOrder) SetBizTime(v time.Time) { d.OrderTime = v }

// 销售链路的库存流水 biz_type（spec §4.3/§4.8：与单据表名一致 + _CANCEL 对，
// 命名延续 #90 的 StockRecordBiz* 风格；#91 采购同构追加）。
const (
	// StockRecordBizSaleOut 销售出库单（审核出账，负流水）。
	StockRecordBizSaleOut = "sale_out"
	// StockRecordBizSaleOutCancel 销售出库单反审核（反向流水，原流水保留）。
	StockRecordBizSaleOutCancel = "sale_out_CANCEL"
	// StockRecordBizSaleReturn 销售退货单（审核回库存，正流水）。
	StockRecordBizSaleReturn = "sale_return"
	// StockRecordBizSaleReturnCancel 销售退货单反审核（反向流水）。
	StockRecordBizSaleReturnCancel = "sale_return_CANCEL"
)

// SaleOrderItem 是销售订单明细（out_count/return_count 进度回写列见迁移注释）。
type SaleOrderItem struct {
	model.BaseEntity
	TenantID    int64   `gorm:"index;not null;default:1" json:"tenantId"`
	SaleOrderID int64   `gorm:"index;not null" json:"saleOrderId"`
	ProductID   int64   `gorm:"not null" json:"productId"`
	Count       qty.Qty `gorm:"type:decimal(24,3);not null" json:"count"`
	// ProductPrice 记录用销售单价（缺省 0.00）。
	ProductPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	// TotalPrice 行金额 = 单价 × 数量（服务端计算）。
	TotalPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
	// OutCount/ReturnCount 进度回写：只计已审核出库/退货（spec §4.5），审批/
	// 反审核重算；未审核单不动此列（占用校验实时聚合，不依赖此列）。
	OutCount    qty.Qty `gorm:"type:decimal(24,3);not null;default:0" json:"outCount"`
	ReturnCount qty.Qty `gorm:"type:decimal(24,3);not null;default:0" json:"returnCount"`
}

func (SaleOrderItem) TableName() string { return "erp_sale_order_items" }

// SaleOut 是销售出库单主表（必挂已审核销售订单，审核经唯一入账入口减库存）。
type SaleOut struct {
	model.BaseEntity
	DocBase    `gorm:"embedded"`
	No         string `gorm:"size:32;not null" json:"no"`
	CustomerID int64  `gorm:"index;not null" json:"customerId"`
	// OrderID 必填，必须挂已审核销售订单（spec §4.5）。
	OrderID     int64     `gorm:"index;not null" json:"orderId"`
	WarehouseID int64     `gorm:"index;not null" json:"warehouseId"`
	OutTime     time.Time `gorm:"not null" json:"outTime"`
	// TotalPrice 明细合计冗余（服务端计算，客户端不采纳）。
	TotalPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
	// ReceiptPrice 已收款回写（本票恒 0，核销回写在 #94）。
	ReceiptPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"receiptPrice"`

	Items []SaleOutItem `gorm:"foreignKey:SaleOutID" json:"items,omitempty"`
}

func (SaleOut) TableName() string { return "erp_sale_out" }

// GetNo/SetNo/GetBizTime/SetBizTime 实现 doc.Header（业务时间列 out_time）。
func (d *SaleOut) GetNo() string          { return d.No }
func (d *SaleOut) SetNo(v string)         { d.No = v }
func (d *SaleOut) GetBizTime() time.Time  { return d.OutTime }
func (d *SaleOut) SetBizTime(v time.Time) { d.OutTime = v }

// SaleOutItem 是销售出库单明细。
type SaleOutItem struct {
	model.BaseEntity
	TenantID     int64       `gorm:"index;not null;default:1" json:"tenantId"`
	SaleOutID    int64       `gorm:"index;not null" json:"saleOutId"`
	ProductID    int64       `gorm:"not null" json:"productId"`
	Count        qty.Qty     `gorm:"type:decimal(24,3);not null" json:"count"`
	ProductPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	TotalPrice   money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
}

func (SaleOutItem) TableName() string { return "erp_sale_out_items" }

// SaleReturn 是销售退货单主表（必挂已审核销售出库，审核经唯一入账入口回库存；
// 仓库取自挂单出库单，服务端派生）。
type SaleReturn struct {
	model.BaseEntity
	DocBase    `gorm:"embedded"`
	No         string `gorm:"size:32;not null" json:"no"`
	CustomerID int64  `gorm:"index;not null" json:"customerId"`
	// OutID 必填，必须挂已审核销售出库（spec §4.5）。
	OutID       int64     `gorm:"index;not null" json:"outId"`
	WarehouseID int64     `gorm:"index;not null" json:"warehouseId"`
	ReturnTime  time.Time `gorm:"not null" json:"returnTime"`
	// TotalPrice 明细合计冗余（服务端计算，客户端不采纳）。
	TotalPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
	// RefundPrice 已退款回写（本票恒 0，核销回写在 #94）。
	RefundPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"refundPrice"`

	Items []SaleReturnItem `gorm:"foreignKey:SaleReturnID" json:"items,omitempty"`
}

func (SaleReturn) TableName() string { return "erp_sale_return" }

// GetNo/SetNo/GetBizTime/SetBizTime 实现 doc.Header（业务时间列 return_time）。
func (d *SaleReturn) GetNo() string          { return d.No }
func (d *SaleReturn) SetNo(v string)         { d.No = v }
func (d *SaleReturn) GetBizTime() time.Time  { return d.ReturnTime }
func (d *SaleReturn) SetBizTime(v time.Time) { d.ReturnTime = v }

// SaleReturnItem 是销售退货单明细。
type SaleReturnItem struct {
	model.BaseEntity
	TenantID     int64       `gorm:"index;not null;default:1" json:"tenantId"`
	SaleReturnID int64       `gorm:"index;not null" json:"saleReturnId"`
	ProductID    int64       `gorm:"not null" json:"productId"`
	Count        qty.Qty     `gorm:"type:decimal(24,3);not null" json:"count"`
	ProductPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	TotalPrice   money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
}

func (SaleReturnItem) TableName() string { return "erp_sale_return_items" }
