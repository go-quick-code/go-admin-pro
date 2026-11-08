package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// sale_out.go 是销售出库单（CK）的数据访问（issue #92，docs/erp-mvp-spec.md
// §3.4/§5）。审核/反审核的库存动作在 service 唯一入账入口（ApplyStockChange），
// 不在本层；进度回写与占用校验见 sale_stats.go。

// SaleOutFilter 是销售出库单列表查询条件（筛选 + 数据范围谓词）。
type SaleOutFilter struct {
	No          string
	OrderId     int64
	CustomerId  int64
	WarehouseId int64
	Status      *int8
	StartTime   *time.Time
	EndTime     *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreateSaleOut 创建销售出库单主表。
func (r *Repository) CreateSaleOut(ctx context.Context, row *model.SaleOut) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreateSaleOutItems 批量创建销售出库单明细。
func (r *Repository) CreateSaleOutItems(ctx context.Context, items []model.SaleOutItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindSaleOutByID 按 ID 查销售出库单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindSaleOutByID(ctx context.Context, id int64) (*model.SaleOut, error) {
	var row model.SaleOut
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindSaleOutByIDForUpdate 行锁读取销售出库单（退货创建的占用校验在同一事务内
// 先锁父出库行，串行化并发占用变更）。只能在事务内调用。
func (r *Repository) FindSaleOutByIDForUpdate(ctx context.Context, id int64) (*model.SaleOut, error) {
	var row model.SaleOut
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindSaleOutWithItems 查询销售出库单带头 + 明细（详情/审核/反审核用）。
func (r *Repository) FindSaleOutWithItems(ctx context.Context, id int64) (*model.SaleOut, error) {
	var row model.SaleOut
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListSaleOuts 按筛选与数据范围谓词分页查询销售出库单（固定 id DESC）。
func (r *Repository) ListSaleOuts(ctx context.Context, f SaleOutFilter, offset, limit int) ([]model.SaleOut, int64, error) {
	q := r.saleOutQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.SaleOut
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllSaleOuts 按筛选与数据范围谓词查询全部销售出库单（Excel 导出用）。
func (r *Repository) ListAllSaleOuts(ctx context.Context, f SaleOutFilter) ([]model.SaleOut, error) {
	var list []model.SaleOut
	if err := r.saleOutQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// saleOutQuery 构造销售出库单列表/导出的公共查询。
func (r *Repository) saleOutQuery(ctx context.Context, f SaleOutFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.SaleOut{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.OrderId > 0 {
		q = q.Where("order_id = ?", f.OrderId)
	}
	if f.CustomerId > 0 {
		q = q.Where("customer_id = ?", f.CustomerId)
	}
	if f.WarehouseId > 0 {
		q = q.Where("warehouse_id = ?", f.WarehouseId)
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

// SaveSaleOut 全量保存销售出库单主表（更新路径）。
func (r *Repository) SaveSaleOut(ctx context.Context, row *model.SaleOut) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteSaleOutIfUnaudited 条件软删除销售出库单（status 谓词 + 影响行数，安全 F1）。
func (r *Repository) DeleteSaleOutIfUnaudited(ctx context.Context, id int64) (int64, error) {
	return r.deleteIfStatus(ctx, &model.SaleOut{}, id, model.DocStatusUnaudited)
}

// DeleteSaleOutItems 软删除销售出库单的全部明细（编辑替换/删除单据时）。
func (r *Repository) DeleteSaleOutItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("sale_out_id = ?", docID).
		Delete(&model.SaleOutItem{}).Error
}

// UpdateSaleOutStatusIf 乐观认领审核状态（防并发双重审核/反审核双重入账）。
func (r *Repository) UpdateSaleOutStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.SaleOut{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// CountSaleOutsByCustomer 引用计数：引用该客户的销售出库单数。
func (r *Repository) CountSaleOutsByCustomer(ctx context.Context, customerID int64) (int64, error) {
	return countRows(ctx, r.db, &model.SaleOut{}, "customer_id = ?", customerID)
}

// CountSaleOutsByOrder 挂单引用计数：挂在该订单上的出库单数（minStatus > 0 时
// 只计状态 >= minStatus 的单，反审核禁令用 DocStatusApproved）。
func (r *Repository) CountSaleOutsByOrder(ctx context.Context, orderID int64, minStatus int8) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.SaleOut{}).Where("order_id = ?", orderID)
	if minStatus > 0 {
		q = q.Where("status >= ?", minStatus)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
