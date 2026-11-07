package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// stock_out.go 是其它出库单的数据访问（issue #90，docs/erp-mvp-spec.md §3.5/§5）。
// 与 stock_in.go 镜像（out_time/type，QC 前缀）。

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

// CreateStockOut 创建其它出库单主表。
func (r *Repository) CreateStockOut(ctx context.Context, row *model.StockOut) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreateStockOutItems 批量创建出库单明细。
func (r *Repository) CreateStockOutItems(ctx context.Context, items []model.StockOutItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindStockOutByID 按 ID 查出库单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindStockOutByID(ctx context.Context, id int64) (*model.StockOut, error) {
	var row model.StockOut
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindStockOutWithItems 查询出库单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindStockOutWithItems(ctx context.Context, id int64) (*model.StockOut, error) {
	var row model.StockOut
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
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

// SaveStockOut 全量保存出库单主表（更新路径）。
func (r *Repository) SaveStockOut(ctx context.Context, row *model.StockOut) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteStockOut 按 ID 软删除出库单主表（仅未审核态，service 层把关）。
func (r *Repository) DeleteStockOut(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.StockOut{}, id).Error
}

// DeleteStockOutItems 软删除出库单的全部明细（编辑替换/删除单据时）。
func (r *Repository) DeleteStockOutItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("stock_out_id = ?", docID).
		Delete(&model.StockOutItem{}).Error
}

// UpdateStockOutStatusIf 乐观认领审核状态：仅当当前状态为 fromStatus 时翻转为
// toStatus，返回影响行数（0 表示状态已被并发请求改变，service 层翻译 409）。
// 审核/反审核先认领再落账：并发重复审核在同一 WHERE 条件上串行化，杜绝双重入账。
func (r *Repository) UpdateStockOutStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.StockOut{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// CountStockOutItemsByProduct 产品删除引用计数的一部分：其它出库单明细中
// 引用该产品的行数（软删行不算引用，spec §4.9）。
func (r *Repository) CountStockOutItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.StockOutItem{}, productID)
}
