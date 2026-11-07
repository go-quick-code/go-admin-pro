package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
)

// product_unit.go 是产品单位的数据访问（issue #88，spec §3.1/§5）。
// 数据范围谓词（owner = 创建人，spec §4.10）由 service 翻译后传入，翻译函数
// applyScope 在 service/scope 逻辑内；Filter 形状与 ProductCategoryFilter 同构。

// ProductUnitFilter 是单位列表查询条件（数据范围谓词，形状与 ProductCategoryFilter 同构）。
type ProductUnitFilter struct {
	Name            string
	Status          *int8
	OwnerDeptIDs    []int64
	SelfOwnerUserID int64
}

// FindProductUnitByID 按 ID 查单位（租户插件自动过滤），不存在返回 nil。
func (r *Repository) FindProductUnitByID(ctx context.Context, id int64) (*model.ProductUnit, error) {
	var row model.ProductUnit
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListProductUnits 按筛选与数据范围谓词分页查询单位。
func (r *Repository) ListProductUnits(ctx context.Context, f ProductUnitFilter, offset, limit int) ([]model.ProductUnit, int64, error) {
	q := r.productUnitQuery(ctx, f)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.ProductUnit
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllProductUnits 按筛选与数据范围谓词查询单位全量（Excel 导出用，不分页）。
func (r *Repository) ListAllProductUnits(ctx context.Context, f ProductUnitFilter) ([]model.ProductUnit, error) {
	var list []model.ProductUnit
	if err := r.productUnitQuery(ctx, f).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// productUnitQuery 构造单位列表/导出的公共查询（筛选 + 数据范围谓词）。
func (r *Repository) productUnitQuery(ctx context.Context, f ProductUnitFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.ProductUnit{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	q = applyOwnerScope(q, f.OwnerDeptIDs, f.SelfOwnerUserID)
	return q
}

// CreateProductUnit 创建单位。
func (r *Repository) CreateProductUnit(ctx context.Context, row *model.ProductUnit) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// SaveProductUnit 全量保存单位（更新路径）。
func (r *Repository) SaveProductUnit(ctx context.Context, row *model.ProductUnit) error {
	return r.db.WithContext(ctx).Save(row).Error
}

// DeleteProductUnit 按 ID 软删除单位（GORM deleted_at，租户插件自动带租户条件）。
func (r *Repository) DeleteProductUnit(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ProductUnit{}, id).Error
}
