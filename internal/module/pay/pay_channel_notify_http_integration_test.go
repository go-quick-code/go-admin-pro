//go:build integration

// pay 渠道回调端点 HTTP 集成测试（PR #140 F1，docs/pay-mvp-spec.md §4.6 渠道
// 回调端点 + §8.2 公开端点行）：真实 router + 真实 MySQL。
//
// 覆盖 POST /pay/channel-notify/:channelCode：
//   - 公开可达（无登录头，公开组在 protected 组外）；
//   - 未实现渠道（平台启用但注册表无 Client）404、未知渠道码 404；
//   - 停用渠道（mock 经平台管理端停用）404；
//   - 解析失败（非 JSON / 缺 outTradeNo / 非法 result）4xx，订单不动；
//   - 成功全链路：mock 报文 → 订单 10 → 回调任务创建 → 重放幂等（不双建任务）；
//   - 定位失败（未知发起单号）业务失败信封 200 + code 500；
//   - 失败回调：extension 置失败、订单保持待支付、无任务；
//   - IP 限速（照 demo 回调先例，每分钟 60 次）。
package pay_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// postChannelNotify 直发渠道回调报文到公开端点（不带登录头——公开端点无登录
// 语义），返回 HTTP 状态与响应体原文。
func postChannelNotify(t *testing.T, env *testutil.IntegrationEnv, channelCode, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/pay/channel-notify/"+channelCode), strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造渠道回调请求: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求渠道回调端点: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return resp.StatusCode, string(buf)
}

// payLatestExtensionNo 查订单最近一次支付发起的 extension no（mock 回调报文
// 的 outTradeNo 定位键 = 订单号-发起序）。
func payLatestExtensionNo(t *testing.T, env *testutil.IntegrationEnv, orderID int64) string {
	t.Helper()
	var no string
	if err := env.DB.Raw("SELECT no FROM pay_order_extension WHERE order_id = ? ORDER BY id DESC LIMIT 1", orderID).Scan(&no).Error; err != nil || no == "" {
		t.Fatalf("查支付发起单号: %v（no=%q）", err, no)
	}
	return no
}

// payPendingMockOrder 走真实链路备一个 mock 待支付订单（建应用 → 绑 mock →
// 下单 → submit），返回订单 ID 与发起单号。
func payPendingMockOrder(t *testing.T, env *testutil.IntegrationEnv, admin, appKey, merchantOrderID, price string) (int64, string) {
	t.Helper()
	appRow := payCreateApp(t, env, admin, appKey)
	payBindChannels(t, env, admin, appRow.ID, []string{"mock"})
	order := payCreateOrder(t, env, admin, appRow.AppKey, merchantOrderID, price)
	orderID := payOrderID(t, order)
	status, api := paySubmit(t, env, admin, orderID, "mock")
	mustOK(t, "mock submit", status, api)
	return orderID, payLatestExtensionNo(t, env, orderID)
}

// TestIntegrationPayChannelNotifyUnknownChannel 未实现渠道 404（spec §4.6
// 「未实现/停用渠道 404」）：wx_pub 平台启用（管理端改状态）但注册表无
// Client → 404；未知渠道码（非 14 常量）→ 404。
func TestIntegrationPayChannelNotifyUnknownChannel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 平台启用 wx_pub（种子默认停用；此处验证的是「启用但未实现」分支）。
	wxpubID := channelIDByCode(t, env, admin, "wx_pub")
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", wxpubID), admin, map[string]any{
		"status": 0,
	})
	mustOK(t, "启用 wx_pub", status, api)

	for _, code := range []string{"wx_pub", "no_such_channel"} {
		body := `{"outTradeNo":"ZF202601010001-1","result":"success"}`
		httpStatus, respBody := postChannelNotify(t, env, code, body)
		if httpStatus != http.StatusNotFound {
			t.Fatalf("渠道 %s 未实现应 404，实际 HTTP %d %s", code, httpStatus, respBody)
		}
	}
}

// TestIntegrationPayChannelNotifyDisabledChannel 停用渠道 404（spec §4.6）：
// mock 有 Client 但平台管理端停用后 → 404（注册表命中也要查平台启用状态）。
func TestIntegrationPayChannelNotifyDisabledChannel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	mockID := channelIDByCode(t, env, admin, "mock")
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/channel/%d", mockID), admin, map[string]any{
		"status": 1,
	})
	mustOK(t, "停用 mock 渠道", status, api)

	body := `{"outTradeNo":"ZF202601010001-1","result":"success"}`
	httpStatus, respBody := postChannelNotify(t, env, "mock", body)
	if httpStatus != http.StatusNotFound {
		t.Fatalf("停用渠道应 404，实际 HTTP %d %s", httpStatus, respBody)
	}
}

