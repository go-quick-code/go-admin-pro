package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock_doc.go 是杂项出入库单据两主两明（issue #90，docs/erp-mvp-spec.md §3.5）：
// 其它入库单（QR）/其它出库单（QC），同构镜像。审核状态口径对齐芋道字典
// erp_audit_status：10 未审核 / 20 已审核；只有审核动作落账（spec §4.1）——
// 已审核不可修改、不可删除，反审核经唯一入账入口新增 _CANCEL 反向流水回滚。
//
// 明细 product_price 为记录用单价（缺省 0.00），total_price = 单价 × 数量
//（Money.MulQty，服务端计算，客户端不采纳，spec §4.2 明细不变量）；主表不设
// total_price（spec §3.5 未列，杂项出入库金额纯记录、不参与结算）。
// 单号 no 由 erp_sequence 事务内原子发放（spec §4.8），客户端提交不采纳——
// DTO 上就没有单号字段。

// 单据审核状态（spec §4.1，对齐芋道 erp_audit_status 字典）。
const (
	// DocStatusUnaudited 未审核（未落账，可改可删）。
	DocStatusUnaudited int8 = 10
	// DocStatusApproved 已审核（已落账，不可改删，可反审核）。
	DocStatusApproved int8 = 20
)

// ValidDocStatus 报告单据审核状态枚举是否合法。
func ValidDocStatus(v int8) bool {
	return v == DocStatusUnaudited || v == DocStatusApproved
}

// 其它入库单类型（字典 erp_stock_in_type，取值对齐芋道数值风格；芋道 master
// 已将 ERP 杂项入库重构为 MES 无权威 SQL，枚举经终审确认，见 seed.go）。
const (
	StockInTypePurchase int8 = 10 // 采购
	StockInTypeReturn   int8 = 20 // 退货
	StockInTypeBorrow   int8 = 30 // 借入
	StockInTypeGiveBack int8 = 40 // 归还
	StockInTypeGift     int8 = 50 // 赠品
	StockInTypeOther    int8 = 99 // 其它
)

// 其它出库单类型（字典 erp_stock_out_type，同上）。
const (
	StockOutTypeLoss  int8 = 10 // 报损
	StockOutTypeUse   int8 = 20 // 领用
	StockOutTypeLend  int8 = 30 // 借出
	StockOutTypeOther int8 = 99 // 其它
)

// ValidStockInType 报告其它入库单类型枚举是否合法。
func ValidStockInType(v int8) bool {
	switch v {
	case StockInTypePurchase, StockInTypeReturn, StockInTypeBorrow,
		StockInTypeGiveBack, StockInTypeGift, StockInTypeOther:
		return true
	}
	return false
}

// ValidStockOutType 报告其它出库单类型枚举是否合法。
func ValidStockOutType(v int8) bool {
	switch v {
	case StockOutTypeLoss, StockOutTypeUse, StockOutTypeLend, StockOutTypeOther:
		return true
	}
	return false
}

// StockIn 是其它入库单主表。
type StockIn struct {
	model.BaseEntity
	DocBase `gorm:"embedded"`
	// No 是单号：QR + yyyyMMdd（业务时区）+ 至少 4 位序号，租户内唯一，
	// erp_sequence 事务内原子发放。
	No          string    `gorm:"size:32;not null" json:"no"`
	WarehouseID int64     `gorm:"not null" json:"warehouseId"`
	InTime      time.Time `gorm:"not null" json:"inTime"`
	Type        int8      `gorm:"not null;default:99" json:"type"`
	Remark      string    `gorm:"size:500" json:"remark"`

	// Items 是明细（Get/Create/Update 路径装载，列表分页不装载）。
	Items []StockInItem `gorm:"foreignKey:StockInID" json:"items,omitempty"`
}

func (StockIn) TableName() string { return "erp_stock_in" }

// GetNo/SetNo/GetBizTime/SetBizTime 实现 doc.Header（业务时间列 in_time）。
func (d *StockIn) GetNo() string          { return d.No }
func (d *StockIn) SetNo(v string)         { d.No = v }
func (d *StockIn) GetBizTime() time.Time  { return d.InTime }
func (d *StockIn) SetBizTime(v time.Time) { d.InTime = v }

// StockInItem 是其它入库单明细。
type StockInItem struct {
	model.BaseEntity
	TenantID  int64   `gorm:"index;not null;default:1" json:"tenantId"`
	StockInID int64   `gorm:"index;not null" json:"stockInId"`
	ProductID int64   `gorm:"not null" json:"productId"`
	Count     qty.Qty `gorm:"type:decimal(24,3);not null" json:"count"`
	// ProductPrice 记录用单价（缺省 0.00）。
	ProductPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	// TotalPrice 行金额 = 单价 × 数量（服务端计算）。
	TotalPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
}

func (StockInItem) TableName() string { return "erp_stock_in_item" }

// StockOut 是其它出库单主表（与 StockIn 镜像：out_time/type、QC 前缀）。
type StockOut struct {
	model.BaseEntity
	DocBase     `gorm:"embedded"`
	No          string    `gorm:"size:32;not null" json:"no"`
	WarehouseID int64     `gorm:"not null" json:"warehouseId"`
	OutTime     time.Time `gorm:"not null" json:"outTime"`
	Type        int8      `gorm:"not null;default:99" json:"type"`
	Remark      string    `gorm:"size:500" json:"remark"`

	Items []StockOutItem `gorm:"foreignKey:StockOutID" json:"items,omitempty"`
}

func (StockOut) TableName() string { return "erp_stock_out" }

// GetNo/SetNo/GetBizTime/SetBizTime 实现 doc.Header（业务时间列 out_time）。
func (d *StockOut) GetNo() string          { return d.No }
func (d *StockOut) SetNo(v string)         { d.No = v }
func (d *StockOut) GetBizTime() time.Time  { return d.OutTime }
func (d *StockOut) SetBizTime(v time.Time) { d.OutTime = v }

// StockOutItem 是其它出库单明细。
type StockOutItem struct {
	model.BaseEntity
	TenantID     int64       `gorm:"index;not null;default:1" json:"tenantId"`
	StockOutID   int64       `gorm:"index;not null" json:"stockOutId"`
	ProductID    int64       `gorm:"not null" json:"productId"`
	Count        qty.Qty     `gorm:"type:decimal(24,3);not null" json:"count"`
	ProductPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"productPrice"`
	TotalPrice   money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"totalPrice"`
}

func (StockOutItem) TableName() string { return "erp_stock_out_item" }
