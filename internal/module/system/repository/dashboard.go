package repository

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/model"
)

// CountUsers 统计当前租户用户数。
func (r *Repository) CountUsers(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Count(&total).Error
	return total, err
}

func (r *Repository) CountRoles(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Role{}).Count(&total).Error
	return total, err
}

func (r *Repository) CountDepts(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Dept{}).Count(&total).Error
	return total, err
}

func (r *Repository) CountMenus(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Menu{}).Count(&total).Error
	return total, err
}

// RecentLoginLogs 返回当前租户最近的登录记录。
func (r *Repository) RecentLoginLogs(ctx context.Context, limit int) ([]model.LoginLog, error) {
	var list []model.LoginLog
	err := r.db.WithContext(ctx).Order("id DESC").Limit(limit).Find(&list).Error
	return list, err
}
