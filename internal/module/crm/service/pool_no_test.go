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

// TestCheckClaimLimit 认领上限（规格 §4.2：名下未删除客户总数，默认 50，
// 不区分是否成交）：名下 49 家可再认领 1 家（认领后恰为 50），
// 名下 50 家及以上拒绝，错误文案可读并带上限值。
func TestCheckClaimLimit(t *testing.T) {
	for _, owned := range []int64{0, 1, 49} {
		if err := service.CheckClaimLimit(owned); err != nil {
			t.Fatalf("名下 %d 家应可认领，实际报错 %v", owned, err)
		}
	}
	for _, owned := range []int64{50, 51, 1000} {
		err := service.CheckClaimLimit(owned)
		if err == nil {
			t.Fatalf("名下 %d 家应达到认领上限被拒绝", owned)
		}
		if msg := err.Error(); !strings.Contains(msg, "50") {
			t.Fatalf("错误文案应带上限值 50，实际 %q", msg)
		}
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
// v1 为 create/claim/release/transfer；recycle 由 #14（自动回收）引入。
func TestValidOwnerHistoryAction(t *testing.T) {
	for _, action := range []string{
		model.OwnerHistoryCreate, model.OwnerHistoryClaim,
		model.OwnerHistoryRelease, model.OwnerHistoryTransfer,
	} {
		if !model.ValidOwnerHistoryAction(action) {
			t.Fatalf("操作类型 %q 应合法", action)
		}
	}
	for _, action := range []string{"", "recycle", "CREATE", "unknown"} {
		if model.ValidOwnerHistoryAction(action) {
			t.Fatalf("操作类型 %q 应非法", action)
		}
	}
}
