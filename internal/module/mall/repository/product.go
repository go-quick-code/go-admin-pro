package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// product.go 是商品域基础三件的数据访问原语与查询（issue #176）。
//
// 四表全部租户级（插件自动隔离）；删除是软删，唯一键不含 deleted_at
//（照 member_level 先例：软删行保持占位，同值不可重建）。唯一键冲突经
// wrapDuplicateEntry 规整，service 层翻译 409。分类删除守卫「被 SPU 引用
// 禁删」的引用计数随 T4 SPU 表落地接线（本文件无 SPU 访问）。

// ---------- 商品分类（两级树） ----------

// FindCategoryByID 按 ID 查分类：未找到返回 (nil, nil)。
func (r *Repository) FindCategoryByID(ctx context.Context, id int64) (*model.ProductCategory, error) {
	var row model.ProductCategory
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ListCategories 全量分类（sort, id 升序——树组装的输入，不在此分页）。
func (r *Repository) ListCategories(ctx context.Context) ([]model.ProductCategory, error) {
	var list []model.ProductCategory
	err := r.db.WithContext(ctx).
		Order("sort ASC, id ASC").
		Find(&list).Error
	return list, err
}

// CreateCategory 创建分类。撞 UNIQUE(tenant_id, parent_id, name)（同级同名）
// 返回规整后的 ErrDuplicatedKey（service 层翻译 409）。
func (r *Repository) CreateCategory(ctx context.Context, row *model.ProductCategory) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdateCategoryFields 按列更新分类（列白名单在 service 层组装）。父级变更/
// 改名撞同级唯一键返回 ErrDuplicatedKey。
func (r *Repository) UpdateCategoryFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.ProductCategory{}).Where("id = ?", id).Updates(fields).Error)
}

// DeleteCategory 软删分类（删除守卫在 service 层：有子级禁删；被未删 SPU
// 引用禁删——引用计数见 CountSPUsByCategory，T4 接线）。
func (r *Repository) DeleteCategory(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductCategory{}, "id = ?", id).Error
}

// CountCategoryChildren 统计子分类数（删除守卫的引用计数；软删过滤由
// gorm 零值条件自动生效）。
func (r *Repository) CountCategoryChildren(ctx context.Context, id int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ProductCategory{}).
		Where("parent_id = ?", id).
		Count(&count).Error
	return count, err
}

// ---------- 商品品牌 ----------

// BrandFilter 是品牌分页查询条件（管理端，spec §4）。
type BrandFilter struct {
	Name   string
	Status *int8
}

