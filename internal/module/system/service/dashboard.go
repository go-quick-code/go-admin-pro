package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// DashboardSummary 是管理后台首页统计数据。
type DashboardSummary struct {
	UserCount    int64            `json:"userCount"`
	RoleCount    int64            `json:"roleCount"`
	DeptCount    int64            `json:"deptCount"`
	MenuCount    int64            `json:"menuCount"`
	RecentLogins []model.LoginLog `json:"recentLogins"`
}

// DashboardSummary 返回当前租户的统计信息。
func (s *Service) DashboardSummary(ctx context.Context) (*DashboardSummary, error) {
	userCount, err := s.repo.CountUsers(ctx)
	if err != nil {
		return nil, err
	}
	roleCount, err := s.repo.CountRoles(ctx)
	if err != nil {
		return nil, err
	}
	deptCount, err := s.repo.CountDepts(ctx)
	if err != nil {
		return nil, err
	}
	menuCount, err := s.repo.CountMenus(ctx)
	if err != nil {
		return nil, err
	}
	logins, err := s.repo.RecentLoginLogs(ctx, 5)
	if err != nil {
		return nil, err
	}
	return &DashboardSummary{
		UserCount:    userCount,
		RoleCount:    roleCount,
		DeptCount:    deptCount,
		MenuCount:    menuCount,
		RecentLogins: logins,
	}, nil
}
