//go:build integration

// mall 支付联动 HTTP 集成测试（issue #181，T8；docs/mall-mvp-spec.md §1
// 第 4 条/§3.4/§6「支付联动（mock 回调 → 订单已支付 + 积分入账 + 库存已扣）」）：
//   - mock 渠道全链：下单（断言 pay 单已建、merchant_order_id=SO 号、
//     payOrderId 记回）→ 发起支付（pay 单幂等复用）→ 模拟回调成功（照 pay
//     demo 回调测试手法——paySvc.MockNotifyOrder 服务入口，与 HTTP 端点同一
//     代码路径）→ mall 订单 10 已支付 + pay 四列回填 + 积分按实付入账
//     （point_per_yuan=1 默认，整数元）+ 库存已扣；
//   - wallet 渠道扣款链：钱包预充值（paySvc.ApplyWalletChange 唯一入账入口）
//     → 发起支付同步成功 → 余额扣减 + mall 已支付；余额不足 409 透传；
//   - 发起支付幂等：重复发起同 pay 单（pay_order 恰 1 笔、extension 递增）；
//   - 取消后发起支付 409 + 守卫回滚：取消后模拟支付成功回调 → 联动拒绝 →
//     整笔支付认领事务回滚（pay 单回未支付、积分无入账、mall 订单留已取消）
//     ——残留待支付 pay 单由 pay 超时关单 job 收口（窗口分析见
//     service/trade_pay.go HandlePaySuccess 守卫注释）；
//   - 支付后不可重复支付 409、重复回调不双入账。
//
// 限速桶核对（tokenGroup 60 次/分钟，每用例独立 env 即独立桶）：各用例
// 令牌端点请求 ≤ 20 次，超限会以 429 假红。
package mall_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	paydto "github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ---------- 助手 ----------

// payTestEnvMember 建会员并直构令牌（绕注册/登录限速桶），返回会员 ID + 令牌。
func payTestEnvMember(t *testing.T, env *testutil.IntegrationEnv, adminToken, mobile string) (int64, string) {
	t.Helper()
	memberID := tradeAdminCreateMember(t, env, adminToken, mobile)
	return memberID, tradeMemberToken(t, env, memberID)
}

// payTenantCtx 构造会员委托主体的服务级 ctx（pay MockNotifyOrder 的「本人
// 订单」校验与租户插件取值源，同 memberAuth 双注入口径）。
func payTenantCtx(memberID, tenantID int64) context.Context {
	ctx := authn.With(context.Background(), authn.Principal{
		UserID: memberID, TenantID: tenantID, Username: "mall:member:test",
	})
	return tenant.WithTenantID(ctx, tenantID)
}

// payFindPayOrder 按 merchant_order_id 读 pay 单（不存在 Fatal）。
func payFindPayOrder(t *testing.T, env *testutil.IntegrationEnv, soNo string) paymodel.Order {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var row paymodel.Order
	if err := env.DB.WithContext(ctx).First(&row, "merchant_order_id = ?", soNo).Error; err != nil {
		t.Fatalf("读 pay_order（merchant_order_id=%s）: %v", soNo, err)
	}
	return row
}

// payCountPayOrders 统计同 merchant_order_id 的 pay 单数（幂等复用断言）。
func payCountPayOrders(t *testing.T, env *testutil.IntegrationEnv, soNo string) int64 {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var count int64
	if err := env.DB.WithContext(ctx).Model(&paymodel.Order{}).
		Where("merchant_order_id = ?", soNo).Count(&count).Error; err != nil {
		t.Fatalf("统计 pay_order: %v", err)
	}
	return count
}

// payCountExtensions 统计 pay 单的发起 extension 数。
func payCountExtensions(t *testing.T, env *testutil.IntegrationEnv, payOrderID int64) int64 {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var count int64
	if err := env.DB.WithContext(ctx).Model(&paymodel.OrderExtension{}).
		Where("order_id = ?", payOrderID).Count(&count).Error; err != nil {
		t.Fatalf("统计 pay_order_extension: %v", err)
	}
	return count
}

// payReadMallOrder 读 mall 订单行（原始行断言 pay 四列/状态）。
func payReadMallOrder(t *testing.T, env *testutil.IntegrationEnv, orderID int64) mallmodel.TradeOrder {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var row mallmodel.TradeOrder
	if err := env.DB.WithContext(ctx).First(&row, "id = ?", orderID).Error; err != nil {
		t.Fatalf("读 trade_order %d: %v", orderID, err)
	}
	return row
}

