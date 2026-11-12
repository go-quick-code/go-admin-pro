//go:build integration

// pay 退款域 HTTP 集成测试（issue #135，docs/pay-mvp-spec.md §8.2 退款行/
// §4.4/§4.9/§4.10）：真实 router + 真实 MySQL，覆盖发起校验矩阵（非 10 订单/
// 超额/非正金额各 409、幂等返回原单、客户端伪造 no 不采纳）、wallet 同事务
// 回加（biz=4 含流水快照、订单累计与全额置 20、回加与支付扣减并发安全）、
// mock 成功/失败两分支（失败分支退款单 20 且订单不累计）、type=2 回调任务
// 字段、TK 编号格式与防伪造、管理端权限与越权矩阵（跨租户 404 / 非本人 403）、
// 应用删除退款引用守卫。
package pay_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	paychannel "github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 编号格式（spec §4.9/§8.2 编号行）：TK + yyyyMMdd + 至少 4 位序号。
var payRefundNoRe = regexp.MustCompile(`^TK\d{8}\d{4,}$`)

// ---------- 复用助手 ----------

// payCreateRefundApp 创建支付应用（带支付/退款回调地址，绑定指定渠道，断言成功）。
func payCreateRefundApp(t *testing.T, env *testutil.IntegrationEnv, token, appKey string, channels []string) payApp {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/app", token, map[string]any{
		"appKey": appKey, "name": "退款测试应用-" + appKey,
		"orderNotifyUrl":  "https://demo.example.com/pay/notify",
		"refundNotifyUrl": "https://demo.example.com/refund/notify",
	})
	mustOK(t, "创建应用 "+appKey, status, api)
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil || row.ID == 0 {
		t.Fatalf("解析应用响应: %v（data=%s）", err, api.Data)
	}
	if len(channels) > 0 {
		payBindChannels(t, env, token, row.ID, channels)
	}
	return payApp{ID: row.ID, AppKey: appKey}
}

// payPayOrder 用 wallet 渠道把订单支付到 10 成功（余额准备由调用方负责）。
func payPayOrder(t *testing.T, env *testutil.IntegrationEnv, token string, orderID int64) {
	t.Helper()
	status, api := paySubmit(t, env, token, orderID, "wallet")
	mustOK(t, "wallet 支付准备", status, api)
}

// payMockPayOrder 用 mock 渠道把订单支付到 10 成功（submit + 模拟成功回调）。
func payMockPayOrder(t *testing.T, env *testutil.IntegrationEnv, token string, orderID int64) {
	t.Helper()
	status, api := paySubmit(t, env, token, orderID, "mock")
	mustOK(t, "mock submit 准备", status, api)
	status, api = env.Request(t, http.MethodPost, "/pay/mock/notify", token, map[string]any{"orderId": orderID, "result": "success"})
	mustOK(t, "mock 模拟回调准备", status, api)
}

// payRefund 发起退款，返回 (HTTP 状态, 响应)。
func payRefund(t *testing.T, env *testutil.IntegrationEnv, token, appKey, merchantOrderID, merchantRefundID, refundPrice string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/pay/refund/create", token, map[string]any{
		"appKey": appKey, "merchantOrderId": merchantOrderID, "merchantRefundId": merchantRefundID,
		"refundPrice": refundPrice, "reason": "集成测试退款",
	})
}

// payRefundRow 读退款单 DB 行（状态/错误列/回执列）。
func payRefundRow(t *testing.T, env *testutil.IntegrationEnv, id int64) struct {
	Status          int8
	PayPrice        string
	RefundPrice     string
	OrderNo         string
	ChannelCode     string
	NotifyURL       string
	ChannelRefundNo string
	ErrorCode       string
	ErrorMsg        string
	NotifyData      string
	SuccessTime     *string
} {
	t.Helper()
	var row struct {
		Status          int8
		PayPrice        string
		RefundPrice     string
		OrderNo         string
		ChannelCode     string
		NotifyURL       string
		ChannelRefundNo string
		ErrorCode       string
		ErrorMsg        string
		NotifyData      string
		SuccessTime     *string
	}
	if err := env.DB.Raw(
		"SELECT status, pay_price, refund_price, order_no, channel_code, notify_url, "+
			"channel_refund_no, channel_error_code AS error_code, channel_error_msg AS error_msg, "+
			"channel_notify_data AS notify_data, success_time "+
			"FROM pay_refund WHERE id = ?", id,
	).Scan(&row).Error; err != nil {
		t.Fatalf("读退款单 %d: %v", id, err)
	}
	return row
}

