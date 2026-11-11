//go:build integration

// pay 订单与收银台域 HTTP 集成测试（issue #133，docs/pay-mvp-spec.md §8.2
// 下单/收银台/钱包/越权/编号行）：真实 router + 真实 MySQL，覆盖下单幂等、
// 金额服务端定、submit 校验矩阵、wallet 同事务扣减（含并发双扣）、mock 全链路
// 到任务建立、模拟回调越权、编号格式与防伪造、管理端权限与租户隔离。
package pay_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 编号格式（spec §4.9/§8.2 编号行）：ZF + yyyyMMdd + 至少 4 位序号。
var payOrderNoRe = regexp.MustCompile(`^ZF\d{8}\d{4,}$`)

var payExtensionNoRe = regexp.MustCompile(`^ZF\d{8}\d{4,}-\d+$`)

// ---------- 复用助手 ----------

// payApp 简易应用请求体。
type payApp struct {
	ID     int64
	AppKey string
}

// payCreateApp 创建支付应用（带支付回调地址，断言成功返回 ID）。
func payCreateApp(t *testing.T, env *testutil.IntegrationEnv, token, appKey string) payApp {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/app", token, map[string]any{
		"appKey": appKey, "name": "订单测试应用-" + appKey, "orderNotifyUrl": "https://demo.example.com/pay/notify",
	})
	mustOK(t, "创建应用 "+appKey, status, api)
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil || row.ID == 0 {
		t.Fatalf("解析应用响应: %v（data=%s）", err, api.Data)
	}
	return payApp{ID: row.ID, AppKey: appKey}
}

// payBindChannels 全量替换应用绑定码（断言成功）。
func payBindChannels(t *testing.T, env *testutil.IntegrationEnv, token string, appID int64, codes []string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appID), token, map[string]any{
		"channelCodes": codes,
	})
	mustOK(t, "绑定渠道", status, api)
}

// payCreateOrder 创建支付订单（断言成功），返回订单 JSON。
func payCreateOrder(t *testing.T, env *testutil.IntegrationEnv, token, appKey, merchantOrderID, price string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/order/create", token, map[string]any{
		"appKey": appKey, "merchantOrderId": merchantOrderID, "subject": "测试商品", "body": "集成测试下单", "price": price,
	})
	mustOK(t, "创建订单 "+merchantOrderID, status, api)
	row := parseJSON(t, string(api.Data))
	if row["id"].(float64) == 0 {
		t.Fatalf("订单响应缺少 id：%s", api.Data)
	}
	return row
}

// payOrderID 提取订单 JSON 的 ID。
func payOrderID(t *testing.T, order map[string]any) int64 {
	t.Helper()
	return int64(order["id"].(float64))
}

// paySubmit 发起支付，返回 (HTTP 状态, 响应)。
func paySubmit(t *testing.T, env *testutil.IntegrationEnv, token string, orderID int64, channelCode string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/pay/order/submit", token, map[string]any{
		"orderId": orderID, "channelCode": channelCode,
	})
}

// payBackdateExpire 把订单过期时间改到过去（过期 submit 用例；服务端设定
// expire_time，请求不收，测试经 DB 造数据）。
func payBackdateExpire(t *testing.T, env *testutil.IntegrationEnv, orderID int64) {
	t.Helper()
	if err := env.DB.Exec("UPDATE pay_order SET expire_time = DATE_SUB(NOW(), INTERVAL 1 HOUR) WHERE id = ?", orderID).Error; err != nil {
		t.Fatalf("回拨过期时间: %v", err)
	}
}

// payCloseOrder 把订单置 30 已关闭（关闭后 submit 用例）。
func payCloseOrder(t *testing.T, env *testutil.IntegrationEnv, orderID int64) {
	t.Helper()
	if err := env.DB.Exec("UPDATE pay_order SET status = 30 WHERE id = ?", orderID).Error; err != nil {
		t.Fatalf("关闭订单: %v", err)
	}
}