// payMemberPointBalance 读会员积分余额。
func payMemberPointBalance(t *testing.T, env *testutil.IntegrationEnv, memberID int64) int {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var row mallmodel.MemberUser
	if err := env.DB.WithContext(ctx).First(&row, "id = ?", memberID).Error; err != nil {
		t.Fatalf("读 member_user %d: %v", memberID, err)
	}
	return row.PointBalance
}

// payCountPointRecords 统计会员积分流水数（守卫回滚断言：无新增）。
func payCountPointRecords(t *testing.T, env *testutil.IntegrationEnv, memberID int64) int64 {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var count int64
	if err := env.DB.WithContext(ctx).Model(&mallmodel.MemberPointRecord{}).
		Where("user_id = ?", memberID).Count(&count).Error; err != nil {
		t.Fatalf("统计 member_point_record: %v", err)
	}
	return count
}

// payReadWallet 读 (userID, userType=admin) 钱包行（不存在 Fatal）。
func payReadWallet(t *testing.T, env *testutil.IntegrationEnv, userID int64) paymodel.Wallet {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var row paymodel.Wallet
	if err := env.DB.WithContext(ctx).First(&row, "user_id = ? AND user_type = ?",
		userID, paymodel.WalletUserTypeAdmin).Error; err != nil {
		t.Fatalf("读 pay_wallet（user_id=%d）: %v", userID, err)
	}
	return row
}

// payRechargeWallet 直调唯一入账入口给会员钱包充值（照 pay wallet 测试先例
// ——wallet 渠道扣的是 pay 钱包（user_type=admin 落点），不是 mall 积分）。
func payRechargeWallet(t *testing.T, env *testutil.IntegrationEnv, memberID int64, cents int64) {
	t.Helper()
	_, err := env.App.Pay.ApplyWalletChange(payTenantCtx(memberID, tenant.DefaultID), payservice.WalletChange{
		UserID:   memberID,
		UserType: paymodel.WalletUserTypeAdmin,
		BizType:  paymodel.WalletBizRecharge,
		BizID:    0,
		Title:    "测试充值",
		Price:    money.FromCents(cents),
	})
	if err != nil {
		t.Fatalf("钱包充值: %v", err)
	}
}

// paySubmit 发起支付（断言 HTTP 状态与业务码，返回响应 JSON）。
func paySubmitOrderPay(t *testing.T, env *testutil.IntegrationEnv, memberToken string, orderID int64, channelCode string, wantStatus int) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost,
		"/mall/app/trade/order/"+itoa(orderID)+"/pay", memberToken,
		map[string]any{"channelCode": channelCode})
	if status != wantStatus {
		t.Fatalf("发起支付：HTTP %d code %d msg %q，期望 HTTP %d", status, api.Code, api.Msg, wantStatus)
	}
	return parseJSON(t, string(api.Data))
}

// payMockNotify 触发 mock 模拟回调（paySvc.MockNotifyOrder 服务入口——与
// POST /pay/mock/notify HTTP 端点同一代码路径；pay_order.user_id 是会员
// 委托主体，代付校验天然通过）。wantErr 为 true 时断言返回错误。
func payMockNotify(t *testing.T, env *testutil.IntegrationEnv, memberID, payOrderID int64, result string, wantErr bool) {
	t.Helper()
	_, err := env.App.Pay.MockNotifyOrder(payTenantCtx(memberID, tenant.DefaultID),
		&paydto.MockNotifyReq{OrderID: payOrderID, Result: result})
	if wantErr && err == nil {
		t.Fatalf("模拟回调应失败（订单 %d result=%s），实际成功", payOrderID, result)
	}
	if !wantErr && err != nil {
		t.Fatalf("模拟回调失败：订单 %d result=%s err=%v", payOrderID, result, err)
	}
}

// ---------- mock 渠道全链（下单建单 → 发起 → 回调 → 联动） ----------

