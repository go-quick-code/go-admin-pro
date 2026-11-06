package api_test

// WebSocket 推送处理器单测（issue #79，spec §4.5.3 / ADR 0013）。
//
// 不依赖 MySQL：直接装配 PushHandler（真实 push.Hub / push.TicketManager +
// 假 resolvePrincipal），挂到 gin 上起 httptest server，用 coder/websocket
// 客户端走真实握手，覆盖 WS 特有路径：
//
//  1. 握手成功（101）与事件送达（与 SSE 同一 hub/事件模型）；
//  2. 服务端只发不收：客户端数据消息被丢弃，连接不断；
//  3. 4KB 消息上限：超限客户端被断开，服务端订阅清理；
//  4. 心跳：服务端周期 ping（客户端经 OnPongReceived 观测），
//     客户端 ping 由服务端读循环自动 pong；
//  5. 票据认证：无票据/过期 401；非升级请求 400 且不消费票据；
//  6. 断开清理：hub.Subscribers 归零，连接数不泄漏。
//
// 完整登录态/跨租户链路在 push_http_integration_test.go（真实 router + Auth）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/notify/api"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/push"
)

// wsEnv 是单测装配：真实 hub/tickets + 假身份解析。
type wsEnv struct {
	server  *httptest.Server
	hub     *push.Hub
	tickets *push.TicketManager
	baseURL string // ws:// 形式的基址
}

// fakeResolvePrincipal 把约定 token 映射为身份：tok-1-1 → (1,1)，tok-2-2 → (2,2)。
func fakeResolvePrincipal(token string) (int64, int64, bool) {
	switch token {
	case "tok-1-1":
		return 1, 1, true
	case "tok-2-2":
		return 2, 2, true
	}
	return 0, 0, false
}

// newWSEnv 起一个只挂 WS 端点的测试 server（票据经 tickets.Issue 直发，
// 换票端点的登录态链路由集成测试覆盖）。
func newWSEnv(tb testing.TB, heartbeat time.Duration) *wsEnv {
	tb.Helper()
	return newWSEnvTTL(tb, heartbeat, 0)
}

// newWSEnvTTL 同 newWSEnv，但票据 TTL 可注入（0 取默认 30 秒）。
func newWSEnvTTL(tb testing.TB, heartbeat, ttl time.Duration) *wsEnv {
	tb.Helper()
	gin.SetMode(gin.TestMode)
	hub := push.NewHub()
	tickets := push.NewTicketManager(ttl)
	h := api.NewPushHandler(hub, tickets, heartbeat, fakeResolvePrincipal)
	r := gin.New()
	r.GET("/notify/push/ws", h.WS)
	server := httptest.NewServer(r)
	tb.Cleanup(server.Close)
	return &wsEnv{
		server:  server,
		hub:     hub,
		tickets: tickets,
		baseURL: "ws" + strings.TrimPrefix(server.URL, "http"),
	}
}

// dialWS 凭票据建立 WS 连接，非 101 时 Fatal（resp/conn 交由调用方管理）。
func dialWS(tb testing.TB, env *wsEnv, ticket string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, env.baseURL+"/notify/push/ws?ticket="+ticket, opts)
	if err != nil {
		tb.Fatalf("WS 握手: %v", err)
	}
	return conn, resp
}

// TestWSHandshakeDeliversEvent 覆盖验收第 1 条：握手成功并收到通知事件，
// 且与 SSE 共用同一 hub（在线订阅数 1，事件结构与 spec §4.5.4 一致）。
func TestWSHandshakeDeliversEvent(t *testing.T) {
	env := newWSEnv(t, time.Second)
	ticket, _ := env.tickets.Issue(1, 1)
	conn, resp := dialWS(t, env, ticket, nil)
	defer conn.CloseNow()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("握手状态码 = %d, want 101", resp.StatusCode)
	}
	if got := env.hub.Subscribers(1, 1); got != 1 {
		t.Fatalf("握手后在线订阅数 = %d, want 1", got)
	}

	env.hub.Publish(1, 1, push.NewNotification(push.EventData{
		Title: "WS 实时推送", Type: "bpm_todo", BizType: "bpm_task", BizID: 42,
		Route: "/bpm/task", RouteParams: `{"taskId":42}`,
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("消息类型 = %v, want 文本", typ)
	}
	var ev push.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("解析事件 %s: %v", data, err)
	}
	if ev.Type != push.EventTypeNotification || ev.Data.Title != "WS 实时推送" ||
		ev.Data.BizID != 42 || ev.Ts <= 0 {
		t.Fatalf("事件不符: %+v", ev)
	}
}

