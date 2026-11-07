package repository

import (
	"context"

	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// 平台级规模统计（issue #84，spec §4.10.1）：监控面板需要全平台的租户数
// 与用户数。两表都是租户表，统计跨全部租户，显式 tenant.Skip；
// gorm 软删除自动排除 deleted_at 非空行。

// CountTenantsAll 统计全部租户数（未删除）。
func (r *Repository) CountTenantsAll(ctx context.Context) (int64, error) {
	var total int64
	err := tenant.Skip(r.db.WithContext(ctx)).Model(&sysmodel.Tenant{}).Count(&total).Error
	return total, err
}

// CountUsersAll 统计全部租户的用户数（未删除，含停用）。
func (r *Repository) CountUsersAll(ctx context.Context) (int64, error) {
	var total int64
	err := tenant.Skip(r.db.WithContext(ctx)).Model(&sysmodel.User{}).Count(&total).Error
	return total, err
}