// FindBrandByID 按 ID 查品牌：未找到返回 (nil, nil)。
func (r *Repository) FindBrandByID(ctx context.Context, id int64) (*model.ProductBrand, error) {
	var row model.ProductBrand
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// brandQuery 组装品牌筛选查询。
func (r *Repository) brandQuery(ctx context.Context, f BrandFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ProductBrand{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// ListBrands 分页查询品牌（id DESC 的 append 次序）。
func (r *Repository) ListBrands(ctx context.Context, f BrandFilter, page web.PageQuery) ([]model.ProductBrand, int64, error) {
	q := r.brandQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ProductBrand
	if err := q.Order("id DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabledBrands 全量启用品牌（id 升序）：SPU 表单品牌下拉的数据源
// （同 ListEnabledLevels 的「清单只读」形状；端点随 T4 SPU 管理接线）。
func (r *Repository) ListEnabledBrands(ctx context.Context) ([]model.ProductBrand, error) {
	var list []model.ProductBrand
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Order("id ASC").Find(&list).Error
	return list, err
}

// CreateBrand 创建品牌。撞 UNIQUE(tenant_id, name) 返回规整后的
// ErrDuplicatedKey（service 层翻译 409）。
func (r *Repository) CreateBrand(ctx context.Context, row *model.ProductBrand) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdateBrandFields 按列更新品牌（列白名单在 service 层组装）。改名撞唯一键
// 返回 ErrDuplicatedKey。
func (r *Repository) UpdateBrandFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.ProductBrand{}).Where("id = ?", id).Updates(fields).Error)
}

// DeleteBrand 软删品牌（SPU 引用守卫在 service 层：被未删 SPU 引用禁删——
// 引用计数见 CountSPUsByBrand，T4 接线）。
func (r *Repository) DeleteBrand(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductBrand{}, "id = ?", id).Error
}

// ---------- 商品属性名 ----------

// PropertyFilter 是属性名分页查询条件（管理端，spec §4）。
type PropertyFilter struct {
	Name string
}

// FindPropertyByID 按 ID 查属性名：未找到返回 (nil, nil)。
func (r *Repository) FindPropertyByID(ctx context.Context, id int64) (*model.ProductProperty, error) {
	var row model.ProductProperty
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// propertyQuery 组装属性名筛选查询。
func (r *Repository) propertyQuery(ctx context.Context, f PropertyFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ProductProperty{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	return q
}

// ListProperties 分页查询属性名（id DESC 的 append 次序）。
func (r *Repository) ListProperties(ctx context.Context, f PropertyFilter, page web.PageQuery) ([]model.ProductProperty, int64, error) {
	q := r.propertyQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ProductProperty
	if err := q.Order("id DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateProperty 创建属性名。撞 UNIQUE(tenant_id, name) 返回规整后的
// ErrDuplicatedKey（service 层翻译 409）。
func (r *Repository) CreateProperty(ctx context.Context, row *model.ProductProperty) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdatePropertyFields 按列更新属性名（列白名单在 service 层组装）。改名撞
// 唯一键返回 ErrDuplicatedKey。
func (r *Repository) UpdatePropertyFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.ProductProperty{}).Where("id = ?", id).Updates(fields).Error)
}

// DeleteProperty 软删属性名（级联删除其属性值由 service 层同事务收口——
// 孤儿值没有意义）。
func (r *Repository) DeleteProperty(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductProperty{}, "id = ?", id).Error
}

// DeletePropertyValuesByProperty 软删属性名下的全部属性值（删属性名时
// 级联清值，service 层事务内调用）。
func (r *Repository) DeletePropertyValuesByProperty(ctx context.Context, propertyID int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductPropertyValue{}, "property_id = ?", propertyID).Error
}

// ---------- 商品属性值 ----------

// FindPropertyValueByID 按 ID 查属性值：未找到返回 (nil, nil)。
func (r *Repository) FindPropertyValueByID(ctx context.Context, id int64) (*model.ProductPropertyValue, error) {
	var row model.ProductPropertyValue
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ListPropertyValuesByProperty 属性名下的全部属性值（id ASC——值清单按
// 创建序稳定展示）。
func (r *Repository) ListPropertyValuesByProperty(ctx context.Context, propertyID int64) ([]model.ProductPropertyValue, error) {
	var list []model.ProductPropertyValue
	err := r.db.WithContext(ctx).
		Where("property_id = ?", propertyID).
		Order("id ASC").Find(&list).Error
	return list, err
}

// CountPropertyValuesByProperty 统计属性名下的属性值数（删除守卫的引用计数）。
func (r *Repository) CountPropertyValuesByProperty(ctx context.Context, propertyID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ProductPropertyValue{}).
		Where("property_id = ?", propertyID).
		Count(&count).Error
	return count, err
}

// CreatePropertyValue 创建属性值。撞 UNIQUE(tenant_id, property_id, name)
// （同属性同名值）返回规整后的 ErrDuplicatedKey（service 层翻译 409）。
func (r *Repository) CreatePropertyValue(ctx context.Context, row *model.ProductPropertyValue) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdatePropertyValueFields 按列更新属性值（列白名单在 service 层组装）。
// 改名撞唯一键返回 ErrDuplicatedKey。
func (r *Repository) UpdatePropertyValueFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.ProductPropertyValue{}).Where("id = ?", id).Updates(fields).Error)
}

// DeletePropertyValue 软删属性值（无 SKU 引用守卫——SKU properties 是落库
// 时写定的名称快照，与属性主数据解耦，主数据软删不影响已建 SKU 的展示与
// T6 订单成交快照；T4 定稿口径）。
func (r *Repository) DeletePropertyValue(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductPropertyValue{}, "id = ?", id).Error
}
