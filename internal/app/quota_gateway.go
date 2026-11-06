package app

import (
	"context"

	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/quota"
)

// quotaGateway 组合 system 与 infra 的配额能力（issue #71，spec §4.2，ADR 0012）：
//   - 对 system 实现其 QuotaProvider（TenantQuota）：套餐限额与用户数来自
//     system 自身，存储用量经 infra 补齐；
//   - 对 infra 实现其 QuotaGateway（StorageLimits）：限额由 system 解释套餐。
//
// 两侧模块只依赖各自定义的窄接口与 internal/pkg/quota 的中性类型，
// 由 internal/app 在装配期组合注入，模块间不直接 import。
type quotaGateway struct {
	sys   *systemservice.Service
	infra *infraservice.Service
}

// TenantQuota 实现 system 侧 QuotaProvider：返回套餐限额 + 用户数（system）
// + 存储用量（infra）。短信/邮件月度用量由消息工单（#75）以同样方式追加。
func (g quotaGateway) TenantQuota(ctx context.Context, tenantID int64) (quota.Status, error) {
	limits, err := g.sys.PackageQuota(ctx, tenantID)
	if err != nil {
		return quota.Status{}, err
	}
	users, err := g.sys.UserUsage(ctx, tenantID)
	if err != nil {
		return quota.Status{}, err
	}
	storage, err := g.infra.StorageUsage(ctx, tenantID)
	if err != nil {
		return quota.Status{}, err
	}
	return quota.Status{Limits: limits, Usage: quota.Usage{Users: users, StorageBytes: storage}}, nil
}

// StorageLimits 实现 infra 侧 QuotaGateway：返回租户套餐限额（未绑定套餐
// 与平台租户为 unlimited，见 system.PackageQuota）。
func (g quotaGateway) StorageLimits(ctx context.Context, tenantID int64) (quota.Limits, error) {
	return g.sys.PackageQuota(ctx, tenantID)
}

var (
	_ systemservice.QuotaProvider = quotaGateway{}
	_ infraservice.QuotaGateway   = quotaGateway{}
)
