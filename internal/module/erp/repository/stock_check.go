package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

// CreateStockCheck 创建库存盘点单主表。
func (r *Repository) CreateStockCheck(ctx context.Context, row *model.StockCheck) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreateStockCheckItems 批量创建盘点单明细。
func (r *Repository) CreateStockCheckItems(ctx context.Context, items []model.StockCheckItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindStockCheckByID 按 ID 查盘点单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindStockCheckByID(ctx context.Context, id int64) (*model.StockCheck, error) {
	var row model.StockCheck
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindStockCheckWithItems 查询盘点单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindStockCheckWithItems(ctx context.Context, id int64) (*model.StockCheck, error) {
	var row model.StockCheck
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
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

// FindStockCheckByIDForUpdate 行锁读取盘点单（安全 F1：编辑事务内重读并复核
// status=10，与审核认领串行化）。只能在事务内调用。
func (r *Repository) FindStockCheckByIDForUpdate(ctx context.Context, id int64) (*model.StockCheck, error) {
	var row model.StockCheck
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// SaveStockCheck 全量保存盘点单主表（更新路径）。
func (r *Repository) SaveStockCheck(ctx context.Context, row *model.StockCheck) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteStockCheckIfUnaudited 条件软删除盘点单（status 谓词 + 影响行数，安全 F1）。
func (r *Repository) DeleteStockCheckIfUnaudited(ctx context.Context, id int64) (int64, error) {
	return r.deleteIfStatus(ctx, &model.StockCheck{}, id, model.DocStatusUnaudited)
}

// DeleteStockCheckItems 软删除盘点单的全部明细（编辑替换/删除单据时）。
func (r *Repository) DeleteStockCheckItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("stock_check_id = ?", docID).
		Delete(&model.StockCheckItem{}).Error
}

// UpdateStockCheckStatusIf 乐观认领审核状态：仅当当前状态为 fromStatus 时翻转为
// toStatus，返回影响行数（0 表示已被并发请求改变，service 层翻译 409）。
func (r *Repository) UpdateStockCheckStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.StockCheck{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// CountStockCheckItemsByProduct 产品删除引用计数：盘点单明细引用该产品的行数
// （软删行不算引用，spec §4.9）。
func (r *Repository) CountStockCheckItemsByProduct(ctx context.Context, productID int64) (int64, error) {
	return countItemsByProduct(ctx, r.db, &model.StockCheckItem{}, productID)
}
