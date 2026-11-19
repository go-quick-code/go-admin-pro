//go:build integration

package mall_test

import (
	"net/http"
	"testing"
	"time"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// mall 超时关单 job 集成测试（issue #182，docs/mall-mvp-spec.md §1 第 7 条/
// §6 验收「超时关单 job——GAP 时钟缝同款拨钟手法」）：
//   - 拨 businessNow 跨 15 分钟阈值直调 job → 过期待支付单 40 已关闭
//    （close_time/close_reason="超时未支付" 落列）+ 库存回补；
//   - 未到期单不动（到期窗内不扫）；
//   - 已取消（30，终态无出边守卫语义）与已支付（10）单不动——即使时间上
//     已过期也不在候选集（WHERE status=0），库存不重复回补。
//
// 手法照 pay 拨钟测试（PR #158 审查修复 984b340 的教训）：env 建好即
// Scheduler.Stop() 停活本 env 调度器（种子 mallOrderTimeoutClose 每 5 分钟
// tick 会与拨钟窗抢任务；各 env 调度器独立，测试显式 Scheduler.Execute 直调
// 不受影响）；SetBusinessClock 用 swap+restore（t.Cleanup 复原 + 用例内显式
// 复原），防固定时钟泄漏到同包后续测试。

// TestIntegrationMallTradeTimeoutClose 超时关单 job 全矩阵：到期关单回补 +
// 未到期/已取消/已支付三类不动。
func TestIntegrationMallTradeTimeoutClose(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	// 停活调度器：防种子 job 的每 5 分钟 tick 抢任务（照 pay 拨钟测试）。
	env.App.Scheduler.Stop()
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "超时关单商品", "3.00", 10)

	memberID := tradeAdminCreateMember(t, env, adminToken, "13895006001")
	addressID := tradeInsertAddress(t, env, memberID, "超时用户")
	memberToken := tradeMemberToken(t, env, memberID)

	// 四笔订单（库存 10，共扣 5 → 5）：
	//   A 待支付 2 件：created_at = T0，拨钟后超时 → job 关闭 + 回补；
	//   B 待支付 1 件：created_at 直改至拨钟点前 5 分钟（到期窗内，未到期
	//     ——job 不动）；
	//   C 待支付 1 件：C 端真实取消（30，回补已发生）——时间上同样超时，
	//     守卫语义验证：终态无出边，job 不得扫到、不得二次回补；
	//   D 待支付 1 件：SQL 直改状态 10（已支付构造，票面指定手法）——
	//     时间上同样超时，job 不动、库存不回补。
	tradeAddCart(t, env, memberToken, skuID, 2)
	orderA := tradeCreateOrder(t, env, memberToken, addressID, "")
	idA := int64(orderA["id"].(float64))
	tradeAddCart(t, env, memberToken, skuID, 1)
	orderB := tradeCreateOrder(t, env, memberToken, addressID, "")
	idB := int64(orderB["id"].(float64))
	tradeAddCart(t, env, memberToken, skuID, 1)
	orderC := tradeCreateOrder(t, env, memberToken, addressID, "")
	idC := int64(orderC["id"].(float64))
	tradeAddCart(t, env, memberToken, skuID, 1)
	orderD := tradeCreateOrder(t, env, memberToken, addressID, "")
	idD := int64(orderD["id"].(float64))
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 5 {
		t.Fatalf("四笔下单（2+1+1+1）后库存应为 5，实际 %d", got)
	}

	// C 端真实取消守卫单（30，取消回补已发生：5→6）。
	status, api := env.Request(t, http.MethodPost, "/mall/app/trade/order/"+itoa(idC)+"/cancel", memberToken, map[string]any{"reason": "不需要了"})
	mustOK(t, "取消守卫单", status, api)
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 6 {
		t.Fatalf("取消回补后库存应为 6，实际 %d", got)
	}

	// D 直改已支付（10；已支付态的库存不回补）。
	if err := env.DB.Exec(
		"UPDATE trade_order SET status = ? WHERE id = ?", mallmodel.TradeOrderStatusPaid, idD,
	).Error; err != nil {
		t.Fatalf("直改 D 状态: %v", err)
	}

	// 拨钟：businessNow = T0+16min（阈值 15min）。A/C/D created_at = T0 →
	// 超时候选；B created_at 直改至 T0+11min（拨钟点前 5 分钟龄 → 未到期）。
	dial := time.Now().Add(16 * time.Minute)
	prev := mallservice.SetBusinessClock(func() time.Time { return dial })
	t.Cleanup(func() { mallservice.SetBusinessClock(prev) })
	if err := env.DB.Exec(
		"UPDATE trade_order SET created_at = ? WHERE id = ?", dial.Add(-5*time.Minute), idB,
	).Error; err != nil {
		t.Fatalf("直改 B created_at: %v", err)
	}

	// 直调 job（平台租户上下文；系统级 handler）。
	env.App.Scheduler.Execute(inframodel.Job{TenantID: 1, HandlerName: mallservice.OrderTimeoutCloseJobHandlerName})
	// 复原时钟（防泄漏同包后续测试；t.Cleanup 兜底双保险）。
	mallservice.SetBusinessClock(prev)

	// A：40 已关闭 + close_time/close_reason 落列 + 库存回补（6→8）。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/"+itoa(idA), memberToken, nil)
	mustOK(t, "A 详情", status, api)
	detailA := parseJSON(t, string(api.Data))
	if detailA["status"] != float64(mallmodel.TradeOrderStatusClosed) {
		t.Fatalf("超时单应已关闭 40：%s", api.Data)
	}
	if detailA["closeReason"] != mallservice.OrderTimeoutCloseReason {
		t.Fatalf("close_reason 应为「超时未支付」：%s", api.Data)
	}
	if ct, _ := detailA["closeTime"].(string); ct == "" {
		t.Fatalf("close_time 应落列：%s", api.Data)
	}
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 8 {
		t.Fatalf("A 关单回补后库存应为 8，实际 %d", got)
	}

	// B：未到期不动（仍待支付 0）。
	assertTradeOrderStatus(t, env, memberToken, idB, mallmodel.TradeOrderStatusPending, "未到期单")
	// C：已取消守卫——终态无出边，job 不得扫到（30 不变）。
	assertTradeOrderStatus(t, env, memberToken, idC, mallmodel.TradeOrderStatusCanceled, "已取消守卫单")
	// D：已支付不动（10 不变）。
	assertTradeOrderStatus(t, env, memberToken, idD, mallmodel.TradeOrderStatusPaid, "已支付单")
	// 终态库存：无重复回补（若 job 误扫 C/D 或关闭 B，库存会 ≠ 8）。
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 8 {
		t.Fatalf("终态库存应保持 8（无重复回补），实际 %d", got)
	}
}

// assertTradeOrderStatus 断言订单当前状态（C 端详情回读）。
func assertTradeOrderStatus(t *testing.T, env *testutil.IntegrationEnv, memberToken string, orderID int64, want int8, phase string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/mall/app/trade/order/"+itoa(orderID), memberToken, nil)
	mustOK(t, phase+" 详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if detail["status"] != float64(want) {
		t.Fatalf("%s状态应保持 %d：%s", phase, want, api.Data)
	}
}
