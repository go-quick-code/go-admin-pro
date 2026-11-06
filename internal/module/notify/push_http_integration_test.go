//go:build integration

// 实时推送 HTTP 集成测试（issue #78，spec §4.5 / ADR 0013）。
//
// 约定的 seam：
//  1. 票据换发 POST /notify/push/ticket（真实 router + Auth）：票据形状、
//     一次性/过期/跨租户 401、WS 占位不消费票据。
//  2. SSE 流 GET /notify/push/sse?ticket=（真实 router，凭票据公开）：
//     响应头、首字节、事件到达（确定性等待）、心跳、断开清理。
//  3. 发布链路 env.App.Notify.Create（与生产装配同一 service 实例）：
//     通知创建 1 秒内在线用户收到；写入失败（回滚）不产生事件。
//
// 捕捉不到 hub 的多连接扇出与慢消费者丢弃计数——那在 internal/pkg/push 单测里。
//
// 测试库走 GAP_TEST_DATABASE_DSN（协调约定用 go_admin_pro_test_p78 后缀）：
//
//	GAP_TEST_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test_p78?charset=utf8mb4&parseTime=True&loc=Local' \
//	  go test -tags=integration ./internal/module/notify/...
package notify_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// pushTicket 是换票端点响应。
type pushTicket struct {
	Ticket     string   `json:"ticket"`
	ExpiresIn  int      `json:"expiresIn"`
	Transports []string `json:"transports"`
}

// issuePushTicket 用登录态换一张一次性推送票据。
func issuePushTicket(tb testing.TB, env *testutil.IntegrationEnv, token string) pushTicket {
	tb.Helper()
	status, api := env.Request(tb, http.MethodPost, "/notify/push/ticket", token, nil)
	mustCode0(tb, status, api, "POST /notify/push/ticket")
	var t pushTicket
	if err := json.Unmarshal(api.Data, &t); err != nil {
		tb.Fatalf("解析换票响应: %v（data=%s）", err, string(api.Data))
	}
	return t
}

// openSSE 打开 SSE 流（凭票据；bearer 非空时做第二身份比对）。
// 返回响应由调用方关闭。非 200 时响应体是 JSON 信封，bodyText 可读出。
func openSSE(tb testing.TB, env *testutil.IntegrationEnv, ticket, bearer string) (*http.Response, string) {
	tb.Helper()
	u := env.APIPrefixURL("/notify/push/sse") + "?ticket=" + url.QueryEscape(ticket)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		tb.Fatalf("构造 SSE 请求: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		tb.Fatalf("GET /notify/push/sse: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		return resp, string(buf[:n])
	}
	return resp, ""
}

// sseStream 在后台逐行读取 SSE 响应体，行经 channel 交给测试做确定性等待。
type sseStream struct {
	lines chan string
	done  chan struct{}
}

func streamSSE(tb testing.TB, resp *http.Response) *sseStream {
	tb.Helper()
	s := &sseStream{lines: make(chan string, 64), done: make(chan struct{})}
	go func() {
		// done 只由读 goroutine 关闭（流自然结束或 body 关闭触发），
		// 不在 Cleanup 里重复 close（避免 panic: close of closed channel）。
		defer close(s.done)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case s.lines <- scanner.Text():
			case <-s.done:
				return
			}
		}
	}()
	tb.Cleanup(func() { resp.Body.Close() })
	return s
}

// nextLine 等待下一行（within 内未到即失败）。
func (s *sseStream) nextLine(tb testing.TB, what string, within time.Duration) string {
	tb.Helper()
	select {
	case line := <-s.lines:
		return line
	case <-time.After(within):
		tb.Fatalf("SSE 等待 %s 超时（%v）", what, within)
	case <-s.done:
		tb.Fatalf("SSE 等待 %s 时流已关闭", what)
	}
	return ""
}

