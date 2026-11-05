// pool_recycle.go 承载公海自动回收的执行层（issue #14）：
// infra_job handler 入口（recyclePoolCustomers）→ 系统级遍历租户 →
// 逐租户按配置回收（每租户一个事务批次）。
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
)

// RecycleJobHandlerName 是自动回收任务的处理器名（infra_job.handler_name），
// 种子默认行与 internal/app 的注册处共用此名。
const RecycleJobHandlerName = "recyclePoolCustomers"

// NoopContractChecker 是 ContractChecker 的 v1 空实现：恒「无合同」。
// 合同实体与作废状态由 #16 落地，届时由 internal/app 装配处用
// SetContractChecker 换成真实实现（接入点说明见 ContractChecker 注释）。
type NoopContractChecker struct{}

func (NoopContractChecker) ActiveContractCustomerIDs(ctx context.Context, customerIDs []int64) (map[int64]bool, error) {
	return map[int64]bool{}, nil
}

// SetContractChecker 注入合同依赖面实现（#16 接入点，见 ContractChecker 注释）。
func (s *Service) SetContractChecker(c ContractChecker) {
	s.contracts = c
}

// PoolRecycleResult 汇总一次回收任务的执行结果（handler 日志与调用方展示用）。
type PoolRecycleResult struct {
	Tenants  int   // 扫过的租户数
	Recycled int64 // 本轮回收的客户总数（各租户之和）
}

// RunPoolRecycle 是自动回收任务入口：系统级动作，不依赖调用方的租户上下文
// ——先以 tenant.Skip 列出启用租户（租户注册表 system_tenant 不在租户插件
// 管辖内，Skip 紧贴查询链），再逐租户携带该租户上下文执行（租户插件 fail-closed
// 保证每条语句只碰本租户数据）。单个租户失败不阻塞其他租户，错误聚合返回。
func (s *Service) RunPoolRecycle(ctx context.Context) (PoolRecycleResult, error) {
	tenantIDs, err := s.sys.ListEnabledTenantIDs(ctx)
	if err != nil {
		return PoolRecycleResult{}, err
	}
	result := PoolRecycleResult{Tenants: len(tenantIDs)}
	var errs []error
	for _, id := range tenantIDs {
		recycled, err := s.recycleTenantPool(tenant.WithTenantID(ctx, id))
		if err != nil {
			errs = append(errs, fmt.Errorf("租户 %d 回收失败: %w", id, err))
			continue
		}
		result.Recycled += recycled
		if recycled > 0 {
			slog.Info("pool auto-recycle tenant done", "tenantId", id, "recycled", recycled)
		}
	}
	if len(errs) > 0 {
		return result, errs[0]
	}
	return result, nil
}

// recycleTenantPool 对 ctx 租户执行一轮回收，返回回收数：
//
//  1. 读租户配置（行缺失按默认值）；
//  2. SQL 预筛候选（owner 非空 + COALESCE 超期，见 repository.ListRecycleCandidates）；
//  3. 纯函数复核超期并排除有未作废合同的客户（SelectRecyclableCustomers）；
//  4. 一个事务内逐条条件回收（乐观并发：负责人已被并发操作变更的客户跳过）
//     并写 recycle 历史——清 owner/部门、落池字段与释放原因，历史操作人为 0
//     （系统动作，无登录用户）。
func (s *Service) recycleTenantPool(ctx context.Context) (int64, error) {
	cfg, err := s.GetPoolConfig(ctx)
	if err != nil {
		return 0, err
	}
	now := s.businessNow()
	cutoff := now.AddDate(0, 0, -cfg.RecycleDays)

	candidates, err := s.repo.ListRecycleCandidates(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	recyclable, err := SelectRecyclableCustomers(ctx, candidates, s.contracts, now, cfg.RecycleDays)
	if err != nil {
		return 0, err
	}
	if len(recyclable) == 0 {
		return 0, nil
	}

	reason := fmt.Sprintf("自动回收：超过 %d 天未跟进", cfg.RecycleDays)
	var recycled int64
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		recycled = 0
		for _, c := range recyclable {
			// 复用释放的条件更新（owner 仍为读到的负责人才生效）：回收判定与
			// 落库之间用户手动认领/转移了该客户时跳过，不覆盖新归属。
			rows, err := tx.ReleaseCustomerToPool(ctx, c.ID, c.OwnerUserID, now, reason)
			if err != nil {
				return err
			}
			if rows == 0 {
				continue
			}
			if err := tx.CreateOwnerHistory(ctx, &model.CustomerOwnerHistory{
				TenantID:        tenant.ID(ctx),
				CustomerID:      c.ID,
				Action:          model.OwnerHistoryRecycle,
				FromOwnerUserID: c.OwnerUserID,
				FromOwnerDeptID: c.OwnerDeptID,
				// 系统动作无登录用户，操作人落 0（历史展示侧对 0 不补名称）。
				OperatorUserID: 0,
			}); err != nil {
				return err
			}
			recycled++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return recycled, nil
}
