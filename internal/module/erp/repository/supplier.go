package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// supplier.go 是供应商的数据访问（issue #89，spec §3.1/§4.10）。

// SupplierFilter 是供应商列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type SupplierFilter struct {
	Name   string
	Mobile string
	Status *int8

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindSupplierByID 按 ID 查供应商（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindSupplierByID(ctx context.Context, id int64) (*model.Supplier, error) {
	var row model.Supplier
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListSuppliers 按筛选与数据范围谓词分页查询供应商。
func (r *Repository) ListSuppliers(ctx context.Context, f SupplierFilter, offset, limit int) ([]model.Supplier, int64, error) {
	q := r.supplierQuery(ctx, f)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Supplier
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllSuppliers 按筛选与数据范围谓词查询全部供应商（Excel 导出用，不分页）。
func (r *Repository) ListAllSuppliers(ctx context.Context, f SupplierFilter) ([]model.Supplier, error) {
	var list []model.Supplier
	if err := r.supplierQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// supplierQuery 构造供应商列表/导出的公共查询（筛选 + 数据范围谓词，见 scope.go）。
func (r *Repository) supplierQuery(ctx context.Context, f SupplierFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Supplier{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Mobile != "" {
		q = q.Where("mobile LIKE ?", "%"+f.Mobile+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CreateSupplier 创建供应商。
func (r *Repository) CreateSupplier(ctx context.Context, row *model.Supplier) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// SaveSupplier 全量保存供应商（更新路径；可空税务/开户字段 nil 落 NULL）。
func (r *Repository) SaveSupplier(ctx context.Context, row *model.Supplier) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteSupplier 按 ID 软删除供应商（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteSupplier(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Supplier{}, id).Error
}
