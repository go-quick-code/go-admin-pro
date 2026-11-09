package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// stock_out.go 是其它出库单的读侧数据访问（issue #90；写侧已由泛型 doc store
// 承担，候选 02a）。与 stock_in.go 镜像（out_time/type，QC 前缀）。

// StockOutFilter 是其它出库单列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type StockOutFilter struct {
	No          string
	WarehouseId int64
	Type        *int8
	Status      *int8
	StartTime   *time.Time
	EndTime     *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// ListStockOuts 按筛选与数据范围谓词分页查询出库单（固定 id DESC）。
func (r *Repository) ListStockOuts(ctx context.Context, f StockOutFilter, offset, limit int) ([]model.StockOut, int64, error) {
	q := r.stockOutQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.StockOut
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllStockOuts 按筛选与数据范围谓词查询全部出库单（Excel 导出用，不分页）。
func (r *Repository) ListAllStockOuts(ctx context.Context, f StockOutFilter) ([]model.StockOut, error) {
	var list []model.StockOut
	if err := r.stockOutQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// stockOutQuery 构造出库单列表/导出的公共查询。
func (r *Repository) stockOutQuery(ctx context.Context, f StockOutFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.StockOut{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.WarehouseId > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseId)
	}
	if f.Type != nil {
		q = q.Where("type = ?", *f.Type)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("out_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("out_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CountStockOutItemsByProduct 产品删除引用计数的一部分：其它出库单明细中
// 引用该产品的行数（软删行不算引用，spec §4.9）。
func (r *Repository) CountStockOutItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.StockOutItem{}, productID)
}
