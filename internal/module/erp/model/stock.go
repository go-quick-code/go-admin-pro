package model

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock.go 是库存账两表（issue #90，docs/erp-mvp-spec.md §3.2，ADR 0016 纯数量账）。
//
// erp_stock 只存数量余额，唯一入账入口在 service 层（spec §4.2：任何代码不得
// 直改 erp_stock；出库 UPDATE 带 count >= ? 条件做 SQL 双保险）。
// erp_stock_record 是 append-only 流水（不更新不删除，spec §4.3）：count 正入
// 负出、total_count 记变动后结存快照；biz_type 与单据表名一致 + _CANCEL 对
//（如 stock_in / stock_in_CANCEL），反审核新增反向流水、原流水保留，余额 =
// 初始 + 流水代数和可审计重放。

// Stock 是仓库×产品的库存余额行。
type Stock struct {
	model.BaseEntity
	TenantID    int64   `gorm:"index;not null;default:1" json:"tenantId"`
	ProductID   int64   `gorm:"not null" json:"productId"`
	WarehouseID int64   `gorm:"not null" json:"warehouseId"`
	Count       qty.Qty `gorm:"type:decimal(24,3);not null;default:0" json:"count"`
}

func (Stock) TableName() string { return "erp_stock" }

// 库存流水的业务类型（biz_type，spec §4.3/§4.8：与单据表名一致，各配 _CANCEL 对）。
// #91+ 扩采购/销售/调拨/盘点时在此追加常量。
const (
	// StockRecordBizStockIn 其它入库单（审核入账）。
	StockRecordBizStockIn = "stock_in"
	// StockRecordBizStockInCancel 其它入库单反审核（反向流水）。
	StockRecordBizStockInCancel = "stock_in_CANCEL"
	// StockRecordBizStockOut 其它出库单（审核出账）。
	StockRecordBizStockOut = "stock_out"
	// StockRecordBizStockOutCancel 其它出库单反审核（反向流水）。
	StockRecordBizStockOutCancel = "stock_out_CANCEL"
)

// StockRecord 是出入库流水（append-only：业务代码只插入，不更新不删除）。
type StockRecord struct {
	model.BaseEntity
	TenantID    int64 `gorm:"index;not null;default:1" json:"tenantId"`
	ProductID   int64 `gorm:"not null" json:"productId"`
	WarehouseID int64 `gorm:"not null" json:"warehouseId"`
	// Count 变动量：正入负出。
	Count qty.Qty `gorm:"type:decimal(24,3);not null" json:"count"`
	// TotalCount 变动后结存快照。
	TotalCount qty.Qty `gorm:"type:decimal(24,3);not null" json:"totalCount"`
	BizType    string  `gorm:"size:32;not null" json:"bizType"`
	BizID      int64   `gorm:"not null" json:"bizId"`
	BizItemID  int64   `gorm:"not null;default:0" json:"bizItemId"`
	BizNo      string  `gorm:"size:64;not null;default:''" json:"bizNo"`
}

func (StockRecord) TableName() string { return "erp_stock_record" }
