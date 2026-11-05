package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
)

// 公海自动回收纯函数（issue #14，docs/crm-mvp-spec.md §4.2）：
// 本文件只测不连库的超期判断与回收筛选；SQL 候选范围与回收落库的
// 全链路由集成测试验证（pool_config_http_integration_test.go）。

// TestPoolRecycleOverdue 超期判断：
//   - last_follow_time 距今超过 N 天 → 超期；未超过 → 未超期；
//   - last_follow_time 为 NULL（从未跟进）→ 回退 created_at 计算
//     （规格验收：从未跟进的新客户按创建时间参与回收）；
//   - 边界：恰好 N 天不算超期——「超过 N 天」取严格更早的语义。
func TestPoolRecycleOverdue(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	old := now.AddDate(0, 0, -31)   // 31 天前
	recent := now.AddDate(0, 0, -7) // 7 天前
	exact := now.AddDate(0, 0, -30) // 恰好 30 天（== cutoff）

	if !service.PoolRecycleOverdue(now, &old, recent, 30) {
		t.Fatal("last_follow_time 距今 31 天应超期")
	}
	if service.PoolRecycleOverdue(now, &recent, old, 30) {
		t.Fatal("last_follow_time 距今 7 天不应超期（即使创建很早）")
	}
	if service.PoolRecycleOverdue(now, &exact, old, 30) {
		t.Fatal("恰好 30 天不算「超过 30 天」，不应超期")
	}

	// NULL 回退：从未跟进的客户按 created_at 参与回收。
	if !service.PoolRecycleOverdue(now, nil, old, 30) {
		t.Fatal("从未跟进且创建于 31 天前应按 created_at 超期")
	}
	if service.PoolRecycleOverdue(now, nil, recent, 30) {
		t.Fatal("从未跟进但刚创建不应超期")
	}

	// 配置天数生效：days=7 时 31 天前的跟进超期、7 天前恰好边界不超期。
	if !service.PoolRecycleOverdue(now, &old, recent, 7) {
		t.Fatal("按 7 天配置，31 天前应超期")
	}
	if service.PoolRecycleOverdue(now, &recent, old, 7) {
		t.Fatal("按 7 天配置，恰好 7 天不算超期")
	}
}

// fakeContractChecker 是 ContractChecker 的内存实现：map 决定哪些客户
// 存在「未作废合同」。map 未命中的客户即「无合同或有已作废合同」。
type fakeContractChecker map[int64]bool

func (f fakeContractChecker) ActiveContractCustomerIDs(ctx context.Context, customerIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(customerIDs))
	for _, id := range customerIDs {
		if f[id] {
			out[id] = true
		}
	}
	return out, nil
}

// candidates 构造候选客户行：lastFollow/createdAt 为nil 时落 NULL 语义。
func recycleCandidate(id int64, lastFollow, createdAt time.Time) model.Customer {
	c := model.Customer{TenantID: 1, Name: "候选客户"}
	c.ID = id
	if !createdAt.IsZero() {
		c.CreatedAt = createdAt
	}
	if !lastFollow.IsZero() {
		c.LastFollowTime = &lastFollow
	}
	c.OwnerUserID = 1
	return c
}

// TestSelectRecyclableCustomers 回收筛选（合同排除分支，规格验收：
// 排除有未作废合同的客户）：
//   - 超期且无合同 → 选中；
//   - 有未作废合同（fake 命中）→ 排除；
//   - 只有已作废合同（fake 不命中）→ 不排除，照常选中；
//   - 未超期 → 不选中（即使无合同）；
//   - 从未跟进（last_follow_time NULL）→ 按 created_at 判断。
func TestSelectRecyclableCustomers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	old := now.AddDate(0, 0, -40)
	recent := now.AddDate(0, 0, -1)

	candidates := []model.Customer{
		recycleCandidate(1, old, recent),      // 超期、无合同 → 回收
		recycleCandidate(2, old, recent),      // 超期、有未作废合同 → 排除
		recycleCandidate(3, old, recent),      // 超期、仅已作废合同（fake 不命中）→ 回收
		recycleCandidate(4, recent, old),      // 刚跟进 → 不回收
		recycleCandidate(5, time.Time{}, old), // 从未跟进（零值哨兵→NULL）、创建已久 → 回收
	}
	checker := fakeContractChecker{2: true}

	selected, err := service.SelectRecyclableCustomers(ctx, candidates, checker, now, 30)
	if err != nil {
		t.Fatalf("筛选不应报错: %v", err)
	}
	ids := map[int64]bool{}
	for _, c := range selected {
		ids[c.ID] = true
	}
	if len(selected) != 3 || !ids[1] || !ids[3] || !ids[5] {
		t.Fatalf("应选中 1/3/5（超期无合同 + 已作废合同 + NULL 回退），实际 %v", ids)
	}
	if ids[2] {
		t.Fatal("有未作废合同的客户应被排除")
	}
	if ids[4] {
		t.Fatal("刚跟进的客户不应回收")
	}
}

// TestSelectRecyclableCustomersEmptyChecker 空候选不触发 checker 查询
// （避免 #16 接入真实实现后对空 IN 列表的查询）。
func TestSelectRecyclableCustomersEmptyChecker(t *testing.T) {
	called := false
	checker := service.ContractCheckerFunc(func(ctx context.Context, ids []int64) (map[int64]bool, error) {
		called = true
		return map[int64]bool{}, nil
	})
	selected, err := service.SelectRecyclableCustomers(
		context.Background(), nil, checker, time.Now(), 30)
	if err != nil {
		t.Fatalf("空候选不应报错: %v", err)
	}
	if len(selected) != 0 {
		t.Fatalf("空候选应返回空集，实际 %d", len(selected))
	}
	if called {
		t.Fatal("空候选不应调用 checker")
	}
}