// TestIntegrationTradePayMockFullChain mock 渠道全链（spec §6 支付联进行）：
// 下单建 pay 单（merchant_order_id=SO、payOrderId 记回）→ 发起支付（幂等
// 复用同 pay 单）→ 模拟回调 → 订单已支付 + pay 四列 + 积分按实付入账
// （¥39.80 → 39 分，point_per_yuan=1 默认整数元）+ 库存已扣；支付后重复
// 发起 409、重复回调不双入账。
func TestIntegrationTradePayMockFullChain(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	memberID, memberToken := payTestEnvMember(t, env, adminToken, "13891007001")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "支付联动商品", "19.90", 5)
	addressID := tradeInsertAddress(t, env, memberID, "收货人")

	tradeAddCart(t, env, memberToken, skuID, 2)
	order := tradeCreateOrder(t, env, memberToken, addressID, "mock 全链")
	orderID := int64(order["id"].(float64))
	soNo, _ := order["no"].(string)
	if !strings.HasPrefix(soNo, "SO") {
		t.Fatalf("订单号应为 SO 前缀：%q", soNo)
	}

	// 下单即建支付单（不同事务）：pay 单已建、merchant_order_id=SO 号、
	// 待支付、金额=pay_money、归属 mall 应用（app_key=mall，种子 5011）、
	// user_id=会员（委托主体）、payOrderId 已记回 mall 订单。
	payOrder := payFindPayOrder(t, env, soNo)
	if payCountPayOrders(t, env, soNo) != 1 {
		t.Fatalf("下单后 pay 单应恰 1 笔")
	}
	if payOrder.Status != paymodel.OrderStatusPending {
		t.Fatalf("下单后 pay 单应待支付 0，实际 %d", payOrder.Status)
	}
	if payOrder.Price.Cents() != 3980 {
		t.Fatalf("pay 单金额应 39.80，实际 %s", payOrder.Price)
	}
	if payOrder.UserID != memberID {
		t.Fatalf("pay 单 user_id 应为会员 %d（委托主体），实际 %d", memberID, payOrder.UserID)
	}
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var appRow paymodel.App
	if err := env.DB.WithContext(ctx).First(&appRow, "id = ?", payOrder.AppID).Error; err != nil {
		t.Fatalf("读 pay_app %d: %v", payOrder.AppID, err)
	}
	if appRow.AppKey != mallservice.MallPayAppKey || appRow.ID != 5011 {
		t.Fatalf("pay 单应挂 mall 应用（app_key=%s id=5011），实际 app_key=%s id=%d",
			mallservice.MallPayAppKey, appRow.AppKey, appRow.ID)
	}
	mallRow := payReadMallOrder(t, env, orderID)
	if mallRow.PayOrderID != payOrder.ID || mallRow.PayOrderNo != payOrder.No {
		t.Fatalf("mall 订单应记回 pay 单 %d/%s，实际 %d/%s",
			payOrder.ID, payOrder.No, mallRow.PayOrderID, mallRow.PayOrderNo)
	}
	if mallRow.Status != mallmodel.TradeOrderStatusPending {
		t.Fatalf("下单后 mall 订单应待支付，实际 %d", mallRow.Status)
	}

	// 发起支付（mock）：订单停在待支付等回调。
	submit := paySubmitOrderPay(t, env, memberToken, orderID, "mock", http.StatusOK)
	if int8(submit["status"].(float64)) != 0 || submit["displayMode"] != "none" {
		t.Fatalf("mock 发起后应 status=0 displayMode=none：%s", submit)
	}
	if int64(submit["payOrderId"].(float64)) != payOrder.ID {
		t.Fatalf("发起支付应复用下单挂的 pay 单 %d，实际 %v", payOrder.ID, submit["payOrderId"])
	}
	if payCountPayOrders(t, env, soNo) != 1 || payCountExtensions(t, env, payOrder.ID) != 1 {
		t.Fatalf("首次发起后 pay 单应仍 1 笔、extension 1 条")
	}

	// 模拟回调成功（服务入口 = HTTP 端点同路径）：联动同事务置单 + 积分。
	payMockNotify(t, env, memberID, payOrder.ID, "success", false)
	mallRow = payReadMallOrder(t, env, orderID)
	if mallRow.Status != mallmodel.TradeOrderStatusPaid {
		t.Fatalf("回调后 mall 订单应已支付 10，实际 %d", mallRow.Status)
	}
	if mallRow.PayOrderID != payOrder.ID || mallRow.PayOrderNo != payOrder.No ||
		mallRow.PayChannelCode != "mock" || mallRow.PayTime == nil {
		t.Fatalf("回调后 pay 四列应回填：%d/%s/%s/%v",
			mallRow.PayOrderID, mallRow.PayOrderNo, mallRow.PayChannelCode, mallRow.PayTime)
	}
	if got := payMemberPointBalance(t, env, memberID); got != 39 {
		t.Fatalf("积分应按实付整数元入账 39（¥39.80 × point_per_yuan=1），实际 %d", got)
	}
	ctxPoints := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	var record mallmodel.MemberPointRecord
	if err := env.DB.WithContext(ctxPoints).First(&record,
		"user_id = ? AND biz_type = ?", memberID, mallmodel.MemberPointBizOrderPay).Error; err != nil {
		t.Fatalf("读积分流水: %v", err)
	}
	if record.Point != 39 || record.BizID != orderID || record.BalanceAfter != 39 {
		t.Fatalf("积分流水应 point=39 biz_id=%d balance_after=39，实际 %+v", orderID, record)
	}
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 3 {
		t.Fatalf("库存应已扣至 3（下单扣减），实际 %d", got)
	}

	// 支付后重复发起 409（状态守卫）；重复回调不双入账（无处理中发起）。
	paySubmitOrderPay(t, env, memberToken, orderID, "mock", http.StatusConflict)
	payMockNotify(t, env, memberID, payOrder.ID, "success", true)
	if got := payMemberPointBalance(t, env, memberID); got != 39 {
		t.Fatalf("重复回调不应双入账，积分仍应 39，实际 %d", got)
	}
	if payCountPointRecords(t, env, memberID) != 1 {
		t.Fatalf("重复回调不应新增流水")
	}
}