// payOrderRow 读订单 DB 行（状态/已退累计）。
func payOrderRow(t *testing.T, env *testutil.IntegrationEnv, id int64) struct {
	Status      int8
	RefundPrice string
} {
	t.Helper()
	var row struct {
		Status      int8
		RefundPrice string
	}
	if err := env.DB.Raw("SELECT status, refund_price FROM pay_order WHERE id = ?", id).Scan(&row).Error; err != nil {
		t.Fatalf("读订单 %d: %v", id, err)
	}
	return row
}

// payRefundNotifyTasks 查退款单的回调任务行（任务端点随 #134，先 DB 直查）。
func payRefundNotifyTasks(t *testing.T, env *testutil.IntegrationEnv, refundID int64) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw(
		"SELECT id, app_id, type, data_id, merchant_refund_id AS merchantRefundId, status, "+
			"notify_url AS notifyUrl, notify_times AS notifyTimes, max_notify_times AS maxNotifyTimes "+
			"FROM pay_notify_task WHERE data_id = ? AND type = 2", refundID,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("查退款回调任务: %v", err)
	}
	return rows
}

// payReplaceMockClient 用指定 refundResult 的 mock 渠道替换注册表（mock 失败
// 分支的测试入口——spec §4.4 失败分支「供测试」，渠道行为由 mock 渠道 config
// 决定；注册表其余实现保持生产装配原样）。
func payReplaceMockClient(t *testing.T, env *testutil.IntegrationEnv, refundResult string) {
	t.Helper()
	mockClient, err := paychannel.NewMockClient(fmt.Sprintf(`{"refundResult":%q}`, refundResult))
	if err != nil {
		t.Fatalf("构造 %s mock 渠道: %v", refundResult, err)
	}
	walletClient, err := paychannel.NewWalletClient("", env.App.Pay, env.App.Pay, env.App.Pay)
	if err != nil {
		t.Fatalf("构造 wallet 渠道: %v", err)
	}
	registry := paychannel.NewRegistry()
	registry.Register(mockClient)
	registry.Register(walletClient)
	env.App.Pay.SetRegistry(registry)
}

// ---------- 发起校验矩阵（spec §8.2 退款行 + 验收：非 10 409 / 超额 409 /
// 非正金额 409 / 幂等返回原单 / 伪造 no 不采纳 / notify_url 快照可覆盖） ----------

