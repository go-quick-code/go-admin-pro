package repository

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
)

// product_category.go 是产品分类树的数据访问（issue #11，手写：codegen 不生成树逻辑）。
// 所有查询/写入通过租户插件自动带 tenant_id 条件，本层不手工拼租户条件。

// FindProductCategoryByID 按 ID 查分类（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindProductCategoryByID(ctx context.Context, id int64) (*model.ProductCategory, error) {
	var row model.ProductCategory
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListProductCategories 查当前租户的全部分类（树组装与父校验都在 service 的纯函数里做）。
func (r *Repository) ListProductCategories(ctx context.Context) ([]model.ProductCategory, error) {
	var list []model.ProductCategory
	if err := r.db.WithContext(ctx).Order("sort, id").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
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

// CountProductsByCategory 统计分类下的产品数（删除约束：有产品禁止删，#15/#16 之外的产品侧约束）。
func (r *Repository) CountProductsByCategory(ctx context.Context, categoryID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.Product{}).
		Where("category_id = ?", categoryID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// wrapDuplicateEntry 把 MySQL 1062 重复键错误规整为 gorm.ErrDuplicatedKey 链
// （IsDuplicateEntry 识别），service 层据此翻译成业务 400。
// uk_crm_product_tenant_code 含软删行（软删不释放编码），service 预检看不到
// 软删行时由唯一键兜底。
func wrapDuplicateEntry(err error) error {
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return errors.Join(gorm.ErrDuplicatedKey, err)
	}
	return err
}

// IsDuplicateEntry 报告错误是否为唯一键冲突（wrapDuplicateEntry 规整后的形态）。
func IsDuplicateEntry(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
