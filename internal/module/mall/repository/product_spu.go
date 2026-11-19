package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// product_spu.go 是商品域 SPU/SKU 的数据访问原语与查询（issue #177）。
//
// 两表全部租户级（插件自动隔离）；SPU 删除是软删，唯一键 UNIQUE(tenant_id,
// name) 不含 deleted_at（照 member_level 先例：软删行保持占位，同名不可
// 重建）。SKU 无独立删除端点：SPU 创建/更新（全量替换 SKU 集）与删除
//（级联软删）事务内收口。引用计数（分类/品牌被 SPU 引用、SPU 被订单引用）
// 是删除守卫的查询点：软删 SPU 不算引用（gorm 零值条件自动过滤）；
// CountOrdersBySPU 在 T6 trade_order 落地前恒返 0（照 T3 countSPUsByCategory
// 预留谓词模式，表随 T6 建后接线真实计数）。

// ---------- 商品 SPU ----------

// SPUFilter 是 SPU 分页查询条件（管理端，spec §4：分类/品牌/状态/关键词过滤）。
type SPUFilter struct {
	Name       string
	CategoryID int64
	BrandID    int64
	Status     *int8
}

// FindSPUByID 按 ID 查 SPU：未找到返回 (nil, nil)。
func (r *Repository) FindSPUByID(ctx context.Context, id int64) (*model.ProductSPU, error) {
	var row model.ProductSPU
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if err != nil {
		if notFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// spuQuery 组装 SPU 筛选查询。
func (r *Repository) spuQuery(ctx context.Context, f SPUFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ProductSPU{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.CategoryID > 0 {
		q = q.Where("category_id = ?", f.CategoryID)
	}
	if f.BrandID > 0 {
		q = q.Where("brand_id = ?", f.BrandID)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// ListSPUs 分页查询 SPU（id DESC 的 append 次序）。
func (r *Repository) ListSPUs(ctx context.Context, f SPUFilter, page web.PageQuery) ([]model.ProductSPU, int64, error) {
	q := r.spuQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ProductSPU
	if err := q.Order("id DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateSPU 创建 SPU（SKU 批量落库与 SPU 同事务，见 service 层）。
// 撞 UNIQUE(tenant_id, name) 返回规整后的 ErrDuplicatedKey（service 层翻译 409）。
func (r *Repository) CreateSPU(ctx context.Context, row *model.ProductSPU) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Create(row).Error)
}

// UpdateSPUFields 按列更新 SPU（列白名单在 service 层组装）。改名撞唯一键
// 返回 ErrDuplicatedKey。
func (r *Repository) UpdateSPUFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.db.WithContext(ctx).Model(&model.ProductSPU{}).Where("id = ?", id).Updates(fields).Error)
}

// DeleteSPU 软删 SPU（删除守卫在 service 层：被订单引用禁删；SKU 级联软删
// 同事务收口）。
func (r *Repository) DeleteSPU(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductSPU{}, "id = ?", id).Error
}

// CountSPUsByCategory 统计引用该分类的未删 SPU 数（分类删除守卫第二谓词的
// 查询点；软删 SPU 不算——分类删除后软删商品不随之复活引用语义）。
func (r *Repository) CountSPUsByCategory(ctx context.Context, categoryID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ProductSPU{}).
		Where("category_id = ?", categoryID).
		Count(&count).Error
	return count, err
}

// CountSPUsByBrand 统计引用该品牌的未删 SPU 数（品牌删除守卫的查询点；
// 软删 SPU 不算，同 CountSPUsByCategory 口径）。
func (r *Repository) CountSPUsByBrand(ctx context.Context, brandID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ProductSPU{}).
		Where("brand_id = ?", brandID).
		Count(&count).Error
	return count, err
}

// CountOrdersBySPU 统计引用该 SPU 的订单数（SPU 删除守卫的查询点）。
// trade_order 随 T6 迁移建表，本票预留恒返 0（照 T3 countSPUsByCategory
// 预留谓词模式）：SPU 删除守卫的谓词调用点已在 service 层落位，T6 只需
// 把本方法换成真实计数（trade_order_item.spu_id 引用计数），无调用方改动。
func (r *Repository) CountOrdersBySPU(ctx context.Context, spuID int64) (int64, error) {
	_ = ctx
	_ = spuID
	return 0, nil
}

// ---------- 商品 SKU ----------

// ListSKUsBySPU 查询 SPU 的全部 SKU（first DESC, id ASC——主 SKU 恒在首位，
// 其余按创建序稳定展示）。
func (r *Repository) ListSKUsBySPU(ctx context.Context, spuID int64) ([]model.ProductSKU, error) {
	var list []model.ProductSKU
	err := r.db.WithContext(ctx).
		Where("spu_id = ?", spuID).
		Order("first DESC, id ASC").Find(&list).Error
	return list, err
}

// CreateSKUs 批量创建 SKU（SPU 创建/更新的同事务落库，service 层调用）。
func (r *Repository) CreateSKUs(ctx context.Context, rows []model.ProductSKU) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&rows).Error
}

// DeleteSKUsBySPU 软删 SPU 名下全部未删 SKU（SPU 更新全量替换 SKU 集与
// SPU 删除级联，service 层事务内调用；gorm 软删自动补 deleted_at）。
func (r *Repository) DeleteSKUsBySPU(ctx context.Context, spuID int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductSKU{}, "spu_id = ?", spuID).Error
}
