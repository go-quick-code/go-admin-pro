package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/quota"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// QuotaProvider 由 internal/app 装配的实现补齐 storage/message 用量；
// system 只负责套餐限额与用户数，不反向依赖 infra/message。
type QuotaProvider interface {
	TenantQuota(ctx context.Context, tenantID int64) (quota.Status, error)
}

func (s *Service) SetQuotaProvider(provider QuotaProvider) {
	s.quotaProvider = provider
}

// PackageQuota 返回指定租户的套餐限额：
//   - 平台租户：不限；
//   - 未绑定套餐：不限（兼容旧租户）；
//   - 套餐丢失：内部错误（fail-closed，避免异常时放开）。
func (s *Service) PackageQuota(ctx context.Context, tenantID int64) (quota.Limits, error) {
	if tenantID <= 0 || tenantID == tenant.DefaultID {
		return quota.UnlimitedLimits(), nil
	}
	t, err := s.repo.FindTenantByID(ctx, tenantID)
	if err != nil {
		return quota.Limits{}, err
	}
	if t == nil {
		return quota.Limits{}, xerr.NotFound("租户不存在")
	}
	if t.PackageID <= 0 {
		return quota.UnlimitedLimits(), nil
	}
	pkg, err := s.repo.FindTenantPackageByID(ctx, t.PackageID)
	if err != nil {
		return quota.Limits{}, err
	}
	if pkg == nil {
		return quota.Limits{}, xerr.Internal(fmt.Errorf("租户 %d 引用的套餐 %d 不存在", tenantID, t.PackageID))
	}
	return quota.Limits{
		MaxUsers:       pkg.MaxUsers,
		MaxStorageMB:   pkg.MaxStorageMB,
		MaxSMSMonthly:  pkg.MaxSMSMonthly,
		MaxMailMonthly: pkg.MaxMailMonthly,
	}, nil
}

// UserUsage 返回租户未删除用户数（含停用，与套餐配额口径一致）。
func (s *Service) UserUsage(ctx context.Context, tenantID int64) (int64, error) {
	return s.repo.CountUsersByTenantID(ctx, tenantID)
}

// TenantQuota 返回限额 + 全量用量；storage/message 用量由 app 装配的
// QuotaProvider 补充，未装配时只返回用户数。
func (s *Service) TenantQuota(ctx context.Context, tenantID int64) (quota.Status, error) {
	if s.quotaProvider != nil {
		return s.quotaProvider.TenantQuota(ctx, tenantID)
	}
	limits, err := s.PackageQuota(ctx, tenantID)
	if err != nil {
		return quota.Status{}, err
	}
	users, err := s.UserUsage(ctx, tenantID)
	if err != nil {
		return quota.Status{}, err
	}
	return quota.Status{Limits: limits, Usage: quota.Usage{Users: users}}, nil
}

// checkUserQuota 在创建用户前校验套餐用户数上限。
// 并发创建可能同时通过检查（无数据库唯一约束兜底），当前接受；
// 单人后台的并发创建概率低，超限只影响配额而非安全边界。
func (s *Service) checkUserQuota(ctx context.Context) error {
	tenantID := tenant.ID(ctx)
	limits, err := s.PackageQuota(ctx, tenantID)
	if err != nil {
		return err
	}
	if limits.Unlimited || limits.MaxUsers <= 0 {
		return nil
	}
	used, err := s.UserUsage(ctx, tenantID)
	if err != nil {
		return err
	}
	if used >= limits.MaxUsers {
		return xerr.Conflict(fmt.Sprintf("租户用户数已达套餐上限（%d）", limits.MaxUsers))
	}
	return nil
}