// payBindRawChannel 绕过绑定校验直接落绑定行（submit 侧「已实现」校验的
// 独立触发：绑定层已拒绝未实现渠道，只能 DB 造数据验证 submit 兜底）。
func payBindRawChannel(t *testing.T, env *testutil.IntegrationEnv, tenantID, appID int64, code string) {
	t.Helper()
	if err := env.DB.Exec(
		"INSERT INTO pay_app_channel (tenant_id, app_id, channel_code, created_at, creator, updater) VALUES (?, ?, ?, NOW(), '0', '0')",
		tenantID, appID, code,
	).Error; err != nil {
		t.Fatalf("直插绑定行: %v", err)
	}
}

// payWalletBalance 查管理员钱包余额字符串（管理端分页 userId=1）。
func payWalletBalance(t *testing.T, env *testutil.IntegrationEnv, token string) string {
	t.Helper()
	_, _, list := pageWallets(t, env, token, "&userId=1")
	if len(list) == 0 {
		t.Fatalf("管理员钱包应存在（懒创建后）")
	}
	got, _ := list[0]["balance"].(string)
	return got
}

// payWalletFlows 查本人流水（biz 过滤可选），返回条目。
func payWalletFlows(t *testing.T, env *testutil.IntegrationEnv, token string, bizType string) []map[string]any {
	t.Helper()
	query := ""
	if bizType != "" {
		query = "&bizType=" + bizType
	}
	status, api := env.Request(t, http.MethodGet, "/pay/wallet-transaction/my?pageNo=1&pageSize=50"+query, token, nil)
	mustOK(t, "查本人流水", status, api)
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析流水: %v（data=%s）", err, api.Data)
	}
	return page.List
}

// payNotifyTasks 查订单的回调任务行（任务端点随 #134，先 DB 直查）。
func payNotifyTasks(t *testing.T, env *testutil.IntegrationEnv, orderID int64) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw("SELECT id, app_id, type, data_id, merchant_order_id AS merchantOrderId, status, notify_url AS notifyUrl, notify_times AS notifyTimes, max_notify_times AS maxNotifyTimes FROM pay_notify_task WHERE data_id = ?", orderID).Scan(&rows).Error; err != nil {
		t.Fatalf("查回调任务: %v", err)
	}
	return rows
}

// payNum 把 gorm map 扫描的数字列（驱动返回形态不定）统一为 int64。
func payNum(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int8:
		return int64(n)
	case int16:
		return int64(n)
	case int32:
		return int64(n)
	case int:
		return int64(n)
	case uint8:
		return int64(n)
	case uint16:
		return int64(n)
	case uint32:
		return int64(n)
	case uint64:
		return int64(n)
	case uint:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	default:
		return -1
	}
}

// payStr 把 gorm map 扫描的字符串列（string/[]byte 形态不定）统一为 string。
func payStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

// ---------- 下单幂等 + 编号 + 金额服务端定（spec §8.2 下单行） ----------

