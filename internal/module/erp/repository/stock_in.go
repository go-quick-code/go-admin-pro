package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// stock_in.go 是其它入库单的数据访问（issue #90，docs/erp-mvp-spec.md §3.5/§5）。
// 主表 + 明细：明细随主表整体替换（编辑 = 软删旧明细 + 插入新明细，仅在
// 未审核态允许）；审核/反审核的库存动作在 service 唯一入账入口，不在本层。

// StockInFilter 是其它入库单列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type StockInFilter struct {
	No          string
	WarehouseId int64
	Type        *int8
	Status      *int8
	StartTime   *time.Time
	EndTime     *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreateStockIn 创建其它入库单主表。
func (r *Repository) CreateStockIn(ctx context.Context, row *model.StockIn) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreateStockInItems 批量创建入库单明细。
func (r *Repository) CreateStockInItems(ctx context.Context, items []model.StockInItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindStockInByID 按 ID 查入库单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindStockInByID(ctx context.Context, id int64) (*model.StockIn, error) {
	var row model.StockIn
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindStockInWithItems 查询入库单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindStockInWithItems(ctx context.Context, id int64) (*model.StockIn, error) {
	var row model.StockIn
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListStockIns 按筛选与数据范围谓词分页查询入库单（固定 id DESC）。
func (r *Repository) ListStockIns(ctx context.Context, f StockInFilter, offset, limit int) ([]model.StockIn, int64, error) {
	q := r.stockInQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.StockIn
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllStockIns 按筛选与数据范围谓词查询全部入库单（Excel 导出用，不分页）。
func (r *Repository) ListAllStockIns(ctx context.Context, f StockInFilter) ([]model.StockIn, error) {
	var list []model.StockIn
	if err := r.stockInQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// stockInQuery 构造入库单列表/导出的公共查询。
func (r *Repository) stockInQuery(ctx context.Context, f StockInFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.StockIn{})
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
		q = q.Where("in_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("in_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// SaveStockIn 全量保存入库单主表（更新路径）。
func (r *Repository) SaveStockIn(ctx context.Context, row *model.StockIn) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteStockIn 按 ID 软删除入库单主表（仅未审核态，service 层把关）。
func (r *Repository) DeleteStockIn(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.StockIn{}, id).Error
}

// DeleteStockInItems 软删除入库单的全部明细（编辑替换/删除单据时）。
func (r *Repository) DeleteStockInItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("stock_in_id = ?", docID).
		Delete(&model.StockInItem{}).Error
}

// UpdateStockInStatusIf 乐观认领审核状态：仅当当前状态为 fromStatus 时翻转为
// toStatus，返回影响行数（0 表示状态已被并发请求改变，service 层翻译 409）。
// 审核/反审核先认领再落账：并发重复审核在同一 WHERE 条件上串行化，杜绝双重入账。
func (r *Repository) UpdateStockInStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.StockIn{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// CountStockInItemsByProduct 产品删除引用计数的一部分：其它入库单明细中
// 引用该产品的行数（软删行不算引用，spec §4.9）。
func (r *Repository) CountStockInItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.StockInItem{}, productID)
}