// nextEvent 等待下一条 data 事件行（within 内未到即失败），跳过注释与空行。
func (s *sseStream) nextEvent(tb testing.TB, what string, within time.Duration) string {
	tb.Helper()
	deadline := time.Now().Add(within)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			tb.Fatalf("SSE 等待 %s 超时（%v）", what, within)
		}
		select {
		case line := <-s.lines:
			if strings.HasPrefix(line, "data: ") {
				return strings.TrimPrefix(line, "data: ")
			}
		case <-time.After(remain):
			tb.Fatalf("SSE 等待 %s 超时（%v）", what, within)
		case <-s.done:
			tb.Fatalf("SSE 等待 %s 时流已关闭", what)
		}
	}
}

// nextPrefixed 等待下一条以 prefix 开头的行（within 内未到即失败），
// 跳过空行等其他行——SSE 每条输出后都有空行分隔。
func (s *sseStream) nextPrefixed(tb testing.TB, what, prefix string, within time.Duration) string {
	tb.Helper()
	deadline := time.Now().Add(within)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			tb.Fatalf("SSE 等待 %s 超时（%v）", what, within)
		}
		select {
		case line := <-s.lines:
			if strings.HasPrefix(line, prefix) {
				return line
			}
		case <-time.After(remain):
			tb.Fatalf("SSE 等待 %s 超时（%v）", what, within)
		case <-s.done:
			tb.Fatalf("SSE 等待 %s 时流已关闭", what)
		}
	}
}

// wantUnauthorized 断言 SSE 换流失败返回 HTTP 401（信封不泄露内部原因）。
func wantUnauthorized(tb testing.TB, status int, body, what string) {
	tb.Helper()
	if status != http.StatusUnauthorized {
		tb.Fatalf("%s: HTTP %d, want 401（body=%s）", what, status, body)
	}
}

// TestIntegrationPushTicket_HTTP 覆盖票据形状：43 字符 base64url、TTL 秒数、
// transports 本期仅 sse（ws 由 #79 追加）。
func TestIntegrationPushTicket_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	tk := issuePushTicket(t, env, token)
	if len(tk.Ticket) != 43 {
		t.Fatalf("ticket 长度 = %d, want 43（32 字节 base64url 无填充）", len(tk.Ticket))
	}
	if tk.ExpiresIn != 30 {
		t.Fatalf("expiresIn = %d, want 30（spec §4.5.2 TTL 30 秒）", tk.ExpiresIn)
	}
	if len(tk.Transports) != 1 || tk.Transports[0] != "sse" {
		t.Fatalf("transports = %v, want [sse]", tk.Transports)
	}
}

// TestIntegrationPushSSE_DeliversWithinSecond_HTTP 覆盖 SSE 主链路：
// 响应头正确、首条注释行即达、通知创建 1 秒内在线用户收到、事件结构符合
// spec §4.5.4。
func TestIntegrationPushSSE_DeliversWithinSecond_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	if env.App.PushHub == nil {
		t.Fatal("默认配置应启用推送（PushHub 为 nil）")
	}
	tk := issuePushTicket(t, env, token)
	resp, body := openSSE(t, env, tk.Ticket, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 连接: HTTP %d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
	stream := streamSSE(t, resp)
	if line := stream.nextLine(t, "首条注释行", 2*time.Second); !strings.HasPrefix(line, ": ") {
		t.Fatalf("首行 = %q, want 注释行（流建立即有字节）", line)
	}

	// 在线用户 1 秒内收到：与生产装配同一 service 实例创建通知。
	ctx := tenant.WithTenantID(t.Context(), tenant.DefaultID)
	start := time.Now()
	if err := env.App.Notify.Create(ctx, []service.Input{
		{UserID: 1, Type: model.TypeBpmTodo, Title: "SSE 实时推送", Content: "合同审批待办",
			BizType: "bpm_task", BizID: 42, Route: "/bpm/task", RouteParams: `{"taskId":42}`},
	}); err != nil {
		t.Fatalf("创建通知: %v", err)
	}
	raw := stream.nextEvent(t, "通知事件", time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("事件到达耗时 %v, want <= 1s", elapsed)
	}
	var ev struct {
		Type string `json:"type"`
		Data struct {
			Title       string `json:"title"`
			Content     string `json:"content"`
			Type        string `json:"type"`
			BizType     string `json:"bizType"`
			BizID       int64  `json:"bizId"`
			Route       string `json:"route"`
			RouteParams string `json:"routeParams"`
		} `json:"data"`
		Ts int64 `json:"ts"`
	}
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("解析事件 %q: %v", raw, err)
	}
	if ev.Type != "notification" {
		t.Fatalf("type = %q, want notification", ev.Type)
	}
	if ev.Data.Title != "SSE 实时推送" || ev.Data.Type != model.TypeBpmTodo ||
		ev.Data.BizType != "bpm_task" || ev.Data.BizID != 42 ||
		ev.Data.Route != "/bpm/task" || ev.Data.RouteParams != `{"taskId":42}` {
		t.Fatalf("data 不符: %+v", ev.Data)
	}
	if ev.Ts <= 0 {
		t.Fatalf("ts = %d, want unix 秒", ev.Ts)
	}
}