// TestIntegrationPayRefundCreateValidation 发起校验矩阵：金额快照与 notify_url
// 默认取应用 refund_notify_url（可覆盖）；TK 编号正则 + 客户端伪造 no 不采纳；
// 非正金额 409；待支付订单 409；已关闭订单 409；累计超额 409（文案含已退/
// 本次/实付）；同 merchant_refund_id 幂等返回原单（重复请求不重复累计）。
func TestIntegrationPayRefundCreateValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateRefundApp(t, env, admin, "refund-app", []string{"mock", "wallet"})

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "退款校验准备", Price: money.FromCents(10_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}
	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-REF-VAL", "25.50")
	orderID := payOrderID(t, order)
	payPayOrder(t, env, admin, orderID)

	// 非正金额 409（refund_price ≤ 0）。
	for _, price := range []string{"0.00", "-1.00"} {
		status, api := payRefund(t, env, admin, appRow.AppKey, "MO-REF-VAL", "RF-ZERO", price)
		if status != http.StatusConflict {
			t.Fatalf("退款金额 %s 应 409，实际 HTTP %d code %d msg %q", price, status, api.Code, api.Msg)
		}
	}

	// 待支付订单 409（订单渠道为 mock 未支付）。
	payCreateOrder(t, env, admin, appRow.AppKey, "MO-REF-PENDING", "10.00")
	status, api := payRefund(t, env, admin, appRow.AppKey, "MO-REF-PENDING", "RF-PENDING", "1.00")
	if status != http.StatusConflict || api.Msg != "订单未支付，无法退款" {
		t.Fatalf("待支付订单退款应 409 文案可区分，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 已关闭订单 409（spec §8.2：已退款/已关闭订单再退 409；30 由 DB 造数据）。
	closedOrder := payCreateOrder(t, env, admin, appRow.AppKey, "MO-REF-CLOSED", "10.00")
	if err := env.DB.Exec("UPDATE pay_order SET status = 30 WHERE id = ?", payOrderID(t, closedOrder)).Error; err != nil {
		t.Fatalf("关闭订单: %v", err)
	}
	status, api = payRefund(t, env, admin, appRow.AppKey, "MO-REF-CLOSED", "RF-CLOSED", "1.00")
	if status != http.StatusConflict || api.Msg != "订单已关闭，无法退款" {
		t.Fatalf("已关闭订单退款应 409 文案可区分，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 首笔部分退款成功：notify_url 缺省取应用 refund_notify_url 快照；
	// pay_price 为原单实付快照；客户端伪造 no（DTO 不收）不采纳。
	status, api = env.Request(t, http.MethodPost, "/pay/refund/create", admin, map[string]any{
		"appKey": appRow.AppKey, "merchantOrderId": "MO-REF-VAL", "merchantRefundId": "RF-1",
		"refundPrice": "20.00", "reason": "部分退款", "no": "TK000000000000",
	})
	mustOK(t, "首笔退款", status, api)
	refund := parseJSON(t, string(api.Data))
	refundID := int64(refund["id"].(float64))
	if no, _ := refund["no"].(string); !payRefundNoRe.MatchString(no) {
		t.Fatalf("退款单号 %v 应匹配 ^TK\\d{8}\\d{4,}$（客户端伪造的 TK000000000000 不采纳）", refund["no"])
	}
	if payPrice, _ := refund["payPrice"].(string); payPrice != "25.50" {
		t.Fatalf("pay_price 应为原单实付快照 25.50，实际 %v", refund["payPrice"])
	}
	if price, _ := refund["refundPrice"].(string); price != "20.00" {
		t.Fatalf("退款金额应为 20.00，实际 %v", refund["refundPrice"])
	}
	if st, _ := refund["status"].(float64); st != 10 {
		t.Fatalf("wallet 渠道退款应同步成功 10，实际 %v", refund["status"])
	}
	row := payRefundRow(t, env, refundID)
	if row.NotifyURL != "https://demo.example.com/refund/notify" {
		t.Fatalf("notify_url 应取应用 refund_notify_url 快照，实际 %q", row.NotifyURL)
	}
	if row.OrderNo != order["no"].(string) {
		t.Fatalf("order_no 应为原订单号快照 %v，实际 %q", order["no"], row.OrderNo)
	}

	// notify_url 请求覆盖（同 #133 订单 notify_url 可覆盖口径）：本次 1.00，
	// 累计已退 21.00。
	status, api = env.Request(t, http.MethodPost, "/pay/refund/create", admin, map[string]any{
		"appKey": appRow.AppKey, "merchantOrderId": "MO-REF-VAL", "merchantRefundId": "RF-OVERRIDE2",
		"refundPrice": "1.00", "notifyUrl": "https://merchant.example.com/refund-hook",
	})
	mustOK(t, "notify_url 覆盖", status, api)
	overrideRefund := parseJSON(t, string(api.Data))
	if row := payRefundRow(t, env, int64(overrideRefund["id"].(float64))); row.NotifyURL != "https://merchant.example.com/refund-hook" {
		t.Fatalf("notify_url 应可被请求覆盖，实际 %q", row.NotifyURL)
	}

	// 累计超额 409（已退 21.00 + 本次 10.00 > 实付 25.50），文案含已退/本次/实付。
	status, api = payRefund(t, env, admin, appRow.AppKey, "MO-REF-VAL", "RF-OVER", "10.00")
	if status != http.StatusConflict {
		t.Fatalf("累计超额退款应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	for _, want := range []string{"21.00", "10.00", "25.50"} {
		if !containsStr(api.Msg, want) {
			t.Fatalf("超额文案应含 %s（已退/本次/实付），实际 %q", want, api.Msg)
		}
	}

	// 幂等：同 merchant_refund_id 重复发起返回原单（不重复累计、不重复建任务）。
	status, api = payRefund(t, env, admin, appRow.AppKey, "MO-REF-VAL", "RF-1", "99.00")
	mustOK(t, "幂等重放", status, api)
	again := parseJSON(t, string(api.Data))
	if int64(again["id"].(float64)) != refundID {
		t.Fatalf("同 merchant_refund_id 应返回原单 %d，实际 %v", refundID, again["id"])
	}
	if no, _ := again["no"].(string); no != refund["no"].(string) {
		t.Fatalf("幂等重放应返回原单号 %v，实际 %v", refund["no"], again["no"])
	}
	if row := payOrderRow(t, env, orderID); row.RefundPrice != "21.00" {
		t.Fatalf("幂等重放不得重复累计：已退应 21.00，实际 %s", row.RefundPrice)
	}
	if tasks := payRefundNotifyTasks(t, env, refundID); len(tasks) != 1 {
		t.Fatalf("幂等重放不得重复建任务，实际 %d 条", len(tasks))
	}

	// 跨应用同 merchant_refund_id 不冲突：第二个应用同号得独立退款单。
	app2 := payCreateRefundApp(t, env, admin, "refund-app-2", []string{"mock", "wallet"})
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "跨应用退款准备", Price: money.FromCents(5_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}
	order2 := payCreateOrder(t, env, admin, app2.AppKey, "MO-REF-APP2", "10.00")
	payPayOrder(t, env, admin, payOrderID(t, order2))
	status, api = payRefund(t, env, admin, app2.AppKey, "MO-REF-APP2", "RF-1", "2.00")
	mustOK(t, "跨应用同号退款", status, api)
	other := parseJSON(t, string(api.Data))
	if int64(other["id"].(float64)) == refundID {
		t.Fatalf("跨应用同 merchant_refund_id 不应命中第一笔退款单")
	}
}

// containsStr 报告 s 是否包含 sub（超额文案断言用）。
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// ---------- wallet 渠道：同事务回加 + 订单累计与状态切换（spec §8.2 退款行） ----------

// TestIntegrationPayWalletRefundFlow wallet 渠道退款全链路：同事务回加余额
// （biz=4 正价流水、biz_id=退款单 ID、balance 快照）→ 退款单 10 → 订单
// refund_price 累计（部分保持 10、全额置 20）→ type=2 回调任务字段齐全；
// 全额退完后再退 409。
func TestIntegrationPayWalletRefundFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateRefundApp(t, env, admin, "wallet-refund-app", []string{"mock", "wallet"})

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "wallet 退款准备", Price: money.FromCents(10_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}
	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-WALLET-REFUND", "40.00")
	orderID := payOrderID(t, order)
	payPayOrder(t, env, admin, orderID)

	// 部分退款 15.00：退款单 10、订单 refund_price 15.00、状态保持 10。
	status, api := payRefund(t, env, admin, appRow.AppKey, "MO-WALLET-REFUND", "RF-PART", "15.00")
	mustOK(t, "部分退款", status, api)
	part := parseJSON(t, string(api.Data))
	partID := int64(part["id"].(float64))
	if st, _ := part["status"].(float64); st != 10 {
		t.Fatalf("部分退款应成功 10，实际 %v", part["status"])
	}
	if no, _ := part["no"].(string); !payRefundNoRe.MatchString(no) {
		t.Fatalf("退款单号 %v 应匹配 ^TK\\d{8}\\d{4,}$", part["no"])
	}
	row := payRefundRow(t, env, partID)
	if row.Status != 10 || row.SuccessTime == nil {
		t.Fatalf("部分退款单应 10 且有 success_time：%+v", row)
	}
	if row.ChannelCode != "wallet" {
		t.Fatalf("退款渠道应原路退回 wallet，实际 %q", row.ChannelCode)
	}
	if ord := payOrderRow(t, env, orderID); ord.Status != 10 || ord.RefundPrice != "15.00" {
		t.Fatalf("部分退款后订单应保持 10 且已退 15.00：%+v", ord)
	}

	// 钱包：余额 75.00（100 - 40 + 15）；biz=4 流水恰 1 条（正价、biz_id=退款单 ID）。
	if got := payWalletBalance(t, env, admin); got != "75.00" {
		t.Fatalf("部分退款后余额应 75.00，实际 %s", got)
	}
	var flows []map[string]any
	if err := env.DB.Raw(
		"SELECT no, biz_type AS bizType, biz_id AS bizId, title, price, balance FROM pay_wallet_transaction WHERE biz_type = 4",
	).Scan(&flows).Error; err != nil {
		t.Fatalf("查 biz=4 流水: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("biz=4 流水应恰 1 条，实际 %d", len(flows))
	}
	flow := flows[0]
	if price, _ := flow["price"].(string); price != "15.00" {
		t.Fatalf("退款回加流水应为 +15.00，实际 %v", flow["price"])
	}
	if payNum(flow["bizId"]) != partID {
		t.Fatalf("退款流水 biz_id 应为退款单 ID %d，实际 %v", partID, flow["bizId"])
	}
	if balance, _ := flow["balance"].(string); balance != "75.00" {
		t.Fatalf("退款流水 balance 快照应 75.00，实际 %v", balance)
	}
	if title, _ := flow["title"].(string); title == "" {
		t.Fatalf("退款流水标题不应为空")
	}

	// 回调任务：type=2 / data_id=退款单 ID / merchant_refund_id / status=0 /
	// max=9 / notify_times=0 / notify_url 快照。
	tasks := payRefundNotifyTasks(t, env, partID)
	if len(tasks) != 1 {
		t.Fatalf("退款成功应建 1 条回调任务，实际 %d", len(tasks))
	}
	task := tasks[0]
	if payNum(task["type"]) != 2 || payNum(task["status"]) != 0 || payNum(task["maxNotifyTimes"]) != 9 || payNum(task["notifyTimes"]) != 0 {
		t.Fatalf("退款回调任务字段不符：%v", task)
	}
	if url := payStr(task["notifyUrl"]); url != "https://demo.example.com/refund/notify" {
		t.Fatalf("任务 notify_url 应为发起快照，实际 %v", url)
	}
	if id := payStr(task["merchantRefundId"]); id != "RF-PART" {
		t.Fatalf("任务 merchant_refund_id 应为 RF-PART，实际 %v", task["merchantRefundId"])
	}

	// 全额退款 25.00：累计 40.00 = price → 订单置 20 已退款。
	status, api = payRefund(t, env, admin, appRow.AppKey, "MO-WALLET-REFUND", "RF-FULL", "25.00")
	mustOK(t, "全额退款", status, api)
	if st := payRefundRow(t, env, mustRefundID(t, api)).Status; st != 10 {
		t.Fatalf("全额退款单应成功 10，实际 %d", st)
	}
	if ord := payOrderRow(t, env, orderID); ord.Status != 20 || ord.RefundPrice != "40.00" {
		t.Fatalf("全额退款后订单应 20 且已退 40.00：%+v", ord)
	}
	// 余额 100.00（回加两笔），biz=4 流水两条。
	if got := payWalletBalance(t, env, admin); got != "100.00" {
		t.Fatalf("全额退款后余额应 100.00，实际 %s", got)
	}
	var biz4 int64
	if err := env.DB.Raw("SELECT COUNT(*) FROM pay_wallet_transaction WHERE biz_type = 4").Scan(&biz4).Error; err != nil {
		t.Fatalf("统计 biz=4 流水: %v", err)
	}
	if biz4 != 2 {
		t.Fatalf("全额退款后 biz=4 流水应 2 条，实际 %d", biz4)
	}

	// 已全额退款订单再退 409（spec §8.2：订单 20/30 后禁止再退）。
	status, api = payRefund(t, env, admin, appRow.AppKey, "MO-WALLET-REFUND", "RF-AGAIN", "1.00")
	if status != http.StatusConflict || api.Msg != "订单已全额退款，无法再次退款" {
		t.Fatalf("已退款订单再退应 409 文案可区分，实际 HTTP %d msg %q", status, api.Msg)
	}
}

// TestIntegrationPayWalletRefundConcurrentWithPayment 回加并发安全（spec §8.2
// 退款行：wallet 退款回加并发安全）：同一钱包两笔订单，一笔支付扣减与一笔
// 退款回加并发——同锁路径（唯一入账入口行锁）串行化，两笔皆成、无负余额、
// 流水快照可按 id 重放出最终余额。
func TestIntegrationPayWalletRefundConcurrentWithPayment(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateRefundApp(t, env, admin, "wallet-race-refund", []string{"mock", "wallet"})

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "回加并发准备", Price: money.FromCents(12_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}
	// 订单 A：先 wallet 支付 60.00（余额 120 - 60 = 60）。
	orderA := payCreateOrder(t, env, admin, appRow.AppKey, "MO-RACE-REFUND-A", "60.00")
	payPayOrder(t, env, admin, payOrderID(t, orderA))
	// 订单 B：待支付 60.00（并发支付扣减方）。
	orderB := payCreateOrder(t, env, admin, appRow.AppKey, "MO-RACE-REFUND-B", "60.00")
	orderBID := payOrderID(t, orderB)

	var wg sync.WaitGroup
	results := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		status, _ := paySubmit(t, env, admin, orderBID, "wallet")
		results[0] = status
	}()
	go func() {
		defer wg.Done()
		status, _ := payRefund(t, env, admin, appRow.AppKey, "MO-RACE-REFUND-A", "RF-RACE", "60.00")
		results[1] = status
	}()
	wg.Wait()

	for i, want := range []int{http.StatusOK, http.StatusOK} {
		if results[i] != want {
			t.Fatalf("并发支付+退款第 %d 笔应 HTTP %d，实际 %d（results=%v）", i+1, want, results[i], results)
		}
	}
	// 余额回到 60.00（两笔皆成：-60 + 60 与顺序无关）；无负余额。
	if got := payWalletBalance(t, env, admin); got != "60.00" {
		t.Fatalf("并发后余额应 60.00，实际 %s", got)
	}
	// 流水快照重放：充值 +120 → 支付 A -60 → {支付 B -60, 退款 A +60}（并发，
	// 顺序不定）→ 最终 60.00；逐条累计必须与 balance 快照一致（账实相符）。
	var rows []struct {
		Price   string
		Balance string
	}
	if err := env.DB.Raw(
		"SELECT price, balance FROM pay_wallet_transaction ORDER BY id ASC",
	).Scan(&rows).Error; err != nil {
		t.Fatalf("查流水: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("流水应恰 4 条（充值/支付A/支付B/退款A），实际 %d", len(rows))
	}
	replayed := int64(0)
	for _, r := range rows {
		cents, err := money.ParseMoney(r.Price)
		if err != nil {
			t.Fatalf("解析流水金额 %s: %v", r.Price, err)
		}
		next, err := money.FromCents(replayed).Add(cents)
		if err != nil {
			t.Fatalf("重放累计: %v", err)
		}
		replayed = next.Cents()
		if money.FromCents(replayed).String() != r.Balance {
			t.Fatalf("流水快照重放不符：累计 %s ≠ 快照 %s", money.FromCents(replayed), r.Balance)
		}
	}
	if money.FromCents(replayed).String() != "60.00" {
		t.Fatalf("重放终值应 60.00，实际 %s", money.FromCents(replayed))
	}
	if got := payWalletBalance(t, env, admin); got != money.FromCents(replayed).String() {
		t.Fatalf("余额 %s 与重放终值 %s 不一致（账实不符）", got, money.FromCents(replayed))
	}
}

// ---------- mock 渠道：成功/失败两分支（spec §4.4：失败分支供测试） ----------

// TestIntegrationPayMockRefundBranches mock 退款两分支：缺省配置成功分支
// （退款单 10 + 订单累计 + type=2 任务 + channel_refund_no 回执）；refundResult
// =failure 配置失败分支（退款单 20 失败、订单不累计、错误列与回调原文落库、
// 无任务）。
func TestIntegrationPayMockRefundBranches(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateRefundApp(t, env, admin, "mock-refund-app", []string{"mock", "wallet"})

	// 成功分支（种子 config={} → 缺省 success）：mock 支付成功的订单部分退款。
	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-MOCK-REFUND-OK", "30.00")
	orderID := payOrderID(t, order)
	payMockPayOrder(t, env, admin, orderID)
	status, api := payRefund(t, env, admin, appRow.AppKey, "MO-MOCK-REFUND-OK", "RF-MOCK-OK", "10.00")
	mustOK(t, "mock 成功分支退款", status, api)
	okRefund := parseJSON(t, string(api.Data))
	okID := int64(okRefund["id"].(float64))
	if st, _ := okRefund["status"].(float64); st != 10 {
		t.Fatalf("mock 成功分支退款单应 10，实际 %v", okRefund["status"])
	}
	row := payRefundRow(t, env, okID)
	if row.Status != 10 {
		t.Fatalf("mock 成功分支退款单 DB 应 10，实际 %d", row.Status)
	}
	if row.ChannelRefundNo != "MOCK-"+payStrFromAny(okRefund["no"]) {
		t.Fatalf("mock 渠道回执应为 MOCK-<TK 单号>，实际 %q（TK=%v）", row.ChannelRefundNo, okRefund["no"])
	}
	if ord := payOrderRow(t, env, orderID); ord.Status != 10 || ord.RefundPrice != "10.00" {
		t.Fatalf("mock 成功分支订单应累计 10.00 且保持 10：%+v", ord)
	}
	if tasks := payRefundNotifyTasks(t, env, okID); len(tasks) != 1 {
		t.Fatalf("mock 成功分支应建 1 条回调任务，实际 %d", len(tasks))
	}

	// 失败分支（refundResult=failure 供测试）：退款单 20 失败、订单不累计、
	// 无新任务、错误码/信息与回调原文落库。
	payReplaceMockClient(t, env, "failure")
	order2 := payCreateOrder(t, env, admin, appRow.AppKey, "MO-MOCK-REFUND-FAIL", "30.00")
	order2ID := payOrderID(t, order2)
	payMockPayOrder(t, env, admin, order2ID)
	status, api = payRefund(t, env, admin, appRow.AppKey, "MO-MOCK-REFUND-FAIL", "RF-MOCK-FAIL", "5.00")
	mustOK(t, "mock 失败分支退款", status, api)
	failRefund := parseJSON(t, string(api.Data))
	failID := int64(failRefund["id"].(float64))
	if st, _ := failRefund["status"].(float64); st != 20 {
		t.Fatalf("mock 失败分支退款单应 20，实际 %v", failRefund["status"])
	}
	row = payRefundRow(t, env, failID)
	if row.Status != 20 || row.SuccessTime != nil {
		t.Fatalf("失败分支退款单应 20 且无 success_time：%+v", row)
	}
	if row.ErrorCode != "MOCK_FAIL" || row.ErrorMsg == "" || row.NotifyData == "" {
		t.Fatalf("失败分支错误列应落库（MOCK_FAIL/信息/回调原文）：%+v", row)
	}
	if ord := payOrderRow(t, env, order2ID); ord.Status != 10 || ord.RefundPrice != "0.00" {
		t.Fatalf("失败分支订单不得累计：%+v", ord)
	}
	if tasks := payRefundNotifyTasks(t, env, failID); len(tasks) != 0 {
		t.Fatalf("失败分支不得建回调任务，实际 %d 条", len(tasks))
	}
	// 失败分支后换新 merchant_refund_id 重试：订单仍 10（校验通过），mock 再失败
	// → 新退款单同样 20（失败分支可重复到达；消费方自负重试语义）。
	status, api = payRefund(t, env, admin, appRow.AppKey, "MO-MOCK-REFUND-FAIL", "RF-MOCK-FAIL2", "5.00")
	mustOK(t, "失败分支重试", status, api)
	retry := parseJSON(t, string(api.Data))
	if st, _ := retry["status"].(float64); st != 20 {
		t.Fatalf("失败分支重试退款单应仍 20，实际 %v", retry["status"])
	}
	if ord := payOrderRow(t, env, order2ID); ord.Status != 10 || ord.RefundPrice != "0.00" {
		t.Fatalf("失败分支重试订单仍不得累计：%+v", ord)
	}
	var refundCount int64
	if err := env.DB.Raw("SELECT COUNT(*) FROM pay_refund WHERE order_id = ?", order2ID).Scan(&refundCount).Error; err != nil {
		t.Fatalf("统计退款单: %v", err)
	}
	if refundCount != 2 {
		t.Fatalf("失败分支两次发起应有 2 条退款单，实际 %d", refundCount)
	}
}

// mustRefundID 从退款创建响应提取退款单 ID（断言成功）。
func mustRefundID(t *testing.T, api testutil.APIResponse) int64 {
	t.Helper()
	row := parseJSON(t, string(api.Data))
	id, _ := row["id"].(float64)
	if id == 0 {
		t.Fatalf("退款响应缺少 id：%s", api.Data)
	}
	return int64(id)
}

// payStrFromAny 断言 any 为非空字符串并返回（退款单号回执断言用）。
func payStrFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---------- 管理端与越权矩阵（spec §8.2 越权行）+ 应用删除退款引用守卫 ----------

// TestIntegrationPayRefundAdminAndGuards 管理端分页/详情（pay:refund:query，
// 详情含关联订单）、普通角色 403、跨租户 404 / 非本人 403 矩阵、应用删除
// 退款引用 409（RefundRefChecker 注册进 RefCheckerChain）。
func TestIntegrationPayRefundAdminAndGuards(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateRefundApp(t, env, admin, "refund-guard-app", []string{"mock", "wallet"})
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "退款守卫准备", Price: money.FromCents(3_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}
	order := payCreateOrder(t, env, admin, appRow.AppKey, "MO-REF-GUARD", "12.00")
	orderID := payOrderID(t, order)
	payPayOrder(t, env, admin, orderID)
	status, api := payRefund(t, env, admin, appRow.AppKey, "MO-REF-GUARD", "RF-GUARD", "3.00")
	mustOK(t, "退款准备", status, api)
	refund := parseJSON(t, string(api.Data))
	refundID := int64(refund["id"].(float64))

	// 管理端分页（固定 id DESC）。
	status, api = env.Request(t, http.MethodGet, "/pay/refund/page?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "退款分页", status, api)
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析退款分页: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("退款分页 total 应 1，实际 %d", page.Total)
	}
	// 管理端详情（含关联订单信息）。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/refund/%d", refundID), admin, nil)
	mustOK(t, "退款详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if int64(detail["id"].(float64)) != refundID {
		t.Fatalf("详情应返回退款单 %d", refundID)
	}
	ord, ok := detail["order"].(map[string]any)
	if !ok || int64(ord["id"].(float64)) != orderID {
		t.Fatalf("详情应携带关联订单 %d：%s", orderID, api.Data)
	}
	// 分页筛选：merchantRefundId 命中 1 条；非法状态筛选 400。
	status, api = env.Request(t, http.MethodGet, "/pay/refund/page?pageNo=1&pageSize=10&merchantRefundId=RF-GUARD", admin, nil)
	mustOK(t, "退款分页筛选", status, api)
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total != 1 {
		t.Fatalf("merchantRefundId 筛选应命中 1 条: %v（%s）", err, api.Data)
	}
	status, _ = env.Request(t, http.MethodGet, "/pay/refund/page?pageNo=1&pageSize=10&status=99", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法状态筛选应 400，实际 HTTP %d", status)
	}

	// 普通角色（role id=2）未授 pay:refund:query → 403（管理端点）；
	// 发起退款只挂登录但归属校验在 service 层强制 → 非本人 403。
	demo := env.Login(t, "demo", "123456")
	status, _ = env.Request(t, http.MethodGet, "/pay/refund/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色退款分页应 403，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/refund/%d", refundID), demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色退款详情应 403，实际 HTTP %d", status)
	}
	status, _ = payRefund(t, env, demo, appRow.AppKey, "MO-REF-GUARD", "RF-OTHER", "1.00")
	if status != http.StatusForbidden {
		t.Fatalf("非本人订单发起退款应 403，实际 HTTP %d", status)
	}

	// 跨租户 404：租户 2 的 token 查租户 1 退款单 / 以同 appKey 发起退款
	//（租户 2 无此应用订单，统一 404 不泄露存在性）。
	tenant2 := createTenantByAPI(t, env, admin, "退款越权矩阵租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/refund/%d", refundID), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("跨租户退款详情应 404，实际 HTTP %d", status)
	}
	payCreateRefundApp(t, env, token2, "refund-guard-app", []string{"mock", "wallet"})
	status, _ = payRefund(t, env, token2, "refund-guard-app", "MO-REF-GUARD", "RF-T2", "1.00")
	if status != http.StatusNotFound {
		t.Fatalf("跨租户订单发起退款应 404，实际 HTTP %d", status)
	}

	// 应用删除引用守卫（spec §4.10）：RefundRefChecker 报告退款引用。
	var referenced bool
	checker := payservice.RefundRefChecker{Repo: payrepo.New(env.DB)}
	referenced, _, err := checker.AppHasReference(walletCtx(1, 1), appRow.ID)
	if err != nil {
		t.Fatalf("退款引用检查: %v", err)
	}
	if !referenced {
		t.Fatalf("有退款单的应用应被 RefundRefChecker 报告为已引用")
	}
	payBindChannels(t, env, admin, appRow.ID, []string{})
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/app/%d", appRow.ID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("有订单/退款引用的应用删除应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
}
