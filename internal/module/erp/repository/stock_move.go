package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

// CreateStockMove 创建库存调拨单主表。
func (r *Repository) CreateStockMove(ctx context.Context, row *model.StockMove) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreateStockMoveItems 批量创建调拨单明细。
func (r *Repository) CreateStockMoveItems(ctx context.Context, items []model.StockMoveItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindStockMoveByID 按 ID 查调拨单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindStockMoveByID(ctx context.Context, id int64) (*model.StockMove, error) {
	var row model.StockMove
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindStockMoveWithItems 查询调拨单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindStockMoveWithItems(ctx context.Context, id int64) (*model.StockMove, error) {
	var row model.StockMove
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
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

// FindStockMoveByIDForUpdate 行锁读取调拨单（安全 F1：编辑事务内重读并复核
// status=10，与审核认领串行化）。只能在事务内调用。
func (r *Repository) FindStockMoveByIDForUpdate(ctx context.Context, id int64) (*model.StockMove, error) {
	var row model.StockMove
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// SaveStockMove 全量保存调拨单主表（更新路径）。
func (r *Repository) SaveStockMove(ctx context.Context, row *model.StockMove) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteStockMoveIfUnaudited 条件软删除调拨单（status 谓词 + 影响行数，安全 F1）。
func (r *Repository) DeleteStockMoveIfUnaudited(ctx context.Context, id int64) (int64, error) {
	return r.deleteIfStatus(ctx, &model.StockMove{}, id, model.DocStatusUnaudited)
}

// DeleteStockMoveItems 软删除调拨单的全部明细（编辑替换/删除单据时）。
func (r *Repository) DeleteStockMoveItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("stock_move_id = ?", docID).
		Delete(&model.StockMoveItem{}).Error
}

// UpdateStockMoveStatusIf 乐观认领审核状态：仅当当前状态为 fromStatus 时翻转为
// toStatus，返回影响行数（0 表示已被并发请求改变，service 层翻译 409）。
func (r *Repository) UpdateStockMoveStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.StockMove{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
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