// TestIntegrationPushSSE_Heartbeat_HTTP 覆盖心跳注释行：默认 25s，测试用
// GAP_PUSH_HEARTBEAT 压缩后确定性等待（心跳期间不结束连接）。
func TestIntegrationPushSSE_Heartbeat_HTTP(t *testing.T) {
	t.Setenv("GAP_PUSH_HEARTBEAT", "150ms")
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	tk := issuePushTicket(t, env, token)
	resp, body := openSSE(t, env, tk.Ticket, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 连接: HTTP %d body=%s", resp.StatusCode, body)
	}
	stream := streamSSE(t, resp)
	stream.nextLine(t, "首条注释行", 2*time.Second)
	if line := stream.nextPrefixed(t, "心跳注释", ": ping", 2*time.Second); line == "" {
		t.Fatal("心跳行不应为空")
	}
	// 心跳之后流仍可用：再等一次心跳确认连接未断。
	if line := stream.nextPrefixed(t, "第二次心跳", ": ping", 2*time.Second); line == "" {
		t.Fatal("第二次心跳不应为空")
	}
}

// TestIntegrationPushTicketReplay401_HTTP 覆盖一次性：首连消费后重放 401。
func TestIntegrationPushTicketReplay401_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	tk := issuePushTicket(t, env, token)

	resp, body := openSSE(t, env, tk.Ticket, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("首连: HTTP %d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	resp2, body2 := openSSE(t, env, tk.Ticket, "")
	defer resp2.Body.Close()
	wantUnauthorized(t, resp2.StatusCode, body2, "重放票据")
}

// TestIntegrationPushTicketExpired401_HTTP 覆盖过期：TTL 压缩到 50ms，
// 等待后换流 401。
func TestIntegrationPushTicketExpired401_HTTP(t *testing.T) {
	t.Setenv("GAP_PUSH_TICKET_TTL", "50ms")
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	tk := issuePushTicket(t, env, token)
	time.Sleep(150 * time.Millisecond)

	resp, body := openSSE(t, env, tk.Ticket, "")
	defer resp.Body.Close()
	wantUnauthorized(t, resp.StatusCode, body, "过期票据")
}

// TestIntegrationPushSSE_BearerMismatch401_HTTP 覆盖跨租户 401：带 Bearer 的
// 客户端，票据与 Bearer 身份不一致（跨租户/跨用户）一律拒绝；一致则放行。
func TestIntegrationPushSSE_BearerMismatch401_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	// 建第二个租户并登录其管理员（默认 admin/123456）。
	status, api := env.Request(t, http.MethodPost, "/system/tenant", adminToken, map[string]any{
		"name": "推送隔离租户", "contactName": "联系人", "status": 0,
	})
	mustCode0(t, status, api, "创建租户")
	tenant2Token := loginTenant(t, env, 2)

	// 平台租户票据 + 租户 2 的 Bearer：跨租户使用 → 401。
	tk := issuePushTicket(t, env, adminToken)
	resp, body := openSSE(t, env, tk.Ticket, tenant2Token)
	defer resp.Body.Close()
	wantUnauthorized(t, resp.StatusCode, body, "票据与 Bearer 身份不一致")

	// Bearer 与票据同人：放行。
	tk2 := issuePushTicket(t, env, adminToken)
	resp2, body2 := openSSE(t, env, tk2.Ticket, adminToken)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("Bearer 与票据同人: HTTP %d body=%s", resp2.StatusCode, body2)
	}
	resp2.Body.Close()
}

