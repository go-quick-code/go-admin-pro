package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// warehouse.go 是仓库的数据访问（issue #89，spec §3.1/§4.10）。

// WarehouseFilter 是仓库列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type WarehouseFilter struct {
	Name   string
	Status *int8

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindWarehouseByID 按 ID 查仓库（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindWarehouseByID(ctx context.Context, id int64) (*model.Warehouse, error) {
	var row model.Warehouse
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListWarehouses 按筛选与数据范围谓词分页查询仓库。
func (r *Repository) ListWarehouses(ctx context.Context, f WarehouseFilter, offset, limit int) ([]model.Warehouse, int64, error) {
	q := r.warehouseQuery(ctx, f)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Warehouse
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllWarehouses 按筛选与数据范围谓词查询全部仓库（Excel 导出用，不分页）。
func (r *Repository) ListAllWarehouses(ctx context.Context, f WarehouseFilter) ([]model.Warehouse, error) {
	var list []model.Warehouse
	if err := r.warehouseQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// warehouseQuery 构造仓库列表/导出的公共查询（筛选 + 数据范围谓词，见 scope.go）。
func (r *Repository) warehouseQuery(ctx context.Context, f WarehouseFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Warehouse{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CreateWarehouse 创建仓库。
func (r *Repository) CreateWarehouse(ctx context.Context, row *model.Warehouse) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// SaveWarehouse 全量保存仓库（更新路径）。
func (r *Repository) SaveWarehouse(ctx context.Context, row *model.Warehouse) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteWarehouse 按 ID 软删除仓库（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteWarehouse(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Warehouse{}, id).Error
}

// CountWarehouseDocRefs 仓库被单据主表引用的行数（spec §4.9：仓库被任何单据
// （含未审核）引用禁删；软删行不计——删除单据即释放引用）。调拨计调出/调入
// 两仓（同一行只计一次），盘点计盘点仓（#98 审查 MINOR）。
func (r *Repository) CountWarehouseDocRefs(ctx context.Context, warehouseID int64) (int64, error) {
	refs := []struct {
		table any
		cond  string
		args  []any
	}{
		{&model.StockIn{}, "warehouse_id = ?", []any{warehouseID}},
		{&model.StockOut{}, "warehouse_id = ?", []any{warehouseID}},
		{&model.PurchaseIn{}, "warehouse_id = ?", []any{warehouseID}},
		{&model.PurchaseReturn{}, "warehouse_id = ?", []any{warehouseID}},
		{&model.SaleOut{}, "warehouse_id = ?", []any{warehouseID}},
		{&model.SaleReturn{}, "warehouse_id = ?", []any{warehouseID}},
		{&model.StockCheck{}, "warehouse_id = ?", []any{warehouseID}},
		{&model.StockMove{}, "from_warehouse_id = ? OR to_warehouse_id = ?", []any{warehouseID, warehouseID}},
	}
	var total int64
	for _, ref := range refs {
		n, err := countRows(ctx, r.db, ref.table, ref.cond, ref.args...)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}
