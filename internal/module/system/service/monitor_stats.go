package service

import (
	"context"
)

// 平台级规模统计（issue #84，spec §4.10.1）：供 internal/app 的监控统计
// 适配器调用（infra 的 MonitorStatsGateway 窄接口的 system 侧实现）。
// 平台级统计是管理能力，不在普通 HTTP 端点暴露；调用方（监控面板）自身
// 已做仅平台租户校验。

// TenantCount 返回全部租户数（未删除）。
func (s *Service) TenantCount(ctx context.Context) (int64, error) {
	return s.repo.CountTenantsAll(ctx)
}

// UserCount 返回全部租户的用户数（未删除，含停用）。
func (s *Service) UserCount(ctx context.Context) (int64, error) {
	return s.repo.CountUsersAll(ctx)
}
