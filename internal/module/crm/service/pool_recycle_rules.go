// pool_recycle_rules.go 承载公海自动回收的纯判断逻辑（issue #14）：
// 超期口径与回收筛选（合同排除）。SQL 候选查询（repository.ListRecycleCandidates）
// 做同样的超期预筛以提高批量效率，两处口径必须一致（见各函数注释）。
package service

import (
	"context"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
)

// ContractChecker 是 crm 对合同模块的依赖面（依赖接口而非实现）：
// 自动回收须排除「有未作废合同」的客户（docs/crm-mvp-spec.md §4.2），
// 而合同实体与作废状态由 issue #16 落地。
//
// #16 接入点（本工单预留的缝）：
//   - 接口：本文件的 ContractChecker（批量方法，避免逐客户查询的 N+1）；
//   - 空实现：pool_recycle.go 的 NoopContractChecker（v1 恒「无合同」）；
//   - 装配：internal/app/app.go 构造 crm Service 后调用
//     crmSvc.SetContractChecker(合同实现) 替换空实现；
//   - 集成测试：#16 落地后在 internal/module/crm/ 补「有未作废合同的客户
//     不被回收、作废合同不拦截」的真实路径用例（当前 fake 分支覆盖见
//     pool_recycle_rules_test.go）。
type ContractChecker interface {
	// ActiveContractCustomerIDs 返回给定客户中存在「未作废合同」的客户 ID 集合：
	// 作废合同与无合同都不进集合。ids 为空时实现方可直接返回空集。
	ActiveContractCustomerIDs(ctx context.Context, customerIDs []int64) (map[int64]bool, error)
}

// ContractCheckerFunc 是 ContractChecker 的函数适配器（单测与轻量装配用）。
type ContractCheckerFunc func(ctx context.Context, customerIDs []int64) (map[int64]bool, error)

func (f ContractCheckerFunc) ActiveContractCustomerIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	return f(ctx, ids)
}

// PoolRecycleOverdue 报告客户是否已超期未跟进（回收条件之二）：
// last_follow_time 优先；为 NULL（从未跟进）时回退 created_at 计算——
// 规格验收明确「从未跟进的新客户按创建时间参与回收」，否则新客户永远不回收。
// 边界语义：「超过 N 天」取严格更早（距今恰好 N 天不回收）。
// 与 ListRecycleCandidates 的 COALESCE(last_follow_time, created_at) 保持一致；
// created_at 为零值（理论脏数据）按未超期处理，宁漏回收不误回收。
func PoolRecycleOverdue(now time.Time, lastFollow *time.Time, createdAt time.Time, days int) bool {
	reference := createdAt
	if lastFollow != nil {
		reference = *lastFollow
	}
	if reference.IsZero() {
		return false
	}
	cutoff := now.AddDate(0, 0, -days)
	return reference.Before(cutoff)
}

// SelectRecyclableCustomers 从 SQL 预筛的候选客户中筛出本轮可回收者：
// 逐条复核超期（同 PoolRecycleOverdue 口径）并排除存在未作废合同的客户。
// checker 为合同依赖面（v1 空实现恒返回空集；#16 接入真实实现），
// 空候选时不触发 checker 查询。
func SelectRecyclableCustomers(ctx context.Context, candidates []model.Customer, checker ContractChecker, now time.Time, days int) ([]model.Customer, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	// 先按超期口径收窄，再只对仍可回收者查合同，减小 checker 的查询面。
	overdue := make([]model.Customer, 0, len(candidates))
	for _, c := range candidates {
		if !c.DeletedAt.Time.IsZero() {
			continue // 防御：软删行不参与回收（查询层已过滤，双保险）
		}
		if c.OwnerUserID == 0 {
			continue // 已在公海的客户不重复回收（回收条件之一：owner 非空）
		}
		if PoolRecycleOverdue(now, c.LastFollowTime, c.CreatedAt, days) {
			overdue = append(overdue, c)
		}
	}
	if len(overdue) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(overdue))
	for _, c := range overdue {
		ids = append(ids, c.ID)
	}
	active, err := checker.ActiveContractCustomerIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	selected := make([]model.Customer, 0, len(overdue))
	for _, c := range overdue {
		if active[c.ID] {
			continue // 有未作废合同：本轮跳过，等合同作废后下轮再回收
		}
		selected = append(selected, c)
	}
	return selected, nil
}
