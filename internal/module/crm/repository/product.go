package repository

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"gorm.io/gorm"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）----------
// 接入补丁（issue #11）：ProductFilter 增加 CategoryID（分类树联动，codegen 不生成
// int64 筛选字段）；新增 FindProductByCode（编码租户内唯一的 service 预检）；
// 创建/保存对 1062 重复键做规整（IsDuplicateEntry，软删行占码的唯一键兜底）。

// ProductFilter 是产品列表查询条件。
type ProductFilter struct {
	Name   string
	Code   string
	Unit   string
	Status *int8

	// CategoryID 按分类过滤（nil 不过滤；树选择器传入，含其子分类由调用方展开）。
	CategoryID *int64
}

// FindProductByID 按 ID 查产品（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindProductByID(ctx context.Context, id int64) (*model.Product, error) {
	var row model.Product
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindProductByCode 按编码查产品（租户插件自动过滤租户；GORM 软删过滤只看未删除行），
// 不存在返回 nil。编码唯一的落库兜底见 CreateProduct/SaveProduct 的重复键规整。
func (r *Repository) FindProductByCode(ctx context.Context, code string) (*model.Product, error) {
	var row model.Product
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListProducts 按筛选分页查询产品。
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

// ListAllProducts 按筛选查询全部产品（Excel 导出用，不分页）。
func (r *Repository) ListAllProducts(ctx context.Context, f ProductFilter) ([]model.Product, error) {
	var list []model.Product
	if err := r.productQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// productQuery 构造产品列表/导出的公共查询。
func (r *Repository) productQuery(ctx context.Context, f ProductFilter) *gorm.DB {
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
	if f.CategoryID != nil {
		q = q.Where("category_id = ?", *f.CategoryID)
	}
	return q
}

// FindProductsByCodes 按编码批量查产品（导入编码唯一性预检用；租户插件自动过滤，
// 软删除行不可见——其占码由唯一键兜底）。
func (r *Repository) FindProductsByCodes(ctx context.Context, codes []string) ([]model.Product, error) {
	unique := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		unique = append(unique, code)
	}
	if len(unique) == 0 {
		return nil, nil
	}
	var list []model.Product
	if err := r.db.WithContext(ctx).Where("code IN ?", unique).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CreateProduct 创建产品；撞 uk_crm_product_tenant_code 时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) CreateProduct(ctx context.Context, row *model.Product) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdateProductFields 只更新请求字段（编辑接口），status 仅在调用方显式放入
// fields 时写入（PUT /product/status 走 UpdateProductStatus 的独立入口）；
// 用 map 更新而不是整行 Save：只写请求列、能写空值，且不覆盖并发状态变更。
// 撞唯一键时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) UpdateProductFields(ctx context.Context, id int64, fields map[string]any) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Product{}).
		Where("id = ?", id).
		Updates(fields)
	return res.RowsAffected, wrapDuplicateEntry(res.Error)
}

// UpdateProductStatus 只更新产品状态列（PUT /crm/product/status，spec §5），
// 不触碰名称/编码/价格等其他字段；租户插件自动带租户条件。
func (r *Repository) UpdateProductStatus(ctx context.Context, id int64, status int8) error {
	return r.db.WithContext(ctx).Model(&model.Product{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// DeleteProduct 按 ID 软删除产品（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteProduct(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Product{}, id).Error
}
