package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// product.go 是产品的数据访问（issue #88，spec §3.1/§4.9/§5）。

// ProductFilter 是产品列表查询条件（筛选 + 数据范围谓词，导出复用同一口径）。
type ProductFilter struct {
	Name     string
	BarCode  string
	Status   *int8
	Category *int64
	Unit     *int64

	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindProductByID 按 ID 查产品（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindProductByID(ctx context.Context, id int64) (*model.Product, error) {
	var row model.Product
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListProducts 按筛选与数据范围谓词分页查询产品。
func (r *Repository) ListProducts(ctx context.Context, f ProductFilter, offset, limit int) ([]model.Product, int64, error) {
	q := r.productQuery(ctx, f)

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

// ListAllProducts 按筛选与数据范围谓词查询全部产品（Excel 导出用，不分页）。
func (r *Repository) ListAllProducts(ctx context.Context, f ProductFilter) ([]model.Product, error) {
	var list []model.Product
	if err := r.productQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// productQuery 构造产品列表/导出的公共查询（筛选 + 数据范围谓词，见 scope.go）。
func (r *Repository) productQuery(ctx context.Context, f ProductFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Product{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.BarCode != "" {
		q = q.Where("bar_code LIKE ?", "%"+f.BarCode+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.Category != nil {
		q = q.Where("category_id = ?", *f.Category)
	}
	if f.Unit != nil {
		q = q.Where("unit_id = ?", *f.Unit)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CreateProduct 创建产品。
func (r *Repository) CreateProduct(ctx context.Context, row *model.Product) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// SaveProduct 全量保存产品（更新路径；可空展示字段 nil 落 NULL）。
func (r *Repository) SaveProduct(ctx context.Context, row *model.Product) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteProduct 按 ID 软删除产品（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteProduct(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Product{}, id).Error
}

// CountProductsByCategory 统计分类下的产品数（删除约束：被引用禁止删，spec §4.9）。
func (r *Repository) CountProductsByCategory(ctx context.Context, categoryID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Product{}).
		Where("category_id = ?", categoryID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// CountProductsByUnit 统计单位下的产品数（删除约束：被引用禁止删，spec §4.9）。
func (r *Repository) CountProductsByUnit(ctx context.Context, unitID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Product{}).
		Where("unit_id = ?", unitID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
