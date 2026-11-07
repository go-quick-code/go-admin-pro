package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock.go 是库存账的数据访问原语（issue #90，docs/erp-mvp-spec.md §3.2/§4.2/§4.3）。
//
// 余额更新三原语（EnsureStockRow → LockStock → IncreaseStock/DecreaseStock）
// 只供 service 层唯一入账入口（service/stock.go ApplyStockChange）在事务内按序
// 调用，任何其它路径不得调用，更不得直改 erp_stock（spec §4.2 硬约束）。
// 全部走显式租户参数的裸 SQL（与 NextSequenceValue 同理，不经租户插件），
// 保证「锁行 → 读写」与租户条件在同一语句口径上，不被插件改写。

// StockFilter 是库存余额分页查询条件（仓库×产品余额，spec §5）。
// 库存行无 owner 语义（仓库不做独立权限，spec §4.10），不做数据范围过滤。
type StockFilter struct {
	WarehouseId int64
	ProductId   int64
}

// EnsureStockRow 使 (tenantID, productID, warehouseID) 的余额行存在：
// 幂等 no-op upsert（已存在时 count 不变）。INSERT ... ON DUPLICATE KEY UPDATE
// 在唯一键上先取行锁物化该行，后续 LockStock 不落 gap 锁。
// 只能在事务内调用（唯一入账入口的第 1 步）。
func (r *Repository) EnsureStockRow(ctx context.Context, tenantID, productID, warehouseID int64) error {
	return r.db.WithContext(ctx).Exec(
		"INSERT INTO `erp_stock` (`tenant_id`, `product_id`, `warehouse_id`, `count`) VALUES (?, ?, ?, 0) "+
			"ON DUPLICATE KEY UPDATE `count` = `count`",
		tenantID, productID, warehouseID,
	).Error
}

// LockStock 取余额行锁并返回当前余额（spec §4.2：事务内先取仓库×产品行锁，
// 多仓库场景调用方按仓库 ID 升序加锁防死锁；本票单仓，规则先行成文）。
// 行刚由 EnsureStockRow 物化，必然存在。只能在事务内调用。
func (r *Repository) LockStock(ctx context.Context, tenantID, productID, warehouseID int64) (qty.Qty, error) {
	var current qty.Qty
	err := r.db.WithContext(ctx).Raw(
		"SELECT `count` FROM `erp_stock` WHERE `tenant_id` = ? AND `product_id` = ? AND `warehouse_id` = ? FOR UPDATE",
		tenantID, productID, warehouseID,
	).Scan(&current).Error
	return current, err
}

// IncreaseStock 入账方向更新余额（delta > 0，行锁已持有，先读后写无并发窗）。
// 返回影响行数（恒 1；0 视为异常）。只能在事务内调用。
func (r *Repository) IncreaseStock(ctx context.Context, tenantID, productID, warehouseID int64, delta qty.Qty) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `erp_stock` SET `count` = `count` + ?, `updated_at` = ? "+
			"WHERE `tenant_id` = ? AND `product_id` = ? AND `warehouse_id` = ?",
		delta, time.Now(), tenantID, productID, warehouseID,
	)
	return res.RowsAffected, res.Error
}

// DecreaseStock 出账方向更新余额：SQL 带 `count >= ?` 条件（spec §4.2 负库存
// 双保险的 SQL 侧），影响行数为 0 表示余额不足——409 由 service 层翻译（提示
// 产品/仓库/当前库存/变更量）。行锁已持有，先读后写。只能在事务内调用。
func (r *Repository) DecreaseStock(ctx context.Context, tenantID, productID, warehouseID int64, amount qty.Qty) (int64, error) {
	res := r.db.WithContext(ctx).Exec(
		"UPDATE `erp_stock` SET `count` = `count` - ?, `updated_at` = ? "+
			"WHERE `tenant_id` = ? AND `product_id` = ? AND `warehouse_id` = ? AND `count` >= ?",
		amount, time.Now(), tenantID, productID, warehouseID, amount,
	)
	return res.RowsAffected, res.Error
}

// InsertStockRecord 插入出入库流水（append-only：只插入，不更新不删除）。
// 只能在事务内调用（与余额更新同事务，保证快照一致）。
func (r *Repository) InsertStockRecord(ctx context.Context, record *model.StockRecord) error {
	return r.db.WithContext(ctx).Create(record).Error
}

// ---------- 查询（分页/合计/引用计数；走租户插件，租户条件自动注入） ----------

// ListStocks 按筛选分页查询库存余额（仓库×产品，spec §5：库存页）。
func (r *Repository) ListStocks(ctx context.Context, f StockFilter, offset, limit int) ([]model.Stock, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Stock{})
	if f.WarehouseId > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseId)
	}
	if f.ProductId > 0 {
		q = q.Where("product_id = ?", f.ProductId)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Stock
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllStocks 按筛选查询全部库存余额（Excel 导出用，不分页）。
func (r *Repository) ListAllStocks(ctx context.Context, f StockFilter) ([]model.Stock, error) {
	q := r.db.WithContext(ctx).Model(&model.Stock{})
	if f.WarehouseId > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseId)
	}
	if f.ProductId > 0 {
		q = q.Where("product_id = ?", f.ProductId)
	}
	var list []model.Stock
	if err := q.Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// SumStockCount 汇总产品的库存数量：warehouseID > 0 时限该仓（单仓余额），
