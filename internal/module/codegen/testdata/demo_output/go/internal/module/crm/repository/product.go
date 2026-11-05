package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）----------

// ProductFilter 是产品列表查询条件。
// OwnerDeptIDs 与 SelfOwnerUserID 共同表达数据范围谓词（internal/pkg/datascope.Filter
// 到列 owner_dept_id / owner_user_id 的翻译，与 system 模块 UserFilter 同构）：
//   - 两者皆零值：不加范围条件（数据范围「全部」）；
//   - 空 OwnerDeptIDs + SelfOwnerUserID = 0：谓词恒假（fail-closed，无可见部门）。
type ProductFilter struct {
	Name   string
	Unit   string
	Status *int8

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
	q := r.db.WithContext(ctx).Model(&model.Product{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Unit != "" {
		q = q.Where("unit = ?", f.Unit)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.OwnerDeptIDs != nil || f.SelfOwnerUserID > 0 {
		switch {
		case len(f.OwnerDeptIDs) > 0 && f.SelfOwnerUserID > 0:
			q = q.Where("(owner_dept_id IN ? OR owner_user_id = ?)", f.OwnerDeptIDs, f.SelfOwnerUserID)
		case len(f.OwnerDeptIDs) > 0:
			q = q.Where("owner_dept_id IN ?", f.OwnerDeptIDs)
		case f.SelfOwnerUserID > 0:
			q = q.Where("owner_user_id = ?", f.SelfOwnerUserID)
		default:
			q = q.Where("1 = 0")
		}
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
