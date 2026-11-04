package repository

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/model"
)

type TenantFilter struct {
	Name   string
	Status *int8
}

func (r *Repository) ListTenants(ctx context.Context, f TenantFilter, offset, limit int) ([]model.Tenant, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Tenant{})
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
	var list []model.Tenant
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) FindTenantByID(ctx context.Context, id int64) (*model.Tenant, error) {
	var t model.Tenant
	if err := r.db.WithContext(ctx).First(&t, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

func (r *Repository) CountTenantByName(ctx context.Context, name string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Tenant{}).Where("name = ?", name)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

func (r *Repository) CreateTenant(ctx context.Context, t *model.Tenant) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *Repository) SaveTenant(ctx context.Context, t *model.Tenant) error {
	return r.db.WithContext(ctx).Save(t).Error
}

func (r *Repository) DeleteTenant(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Tenant{}, id).Error
}