// TestWSClientMessageDiscarded 覆盖「服务端只发不收」（spec §4.5.3）：
// 客户端发数据消息被静默丢弃，连接不断，后续事件照常送达。
func TestWSClientMessageDiscarded(t *testing.T) {
	env := newWSEnv(t, time.Second)
	ticket, _ := env.tickets.Issue(1, 1)
	conn, _ := dialWS(t, env, ticket, nil)
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"evil":"client says"}`)); err != nil {
		t.Fatalf("写客户端消息: %v", err)
	}
	// 消息之后事件照常送达（未被断连、未被当指令处理）。
	env.hub.Publish(1, 1, push.NewNotification(push.EventData{Title: "消息后送达"}))
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("发消息后读事件: %v", err)
	}
	if typ != websocket.MessageText || !strings.Contains(string(data), "消息后送达") {
		t.Fatalf("收到 = (%v, %s), want 通知事件", typ, data)
	}
}

// TestWSOversizedMessageClosed 覆盖 4KB 消息上限（issue #79）：客户端发
// >4KB 消息被服务端断开，订阅清理归零，服务端进程无恙（还能继续握手）。
func TestWSOversizedMessageClosed(t *testing.T) {
	env := newWSEnv(t, time.Second)
	ticket, _ := env.tickets.Issue(1, 1)
	conn, _ := dialWS(t, env, ticket, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, make([]byte, 5*1024)); err != nil {
		t.Fatalf("写超限消息: %v", err)
	}
	// 服务端读循环超限即断连：客户端后续读必然出错（关闭帧/EOF）。
	for {
		rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			break
		}
	}
	// 用新鲜 ctx 验证连接确实已关（读循环在等 pong 时自动应答服务端
	// 不会再有 pong——超限断开后 Ping 必失败）。
	pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pcancel()
	if err := conn.Ping(pctx); err == nil {
		t.Fatal("超限后连接应已关闭：Ping 不应成功")
	}
	if got := env.hub.Subscribers(1, 1); got != 0 {
		t.Fatalf("超限断开后订阅数 = %d, want 0", got)
	}
	// 服务端无恙：新票据仍可正常握手。
	ticket2, _ := env.tickets.Issue(1, 1)
	conn2, _ := dialWS(t, env, ticket2, nil)
	conn2.CloseNow()
}

// TestWSDisconnectCleansUp 覆盖断开清理：客户端断开后 hub.Subscribers 归零，
// 连接数不泄漏（spec §4.5 / 验收第 2 条）。
func TestWSDisconnectCleansUp(t *testing.T) {
	env := newWSEnv(t, time.Second)
	ticket, _ := env.tickets.Issue(1, 1)
	conn, _ := dialWS(t, env, ticket, nil)
	if got := env.hub.Subscribers(1, 1); got != 1 {
		t.Fatalf("在线订阅数 = %d, want 1", got)
	}
	conn.CloseNow()
	deadline := time.Now().Add(2 * time.Second)
	for env.hub.Subscribers(1, 1) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("断开后订阅未清理：Subscribers = %d", env.hub.Subscribers(1, 1))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWSHeartbeat 覆盖心跳（spec §4.5.3，issue #79 验收第 2 条）：heartbeat
// 压缩到 50ms 后，客户端经 OnPongReceived 观测服务端主动 ping（客户端库
// 自动回 pong，浏览器亦然）；随后客户端主动 Ping 成功——证明服务端读循环
// 存活且自动应答 pong，心跳期间连接不断（与 SSE 心跳测试同口径）。
func TestWSHeartbeat(t *testing.T) {
	env := newWSEnv(t, 50*time.Millisecond)
	ticket, _ := env.tickets.Issue(1, 1)
	ponged := make(chan struct{}, 4)
	conn, _ := dialWS(t, env, ticket, &websocket.DialOptions{
		// 观测服务端主动 ping：客户端库收到 ping 帧时同步回调（随后自动回 pong）。
		OnPingReceived: func(_ context.Context, _ []byte) bool { ponged <- struct{}{}; return true },
	})
	defer conn.CloseNow()

	// 客户端常驻读（等价浏览器的 WS 事件循环）：库只在 Read 期间处理入站
	// 控制帧，不读则不会自动 pong，服务端心跳观测不到。
	readCtx, readCancel := context.WithCancel(context.Background())
	defer readCancel()
	go func() {
		for {
			if _, _, err := conn.Read(readCtx); err != nil {
				return
			}
		}
	}()

	select {
	case <-ponged:
	case <-time.After(2 * time.Second):
		t.Fatal("2 秒内未观测到服务端心跳 ping（客户端库应已自动 pong）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("心跳后客户端 Ping: %v（连接不应断开）", err)
	}
}

// dialWSStatus 尝试握手并只返回 HTTP 状态码（预期失败场景用）。
func dialWSStatus(tb testing.TB, env *wsEnv, ticket, bearer string) int {
	tb.Helper()
	opts := &websocket.DialOptions{}
	if bearer != "" {
		opts.HTTPHeader = http.Header{"Authorization": {"Bearer " + bearer}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, env.baseURL+"/notify/push/ws?ticket="+ticket, opts)
	if err == nil {
		conn.CloseNow()
		tb.Fatalf("应握手失败，实际成功（status=%d）", resp.StatusCode)
	}
	if resp == nil {
		tb.Fatalf("握手失败且无 HTTP 响应: %v", err)
	}
	return resp.StatusCode
}

// wantWSUnauthorized 断言认证失败路径一律 401（信封不泄露内部原因，spec §4.11.3）。
func wantWSUnauthorized(tb testing.TB, status int, what string) {
	tb.Helper()
	if status != http.StatusUnauthorized {
		t := tb
		t.Fatalf("%s: HTTP %d, want 401", what, status)
	}
}

// TestWSAuthMatrix401 覆盖票据认证失败矩阵（与 SSE 同语义，issue #79）：
// 无票据、过期票据、票据与 Bearer 身份不一致（跨租户）一律 401。
func TestWSAuthMatrix401(t *testing.T) {
	env := newWSEnv(t, time.Second)
	wantWSUnauthorized(t, dialWSStatus(t, env, "", ""), "无票据")
	wantWSUnauthorized(t, dialWSStatus(t, env, "not-a-ticket", ""), "非法票据")

	// 过期票据：TTL 压缩到 50ms，等待后换流 401。
	expired := newWSEnvTTL(t, time.Second, 50*time.Millisecond)
	ticket, _ := expired.tickets.Issue(1, 1)
	time.Sleep(150 * time.Millisecond)
	wantWSUnauthorized(t, dialWSStatus(t, expired, ticket, ""), "过期票据")

	// Bearer 第二身份比对：租户 1 的票据配租户 2 的 Bearer → 401。
	env2 := newWSEnv(t, time.Second)
	tk, _ := env2.tickets.Issue(1, 1)
	wantWSUnauthorized(t, dialWSStatus(t, env2, tk, "tok-2-2"), "票据与 Bearer 身份不一致（跨租户）")
}

// TestWSNonUpgradeKeepsTicket 覆盖「失败路径不消费票据」：非 WebSocket
// 升级请求在票据消费之前即被拒绝（400），一次性票据完好可继续使用。
func TestWSNonUpgradeKeepsTicket(t *testing.T) {
	env := newWSEnv(t, time.Second)
	ticket, _ := env.tickets.Issue(1, 1)

	resp, err := http.Get(env.server.URL + "/notify/push/ws?ticket=" + ticket)
	if err != nil {
		t.Fatalf("普通 GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非升级请求: HTTP %d, want 400", resp.StatusCode)
	}
	// 票据未被消费：仍可凭它完成握手。
	conn, _ := dialWS(t, env, ticket, nil)
	conn.CloseNow()
}
