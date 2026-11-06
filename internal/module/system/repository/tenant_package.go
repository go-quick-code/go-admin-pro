package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// TenantPackageFilter 是租户套餐列表过滤条件。
type TenantPackageFilter struct {
	Name   string
	Status *int8
}

func (r *Repository) ListTenantPackages(ctx context.Context, f TenantPackageFilter, offset, limit int) ([]model.TenantPackage, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.TenantPackage{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.TenantPackage
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) ListSimpleTenantPackages(ctx context.Context) ([]model.TenantPackage, error) {
	var list []model.TenantPackage
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Order("id ASC").
		Find(&list).Error
	return list, err
}

func (r *Repository) FindTenantPackageByID(ctx context.Context, id int64) (*model.TenantPackage, error) {
	var pkg model.TenantPackage
	if err := r.db.WithContext(ctx).First(&pkg, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &pkg, nil
}

func (r *Repository) CountTenantPackageByName(ctx context.Context, name string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.TenantPackage{}).Where("name = ?", name)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

func (r *Repository) CreateTenantPackage(ctx context.Context, pkg *model.TenantPackage) error {
	return r.db.WithContext(ctx).Create(pkg).Error
}

func (r *Repository) SaveTenantPackage(ctx context.Context, pkg *model.TenantPackage) error {
	return r.db.WithContext(ctx).Save(pkg).Error
}

func (r *Repository) DeleteTenantPackage(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.TenantPackage{}, id).Error
}

// CountTenantsByPackageID 统计引用套餐的租户数（含停用租户，排除已删除）。
func (r *Repository) CountTenantsByPackageID(ctx context.Context, packageID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Tenant{}).
		Where("package_id = ?", packageID).
		Count(&total).Error
	return total, err
}
