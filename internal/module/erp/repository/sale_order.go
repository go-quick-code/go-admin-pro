package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// sale_order.go 是销售订单（XS）的数据访问（issue #92，docs/erp-mvp-spec.md
// §3.4/§5）。明细随主表整体替换（编辑 = 软删旧明细 + 插入新明细，仅在未审核
// 态允许）；进度回写（out_count/return_count）与占用校验见 sale_stats.go。

// SaleOrderFilter 是销售订单列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type SaleOrderFilter struct {
	OrderNo    string
	CustomerId int64
	Status     *int8
	StartTime  *time.Time
	EndTime    *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// CreateSaleOrder 创建销售订单主表。
func (r *Repository) CreateSaleOrder(ctx context.Context, row *model.SaleOrder) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CreateSaleOrderItems 批量创建销售订单明细。
func (r *Repository) CreateSaleOrderItems(ctx context.Context, items []model.SaleOrderItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&items).Error
}

// FindSaleOrderByID 按 ID 查销售订单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindSaleOrderByID(ctx context.Context, id int64) (*model.SaleOrder, error) {
	var row model.SaleOrder
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindSaleOrderByIDForUpdate 行锁读取销售订单（出库/退货创建与订单编辑的占用
// 校验在同一事务内先锁父单行，串行化并发占用变更，防超卖）。只能在事务内调用。
func (r *Repository) FindSaleOrderByIDForUpdate(ctx context.Context, id int64) (*model.SaleOrder, error) {
	var row model.SaleOrder
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindSaleOrderWithItems 查询销售订单带头 + 明细（详情/进度重算用）。
func (r *Repository) FindSaleOrderWithItems(ctx context.Context, id int64) (*model.SaleOrder, error) {
	var row model.SaleOrder
	if err := r.db.WithContext(ctx).Preload("Items").First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListSaleOrders 按筛选与数据范围谓词分页查询销售订单（固定 id DESC）。
func (r *Repository) ListSaleOrders(ctx context.Context, f SaleOrderFilter, offset, limit int) ([]model.SaleOrder, int64, error) {
	q := r.saleOrderQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.SaleOrder
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllSaleOrders 按筛选与数据范围谓词查询全部销售订单（Excel 导出用）。
func (r *Repository) ListAllSaleOrders(ctx context.Context, f SaleOrderFilter) ([]model.SaleOrder, error) {
	var list []model.SaleOrder
	if err := r.saleOrderQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// saleOrderQuery 构造销售订单列表/导出的公共查询。
func (r *Repository) saleOrderQuery(ctx context.Context, f SaleOrderFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.SaleOrder{})
	if f.OrderNo != "" {
		q = q.Where("order_no LIKE ?", "%"+f.OrderNo+"%")
	}
	if f.CustomerId > 0 {
		q = q.Where("customer_id = ?", f.CustomerId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("order_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("order_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// SaveSaleOrder 全量保存销售订单主表（更新路径）。
func (r *Repository) SaveSaleOrder(ctx context.Context, row *model.SaleOrder) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteSaleOrderIfUnaudited 条件软删除销售订单（status 谓词 + 影响行数，安全 F1）。
func (r *Repository) DeleteSaleOrderIfUnaudited(ctx context.Context, id int64) (int64, error) {
	return r.deleteIfStatus(ctx, &model.SaleOrder{}, id, model.DocStatusUnaudited)
}

// DeleteSaleOrderItems 软删除销售订单的全部明细（编辑替换/删除单据时）。
func (r *Repository) DeleteSaleOrderItems(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).
		Where("sale_order_id = ?", docID).
		Delete(&model.SaleOrderItem{}).Error
}

// UpdateSaleOrderStatusIf 乐观认领审核状态：仅当当前状态为 fromStatus 时翻转为
// toStatus，返回影响行数（0 表示状态已被并发请求改变，service 层翻译 409）。
func (r *Repository) UpdateSaleOrderStatusIf(ctx context.Context, id int64, fromStatus, toStatus int8) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.SaleOrder{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Update("status", toStatus)
	return res.RowsAffected, res.Error
}

// CountSaleOrdersByCustomer 引用计数：引用该客户的销售订单数（软删行不算引用）。
func (r *Repository) CountSaleOrdersByCustomer(ctx context.Context, customerID int64) (int64, error) {
	return countRows(ctx, r.db, &model.SaleOrder{}, "customer_id = ?", customerID)
}