// TestIntegrationPayChannelNotifyParseFailure 解析失败 4xx（spec §4.6
// 「验签失败 4xx」的 mock 对应——ParseOrderNotify 拒绝即 4xx）：非 JSON、
// 缺 outTradeNo、非法 result；订单保持待支付、不建任务。
func TestIntegrationPayChannelNotifyParseFailure(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	orderID, extNo := payPendingMockOrder(t, env, admin, "cnfail-app", "MO-CNFAIL", "1.00")

	cases := []struct {
		name string
		body string
	}{
		{"非 JSON", "not-json"},
		{"缺 outTradeNo", `{"result":"success"}`},
		{"非法 result", fmt.Sprintf(`{"outTradeNo":%q,"result":"bogus"}`, extNo)},
	}
	for _, tc := range cases {
		httpStatus, respBody := postChannelNotify(t, env, "mock", tc.body)
		if httpStatus < 400 || httpStatus > 499 {
			t.Fatalf("%s：解析失败应 4xx，实际 HTTP %d %s", tc.name, httpStatus, respBody)
		}
	}
	// 订单不动（保持 0 待支付），无回调任务。
	assertOrderStatus(t, env, orderID, 0, "解析失败订单应保持待支付")
	if tasks := payNotifyTasks(t, env, orderID); len(tasks) != 0 {
		t.Fatalf("解析失败不应建回调任务，实际 %d 条", len(tasks))
	}
}

// TestIntegrationPayChannelNotifySuccessChain 成功全链路（spec §4.6）：mock
// 报文 → 订单 10 → 回调任务创建；重放同一报文幂等（仍 code 0、任务不双建、
// 订单不变）——支付成功入账函数的状态认领是幂等裁决；未知发起单号 → 业务失败
// 信封（200 + code 500，联动失败语义同 demo 端点）。
func TestIntegrationPayChannelNotifySuccessChain(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	orderID, extNo := payPendingMockOrder(t, env, admin, "cnok-app", "MO-CNOK", "7.50")

	successBody := fmt.Sprintf(`{"outTradeNo":%q,"result":"success"}`, extNo)
	httpStatus, respBody := postChannelNotify(t, env, "mock", successBody)
	if httpStatus != http.StatusOK || !strings.Contains(respBody, `"code":0`) {
		t.Fatalf("成功回调应 200 code 0，实际 HTTP %d %s", httpStatus, respBody)
	}

	// 订单 10 + 恰好 1 条回调任务（type=1）。
	assertOrderStatus(t, env, orderID, 10, "成功回调应置订单 10")
	tasks := payNotifyTasks(t, env, orderID)
	if len(tasks) != 1 {
		t.Fatalf("成功回调应建 1 条回调任务，实际 %d 条", len(tasks))
	}
	if payNum(tasks[0]["type"]) != 1 {
		t.Fatalf("回调任务应为 type=1（支付）：%v", tasks[0])
	}

	// 重放同一报文：幂等成功应答，任务不双建、订单不变。
	httpStatus, respBody = postChannelNotify(t, env, "mock", successBody)
	if httpStatus != http.StatusOK || !strings.Contains(respBody, `"code":0`) {
		t.Fatalf("重放回调应幂等 200 code 0，实际 HTTP %d %s", httpStatus, respBody)
	}
	if tasks := payNotifyTasks(t, env, orderID); len(tasks) != 1 {
		t.Fatalf("重放回调不应重复建任务，实际 %d 条", len(tasks))
	}
	assertOrderStatus(t, env, orderID, 10, "重放回调订单应保持 10")

	// 未知发起单号：定位失败 → 业务失败信封（200 + code 500，回调方可重试）。
	httpStatus, respBody = postChannelNotify(t, env, "mock", `{"outTradeNo":"ZF202601099999-9","result":"success"}`)
	if httpStatus != http.StatusOK || !strings.Contains(respBody, `"code":500`) {
		t.Fatalf("未知发起单号应 200 code 500，实际 HTTP %d %s", httpStatus, respBody)
	}
}

// TestIntegrationPayChannelNotifyFailureResult 失败回调（mock 报文
// result=failure）：extension 置失败、订单保持待支付、不建任务；应答仍
// code 0（失败是合法回调结论，渠道无需重试）。
func TestIntegrationPayChannelNotifyFailureResult(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	orderID, extNo := payPendingMockOrder(t, env, admin, "cnfailres-app", "MO-CNFAILRES", "2.00")

	body := fmt.Sprintf(`{"outTradeNo":%q,"result":"failure"}`, extNo)
	httpStatus, respBody := postChannelNotify(t, env, "mock", body)
	if httpStatus != http.StatusOK || !strings.Contains(respBody, `"code":0`) {
		t.Fatalf("失败回调应 200 code 0，实际 HTTP %d %s", httpStatus, respBody)
	}
	assertOrderStatus(t, env, orderID, 0, "失败回调订单应保持待支付")
	if tasks := payNotifyTasks(t, env, orderID); len(tasks) != 0 {
		t.Fatalf("失败回调不应建任务，实际 %d 条", len(tasks))
	}
	var extStatus int8
	if err := env.DB.Raw("SELECT status FROM pay_order_extension WHERE order_id = ? ORDER BY id DESC LIMIT 1", orderID).Scan(&extStatus).Error; err != nil {
		t.Fatalf("查支付发起状态: %v", err)
	}
	if extStatus != 20 {
		t.Fatalf("失败回调应把发起置 20 失败，实际 %d", extStatus)
	}
}

// TestIntegrationPayChannelNotifyRateLimit IP 限速（照 demo 回调先例，每分钟
// 60 次）：窗口内第 61 次起 429。
func TestIntegrationPayChannelNotifyRateLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	var saw429 bool
	for i := 1; i <= 61; i++ {
		status, _ := postChannelNotify(t, env, "mock", `{"outTradeNo":"ZF-RATELIMIT-1","result":"success"}`)
		if status == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatal("窗口内第 61 次起应触发 429 限速")
	}
}
