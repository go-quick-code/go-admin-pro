package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock_move.go 是库存调拨单的数据访问（issue #93，docs/erp-mvp-spec.md
// §3.5/§4.6/§5）。主表 + 明细：明细随主表整体替换（编辑 = 软删旧明细 +
// 插入新明细，仅在未审核态允许）；审核/反审核的库存动作在 service 唯一入账
// 入口，不在本层。

// StockMoveFilter 是库存调拨单列表查询条件（筛选 + 数据范围谓词，导出复用）。
type StockMoveFilter struct {
	No              string
	FromWarehouseId int64
	ToWarehouseId   int64
	Status          *int8
	StartTime       *time.Time
	EndTime         *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// ListStockMoves 按筛选与数据范围谓词分页查询调拨单（固定 id DESC）。
func (r *Repository) ListStockMoves(ctx context.Context, f StockMoveFilter, offset, limit int) ([]model.StockMove, int64, error) {
	q := r.stockMoveQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.StockMove
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllStockMoves 按筛选与数据范围谓词查询全部调拨单（Excel 导出用）。
func (r *Repository) ListAllStockMoves(ctx context.Context, f StockMoveFilter) ([]model.StockMove, error) {
	var list []model.StockMove
	if err := r.stockMoveQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// stockMoveQuery 构造调拨单列表/导出的公共查询。
func (r *Repository) stockMoveQuery(ctx context.Context, f StockMoveFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.StockMove{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.FromWarehouseId > 0 {
		q = q.Where("from_warehouse_id = ?", f.FromWarehouseId)
	}
	if f.ToWarehouseId > 0 {
		q = q.Where("to_warehouse_id = ?", f.ToWarehouseId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("move_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("move_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CountStockMoveItemsByProduct 产品删除引用计数：调拨单明细引用该产品的行数
// （软删行不算引用，spec §4.9）。
func (r *Repository) CountStockMoveItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.StockMoveItem{}, productID)
}

// FindStockCountsIn 返回仓库内一批产品的当前账面余额（无余额行的产品不出现在
// 映射中，调用方按 0 处理）。盘点创建时的 base_count 快照用（只读、不加锁）。
func (r *Repository) FindStockCountsIn(ctx context.Context, warehouseID int64, productIDs []int64) (map[int64]qty.Qty, error) {
	counts := make(map[int64]qty.Qty, len(productIDs))
	if len(productIDs) == 0 {
		return counts, nil
	}
	var rows []struct {
		ProductID int64
		Count     qty.Qty
	}
	if err := r.db.WithContext(ctx).Model(&model.Stock{}).
		Select("product_id, `count`").
		Where("warehouse_id = ? AND product_id IN ?", warehouseID, productIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.ProductID] = row.Count
	}
	return counts, nil
}

// ListStockRecordsByBiz 查询某业务单据的全部流水（正向 + _CANCEL，append-only
// 全集，按 ID 升序）。盘点反审核按净额反向时使用：同一单据多轮审核/反审核后，
// 全部流水代数和 = 当前生效的调整量（spec §4.3/§4.6）。
func (r *Repository) ListStockRecordsByBiz(ctx context.Context, bizType string, bizID int64) ([]model.StockRecord, error) {
	var list []model.StockRecord
	if err := r.db.WithContext(ctx).Model(&model.StockRecord{}).
		Where("biz_type IN ?", []string{bizType, bizType + "_CANCEL"}).
		Where("biz_id = ?", bizID).
		Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