// 否则全仓合计（spec §5：get-count）。无余额行时返回 0。
func (r *Repository) SumStockCount(ctx context.Context, productID, warehouseID int64) (qty.Qty, error) {
	q := r.db.WithContext(ctx).Model(&model.Stock{}).
		Select("COALESCE(SUM(`count`), 0)").
		Where("product_id = ?", productID)
	if warehouseID > 0 {
		q = q.Where("warehouse_id = ?", warehouseID)
	}
	var total qty.Qty
	if err := q.Scan(&total).Error; err != nil {
		return qty.Qty{}, err
	}
	return total, nil
}

// StockRecordFilter 是出入库流水分页查询条件（spec §5：按 biz_type/仓库/产品/时间筛选）。
type StockRecordFilter struct {
	BizType     string
	WarehouseId int64
	ProductId   int64
	// StartTime/EndTime 过滤入账时间（created_at），服务端已解析为业务时区时刻。
	StartTime *time.Time
	EndTime   *time.Time
}

// ListStockRecords 按筛选分页查询流水（固定 id DESC，最新在前）。
func (r *Repository) ListStockRecords(ctx context.Context, f StockRecordFilter, offset, limit int) ([]model.StockRecord, int64, error) {
	q := r.stockRecordQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.StockRecord
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllStockRecords 按筛选查询全部流水（Excel 导出用，不分页）。
func (r *Repository) ListAllStockRecords(ctx context.Context, f StockRecordFilter) ([]model.StockRecord, error) {
	var list []model.StockRecord
	if err := r.stockRecordQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListStockRecordsByDoc 查询单据的全部流水（集成测试/审计用：按 biz 引用聚合，
// 不分页；含正向与 _CANCEL 反向，append-only 全集）。
func (r *Repository) ListStockRecordsByDoc(ctx context.Context, bizType, bizNo string) ([]model.StockRecord, error) {
	var list []model.StockRecord
	if err := r.db.WithContext(ctx).Model(&model.StockRecord{}).
		Where("biz_type IN ?", []string{bizType, bizType + "_CANCEL"}).
		Where("biz_no = ?", bizNo).
		Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// stockRecordQuery 构造流水列表/导出的公共查询。
func (r *Repository) stockRecordQuery(ctx context.Context, f StockRecordFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.StockRecord{})
	if f.BizType != "" {
		q = q.Where("biz_type = ?", f.BizType)
	}
	if f.WarehouseId > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseId)
	}
	if f.ProductId > 0 {
		q = q.Where("product_id = ?", f.ProductId)
	}
	if f.StartTime != nil {
		q = q.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("created_at < ?", *f.EndTime)
	}
	return q
}

// CheckWarehouseZeroStock 仓库被引用计数的一部分：非零余额行数（spec §4.9：
// 仓库被 erp_stock 非零记录或任何流水引用禁删）。
func (r *Repository) CheckWarehouseZeroStock(ctx context.Context, warehouseID int64) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.Stock{}).
		Where("warehouse_id = ? AND `count` <> 0", warehouseID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CheckWarehouseStockRecordRef 仓库被流水引用的行数（含零余额行，append-only
// 历史不可删；软删行不算引用）。
func (r *Repository) CheckWarehouseStockRecordRef(ctx context.Context, warehouseID int64) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.StockRecord{}).
		Where("warehouse_id = ?", warehouseID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// ---------- 名称批量解析（库存页/流水页展示列，service 层组装） ----------

// FindProductNamesIn 返回租户内产品 ID→名称映射（未删除行）。
func (r *Repository) FindProductNamesIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNamesIn(ctx, r.db, &model.Product{}, ids)
}

// FindWarehouseNamesIn 返回租户内仓库 ID→名称映射（未删除行）。
func (r *Repository) FindWarehouseNamesIn(ctx context.Context, ids []int64) (map[int64]string, error) {
	return findNamesIn(ctx, r.db, &model.Warehouse{}, ids)
}

// findNamesIn 通用 ID→name 批量解析（租户插件自动带租户条件）。
func findNamesIn(ctx context.Context, db *gorm.DB, table any, ids []int64) (map[int64]string, error) {
	names := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	var rows []struct {
		ID   int64
		Name string
	}
	if err := db.WithContext(ctx).Model(table).
		Select("id, name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}

// countItemsByProduct 单据明细的产品引用计数（CountStockInItemsByProduct /
// CountStockOutItemsByProduct 共用；软删行不算引用）。
func countItemsByProduct(ctx context.Context, db *gorm.DB, table any, productID int64) (int64, error) {
	var n int64
	if err := db.WithContext(ctx).Model(table).
		Where("product_id = ?", productID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
