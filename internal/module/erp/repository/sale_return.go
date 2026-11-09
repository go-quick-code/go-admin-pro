package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// sale_return.go 是销售退货单（ST）的数据访问（issue #92，docs/erp-mvp-spec.md
// §3.4/§5）。审核/反审核的库存动作在 service 唯一入账入口（ApplyStockChange），
// 不在本层；进度回写与占用校验见 sale_stats.go。

// SaleReturnFilter 是销售退货单列表查询条件（筛选 + 数据范围谓词）。
type SaleReturnFilter struct {
	No         string
	OutId      int64
	CustomerId int64
	Status     *int8
	StartTime  *time.Time
	EndTime    *time.Time

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindSaleReturnByID 按 ID 查销售退货单主表（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindSaleReturnByID(ctx context.Context, id int64) (*model.SaleReturn, error) {
	var row model.SaleReturn
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListSaleReturns 按筛选与数据范围谓词分页查询销售退货单（固定 id DESC）。
func (r *Repository) ListSaleReturns(ctx context.Context, f SaleReturnFilter, offset, limit int) ([]model.SaleReturn, int64, error) {
	q := r.saleReturnQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.SaleReturn
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllSaleReturns 按筛选与数据范围谓词查询全部销售退货单（Excel 导出用）。
func (r *Repository) ListAllSaleReturns(ctx context.Context, f SaleReturnFilter) ([]model.SaleReturn, error) {
	var list []model.SaleReturn
	if err := r.saleReturnQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// saleReturnQuery 构造销售退货单列表/导出的公共查询。
func (r *Repository) saleReturnQuery(ctx context.Context, f SaleReturnFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.SaleReturn{})
	if f.No != "" {
		q = q.Where("no LIKE ?", "%"+f.No+"%")
	}
	if f.OutId > 0 {
		q = q.Where("out_id = ?", f.OutId)
	}
	if f.CustomerId > 0 {
		q = q.Where("customer_id = ?", f.CustomerId)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.StartTime != nil {
		q = q.Where("return_time >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		q = q.Where("return_time < ?", *f.EndTime)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CountSaleReturnsByCustomer 引用计数：引用该客户的销售退货单数。
func (r *Repository) CountSaleReturnsByCustomer(ctx context.Context, customerID int64) (int64, error) {
	return countRows(ctx, r.db, &model.SaleReturn{}, "customer_id = ?", customerID)
}

// CountSaleReturnsByOut 挂单引用计数：挂在该出库单上的退货单数（minStatus > 0
// 时只计状态 >= minStatus 的单，反审核禁令用 DocStatusApproved）。
func (r *Repository) CountSaleReturnsByOut(ctx context.Context, outID int64, minStatus int8) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.SaleReturn{}).Where("out_id = ?", outID)
	if minStatus > 0 {
		q = q.Where("status >= ?", minStatus)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
