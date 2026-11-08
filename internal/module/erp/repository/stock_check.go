package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// stock_check.go 是库存盘点单的数据访问（issue #93，docs/erp-mvp-spec.md
// §3.5/§4.6/§5）。主表 + 明细：明细随主表整体替换（编辑 = 软删旧明细 +
// 插入新明细，仅在未审核态允许）；审核/反审核的库存动作在 service 唯一入账
// 入口，不在本层。

// StockCheckFilter 是库存盘点单列表查询条件（筛选 + 数据范围谓词，导出复用）。
type StockCheckFilter struct {
	No          string
	WarehouseId int64
	Status      *int8
	StartTime   *time.Time
	EndTime     *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// ListStockChecks 按筛选与数据范围谓词分页查询盘点单（固定 id DESC）。
func (r *Repository) ListStockChecks(ctx context.Context, f StockCheckFilter, offset, limit int) ([]model.StockCheck, int64, error) {
	q := r.stockCheckQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.StockCheck
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllStockChecks 按筛选与数据范围谓词查询全部盘点单（Excel 导出用）。
func (r *Repository) ListAllStockChecks(ctx context.Context, f StockCheckFilter) ([]model.StockCheck, error) {
	var list []model.StockCheck
	if err := r.stockCheckQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// stockCheckQuery 构造盘点单列表/导出的公共查询。
func (r *Repository) stockCheckQuery(ctx context.Context, f StockCheckFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.StockCheck{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.WarehouseId > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("check_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("check_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CountStockCheckItemsByProduct 产品删除引用计数：盘点单明细引用该产品的行数
// （软删行不算引用，spec §4.9）。
func (r *Repository) CountStockCheckItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.StockCheckItem{}, productID)
}
