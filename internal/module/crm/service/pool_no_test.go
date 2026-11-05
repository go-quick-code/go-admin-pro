package service_test

import (
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// 公海纯函数（issue #13，docs/crm-mvp-spec.md §4.2）：
// 本文件只测不连库的上限判断与操作类型词汇表；
// 释放/认领/转移的全链路与并发行为由集成测试验证。

// TestCheckClaimLimit 认领上限（规格 §4.2：名下未删除客户总数，不区分是否
// 成交；#14 起上限值来自租户配置，缺省仍为 50）：名下数量达上限即拒绝
// （认领后总数不得超过上限），错误文案可读并带本次生效的上限值。
// 默认值行为（50）与 #13 保持不变；租户改配后按新值生效。
func TestCheckClaimLimit(t *testing.T) {
	// 默认口径：上限 50——49 家可再认领 1 家（认领后恰为 50），50 家起拒绝。
	for _, owned := range []int64{0, 1, 49} {
		if err := service.CheckClaimLimit(owned, service.DefaultClaimLimit); err != nil {
			t.Fatalf("名下 %d 家应可认领，实际报错 %v", owned, err)
		}
	}
	for _, owned := range []int64{50, 51, 1000} {
		err := service.CheckClaimLimit(owned, service.DefaultClaimLimit)
		if err == nil {
			t.Fatalf("名下 %d 家应达到认领上限被拒绝", owned)
		}
		if msg := err.Error(); !strings.Contains(msg, "50") {
			t.Fatalf("错误文案应带上限值 50，实际 %q", msg)
		}
	}

	// 租户配置生效：上限改小/改大后按配置值判断，文案带配置值。
	if err := service.CheckClaimLimit(5, 5); err == nil {
		t.Fatal("名下 5 家、上限 5 应拒绝")
	} else if msg := err.Error(); !strings.Contains(msg, "5") {
		t.Fatalf("错误文案应带配置的上限值 5，实际 %q", msg)
	}
	if err := service.CheckClaimLimit(5, 6); err != nil {
		t.Fatalf("名下 5 家、上限 6 应可认领，实际报错 %v", err)
	}
}

// TestDefaultClaimLimit 上限常量锁定为 50（规格缺省值）。
// #14 会改为读租户级配置 crm.pool.claim_limit，届时本断言随实现调整。
func TestDefaultClaimLimit(t *testing.T) {
	if service.DefaultClaimLimit != 50 {
		t.Fatalf("认领上限默认值应为 50，实际 %d", service.DefaultClaimLimit)
	}
}

// TestValidOwnerHistoryAction 负责人历史操作类型词汇表：
// v1 为 create/claim/release/transfer + recycle（#14 自动回收引入，
// 原断言将 recycle 视为非法，随 #14 落地调整，语义不变）。
func TestValidOwnerHistoryAction(t *testing.T) {
	for _, action := range []string{
		model.OwnerHistoryCreate, model.OwnerHistoryClaim,
		model.OwnerHistoryRelease, model.OwnerHistoryTransfer,
		model.OwnerHistoryRecycle,
	} {
		if !model.ValidOwnerHistoryAction(action) {
			t.Fatalf("操作类型 %q 应合法", action)
		}
	}
	for _, action := range []string{"", "CREATE", "unknown"} {
		if model.ValidOwnerHistoryAction(action) {
			t.Fatalf("操作类型 %q 应非法", action)
		}
	}
}
