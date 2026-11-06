package service

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/quota"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// QuotaGateway 是 infra 上传存储配额的窄接口：限额由租户套餐决定（system
// 模块解释，ADR 0012），由 internal/app 装配期注入适配器；infra 只依赖本
// 接口与 internal/pkg/quota 的中性类型，不反向依赖 system。
type QuotaGateway interface {
	// StorageLimits 返回租户的套餐限额；Unlimited 或 MaxStorageMB<=0 表示不限。
	StorageLimits(ctx context.Context, tenantID int64) (quota.Limits, error)
}

// SetQuotaGateway 注入存储配额限额来源（internal/app 装配调用）。
// 未注入时（如单元测试直连 Service）上传不做配额校验。
func (s *Service) SetQuotaGateway(gw QuotaGateway) {
	s.quotaGateway = gw
}

// StorageUsage 返回租户未删除文件的字节总量（存储配额用量口径，spec §4.2.3），
// 供 app 装配的全量配额提供者补齐 storage 用量。
func (s *Service) StorageUsage(ctx context.Context, tenantID int64) (int64, error) {
	return s.repo.SumFileSize(ctx, tenantID)
}

// checkStorageQuota 在落盘之前校验租户存储配额：现有用量 + 本次文件大小
// 超过 max_storage_mb 时返回 409「租户存储空间不足」，调用方不得写磁盘、
// 不得插 infra_file 行（spec §4.2.6 配额校验失败不写入业务数据）。
// 校验在写盘前执行，失败不留任何磁盘残留。
func (s *Service) checkStorageQuota(ctx context.Context, incoming int64) error {
	if s.quotaGateway == nil {
		return nil
	}
	tenantID := tenant.ID(ctx)
	limits, err := s.quotaGateway.StorageLimits(ctx, tenantID)
	if err != nil {
		return err
	}
	if limits.Unlimited || limits.MaxStorageMB <= 0 {
		return nil
	}
	used, err := s.StorageUsage(ctx, tenantID)
	if err != nil {
		return err
	}
	if used+incoming > limits.MaxStorageMB*1024*1024 {
		return xerr.Conflict(fmt.Sprintf("租户存储空间不足（已用 %d 字节，上限 %d MB）", used, limits.MaxStorageMB))
	}
	return nil
}