// ---------- wallet 渠道扣款链 ----------

// TestIntegrationTradePayWalletChannel wallet 渠道：预充值 → 发起支付同步
// 成功（联动同事务置单）→ 余额扣减；余额不足 409 透传、mall 订单留待支付。
func TestIntegrationTradePayWalletChannel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	memberID, memberToken := payTestEnvMember(t, env, adminToken, "13891007002")
	_, skuID := tradeCreateSaleableSPU(t, env, adminToken, "钱包扣款商品", "30.00", 5)
	addressID := tradeInsertAddress(t, env, memberID, "收货人")

	// 钱包预充值（pay 钱包唯一入账入口，余额 100）。
	payRechargeWallet(t, env, memberID, 10_000)

	tradeAddCart(t, env, memberToken, skuID, 1)
	order := tradeCreateOrder(t, env, memberToken, addressID, "wallet 全链")
	orderID := int64(order["id"].(float64))
	soNo := order["no"].(string)
	payFindPayOrder(t, env, soNo) // 下单即建单：wallet 发起前 pay 单已存在

	// wallet 发起：同步扣款成功即支付成功（联动 handler 同事务置单）。
	submit := paySubmitOrderPay(t, env, memberToken, orderID, "wallet", http.StatusOK)
	if int8(submit["status"].(float64)) != 10 {
		t.Fatalf("wallet 发起应同步成功 status=10：%s", submit)
	}
	mallRow := payReadMallOrder(t, env, orderID)
	if mallRow.Status != mallmodel.TradeOrderStatusPaid || mallRow.PayChannelCode != "wallet" ||
		mallRow.PayTime == nil {
		t.Fatalf("wallet 支付后 mall 订单应已支付（channel=wallet）：status=%d channel=%s",
			mallRow.Status, mallRow.PayChannelCode)
	}
	wallet := payReadWallet(t, env, memberID)
	if wallet.Balance.Cents() != 7000 {
		t.Fatalf("钱包余额应扣至 70.00，实际 %s", wallet.Balance)
	}
	if got := payMemberPointBalance(t, env, memberID); got != 30 {
		t.Fatalf("积分应入账 30（¥30.00），实际 %d", got)
	}

	// 余额不足：第二单 3 件 × ¥30 = ¥90 > 余额 ¥70 → wallet 发起 409 透传
	//（extension 回滚无部分扣款），mall 订单留待支付、钱包余额不变。
	tradeAddCart(t, env, memberToken, skuID, 3)
	order2 := tradeCreateOrder(t, env, memberToken, addressID, "余额不足")
	orderID2 := int64(order2["id"].(float64))
	soNo2 := order2["no"].(string)
	if payFindPayOrder(t, env, soNo2).Status != paymodel.OrderStatusPending {
		t.Fatalf("第二单 pay 单应已建且待支付")
	}
	status, api := env.Request(t, http.MethodPost,
		"/mall/app/trade/order/"+itoa(orderID2)+"/pay", memberToken,
		map[string]any{"channelCode": "wallet"})
	if status != http.StatusConflict || !strings.Contains(api.Msg, "余额不足") {
		t.Fatalf("余额不足应 409 透传，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	mallRow2 := payReadMallOrder(t, env, orderID2)
	if mallRow2.Status != mallmodel.TradeOrderStatusPending {
		t.Fatalf("余额不足后 mall 订单应留待支付，实际 %d", mallRow2.Status)
	}
	if got := payReadWallet(t, env, memberID).Balance.Cents(); got != 7000 {
		t.Fatalf("余额不足不应扣款，钱包应仍 70.00，实际 %d", got)
	}
}

// ---------- 发起支付幂等 + 取消守卫回滚 ----------

// TestIntegrationTradePayIdempotentAndCancelGuard 发起支付幂等（同 pay 单、
// extension 递增）与取消后守卫：取消后发起支付 409；取消后在途 pay 单的
// 成功回调被守卫拒绝——整笔支付认领事务回滚（pay 单回未支付、积分无入账、
// mall 订单留已取消），残留待支付 pay 单由 pay 超时关单 job 收口。
func TestIntegrationTradePayIdempotentAndCancelGuard(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	memberID, memberToken := payTestEnvMember(t, env, adminToken, "13891007003")
	_, skuID := tradeCreateSaleableSPU(t, env, adminToken, "取消守卫商品", "10.00", 2)
	addressID := tradeInsertAddress(t, env, memberID, "收货人")

	tradeAddCart(t, env, memberToken, skuID, 1)
	order := tradeCreateOrder(t, env, memberToken, addressID, "幂等与守卫")
	orderID := int64(order["id"].(float64))
	soNo := order["no"].(string)
	payOrder := payFindPayOrder(t, env, soNo)

	// 重复发起支付幂等：同 pay 单复用（pay_order 恒 1 笔），extension 每次递增
	//（pay 侧「待支付订单可重复 submit」语义，mall 侧不重复挂单）。
	first := paySubmitOrderPay(t, env, memberToken, orderID, "mock", http.StatusOK)
	second := paySubmitOrderPay(t, env, memberToken, orderID, "mock", http.StatusOK)
	if int64(first["payOrderId"].(float64)) != payOrder.ID ||
		int64(second["payOrderId"].(float64)) != payOrder.ID {
		t.Fatalf("重复发起应复用同 pay 单 %d，实际 %v/%v",
			payOrder.ID, first["payOrderId"], second["payOrderId"])
	}
	if payCountPayOrders(t, env, soNo) != 1 {
		t.Fatalf("重复发起不应新建 pay 单")
	}
	if payCountExtensions(t, env, payOrder.ID) != 2 {
		t.Fatalf("重复发起应产生 2 条 extension，实际 %d", payCountExtensions(t, env, payOrder.ID))
	}

	// 取消待支付订单（库存回补、状态 30）；pay 单不动（取消联动口径）。
	status, api := env.Request(t, http.MethodPost,
		"/mall/app/trade/order/"+itoa(orderID)+"/cancel", memberToken,
		map[string]any{"reason": "不想要了"})
	mustOK(t, "取消订单", status, api)
	mallRow := payReadMallOrder(t, env, orderID)
	if mallRow.Status != mallmodel.TradeOrderStatusCanceled {
		t.Fatalf("取消后订单应 30，实际 %d", mallRow.Status)
	}
	if payFindPayOrder(t, env, soNo).Status != paymodel.OrderStatusPending {
		t.Fatalf("取消不动 pay 单：pay 单应留待支付（超时关单收口）")
	}

	// 取消后发起支付 409（状态守卫）。
	paySubmitOrderPay(t, env, memberToken, orderID, "mock", http.StatusConflict)

	// 守卫回滚：取消后在途 pay 单的成功回调 → 联动拒绝 → 整笔支付认领
	// 事务回滚（pay 认领/extension 置成功/回调任务一并回滚，pay 单回待
	// 支付；wallet 渠道扣款同事务回滚即返还，mock 为模拟资金）。
	payMockNotify(t, env, memberID, payOrder.ID, "success", true)
	if got := payFindPayOrder(t, env, soNo); got.Status != paymodel.OrderStatusPending {
		t.Fatalf("守卫回滚后 pay 单应回待支付 0，实际 %d", got.Status)
	}
	if got := payReadMallOrder(t, env, orderID); got.Status != mallmodel.TradeOrderStatusCanceled {
		t.Fatalf("守卫回滚后 mall 订单应留已取消 30，实际 %d", got.Status)
	}
	if payCountPointRecords(t, env, memberID) != 0 ||
		payMemberPointBalance(t, env, memberID) != 0 {
		t.Fatalf("守卫回滚后积分不应入账（无流水/余额 0）")
	}
	if got := tradeSKUStock(t, env, adminToken, skuID); got != 2 {
		t.Fatalf("取消回补后库存应 2，实际 %d", got)
	}
	if payCountExtensions(t, env, payOrder.ID) != 2 {
		t.Fatalf("守卫回滚不应保留本次回调的 extension 置成功（extension 恒 2）")
	}
}
