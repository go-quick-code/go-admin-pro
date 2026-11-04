package repository

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）----------

// ProductFilter 是产品列表查询条件。
type ProductFilter struct {
	Name   string
	Code   string
	Unit   string
	Status *int8
}

// FindProductByID 按 ID 查产品（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindProductByID(ctx context.Context, id int64) (*model.Product, error) {
	var row model.Product
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListProducts 按筛选分页查询产品。
func (r *Repository) ListProducts(ctx context.Context, f ProductFilter, offset, limit int) ([]model.Product, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Product{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Code != "" {
		q = q.Where("code LIKE ?", "%"+f.Code+"%")
	}
	if f.Unit != "" {
		q = q.Where("unit = ?", f.Unit)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Product
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateProduct 创建产品。
func (r *Repository) CreateProduct(ctx context.Context, row *model.Product) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// SaveProduct 全量保存产品（更新路径）。
func (r *Repository) SaveProduct(ctx context.Context, row *model.Product) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteProduct 按 ID 软删除产品（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteProduct(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Product{}, id).Error
}
