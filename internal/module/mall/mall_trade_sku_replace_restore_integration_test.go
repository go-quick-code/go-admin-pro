//go:build integration

// mall 库存回补 × SPU 全量替换回归测试（PR #184 审查 P1 修复回归）：
//
//	UpdateProductSPU 对 SKU 集做全量替换（每次保存都软删全部旧 SKU，无
//	「被订单引用禁改」守卫）。修复前 RestoreSKUStock 的 WHERE 带
//	`deleted_at IS NULL`，在途订单引用的旧 SKU 被软删后回补恒 0 行 →
//	C 端/管理端取消永久 409「商品库存回补失败」、超时关单 job 中断——
//	在途订单永久卡死。修复后回补是簿记动作不过滤软删（按 id+tenant 定位，
//	见 repository/trade_stock.go RestoreSKUStock 注释）。
//
// 覆盖三条失效链的回归：
//  1. SPU 保存（全量替换软删旧 SKU）→ C 端取消成功，库存回补命中软删行；
//  2. 同链走超时关单 job（拨钟，照 TestIntegrationMallTradeTimeoutClose 手法）
//     ——关单回补同样命中软删行；
//  3. closeTimeoutOrders 单单失败聚合 continue：毒订单（订单项 SKU 指向
//     不存在行，回补恒 0 行）不再中断同租户后续订单的关单。
package mall_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// skuReplaceSPUCategoryID 读 SPU 详情的归属分类 ID（保存请求必填，创建时
// 的 categoryId 需回读）。
func skuReplaceSPUCategoryID(t *testing.T, env *testutil.IntegrationEnv, adminToken string, spuID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/mall/product/spu/"+itoa(spuID), adminToken, nil)
	mustOK(t, "SPU 详情（读分类归属）", status, api)
	detail := parseJSON(t, string(api.Data))
	return int64(detail["categoryId"].(float64))
}

// skuReplaceSaveSPU 管理端保存 SPU（PUT /mall/product/spu，全量覆盖 +
// SKU 集全量替换）：改名保存即触发旧 SKU 软删（前端 spu.vue 保存即全量
// 替换的同款链路）。新 SKU 价格 1.00/库存 1（新行独立，不影响旧行断言）。
func skuReplaceSaveSPU(t *testing.T, env *testutil.IntegrationEnv, adminToken string, spuID int64, categoryID int64, name string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, "/mall/product/spu", adminToken, map[string]any{
		"id": spuID, "categoryId": categoryID, "name": name,
		"picUrl":   "https://cdn.example.com/" + name + ".png",
		"specType": 0, "status": 1,
		"skus": []map[string]any{
			{"properties": []map[string]any{}, "price": "1.00", "stock": 1, "first": true},
		},
	})
	mustOK(t, "管理端保存 SPU（SKU 全量替换）", status, api)
}

// skuReplaceRawSKU 按 ID 直查 SKU 行（Unscoped 含软删，断言回补落点）。
func skuReplaceRawSKU(t *testing.T, env *testutil.IntegrationEnv, skuID int64) mallmodel.ProductSKU {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var row mallmodel.ProductSKU
	if err := env.DB.WithContext(ctx).Unscoped().First(&row, "id = ?", skuID).Error; err != nil {
		t.Fatalf("Unscoped 读 product_sku %d: %v", skuID, err)
	}
	return row
}

// TestIntegrationTradeCancelAfterSKUReplace 失效链 1：下单扣库存 → 管理端
// 保存 SPU（全量替换软删旧 SKU）→ C 端取消必须成功且库存回补命中软删行。
func TestIntegrationTradeCancelAfterSKUReplace(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "替换回补商品", "5.00", 10)
	memberID := tradeAdminCreateMember(t, env, adminToken, "13897001001")
	addressID := tradeInsertAddress(t, env, memberID, "替换回补用户")
	memberToken := tradeMemberToken(t, env, memberID)

	// 下单扣库存：10 → 8。
	tradeAddCart(t, env, memberToken, skuID, 2)
	order := tradeCreateOrder(t, env, memberToken, addressID, "")
	orderID := int64(order["id"].(float64))
	if got := skuReplaceRawSKU(t, env, skuID).Stock; got != 8 {
		t.Fatalf("下单后旧 SKU 库存应为 8，实际 %d", got)
	}

	// 管理端保存 SPU（改名保存，触发 SKU 全量替换：旧 SKU 软删 + 新 SKU 落库）。
	categoryID := skuReplaceSPUCategoryID(t, env, adminToken, spuID)
	skuReplaceSaveSPU(t, env, adminToken, spuID, categoryID, "替换回补商品-改")
	oldSKU := skuReplaceRawSKU(t, env, skuID)
	if !oldSKU.DeletedAt.Valid {
		t.Fatalf("保存 SPU 后旧 SKU 应被软删（全量替换），实际未删")
	}

	// C 端取消：修复前在此 409「商品库存回补失败」且永远无法取消；修复后
	// 回补命中软删行，取消成功。
	status, api := env.Request(t, http.MethodPost, "/mall/app/trade/order/"+itoa(orderID)+"/cancel",
		memberToken, map[string]any{"reason": "不想要了"})
	mustOK(t, "取消订单（回补命中软删行）", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/"+itoa(orderID), memberToken, nil)
	mustOK(t, "订单详情", status, api)
	if parseJSON(t, string(api.Data))["status"] != float64(mallmodel.TradeOrderStatusCanceled) {
		t.Fatalf("取消后订单应 30 已取消：%s", api.Data)
	}
	// 回补落点断言：软删旧 SKU 行库存 8 → 10（+2）。
	if got := skuReplaceRawSKU(t, env, skuID).Stock; got != 10 {
		t.Fatalf("软删旧 SKU 库存应回补至 10，实际 %d", got)
	}
}

