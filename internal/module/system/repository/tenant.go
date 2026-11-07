package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
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

// ListEnabledTenantIDs 返回全部启用租户的 ID（升序，系统级）：
// 公海自动回收 job 遍历租户用（issue #14）。租户注册表是系统级数据
// （不在租户插件 TenantTables 内，也没有租户上下文可依赖），查询显式
// tenant.Skip——Skip 紧贴本条查询链，不影响调用方后续语句。
func (r *Repository) ListEnabledTenantIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := tenant.Skip(r.db.WithContext(ctx).Model(&model.Tenant{})).
		Where("status = ?", model.StatusEnable).
		Order("id ASC").
		Pluck("id", &ids).Error
	return ids, err
}

// ListTenantIDsByPackageID 返回引用指定套餐的租户 ID（升序，系统级）。
// 套餐是平台级资源，换菜单后需要批量重绑这些租户的超管菜单。
func (r *Repository) ListTenantIDsByPackageID(ctx context.Context, packageID int64) ([]int64, error) {
	var ids []int64
	err := tenant.Skip(r.db.WithContext(ctx).Model(&model.Tenant{})).
		Where("package_id = ?", packageID).
		Order("id ASC").
		Pluck("id", &ids).Error
	return ids, err
}

// ListRolesByTenantAndCode 跨租户读取指定编码的角色（系统级，显式 tenant.Skip + tenant_id）。
// 套餐绑定/换套餐时用于定位目标租户的 super_admin 角色。
func (r *Repository) ListRolesByTenantAndCode(ctx context.Context, tenantID int64, code string) ([]model.Role, error) {
	var roles []model.Role
	err := tenant.Skip(r.db.WithContext(ctx)).
		Where("tenant_id = ? AND code = ?", tenantID, code).
		Order("id ASC").
		Find(&roles).Error
	return roles, err
}