// loginTenant 以指定租户身份登录（新租户默认管理员 admin/123456）。
func loginTenant(tb testing.TB, env *testutil.IntegrationEnv, tenantID int64) string {
	tb.Helper()
	status, api := env.Request(tb, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin",
		"password": "123456",
		"tenantId": tenantID,
	})
	if status != http.StatusOK || api.Code != 0 {
		tb.Fatalf("租户 %d 管理员登录: HTTP %d code %d msg %q", tenantID, status, api.Code, api.Msg)
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		tb.Fatalf("解析登录响应: %v", err)
	}
	return data.AccessToken
}

// TestIntegrationPushCrossTenantIsolation_HTTP 覆盖订阅隔离：跨租户票据
// 不能订阅他人事件——租户 2 的连接收不到租户 1 的推送，反之亦然。
func TestIntegrationPushCrossTenantIsolation_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/system/tenant", adminToken, map[string]any{
		"name": "推送隔离租户B", "contactName": "联系人", "status": 0,
	})
	mustCode0(t, status, api, "创建租户")
	tenant2Token := loginTenant(t, env, 2)

	// 两条流各自建立。
	resp1, body1 := openSSE(t, env, issuePushTicket(t, env, adminToken).Ticket, "")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("租户 1 SSE: HTTP %d body=%s", resp1.StatusCode, body1)
	}
	stream1 := streamSSE(t, resp1)
	resp2, body2 := openSSE(t, env, issuePushTicket(t, env, tenant2Token).Ticket, "")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("租户 2 SSE: HTTP %d body=%s", resp2.StatusCode, body2)
	}
	stream2 := streamSSE(t, resp2)
	stream1.nextLine(t, "租户1首条注释", 2*time.Second)
	stream2.nextLine(t, "租户2首条注释", 2*time.Second)

	// 租户 2 管理员的用户 ID（bootstrapTenant 自动创建）。
	var tenant2UserID int64
	if err := env.DB.Raw("SELECT id FROM system_user WHERE tenant_id = 2 AND username = 'admin'").Scan(&tenant2UserID).Error; err != nil || tenant2UserID == 0 {
		t.Fatalf("查询租户 2 管理员: err=%v id=%d", err, tenant2UserID)
	}

	// 只推租户 1 的 admin：租户 2 的流必须静默。
	ctx := tenant.WithTenantID(t.Context(), tenant.DefaultID)
	if err := env.App.Notify.Create(ctx, []service.Input{
		{UserID: 1, Type: model.TypeAnnouncement, Title: "租户 1 专属"},
	}); err != nil {
		t.Fatalf("创建通知: %v", err)
	}
	raw := stream1.nextEvent(t, "租户 1 事件", time.Second)
	var ev struct {
		Data struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("解析事件: %v", err)
	}
	if ev.Data.Title != "租户 1 专属" {
		t.Fatalf("租户 1 收到 %q, want 租户 1 专属", ev.Data.Title)
	}
	select {
	case line := <-stream2.lines:
		if strings.HasPrefix(line, "data: ") {
			t.Fatalf("租户 2 不应收到租户 1 的事件: %s", line)
		}
	case <-time.After(300 * time.Millisecond):
	}

	// 推租户 2 管理员：只有租户 2 的流收到。
	if err := env.App.Notify.Create(tenant.WithTenantID(t.Context(), 2), []service.Input{
		{UserID: tenant2UserID, Type: model.TypeAnnouncement, Title: "租户 2 专属"},
	}); err != nil {
		t.Fatalf("创建通知: %v", err)
	}
	raw2 := stream2.nextEvent(t, "租户 2 事件", time.Second)
	var ev2 struct {
		Data struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw2), &ev2); err != nil {
		t.Fatalf("解析事件: %v", err)
	}
	if ev2.Data.Title != "租户 2 专属" {
		t.Fatalf("租户 2 收到 %q, want 租户 2 专属", ev2.Data.Title)
	}
	select {
	case line := <-stream1.lines:
		if strings.HasPrefix(line, "data: ") {
			t.Fatalf("租户 1 不应收到租户 2 的事件: %s", line)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// TestIntegrationPushNoEventOnFailedCreate_HTTP 覆盖事务语义：写入失败
// （校验失败整批不写）不产生任何推送事件——回滚不推送。
func TestIntegrationPushNoEventOnFailedCreate_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	tk := issuePushTicket(t, env, token)

	resp, body := openSSE(t, env, tk.Ticket, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 连接: HTTP %d body=%s", resp.StatusCode, body)
	}
	stream := streamSSE(t, resp)
	stream.nextLine(t, "首条注释行", 2*time.Second)

	// 非法类型 → 整批不写；无事务提交即无发布。
	ctx := tenant.WithTenantID(t.Context(), tenant.DefaultID)
	if err := env.App.Notify.Create(ctx, []service.Input{
		{UserID: 1, Type: "no_such_type", Title: "不应送达"},
	}); err == nil {
		t.Fatal("非法类型应报错")
	}
	select {
	case line := <-stream.lines:
		if strings.HasPrefix(line, "data: ") {
			t.Fatalf("写入失败不应推送事件: %s", line)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// TestIntegrationPushDisconnectCleansUp_HTTP 覆盖断开清理：客户端断开后
// hub 订阅归零，连接数不泄漏。
func TestIntegrationPushDisconnectCleansUp_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	tk := issuePushTicket(t, env, token)

	resp, body := openSSE(t, env, tk.Ticket, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 连接: HTTP %d body=%s", resp.StatusCode, body)
	}
	stream := streamSSE(t, resp)
	stream.nextLine(t, "首条注释行", 2*time.Second)
	if got := env.App.PushHub.Subscribers(tenant.DefaultID, 1); got != 1 {
		t.Fatalf("在线订阅数 = %d, want 1", got)
	}

	resp.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if env.App.PushHub.Subscribers(tenant.DefaultID, 1) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("断开后订阅未清理：Subscribers = %d", env.App.PushHub.Subscribers(tenant.DefaultID, 1))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestIntegrationPushWSPlaceholder_HTTP 覆盖 WS 占位：501 且不消费票据
// （票据仍可用于 SSE）。
func TestIntegrationPushWSPlaceholder_HTTP(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	tk := issuePushTicket(t, env, token)

	status, api := env.Request(t, http.MethodGet, "/notify/push/ws?ticket="+url.QueryEscape(tk.Ticket), "", nil)
	if status != http.StatusNotImplemented || api.Code != http.StatusNotImplemented {
		t.Fatalf("WS 占位: HTTP %d code %d, want 501", status, api.Code)
	}

	// 票据未被消费：SSE 仍可正常建立。
	resp, body := openSSE(t, env, tk.Ticket, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("WS 占位不应消费票据: HTTP %d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

// TestIntegrationPushDisabled_HTTP 覆盖推送关闭：路由不注册（换票 404），
// SSE 端点不存在；前端据此降级轮询。
func TestIntegrationPushDisabled_HTTP(t *testing.T) {
	t.Setenv("GAP_PUSH_ENABLED", "false")
	env := testutil.NewIntegrationEnv(t)
	if env.App.PushHub != nil {
		t.Fatal("push.enabled=false 时 PushHub 应为 nil")
	}
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/notify/push/ticket", token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("推送关闭时换票: HTTP %d code %d, want 404", status, api.Code)
	}
	status2, _ := env.Request(t, http.MethodGet, "/notify/push/sse?ticket=x", "", nil)
	if status2 != http.StatusNotFound {
		t.Fatalf("推送关闭时 SSE: HTTP %d, want 404", status2)
	}
}