// TestIntegrationPayOrderCreateIdempotent 下单幂等：同 merchant_order_id 返回
// 原单不报错；跨应用/跨租户同号不冲突；金额服务端定（price 即业务价，客户端
// 提交的 no 不采纳，格式 ^ZF\d{8}\d{4,}$）。
func TestIntegrationPayOrderCreateIdempotent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateApp(t, env, admin, "order-app")

	// 首单：金额 25.50（服务端定）；no 客户端伪造不采纳（DTO 不收 no 字段）。
	status, api := env.Request(t, http.MethodPost, "/pay/order/create", admin, map[string]any{
		"appKey": appRow.AppKey, "merchantOrderId": "MO-100", "subject": "测试商品",
		"price": "25.50", "no": "ZF000000000000",
	})
	mustOK(t, "创建订单", status, api)
	order := parseJSON(t, string(api.Data))
	orderID := payOrderID(t, order)
	if no, _ := order["no"].(string); !payOrderNoRe.MatchString(no) {
		t.Fatalf("订单号 %v 应匹配 ^ZF\\d{8}\\d{4,}$（客户端伪造的 ZF000000000000 不采纳）", order["no"])
	}
	if price, _ := order["price"].(string); price != "25.50" {
		t.Fatalf("订单金额应为 25.50，实际 %v", order["price"])
	}
	if status8, _ := order["status"].(float64); status8 != 0 {
		t.Fatalf("新订单应待支付 0，实际 %v", order["status"])
	}
	if notifyURL, _ := order["notifyUrl"].(string); notifyURL != "https://demo.example.com/pay/notify" {
		t.Fatalf("notify_url 应取应用快照，实际 %v", order["notifyUrl"])
	}
	if expire, _ := order["expireTime"].(string); expire == "" {
		t.Fatalf("expire_time 应服务端设定，实际为空")
	}

	// 同 merchant_order_id 重复下单：返回原单（200，不报错）。
	status, api = env.Request(t, http.MethodPost, "/pay/order/create", admin, map[string]any{
		"appKey": appRow.AppKey, "merchantOrderId": "MO-100", "subject": "换个标题也不影响", "price": "999.00",
	})
	mustOK(t, "重复下单返回原单", status, api)
	again := parseJSON(t, string(api.Data))
	if payOrderID(t, again) != orderID {
		t.Fatalf("重复下单应返回原单 %d，实际 %v", orderID, again["id"])
	}
	if no, _ := again["no"].(string); no != order["no"].(string) {
		t.Fatalf("重复下单应返回原单号 %v，实际 %v", order["no"], again["no"])
	}
	if price, _ := again["price"].(string); price != "25.50" {
		t.Fatalf("原单金额不应被覆盖：期望 25.50，实际 %v", price)
	}

	// 跨应用同号不冲突：第二个应用同 merchant_order_id 得到独立订单。
	app2 := payCreateApp(t, env, admin, "order-app-2")
	order2 := payCreateOrder(t, env, admin, app2.AppKey, "MO-100", "10.00")
	if payOrderID(t, order2) == orderID {
		t.Fatalf("跨应用同号不应命中第一笔订单")
	}

	// 跨租户同号不冲突：租户 2 同 appKey 同 merchant_order_id。
	tenantAdmin := env.Login(t, "admin", "123456")
	tenant2 := createTenantByAPI(t, env, tenantAdmin, "下单幂等跨租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	payCreateApp(t, env, token2, "order-app")
	orderT2 := payCreateOrder(t, env, token2, "order-app", "MO-100", "5.00")
	if payOrderID(t, orderT2) == orderID {
		t.Fatalf("跨租户同号不应命中租户 1 订单")
	}
}

// ---------- 收银台可用渠道 + submit 校验矩阵（spec §8.2 收银台行） ----------

