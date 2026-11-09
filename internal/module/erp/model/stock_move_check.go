package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock_move_check.go 是库存调拨与盘点单据（issue #93，docs/erp-mvp-spec.md
// §3.5/§4.6/§4.8）：调拨两主两明（DB）/盘点两主两明（PD）。
//
// 审核语义沿用 00030 的两类杂项单据（spec §4.1）：status 10 未审核 / 20 已审核，
// 只有审核动作落账，已审核不可改删，反审核经唯一入账入口新增 _CANCEL 反向
// 流水（原流水保留）。
//
// 调拨：主表 from_warehouse_id/to_warehouse_id（审核时两仓必须不同，同仓 409）；
// 明细 count > 0。审核在同一事务内出仓取负、入仓取正，两笔流水同额成对。
//
// 盘点：主表 warehouse_id/check_time/status/remark；明细 actual_count（实际
// 清点数 ≥ 0）与 base_count（创建时账面数快照，仅记录、审核不参与计算——
// 审核以当时账面为准，spec §4.6）。
//
// 单号 no 由 erp_sequence 事务内原子发放（spec §4.8：DB/PD 前缀），客户端
// 提交不采纳（DTO 无单号字段）。

// StockMove 是库存调拨单主表。
type StockMove struct {
	model.BaseEntity
	DocBase `gorm:"embedded"`
	// No 是单号：DB + yyyyMMdd（业务时区）+ 至少 4 位序号，租户内唯一。
	No              string    `gorm:"size:32;not null" json:"no"`
	FromWarehouseID int64     `gorm:"not null" json:"fromWarehouseId"`
	ToWarehouseID   int64     `gorm:"not null" json:"toWarehouseId"`
	MoveTime        time.Time `gorm:"not null" json:"moveTime"`

	// Items 是明细（Get/Create/Update 路径装载，列表分页不装载）。
	Items []StockMoveItem `gorm:"foreignKey:StockMoveID" json:"items,omitempty"`
}

func (StockMove) TableName() string { return "erp_stock_move" }

// SetNo 实现 doc.Header（业务时间列 move_time）。
func (d *StockMove) SetNo(v string) { d.No = v }

// StockMoveItem 是库存调拨单明细。
type StockMoveItem struct {
	model.BaseEntity
	TenantID    int64   `gorm:"index;not null;default:1" json:"tenantId"`
	StockMoveID int64   `gorm:"index;not null" json:"stockMoveId"`
	ProductID   int64   `gorm:"not null" json:"productId"`
	Count       qty.Qty `gorm:"type:decimal(24,3);not null" json:"count"`
}

func (StockMoveItem) TableName() string { return "erp_stock_move_item" }

// StockCheck 是库存盘点单主表。
type StockCheck struct {
	model.BaseEntity
	DocBase `gorm:"embedded"`
	// No 是单号：PD + yyyyMMdd（业务时区）+ 至少 4 位序号，租户内唯一。
	No          string    `gorm:"size:32;not null" json:"no"`
	WarehouseID int64     `gorm:"not null" json:"warehouseId"`
	CheckTime   time.Time `gorm:"not null" json:"checkTime"`
	Remark      string    `gorm:"size:500" json:"remark"`

	// Items 是明细（Get/Create/Update 路径装载，列表分页不装载）。
	Items []StockCheckItem `gorm:"foreignKey:StockCheckID" json:"items,omitempty"`
}

func (StockCheck) TableName() string { return "erp_stock_check" }

// SetNo 实现 doc.Header（业务时间列 check_time）。
func (d *StockCheck) SetNo(v string) { d.No = v }

// StockCheckItem 是库存盘点单明细。
type StockCheckItem struct {
	model.BaseEntity
	TenantID     int64 `gorm:"index;not null;default:1" json:"tenantId"`
	StockCheckID int64 `gorm:"index;not null" json:"stockCheckId"`
	ProductID    int64 `gorm:"not null" json:"productId"`
	// ActualCount 实际清点数（≥ 0，qty 毫单位定点）。
	ActualCount qty.Qty `gorm:"type:decimal(24,3);not null" json:"actualCount"`
	// BaseCount 创建时账面数快照（仅记录；审核以当时账面为准，spec §4.6）。
	BaseCount qty.Qty `gorm:"type:decimal(24,3);not null;default:0" json:"baseCount"`
}

func (StockCheckItem) TableName() string { return "erp_stock_check_item" }
