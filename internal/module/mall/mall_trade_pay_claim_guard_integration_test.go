//go:build integration

// mall 支付联动「应用 + 金额」绑定回归测试（run-11 安全审计 C2 MEDIUM 修复
// 回归，照审计探针 probe-b 手法）：
//
//	HandlePaySuccess 修复前只凭 pay_order.merchant_order_id=SO 号定位 mall
//	订单、守卫只有 status=0——pay 的建单/发起面只挂登录且幂等键是
//	(app_id, merchant_order_id)，攻击者在自建 mock 应用下用受害者的 SO 号
//	以 ¥0.01 建单、本人发起、mock 回调成功即可把受害者的待支付商城订单
//	低价认领为已支付并全额入账积分（探针 B 实证）。修复后定位键升级为
//	「应用（app_key=mall）+ 单号 + 金额」三元一致（见 trade_pay.go
//	HandlePaySuccess 步骤 1.5），跨应用/金额不符一律拒绝联动、整笔支付认领
//	事务回滚。
package mall_test

import (
	"context"
	"net/http"
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// claimGuardReadPayOrderByID 按 ID 直查 pay 单（跨应用同号场景不能按
// merchant_order_id 读——会撞到攻击者的低价单）。
func claimGuardReadPayOrderByID(t *testing.T, env *testutil.IntegrationEnv, orderID int64) paymodel.Order {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var row paymodel.Order
	if err := env.DB.WithContext(ctx).First(&row, "id = ?", orderID).Error; err != nil {
		t.Fatalf("读 pay_order %d: %v", orderID, err)
	}
	return row
}

// claimGuardAdminID 读内置管理员 ID（probe-b 攻击主体：pay 建单/发起只挂
// 登录，admin 令牌即可走全链）。
func claimGuardAdminID(t *testing.T, env *testutil.IntegrationEnv) int64 {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var row sysmodel.User
	if err := env.DB.WithContext(ctx).First(&row, "username = ?", "admin").Error; err != nil {
		t.Fatalf("读 admin 用户: %v", err)
	}
	return row.ID
}

// TestIntegrationTradePayCrossAppClaimRejected 跨应用同号低价单回调（probe-b
// 链）：攻击者自建 mock 应用 → 以受害者 SO 号 ¥0.01 建单 → 本人发起 → 模拟
// 回调 → 联动拒绝整笔回滚：mall 订单不被认领（留待支付、pay_order_id 仍指
// mall 应用原单）、积分零流水、攻击者 pay 单留待支付；合法 mall 应用 mock
// 链路不受影响（同单随后真实支付成功）。
func TestIntegrationTradePayCrossAppClaimRejected(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	memberID, memberToken := payTestEnvMember(t, env, adminToken, "13891008001")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "认领守卫商品", "30.00", 5)
	addressID := tradeInsertAddress(t, env, memberID, "受害会员")

	// 受害者真实下单（¥30 待支付，下单即挂 mall 应用 pay 单）。
	tradeAddCart(t, env, memberToken, skuID, 1)
	order := tradeCreateOrder(t, env, memberToken, addressID, "受害订单")
	orderID := int64(order["id"].(float64))
	soNo := order["no"].(string)
	mallPayOrderID := payReadMallOrder(t, env, orderID).PayOrderID
	if mallPayOrderID == 0 {
		t.Fatalf("下单后 mall 订单应已记回 pay 单")
	}

	// 攻击链（probe-b 同款 HTTP 手法，admin 主体——pay 建单/发起只挂登录）：
	// 自建应用绑定 mock → 同 SO 号 ¥0.01 建单（换应用即新单）→ 本人发起。
	status, api := env.Request(t, http.MethodPost, "/pay/app", adminToken, map[string]any{
		"appKey": "claimprobe", "name": "认领守卫探针应用",
	})
	mustOK(t, "攻击者建应用", status, api)
	attackAppID := int64(parseJSON(t, string(api.Data))["id"].(float64))
	status, api = env.Request(t, http.MethodPut, "/pay/app/"+itoa(attackAppID)+"/channels", adminToken,
		map[string]any{"channelCodes": []string{"mock"}})
	mustOK(t, "攻击者绑定 mock", status, api)
	status, api = env.Request(t, http.MethodPost, "/pay/order/create", adminToken, map[string]any{
		"appKey": "claimprobe", "merchantOrderId": soNo,
		"subject": "probe underpay", "price": "0.01",
	})
	mustOK(t, "攻击者同号低价建单", status, api)
	attackOrderID := int64(parseJSON(t, string(api.Data))["id"].(float64))
	status, api = env.Request(t, http.MethodPost, "/pay/order/submit", adminToken,
		map[string]any{"orderId": attackOrderID, "channelCode": "mock"})
	mustOK(t, "攻击者发起支付", status, api)

	// 攻击单 sanity：挂攻击者应用、金额 ¥0.01、与 mall 应用原单同号并存。
	attackOrder := claimGuardReadPayOrderByID(t, env, attackOrderID)
	if attackOrder.AppID != attackAppID || attackOrder.Price.Cents() != 1 {
		t.Fatalf("攻击单应挂自建应用 %d 且金额 0.01，实际 appID=%d price=%s",
			attackAppID, attackOrder.AppID, attackOrder.Price)
	}

	// 模拟回调：联动拒绝（应用绑定不符）→ 整笔支付认领事务回滚 → 回调报错。
	payMockNotify(t, env, claimGuardAdminID(t, env), attackOrderID, "success", true)

	// 资金/状态面断言：mall 订单不被认领（待支付、pay_order_id 仍指 mall
	// 应用原单而非攻击单）、积分零流水、攻击单留待支付。
	mallRow := payReadMallOrder(t, env, orderID)
	if mallRow.Status != mallmodel.TradeOrderStatusPending {
		t.Fatalf("跨应用低价回调后 mall 订单应留待支付，实际 %d", mallRow.Status)
	}
	if mallRow.PayOrderID != mallPayOrderID || mallRow.PayOrderID == attackOrderID {
		t.Fatalf("mall 订单 pay_order_id 应保持 mall 应用原单 %d，实际 %d",
			mallPayOrderID, mallRow.PayOrderID)
	}
	if got := claimGuardReadPayOrderByID(t, env, attackOrderID); got.Status != paymodel.OrderStatusPending {
		t.Fatalf("攻击单应留待支付（认领回滚），实际 %d", got.Status)
	}
	if payCountPointRecords(t, env, memberID) != 0 || payMemberPointBalance(t, env, memberID) != 0 {
		t.Fatalf("积分应零流水零入账")
	}
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 4 {
		t.Fatalf("库存应保持下单扣减后的 4（认领回滚不回补），实际 %d", got)
	}

	// 无误伤：受害订单走 mall 应用合法 mock 链路照常支付成功 + 积分按实付入账。
	submit := paySubmitOrderPay(t, env, memberToken, orderID, "mock", http.StatusOK)
	if int8(submit["status"].(float64)) != 0 {
		t.Fatalf("mall 应用 mock 发起应 status=0：%s", submit)
	}
	payMockNotify(t, env, memberID, mallPayOrderID, "success", false)
	mallRow = payReadMallOrder(t, env, orderID)
	if mallRow.Status != mallmodel.TradeOrderStatusPaid || mallRow.PayOrderID != mallPayOrderID {
		t.Fatalf("合法链路应正常支付：status=%d payOrderID=%d", mallRow.Status, mallRow.PayOrderID)
	}
	if got := payMemberPointBalance(t, env, memberID); got != 30 {
		t.Fatalf("合法支付后积分应入账 30（¥30.00），实际 %d", got)
	}
}
