package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// product_category.go 是产品分类树的数据访问（issue #88，spec §3.1/§5）。
// 列表固定全量（前端组树契约，spec §5）；数据范围谓词（owner = 创建人，
// spec §4.10）由 service 翻译成 OwnerDeptIDs/SelfOwnerUserID 传入。

// ProductCategoryFilter 是分类列表查询条件（数据范围谓词，与 crm CustomerFilter
// 同构：两者皆零值 = 范围「全部」；空 OwnerDeptIDs 且不含本人 = 谓词恒假）。
type ProductCategoryFilter struct {
	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindProductCategoryByID 按 ID 查分类（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindProductCategoryByID(ctx context.Context, id int64) (*model.ProductCategory, error) {
	var row model.ProductCategory
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListProductCategories 按数据范围谓词查当前租户的全部分类
// （树组装在前端，spec §5；父校验在 service 的纯函数里做）。
func (r *Repository) ListProductCategories(ctx context.Context, f ProductCategoryFilter) ([]model.ProductCategory, error) {
	var list []model.ProductCategory
	if err := r.productCategoryQuery(ctx, f).Order("sort, id").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// productCategoryQuery 构造分类列表/导出的公共查询（数据范围谓词，见 scope.go）。
func (r *Repository) productCategoryQuery(ctx context.Context, f ProductCategoryFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ProductCategory{})
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CreateProductCategory 创建分类。
func (r *Repository) CreateProductCategory(ctx context.Context, row *model.ProductCategory) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// SaveProductCategory 全量保存分类（更新路径）。
func (r *Repository) SaveProductCategory(ctx context.Context, row *model.ProductCategory) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteProductCategory 按 ID 软删除分类（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteProductCategory(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductCategory{}, id).Error
}

// CountProductCategoryChildren 统计分类的直接子分类数（删除约束：有子分类禁止删）。
func (r *Repository) CountProductCategoryChildren(ctx context.Context, id int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.ProductCategory{}).
		Where("parent_id = ?", id).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