// TestIntegrationTradeTimeoutAfterSKUReplace 失效链 2：同链走超时关单 job
// （拨钟跨 15 分钟阈值直调，照 TestIntegrationMallTradeTimeoutClose 手法）
// ——关单 + 回补命中软删行。
func TestIntegrationTradeTimeoutAfterSKUReplace(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	// 停活调度器：防种子 job 的每 5 分钟 tick 抢任务（照拨钟测试）。
	env.App.Scheduler.Stop()
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "替换超时商品", "3.00", 5)
	memberID := tradeAdminCreateMember(t, env, adminToken, "13897001002")
	addressID := tradeInsertAddress(t, env, memberID, "替换超时用户")
	memberToken := tradeMemberToken(t, env, memberID)

	// 下单扣库存：5 → 4。
	tradeAddCart(t, env, memberToken, skuID, 1)
	order := tradeCreateOrder(t, env, memberToken, addressID, "")
	orderID := int64(order["id"].(float64))

	// 管理端保存 SPU（改名保存触发全量替换）。
	categoryID := skuReplaceSPUCategoryID(t, env, adminToken, spuID)
	skuReplaceSaveSPU(t, env, adminToken, spuID, categoryID, "替换超时商品-改")
	if !(skuReplaceRawSKU(t, env, skuID)).DeletedAt.Valid {
		t.Fatalf("保存 SPU 后旧 SKU 应被软删（全量替换）")
	}

	// 拨钟 businessNow = T0+16min（阈值 15min）→ 直调 job。
	dial := time.Now().Add(16 * time.Minute)
	prev := mallservice.SetBusinessClock(func() time.Time { return dial })
	t.Cleanup(func() { mallservice.SetBusinessClock(prev) })
	env.App.Scheduler.Execute(inframodel.Job{TenantID: 1, HandlerName: mallservice.OrderTimeoutCloseJobHandlerName})
	mallservice.SetBusinessClock(prev)

	// 订单 40 已关闭；软删旧 SKU 行库存 4 → 5（回补命中软删行）。
	assertTradeOrderStatus(t, env, memberToken, orderID, mallmodel.TradeOrderStatusClosed, "超时关单（软删 SKU）")
	if got := skuReplaceRawSKU(t, env, skuID).Stock; got != 5 {
		t.Fatalf("软删旧 SKU 库存应回补至 5，实际 %d", got)
	}
}

// TestIntegrationTradeTimeoutPoisonOrderIsolated 失效链 3：单单失败聚合
// continue——毒订单（订单项 SKU 指向不存在行，回补恒 0 行失败）不再中断
// 本轮同租户后续订单的关单（修复前首个失败单 return，同租户 id 更大的
// 待支付订单永不关单、库存永久占用）。
func TestIntegrationTradeTimeoutPoisonOrderIsolated(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	env.App.Scheduler.Stop()
	adminToken := env.Login(t, "admin", "123456")
	_, skuID := tradeCreateSaleableSPU(t, env, adminToken, "毒单隔离商品", "3.00", 10)
	memberID := tradeAdminCreateMember(t, env, adminToken, "13897001003")
	addressID := tradeInsertAddress(t, env, memberID, "毒单隔离用户")
	memberToken := tradeMemberToken(t, env, memberID)

	// 两笔待支付订单：P（毒，id 小）与 Q（正常，id 大）。P 的订单项 SKU
	// 指向不存在的行（SQL 直改，构造回补恒 0 行——任何实现都无法回补）。
	tradeAddCart(t, env, memberToken, skuID, 1)
	orderP := tradeCreateOrder(t, env, memberToken, addressID, "毒单")
	idP := int64(orderP["id"].(float64))
	tradeAddCart(t, env, memberToken, skuID, 1)
	orderQ := tradeCreateOrder(t, env, memberToken, addressID, "正常单")
	idQ := int64(orderQ["id"].(float64))
	if idP >= idQ {
		t.Fatalf("毒订单 P 应先建（id 更小）：%d/%d", idP, idQ)
	}
	if err := env.DB.Exec(
		"UPDATE trade_order_item SET sku_id = ? WHERE order_id = ?", int64(987654321), idP,
	).Error; err != nil {
		t.Fatalf("构造毒订单（订单项 SKU 指向不存在行）: %v", err)
	}

	// 拨钟 + 直调 job：P 回补失败（聚合记错继续），Q 必须仍被关单。
	dial := time.Now().Add(16 * time.Minute)
	prev := mallservice.SetBusinessClock(func() time.Time { return dial })
	t.Cleanup(func() { mallservice.SetBusinessClock(prev) })
	env.App.Scheduler.Execute(inframodel.Job{TenantID: 1, HandlerName: mallservice.OrderTimeoutCloseJobHandlerName})
	mallservice.SetBusinessClock(prev)

	// Q：正常单必须被关闭（修复前被毒订单 P 中断，永不关单）+ 库存回补
	//（10 − 2 + 1 = 9；P 未回补且其订单项 SKU 不可回补）。
	assertTradeOrderStatus(t, env, memberToken, idQ, mallmodel.TradeOrderStatusClosed, "毒单后的正常单")
	if got := tradeSKUStock(t, env, adminToken, skuID); got != 9 {
		t.Fatalf("Q 关单回补后库存应 9，实际 %d", got)
	}
	// P：毒订单本轮失败留待支付（下轮重试，聚合不吞单）。
	assertTradeOrderStatus(t, env, memberToken, idP, mallmodel.TradeOrderStatusPending, "毒订单 P")
}