// TestIntegrationPayCashierAndSubmitMatrix 收银台渠道过滤（绑定 ∧ 启用 ∧ 已
// 实现）+ submit 四重校验矩阵（过期/未绑定/停用/未实现/已关闭各 409，文案
// 可区分）+ 重复 submit 新 extension + 金额服务端定（submit 不收金额）。
func TestIntegrationPayCashierAndSubmitMatrix(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateApp(t, env, admin, "cashier-app")
	payBindChannels(t, env, admin, appRow.ID, []string{"mock", "wallet"})

	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-CASHIER", "30.00")
	orderID := payOrderID(t, order)

	// 收银台：订单 + 可用渠道（mock/wallet 均绑定 ∧ 启用 ∧ 已实现）。
	status, api := env.Request(t, http.MethodGet, "/pay/cashier/order?appKey=cashier-app&merchantOrderId=MO-CASHIER", admin, nil)
	mustOK(t, "收银台", status, api)
	cashier := parseJSON(t, string(api.Data))
	if payOrderID(t, cashier) != orderID {
		t.Fatalf("收银台应返回订单 %d，实际 %v", orderID, cashier["id"])
	}
	channelNames := map[string]bool{}
	if chans, ok := cashier["channels"].([]any); !ok || len(chans) != 2 {
		t.Fatalf("收银台应有 2 个可用渠道，实际 %v", cashier["channels"])
	} else {
		for _, raw := range chans {
			ch := raw.(map[string]any)
			channelNames[ch["code"].(string)] = true
		}
	}
	if !channelNames["mock"] || !channelNames["wallet"] {
		t.Fatalf("可用渠道应含 mock 与 wallet，实际 %v", channelNames)
	}

	// 过期 409：expire_time 服务端设定，DB 回拨后 submit 拒绝。
	payBackdateExpire(t, env, orderID)
	status, api = paySubmit(t, env, admin, orderID, "mock")
	if status != http.StatusConflict {
		t.Fatalf("过期订单 submit 应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if msg := api.Msg; msg != "订单已过期，请重新下单" {
		t.Fatalf("过期文案应可区分，实际 %q", msg)
	}

	// 未绑定 409：wallet 已启用已实现但应用未绑定。
	order2 := payCreateOrder(t, env, admin, appRow.AppKey, "MO-UNBOUND", "30.00")
	payBindChannels(t, env, admin, appRow.ID, []string{"mock"})
	status, api = paySubmit(t, env, admin, payOrderID(t, order2), "wallet")
	if status != http.StatusConflict || api.Msg != "应用未绑定该渠道：钱包渠道（wallet）" {
		t.Fatalf("未绑定渠道应 409 文案可区分，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 停用 409：先恢复绑定（上一步全量替换掉了 wallet），再平台停用 wallet。
	payBindChannels(t, env, admin, appRow.ID, []string{"mock", "wallet"})
	walletID := channelIDByCode(t, env, admin, "wallet")
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", walletID), admin, map[string]any{"status": 1})
	mustOK(t, "停用 wallet", status, api)
	status, api = env.Request(t, http.MethodGet, "/pay/cashier/order?appKey=cashier-app&merchantOrderId=MO-CASHIER", admin, nil)
	mustOK(t, "收银台", status, api)
	cashier = parseJSON(t, string(api.Data))
	if chans, _ := cashier["channels"].([]any); len(chans) != 1 {
		t.Fatalf("停用后收银台只应剩 mock 1 个渠道，实际 %v", cashier["channels"])
	}
	status, api = paySubmit(t, env, admin, payOrderID(t, order2), "wallet")
	if status != http.StatusConflict || api.Msg != "支付渠道已停用：钱包渠道（wallet）" {
		t.Fatalf("停用渠道 submit 应 409 文案可区分，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", walletID), admin, map[string]any{"status": 0})
	mustOK(t, "恢复 wallet 启用", status, api)

	// 未实现 409：平台启用 wx_pub（已实现才可绑定——绑定层直接拒绝）。
	wxPubID := channelIDByCode(t, env, admin, "wx_pub")
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", wxPubID), admin, map[string]any{"status": 0})
	mustOK(t, "启用 wx_pub", status, api)
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", appRow.ID), admin, map[string]any{"channelCodes": []string{"mock", "wallet", "wx_pub"}})
	if status != http.StatusConflict || api.Msg != "渠道未实现：微信公众号（wx_pub）" {
		t.Fatalf("未实现渠道绑定应 409 渠道未实现，实际 HTTP %d msg %q", status, api.Msg)
	}
	// submit 兜底（绑定层被绕过的 DB 造数据场景）同样 409。
	payBindRawChannel(t, env, 1, appRow.ID, "wx_pub")
	status, api = paySubmit(t, env, admin, payOrderID(t, order2), "wx_pub")
	if status != http.StatusConflict || api.Msg != "渠道未实现：微信公众号（wx_pub）" {
		t.Fatalf("未实现渠道 submit 应 409，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 已关闭 409：status=30（超时关单 job 是 #134，测试 DB 造数据）。
	order3 := payCreateOrder(t, env, admin, appRow.AppKey, "MO-CLOSED", "30.00")
	payCloseOrder(t, env, payOrderID(t, order3))
	status, api = paySubmit(t, env, admin, payOrderID(t, order3), "mock")
	if status != http.StatusConflict || api.Msg != "订单已关闭，无法发起支付" {
		t.Fatalf("已关闭订单 submit 应 409 文案可区分，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 重复 submit 新 extension + 金额服务端定（submit 携带 price 字段被忽略）。
	order4 := payCreateOrder(t, env, admin, appRow.AppKey, "MO-REPEAT", "30.00")
	id4 := payOrderID(t, order4)
	status, api = env.Request(t, http.MethodPost, "/pay/order/submit", admin, map[string]any{
		"orderId": id4, "channelCode": "mock", "price": "0.01",
	})
	mustOK(t, "首次 submit", status, api)
	first := parseJSON(t, string(api.Data))
	extNo1, _ := first["extensionNo"].(string)
	if !payExtensionNoRe.MatchString(extNo1) {
		t.Fatalf("extension no %q 应匹配 ^ZF\\d{8}\\d{4,}-\\d+$", extNo1)
	}
	if status8, _ := first["status"].(float64); status8 != 0 {
		t.Fatalf("mock submit 后订单应停在待支付 0，实际 %v", first["status"])
	}
	if dm, _ := first["displayMode"].(string); dm != "none" {
		t.Fatalf("mock submit displayMode 应为 none，实际 %v", first["displayMode"])
	}
	status, api = paySubmit(t, env, admin, id4, "mock")
	mustOK(t, "重复 submit", status, api)
	second := parseJSON(t, string(api.Data))
	extNo2, _ := second["extensionNo"].(string)
	if extNo2 == extNo1 {
		t.Fatalf("重复 submit 应产生新 extension no，实际仍为 %q", extNo2)
	}
	var extCount int64
	if err := env.DB.Raw("SELECT COUNT(*) FROM pay_order_extension WHERE order_id = ?", id4).Scan(&extCount).Error; err != nil {
		t.Fatalf("统计 extension: %v", err)
	}
	if extCount != 2 {
		t.Fatalf("重复 submit 应有 2 条 extension，实际 %d", extCount)
	}
	// 金额服务端定：订单金额不受 submit 携带 price 影响。
	var storedPrice string
	if err := env.DB.Raw("SELECT price FROM pay_order WHERE id = ?", id4).Scan(&storedPrice).Error; err != nil {
		t.Fatalf("读订单金额: %v", err)
	}
	if storedPrice != "30.00" {
		t.Fatalf("submit 不收金额：期望 30.00，实际 %s", storedPrice)
	}
}

// ---------- wallet 渠道：同事务扣减 + 并发双扣（spec §8.2 钱包行） ----------

// TestIntegrationPayWalletChannelSubmit wallet 渠道支付：同事务扣钱包
// （经唯一入账入口 biz=3）→ 订单 10 → 建任务；余额不足 409 且无部分扣减
// （无流水、无 extension、订单停待支付）。
func TestIntegrationPayWalletChannelSubmit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateApp(t, env, admin, "wallet-app")
	payBindChannels(t, env, admin, appRow.ID, []string{"mock", "wallet"})

	// 准备余额 100.00（懒创建 + 充值走唯一入账入口）。
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "钱包支付准备", Price: money.FromCents(10_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}

	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-WALLET", "40.00")
	orderID := payOrderID(t, order)
	status, api := paySubmit(t, env, admin, orderID, "wallet")
	mustOK(t, "wallet submit", status, api)
	resp := parseJSON(t, string(api.Data))
	if status8, _ := resp["status"].(float64); status8 != 10 {
		t.Fatalf("wallet 同步支付成功订单应 10，实际 %v", resp["status"])
	}

	// 订单/extension 状态与渠道回执。
	var ord struct {
		Status        int8
		SuccessTime   *string
		ChannelCode   string
		ChannelUserID string
		ExtensionID   int64
	}
	if err := env.DB.Raw("SELECT status, success_time, channel_code, channel_user_id, extension_id FROM pay_order WHERE id = ?", orderID).Scan(&ord).Error; err != nil {
		t.Fatalf("读订单: %v", err)
	}
	if ord.Status != 10 || ord.SuccessTime == nil || ord.ChannelCode != "wallet" || ord.ChannelUserID != "1" || ord.ExtensionID == 0 {
		t.Fatalf("订单认领状态不符：%+v", ord)
	}
	var ext struct {
		Status int8
	}
	if err := env.DB.Raw("SELECT status FROM pay_order_extension WHERE id = ?", ord.ExtensionID).Scan(&ext).Error; err != nil {
		t.Fatalf("读 extension: %v", err)
	}
	if ext.Status != 10 {
		t.Fatalf("extension 应成功 10，实际 %d", ext.Status)
	}

	// 钱包：余额 60.00，biz=3 流水存在（负价、biz_id=订单 ID）。
	if got := payWalletBalance(t, env, admin); got != "60.00" {
		t.Fatalf("支付后余额应 60.00，实际 %s", got)
	}
	flows := payWalletFlows(t, env, admin, "3")
	if len(flows) != 1 {
		t.Fatalf("biz=3 流水应恰 1 条，实际 %d", len(flows))
	}
	if price, _ := flows[0]["price"].(string); price != "-40.00" {
		t.Fatalf("支付流水应为 -40.00，实际 %v", flows[0]["price"])
	}
	if bizID, _ := flows[0]["bizId"].(float64); int64(bizID) != orderID {
		t.Fatalf("支付流水 biz_id 应为订单 ID %d，实际 %v", orderID, flows[0]["bizId"])
	}

	// 回调任务：type=1 / status=0 / max=9 / notify_url 快照。
	tasks := payNotifyTasks(t, env, orderID)
	if len(tasks) != 1 {
		t.Fatalf("支付成功应建 1 条回调任务，实际 %d", len(tasks))
	}
	task := tasks[0]
	if payNum(task["type"]) != 1 || payNum(task["status"]) != 0 || payNum(task["maxNotifyTimes"]) != 9 {
		t.Fatalf("回调任务字段不符：type %T %v / status %T %v / max %T %v",
			task["type"], task["type"], task["status"], task["status"], task["maxNotifyTimes"], task["maxNotifyTimes"])
	}
	if url := payStr(task["notifyUrl"]); url != "https://demo.example.com/pay/notify" {
		t.Fatalf("任务 notify_url 应为下单快照，实际 %v", task["notifyUrl"])
	}

	// 余额不足 409 且无部分扣减：第二单 999.00。
	order2 := payCreateOrder(t, env, admin, appRow.AppKey, "MO-POOR", "999.00")
	status, api = paySubmit(t, env, admin, payOrderID(t, order2), "wallet")
	if status != http.StatusConflict {
		t.Fatalf("余额不足应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := payWalletBalance(t, env, admin); got != "60.00" {
		t.Fatalf("余额不足不得部分扣减：期望 60.00，实际 %s", got)
	}
	if flows := payWalletFlows(t, env, admin, "3"); len(flows) != 1 {
		t.Fatalf("余额不足不得产生新流水，实际 %d 条", len(flows))
	}
	var ext2 int64
	if err := env.DB.Raw("SELECT COUNT(*) FROM pay_order_extension WHERE order_id = ?", payOrderID(t, order2)).Scan(&ext2).Error; err != nil {
		t.Fatalf("统计 extension: %v", err)
	}
	if ext2 != 0 {
		t.Fatalf("失败 submit 整体回滚：extension 不应落库，实际 %d 条", ext2)
	}
}

// TestIntegrationPayWalletConcurrentSubmit 并发双扣（spec §8.2 钱包行）：同一
// 钱包两笔订单并发 submit，一成一 409，无负余额。
func TestIntegrationPayWalletConcurrentSubmit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateApp(t, env, admin, "wallet-race")
	payBindChannels(t, env, admin, appRow.ID, []string{"wallet"})

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "并发双扣准备", Price: money.FromCents(6_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}

	orderA := payCreateOrder(t, env, admin, appRow.AppKey, "MO-RACE-A", "60.00")
	orderB := payCreateOrder(t, env, admin, appRow.AppKey, "MO-RACE-B", "60.00")

	const goroutines = 2
	results := make([]int, goroutines)
	ids := []int64{payOrderID(t, orderA), payOrderID(t, orderB)}
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, _ := paySubmit(t, env, admin, ids[i], "wallet")
			results[i] = status
		}(i)
	}
	wg.Wait()

	okCount, conflictCount := 0, 0
	for _, st := range results {
		switch st {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		default:
			t.Fatalf("并发双扣结果异常：HTTP %d（results=%v）", st, results)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("并发双扣应一成一 409，实际 %v", results)
	}
	if got := payWalletBalance(t, env, admin); got != "0.00" {
		t.Fatalf("并发双扣后余额应恰 0.00（无负余额），实际 %s", got)
	}
	if flows := payWalletFlows(t, env, admin, "3"); len(flows) != 1 {
		t.Fatalf("成功一笔只应有一条支付流水，实际 %d", len(flows))
	}
}

// ---------- mock 渠道全链路（spec §8.2 回调行，任务建立止于 #133 边界） ----------

// TestIntegrationPayMockNotifyChain mock 全链路：submit 停待支付 → 模拟失败
// （extension 20、订单保持待支付）→ 再次 submit → 模拟成功走解析 → 入账 →
// 建任务（订单 10、任务 type=1）；代他人触发 403；非 mock 渠道 409。
func TestIntegrationPayMockNotifyChain(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateApp(t, env, admin, "mock-app")
	payBindChannels(t, env, admin, appRow.ID, []string{"mock", "wallet"})

	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-MOCK", "10.00")
	orderID := payOrderID(t, order)
	status, api := paySubmit(t, env, admin, orderID, "mock")
	mustOK(t, "mock submit", status, api)
	first := parseJSON(t, string(api.Data))
	extNo1, _ := first["extensionNo"].(string)

	// 模拟失败：extension 20（错误码 MOCK_FAIL），订单保持待支付。
	status, api = env.Request(t, http.MethodPost, "/pay/mock/notify", admin, map[string]any{"orderId": orderID, "result": "failure"})
	mustOK(t, "模拟失败", status, api)
	var ext1 struct {
		Status     int8
		ErrorCode  string
		NotifyData string
	}
	if err := env.DB.Raw("SELECT status, channel_error_code AS error_code, channel_notify_data AS notify_data FROM pay_order_extension WHERE no = ?", extNo1).Scan(&ext1).Error; err != nil {
		t.Fatalf("读 extension: %v", err)
	}
	if ext1.Status != 20 || ext1.ErrorCode != "MOCK_FAIL" || ext1.NotifyData == "" {
		t.Fatalf("失败分支 extension 状态不符：%+v", ext1)
	}
	var st1 int8
	if err := env.DB.Raw("SELECT status FROM pay_order WHERE id = ?", orderID).Scan(&st1).Error; err != nil {
		t.Fatalf("读订单: %v", err)
	}
	if st1 != 0 {
		t.Fatalf("模拟失败后订单应保持待支付，实际 %d", st1)
	}

	// 再次 submit → 模拟成功：走与真实回调相同的解析入账链路。
	status, api = paySubmit(t, env, admin, orderID, "mock")
	mustOK(t, "再次 mock submit", status, api)
	second := parseJSON(t, string(api.Data))
	extNo2, _ := second["extensionNo"].(string)
	status, api = env.Request(t, http.MethodPost, "/pay/mock/notify", admin, map[string]any{"orderId": orderID, "result": "success"})
	mustOK(t, "模拟成功", status, api)

	var ord struct {
		Status         int8
		SuccessTime    *string
		ChannelOrderNo string
	}
	if err := env.DB.Raw("SELECT status, success_time, channel_order_no FROM pay_order WHERE id = ?", orderID).Scan(&ord).Error; err != nil {
		t.Fatalf("读订单: %v", err)
	}
	if ord.Status != 10 || ord.SuccessTime == nil || ord.ChannelOrderNo != "MOCK-"+extNo2 {
		t.Fatalf("模拟成功后订单状态不符：%+v（extNo2=%s）", ord, extNo2)
	}
	var ext2 struct {
		Status int8
	}
	if err := env.DB.Raw("SELECT status FROM pay_order_extension WHERE no = ?", extNo2).Scan(&ext2).Error; err != nil {
		t.Fatalf("读 extension: %v", err)
	}
	if ext2.Status != 10 {
		t.Fatalf("成功分支 extension 应 10，实际 %d", ext2.Status)
	}
	tasks := payNotifyTasks(t, env, orderID)
	if len(tasks) != 1 {
		t.Fatalf("模拟成功应建 1 条回调任务，实际 %d", len(tasks))
	}
	if payNum(tasks[0]["type"]) != 1 || payNum(tasks[0]["status"]) != 0 {
		t.Fatalf("回调任务应为 type=1 status=0，实际 %v", tasks[0])
	}

	// 幂等：重复模拟成功（无处理中 extension）→ 409，不重复建任务。
	status, api = env.Request(t, http.MethodPost, "/pay/mock/notify", admin, map[string]any{"orderId": orderID, "result": "success"})
	if status != http.StatusConflict {
		t.Fatalf("已成功订单再触发模拟回调应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
	if tasks := payNotifyTasks(t, env, orderID); len(tasks) != 1 {
		t.Fatalf("不得重复建任务，实际 %d 条", len(tasks))
	}

	// 代他人触发 403（防代他人确认支付，spec §4.6）：demo 是同租户其他用户。
	demo := env.Login(t, "demo", "123456")
	status, api = env.Request(t, http.MethodPost, "/pay/mock/notify", demo, map[string]any{"orderId": orderID, "result": "success"})
	if status != http.StatusForbidden {
		t.Fatalf("代他人触发模拟回调应 403，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 非 mock 渠道订单触发模拟回调 409：wallet 支付成功的订单。
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "非 mock 渠道准备", Price: money.FromCents(5_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}
	order2 := payCreateOrder(t, env, admin, appRow.AppKey, "MO-NOTMOCK", "50.00")
	status, api = paySubmit(t, env, admin, payOrderID(t, order2), "wallet")
	mustOK(t, "wallet submit", status, api)
	status, api = env.Request(t, http.MethodPost, "/pay/mock/notify", admin, map[string]any{"orderId": payOrderID(t, order2), "result": "success"})
	if status != http.StatusConflict {
		t.Fatalf("非 mock 渠道触发模拟回调应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
}

// ---------- 管理端与越权矩阵（spec §8.2 越权行）+ 应用删除引用守卫 ----------

// TestIntegrationPayOrderAdminAndGuards 管理端分页/详情（pay:order:query）、
// 普通角色 403、跨租户 404 / 非本人 403 矩阵、应用删除订单引用 409。
func TestIntegrationPayOrderAdminAndGuards(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateApp(t, env, admin, "guard-app")
	payBindChannels(t, env, admin, appRow.ID, []string{"mock"})
	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-GUARD", "12.00")
	orderID := payOrderID(t, order)
	status, _ := paySubmit(t, env, admin, orderID, "mock")
	if status != http.StatusOK {
		t.Fatalf("mock submit 准备应成功，HTTP %d", status)
	}

	// 管理端分页 + 详情（含最近 extension）。
	status, api := env.Request(t, http.MethodGet, "/pay/order/page?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "订单分页", status, api)
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析订单分页: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("订单分页 total 应 1，实际 %d", page.Total)
	}
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/order/%d", orderID), admin, nil)
	mustOK(t, "订单详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if payOrderID(t, detail) != orderID {
		t.Fatalf("详情应返回订单 %d", orderID)
	}
	if ext, ok := detail["extension"].(map[string]any); !ok || ext["no"] == nil {
		t.Fatalf("详情应携带最近 extension：%s", api.Data)
	}

	// 普通角色（role id=2）未授 pay:order:query → 403。
	demo := env.Login(t, "demo", "123456")
	status, _ = env.Request(t, http.MethodGet, "/pay/order/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色订单分页应 403，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/order/%d", orderID), demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色订单详情应 403，实际 HTTP %d", status)
	}

	// 非本人 403（自助端点只挂登录，归属校验在 service 层）：收银台/submit/mock。
	status, api = env.Request(t, http.MethodGet, "/pay/cashier/order?appKey=guard-app&merchantOrderId=MO-GUARD", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("非本人收银台应 403，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, _ = paySubmit(t, env, demo, orderID, "mock")
	if status != http.StatusForbidden {
		t.Fatalf("非本人 submit 应 403，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/pay/mock/notify", demo, map[string]any{"orderId": orderID, "result": "success"})
	if status != http.StatusForbidden {
		t.Fatalf("非本人模拟回调应 403，实际 HTTP %d", status)
	}

	// 跨租户 404：租户 2 的 token 查租户 1 订单（详情/收银台均不可见）。
	tenant2 := createTenantByAPI(t, env, admin, "订单越权矩阵租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/order/%d", orderID), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("跨租户订单详情应 404，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodGet, "/pay/cashier/order?appKey=guard-app&merchantOrderId=MO-GUARD", token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("跨租户收银台应 404（同 appKey 不同租户无此单），实际 HTTP %d", status)
	}

	// 应用删除引用守卫（spec §4.10）：先解绑渠道（绑定检查在前），订单引用仍禁删。
	payBindChannels(t, env, admin, appRow.ID, []string{})
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/app/%d", appRow.ID), admin, nil)
	if status != http.StatusConflict || api.Msg != "应用存在订单引用，禁止删除" {
		t.Fatalf("有订单引用的应用删除应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 无引用应用可删（既有行为回归）。
	app2 := payCreateApp(t, env, admin, "guard-app-deletable")
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/app/%d", app2.ID), admin, nil)
	if status != http.StatusOK {
		t.Fatalf("无引用应用应可删除，实际 HTTP %d", status)
	}
}
