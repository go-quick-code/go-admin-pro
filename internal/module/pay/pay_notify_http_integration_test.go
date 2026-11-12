//go:build integration

// pay 回调域 HTTP 集成测试（issue #134，docs/pay-mvp-spec.md §8.2 回调行 +
// §8.1 job seam）：真实 router + 真实 MySQL + httptest 商户端点。
//
// 覆盖：重试响应语义（10/21/22）、9 次彻底失败、并发认领防重发、日志
// append-only 与响应摘要、签名头（HMAC-SHA256）、密钥缺失拒发（fail-closed）、
// demo 回调三端点公开可达/伪造签名 4xx/IP 限速/E2E 联动、回调任务管理端
// （普通角色 403、跨租户 404）、订单超时关闭。
//
// job seam：handler 直调（Scheduler.Execute / Service.RunNotifyRetry），
// 不走真实 cron（spec §8.1）。
package pay_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// notifyMerchant 是模拟商户回调端点：记录每次收到的请求（方法/路径/签名头/
// 报文原文），按序返回预设应答（预设耗尽后重复最后一个）。
type notifyMerchant struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []notifyRequestRecord
	statuses []int
	bodies   []string
}

type notifyRequestRecord struct {
	Method    string
	Path      string
	Signature string
	Body      string
}

func newNotifyMerchant(t *testing.T, statuses []int, bodies []string) *notifyMerchant {
	t.Helper()
	m := &notifyMerchant{statuses: statuses, bodies: bodies}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAllLimited(r)
		m.mu.Lock()
		m.requests = append(m.requests, notifyRequestRecord{
			Method:    r.Method,
			Path:      r.URL.Path,
			Signature: r.Header.Get("Pay-Signature"),
			Body:      string(body),
		})
		seq := len(m.requests) - 1
		m.mu.Unlock()
		status := statuses[len(statuses)-1]
		if seq < len(statuses) {
			status = statuses[seq]
		}
		resp := bodies[len(bodies)-1]
		if seq < len(bodies) {
			resp = bodies[seq]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func readAllLimited(r *http.Request) ([]byte, error) {
	const limit = 1 << 20
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil || len(buf) > limit {
			return buf, nil
		}
	}
}

func (m *notifyMerchant) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *notifyMerchant) request(seq int) notifyRequestRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests[seq]
}

// notifyHMAC 用配置密钥计算报文签名（独立于被测实现：测试侧直接 hmac 标准库）。
func notifyHMAC(key string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// payMockPaySuccess 走真实链路把订单支付成功（submit mock + 模拟成功回调），
// 返回订单的回调任务 ID（支付成功入账函数创建 type=1 任务）。
func payMockPaySuccess(t *testing.T, env *testutil.IntegrationEnv, admin, appKey, merchantOrderID, price string) (orderID, taskID int64) {
	t.Helper()
	// 绑定 mock 渠道（payCreateAppWithNotifyURL 只建应用不绑定）。
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/app/%d/channels", payAppIDByKey(t, env, admin, appKey)), admin, map[string]any{
		"channelCodes": []string{"mock"},
	})
	mustOK(t, "绑定 mock 渠道", status, api)
	order := payCreateOrder(t, env, admin, appKey, merchantOrderID, price)
	orderID = payOrderID(t, order)
	status, api = paySubmit(t, env, admin, orderID, "mock")
	mustOK(t, "mock submit", status, api)
	status, api = env.Request(t, http.MethodPost, "/pay/mock/notify", admin, map[string]any{"orderId": orderID, "result": "success"})
	mustOK(t, "模拟支付成功", status, api)
	tasks := payNotifyTasks(t, env, orderID)
	if len(tasks) != 1 {
		t.Fatalf("支付成功应建 1 条回调任务，实际 %d", len(tasks))
	}
	return orderID, payNum(tasks[0]["id"])
}

// payRunNotifyJob 直调回调重试任务（job seam，spec §8.1）：经 Scheduler 按
// handler 名解析（同时验证系统级注册）。
func payRunNotifyJob(t *testing.T, env *testutil.IntegrationEnv) {
	t.Helper()
	if !env.App.Scheduler.IsSystemHandler(payservice.NotifyRetryJobHandlerName) {
		t.Fatalf("%s 应注册为系统级任务处理器（跨租户动作）", payservice.NotifyRetryJobHandlerName)
	}
	env.App.Scheduler.Execute(inframodel.Job{TenantID: 1, HandlerName: payservice.NotifyRetryJobHandlerName})
}

// inframodelJob 构造直调任务的 job 行（系统级 handler 以租户 1 身份触发，
// handler 内部按启用租户迭代）。
func inframodelJob(handler string) inframodel.Job {
	return inframodel.Job{TenantID: 1, HandlerName: handler}
}

// payBackdateTask 把任务 next_notify_time 拨到过去（重试逐跳断言，不睡时钟）。
func payBackdateTask(t *testing.T, env *testutil.IntegrationEnv, taskID int64) {
	t.Helper()
	if err := env.DB.Exec("UPDATE pay_notify_task SET next_notify_time = DATE_SUB(NOW(), INTERVAL 1 SECOND) WHERE id = ?", taskID).Error; err != nil {
		t.Fatalf("回拨任务 next_notify_time: %v", err)
	}
}

// payNotifyLogs 查任务执行日志（append-only 断言用）。
func payNotifyLogs(t *testing.T, env *testutil.IntegrationEnv, taskID int64) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw("SELECT id, task_id, notify_times AS notifyTimes, response, status FROM pay_notify_log WHERE task_id = ? ORDER BY id ASC", taskID).Scan(&rows).Error; err != nil {
		t.Fatalf("查回调日志: %v", err)
	}
	return rows
}

// payTaskRow 查单个任务行（含时间字段）。
func payTaskRow(t *testing.T, env *testutil.IntegrationEnv, taskID int64) map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw("SELECT id, status, notify_times AS notifyTimes, next_notify_time AS nextNotifyTime, last_execute_time AS lastExecuteTime FROM pay_notify_task WHERE id = ?", taskID).Scan(&rows).Error; err != nil {
		t.Fatalf("查回调任务: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("回调任务 %d 应存在，实际 %d 行", taskID, len(rows))
	}
	return rows[0]
}

// ---------- 响应语义 + 签名 + 日志（spec §8.2 回调行） ----------

// TestIntegrationPayNotifyRetrySuccess 成功链路：2xx + {"code":0} → 任务 10
// 成功、日志 status=10、签名头与报文内容正确、next_notify_time 清空。
func TestIntegrationPayNotifyRetrySuccess(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	merchant := newNotifyMerchant(t, []int{200}, []string{`{"code":0,"msg":"success"}`})
	appRow := payCreateAppWithNotifyURL(t, env, admin, "notify-ok-app", merchant.srv.URL+"/pay/notify")

	orderID, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-NOTIFY-OK", "8.00")

	payRunNotifyJob(t, env)

	// 商户恰好收到 1 次 POST，签名与报文正确。
	if got := merchant.count(); got != 1 {
		t.Fatalf("商户应恰好收到 1 次回调，实际 %d", got)
	}
	req := merchant.request(0)
	if req.Method != http.MethodPost || req.Path != "/pay/notify" {
		t.Fatalf("回调应为 POST %s，实际 %s %s", "/pay/notify", req.Method, req.Path)
	}
	wantSig := notifyHMAC(env.Config.Pay.NotifyHMACKey, []byte(req.Body))
	if req.Signature != wantSig {
		t.Fatalf("Pay-Signature 应为 HMAC-SHA256(rawBody, 配置密钥)：%q 期望 %q", req.Signature, wantSig)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(req.Body), &payload); err != nil {
		t.Fatalf("回调报文必须是 JSON：%v（%s）", err, req.Body)
	}
	if payload["merchantOrderId"] != "MO-NOTIFY-OK" {
		t.Fatalf("回调报文应带消费方单号：%s", req.Body)
	}
	if payload["type"].(float64) != 1 || payload["status"] != "success" {
		t.Fatalf("回调报文 type/status 不符：%s", req.Body)
	}
	if payload["dataId"].(float64) != float64(orderID) || payload["orderNo"] == "" || payload["price"] != "8.00" {
		t.Fatalf("回调报文应带订单详情（type=1 内置 enricher）：%s", req.Body)
	}

	// 任务 10 成功 + 日志 1 行（status 10，摘要截断保留响应原文）。
	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 10 {
		t.Fatalf("code=0 应使任务转 10 成功，实际 %v", task["status"])
	}
	if task["nextNotifyTime"] != nil {
		t.Fatalf("成功任务应清空 next_notify_time，实际 %v", task["nextNotifyTime"])
	}
	logs := payNotifyLogs(t, env, taskID)
	if len(logs) != 1 {
		t.Fatalf("成功链路应恰好 1 条日志，实际 %d", len(logs))
	}
	if payNum(logs[0]["status"]) != 10 || payNum(logs[0]["notifyTimes"]) != 1 {
		t.Fatalf("日志应为第 1 次且 status=10：%v", logs[0])
	}
	if !strings.Contains(payStr(logs[0]["response"]), `"code":0`) {
		t.Fatalf("日志响应摘要应保留商户应答原文：%q", payStr(logs[0]["response"]))
	}
}

// TestIntegrationPayNotifyRetrySemantics 21/22 语义与重试逐跳：
//   - 2xx 业务码非 0 → 21（继续重试，间隔 15s）；
//   - 非 2xx → 22（继续重试）；
//   - 每次执行追加日志（append-only，notify_times 递增）。
func TestIntegrationPayNotifyRetrySemantics(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	merchant := newNotifyMerchant(t, []int{200, 500, 200}, []string{
		`{"code":1001,"msg":"订单不存在"}`,
		`internal error`,
		`{"code":0,"msg":"success"}`,
	})
	appRow := payCreateAppWithNotifyURL(t, env, admin, "notify-sem-app", merchant.srv.URL+"/pay/notify")
	_, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-NOTIFY-SEM", "5.00")

	// 第 1 轮：2xx 业务码非 0 → 21，next_notify_time = +15s（第 1 次失败的间隔）。
	payRunNotifyJob(t, env)
	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 21 {
		t.Fatalf("2xx 业务码非 0 应转 21，实际 %v", task["status"])
	}
	assertRetryIn(t, env, taskID, 15*time.Second, "第 1 次失败后")
	if logs := payNotifyLogs(t, env, taskID); len(logs) != 1 || payNum(logs[0]["status"]) != 21 {
		t.Fatalf("第 1 轮应有 1 条 status=21 日志：%v", logs)
	}

	// 第 2 轮（回拨后重试）：非 2xx → 22，间隔仍 15s。
	payBackdateTask(t, env, taskID)
	payRunNotifyJob(t, env)
	task = payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 22 {
		t.Fatalf("非 2xx 应转 22，实际 %v", task["status"])
	}
	assertRetryIn(t, env, taskID, 15*time.Second, "第 2 次失败后")
	if payNum(task["notifyTimes"]) != 2 {
		t.Fatalf("两次执行后 notify_times 应为 2，实际 %v", task["notifyTimes"])
	}

	// 第 3 轮：code=0 → 10 成功（21/22 语义确认后重试可恢复成功）。
	payBackdateTask(t, env, taskID)
	payRunNotifyJob(t, env)
	task = payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 10 {
		t.Fatalf("重试成功应转 10，实际 %v", task["status"])
	}
	logs := payNotifyLogs(t, env, taskID)
	if len(logs) != 3 {
		t.Fatalf("三轮应恰好 3 条 append-only 日志，实际 %d", len(logs))
	}
	for i, want := range []int64{21, 22, 10} {
		if payNum(logs[i]["status"]) != want {
			t.Fatalf("日志 %d 的 status 应为 %d，实际 %v", i+1, want, logs[i]["status"])
		}
	}
	// 商户共收到 3 次。
	if got := merchant.count(); got != 3 {
		t.Fatalf("商户应共收到 3 次回调，实际 %d", got)
	}
}

// assertRetryIn 断言任务 next_notify_time 落在 [now+delta-2s, now+delta+2s]
// （间隔逐跳断言；±2s 容忍执行耗时）。
func assertRetryIn(t *testing.T, env *testutil.IntegrationEnv, taskID int64, delta time.Duration, phase string) {
	t.Helper()
	task := payTaskRow(t, env, taskID)
	raw, ok := task["nextNotifyTime"].(time.Time)
	if !ok {
		t.Fatalf("%s：next_notify_time 应存在，实际 %v", phase, task["nextNotifyTime"])
	}
	diff := time.Until(raw)
	lower := delta - 2*time.Second
	upper := delta + 2*time.Second
	if diff < lower || diff > upper {
		t.Fatalf("%s：next_notify_time 应约在 %s 后，实际 %s（%v）", phase, delta, raw.Format(time.RFC3339), diff)
	}
}

// TestIntegrationPayNotifyRetryNineFailures 9 次封顶：9 次失败 → 20 彻底失败；
// 之后不再发送（扫描守卫 + 状态不在 (0,21,22)）；日志 9 行。
func TestIntegrationPayNotifyRetryNineFailures(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	merchant := newNotifyMerchant(t, []int{200}, []string{`{"code":500,"msg":"商户处理失败"}`})
	appRow := payCreateAppWithNotifyURL(t, env, admin, "notify-nine-app", merchant.srv.URL+"/pay/notify")
	_, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-NOTIFY-NINE", "3.00")

	for i := 1; i <= 9; i++ {
		payBackdateTask(t, env, taskID)
		payRunNotifyJob(t, env)
		task := payTaskRow(t, env, taskID)
		if payNum(task["notifyTimes"]) != int64(i) {
			t.Fatalf("第 %d 轮后 notify_times 应为 %d，实际 %v", i, i, task["notifyTimes"])
		}
		want := int64(21)
		if i == 9 {
			want = 20
		}
		if payNum(task["status"]) != want {
			t.Fatalf("第 %d 次失败后任务状态应为 %d，实际 %v", i, want, task["status"])
		}
		if got := merchant.count(); got != i {
			t.Fatalf("第 %d 轮后商户应收到 %d 次，实际 %d", i, i, got)
		}
	}

	// 第 10 轮：状态 20 不再扫描、不再发送、不再追加日志。
	payBackdateTask(t, env, taskID)
	payRunNotifyJob(t, env)
	if got := merchant.count(); got != 9 {
		t.Fatalf("彻底失败后不应再发送，实际 %d 次", got)
	}
	if logs := payNotifyLogs(t, env, taskID); len(logs) != 9 {
		t.Fatalf("9 次执行应恰好 9 条日志，实际 %d", len(logs))
	}
	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 20 {
		t.Fatalf("第 9 次失败应置 20 彻底失败，实际 %v", task["status"])
	}
}

// TestIntegrationPayNotifyRetryConcurrentClaim 并发认领（spec §8.2 回调行）：
// 同任务双触发（两轮并发执行）只发送一次（条件更新认领，后到者 0 行跳过）。
func TestIntegrationPayNotifyRetryConcurrentClaim(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	merchant := newNotifyMerchant(t, []int{200}, []string{`{"code":0,"msg":"success"}`})
	appRow := payCreateAppWithNotifyURL(t, env, admin, "notify-race-app", merchant.srv.URL+"/pay/notify")
	_, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-NOTIFY-RACE", "2.00")

	var wg sync.WaitGroup
	var r1, r2 payservice.NotifyRetryResult
	wg.Add(2)
	go func() { defer wg.Done(); r1, _ = env.App.Pay.RunNotifyRetry(context.Background()) }()
	go func() { defer wg.Done(); r2, _ = env.App.Pay.RunNotifyRetry(context.Background()) }()
	wg.Wait()

	if got := merchant.count(); got != 1 {
		t.Fatalf("并发双触发应只发送一次，实际 %d 次", got)
	}
	if r1.Executed+r2.Executed != 1 {
		t.Fatalf("两轮 Executed 之和应为 1（后认领者 0 行跳过），实际 %d+%d", r1.Executed, r2.Executed)
	}
	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 10 || payNum(task["notifyTimes"]) != 1 {
		t.Fatalf("任务应 10 成功且 notify_times=1：%v", task)
	}
	if logs := payNotifyLogs(t, env, taskID); len(logs) != 1 {
		t.Fatalf("并发下应恰好 1 条日志，实际 %d", len(logs))
	}
}

// TestIntegrationPayNotifyFailClosed 密钥缺失拒发（fail-closed，spec §4.5）：
// 不发送、不消耗重试次数、任务保持待重试、next_notify_time 推进、记日志。
func TestIntegrationPayNotifyFailClosed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	merchant := newNotifyMerchant(t, []int{200}, []string{`{"code":0,"msg":"success"}`})
	appRow := payCreateAppWithNotifyURL(t, env, admin, "notify-fc-app", merchant.srv.URL+"/pay/notify")
	_, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-NOTIFY-FC", "1.00")

	// 独立 Service 实例：不注入密钥（空 = 未配置），租户清单用测试桩。
	svc := payservice.New(payrepo.New(env.App.DB))
	svc.SetLocation(env.Config.Location())
	svc.SetTenantLister(stubTenantLister{ids: []int64{1}})
	result, err := svc.RunNotifyRetry(context.Background())
	if err != nil {
		t.Fatalf("fail-closed 执行不应报错（拒发非失败）: %v", err)
	}
	if result.FailClosed != 1 || result.Executed != 0 {
		t.Fatalf("应拒发 1 次且不执行发送，实际 %+v", result)
	}

	// 商户零请求。
	if got := merchant.count(); got != 0 {
		t.Fatalf("密钥缺失时不应发送，实际 %d 次", got)
	}
	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 0 {
		t.Fatalf("fail-closed 任务应保持等待（0），实际 %v", task["status"])
	}
	if payNum(task["notifyTimes"]) != 0 {
		t.Fatalf("fail-closed 不应消耗重试次数，实际 %v", task["notifyTimes"])
	}
	if task["nextNotifyTime"] == nil {
		t.Fatal("fail-closed 应推进 next_notify_time（防热循环）")
	}
	if raw, ok := task["nextNotifyTime"].(time.Time); !ok || time.Until(raw) <= 0 {
		t.Fatalf("next_notify_time 应被推到未来，实际 %v", task["nextNotifyTime"])
	}
	// 记日志（status 22 请求失败语义 + 摘要说明拒发原因）。
	logs := payNotifyLogs(t, env, taskID)
	if len(logs) != 1 || payNum(logs[0]["status"]) != 22 {
		t.Fatalf("fail-closed 应记 1 条 status=22 日志：%v", logs)
	}
	if !strings.Contains(payStr(logs[0]["response"]), "密钥") {
		t.Fatalf("fail-closed 日志摘要应说明拒发原因：%q", payStr(logs[0]["response"]))
	}

	// 注入密钥后同一任务自动恢复投递（无需人工干预；fail-closed 只把
	// next_notify_time 推进一个间隔，回拨后下一轮即恢复）。
	svc.SetNotifyHMACKey(env.Config.Pay.NotifyHMACKey)
	payBackdateTask(t, env, taskID)
	if _, err := svc.RunNotifyRetry(context.Background()); err != nil {
		t.Fatalf("补配密钥后执行失败: %v", err)
	}
	if got := merchant.count(); got != 1 {
		t.Fatalf("补配密钥后应完成投递，实际 %d 次", got)
	}
	if payNum(payTaskRow(t, env, taskID)["status"]) != 10 {
		t.Fatal("补配密钥后任务应投递成功（10）")
	}
}

// stubTenantLister 是 TenantLister 的测试桩（fail-closed 用例的独立 Service）。
type stubTenantLister struct{ ids []int64 }

func (s stubTenantLister) ListEnabledTenantIDs(ctx context.Context) ([]int64, error) {
	return s.ids, nil
}

// ---------- demo 商户回调端点（公开路由，spec §4.6 demo 回调） ----------

// TestIntegrationPayDemoNotifyEndpoints demo 三端点：无登录可达（任务不校验
// 登录）、伪造/缺失签名 4xx、正确签名 2xx + {"code":0}（#137 起联动已注册：
// 报文 type 与端点种类匹配、DataID 无对应消费行 → 幂等应答不联动）；
// kind↔type 不符 → 业务失败应答（code != 0）。
func TestIntegrationPayDemoNotifyEndpoints(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	key := env.Config.Pay.NotifyHMACKey
	bodyByKind := map[string][]byte{
		"order":    []byte(`{"type":1,"status":"success","merchantOrderId":"MO-DEMO-1","dataId":42}`),
		"refund":   []byte(`{"type":2,"status":"success","merchantRefundId":"MR-DEMO-1","dataId":42}`),
		"transfer": []byte(`{"type":3,"status":"success","merchantTransferId":"MT-DEMO-1","dataId":42}`),
	}

	base := env.Server.URL + env.Config.App.APIPrefix + "/pay/demo/notify"
	post := func(path string, payload []byte, signature string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, base+"/"+path, strings.NewReader(string(payload)))
		if err != nil {
			t.Fatalf("构造请求: %v", err)
		}
		if signature != "" {
			req.Header.Set("Pay-Signature", signature)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("请求 demo 回调端点: %v", err)
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

	for _, kind := range []string{"order", "refund", "transfer"} {
		body := bodyByKind[kind]
		// 正确签名 + 无对应消费行 → 200 {"code":0}（#137 联动注册后：非 demo
		// 行的回调幂等应答不联动，demo 端点是商户模拟器）。
		status, respBody := post(kind, body, notifyHMAC(key, body))
		if status != http.StatusOK || !strings.Contains(respBody, `"code":0`) {
			t.Fatalf("demo/%s 正确签名应 200 {\"code\":0}，实际 HTTP %d %s", kind, status, respBody)
		}
		// 伪造签名 → 400。
		status, respBody = post(kind, body, "deadbeef")
		if status != http.StatusBadRequest {
			t.Fatalf("demo/%s 伪造签名应 400，实际 HTTP %d %s", kind, status, respBody)
		}
		// 缺失签名 → 400。
		status, _ = post(kind, body, "")
		if status != http.StatusBadRequest {
			t.Fatalf("demo/%s 缺失签名应 400，实际 HTTP %d", kind, status)
		}
		// 报文被篡改（签名仍是原文的）→ 400。
		status, _ = post(kind, append(body, []byte(` `)...), notifyHMAC(key, body))
		if status != http.StatusBadRequest {
			t.Fatalf("demo/%s 报文篡改应 400，实际 HTTP %d", kind, status)
		}
		// 非 JSON 报文 → 400。
		status, _ = post(kind, []byte(`not-json`), notifyHMAC(key, []byte(`not-json`)))
		if status != http.StatusBadRequest {
			t.Fatalf("demo/%s 非 JSON 报文应 400，实际 HTTP %d", kind, status)
		}
	}

	// kind↔type 不符（#137 严格校验）：order 端点收到 type=2 报文 → 200 +
	// 非 0 业务码（任务 21 重试，路由缺陷不被静默吞掉）。
	wrong := []byte(`{"type":2,"status":"success","merchantRefundId":"MR-WRONG","dataId":42}`)
	status, respBody := post("order", wrong, notifyHMAC(key, wrong))
	if status != http.StatusOK || !strings.Contains(respBody, `"code":500`) {
		t.Fatalf("order 端点收到 type=2 报文应 200 code=500，实际 HTTP %d %s", status, respBody)
	}
}

// TestIntegrationPayDemoNotifyRateLimit IP 限速（照社交回调先例，每分钟 60 次）：
// 窗口内第 61 次起 429。
func TestIntegrationPayDemoNotifyRateLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	base := env.Server.URL + env.Config.App.APIPrefix + "/pay/demo/notify/transfer"
	body := []byte(`{"type":3,"status":"success","merchantTransferId":"MT-1","dataId":1}`)
	var saw429 bool
	for i := 1; i <= 61; i++ {
		req, err := http.NewRequest(http.MethodPost, base, strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("构造请求: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("请求 demo 回调端点: %v", err)
		}
		_, _ = ioCopyAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatal("窗口内第 61 次起应触发 429 限速")
	}
}

func ioCopyAll(r interface{ Read([]byte) (int, error) }) (int64, error) {
	var total int64
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		total += int64(n)
		if err != nil {
			return total, nil
		}
	}
}

// TestIntegrationPayNotifyDemoE2E 端到端（spec §8.2 回调行）：mock 支付成功
// 建任务 → job 按 notify_url POST 到本服务 demo 回调端点 → 验签通过 →
// 任务 10 成功（业务联动扩展点未注册即回 {"code":0}）。
func TestIntegrationPayNotifyDemoE2E(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demoURL := env.Server.URL + env.Config.App.APIPrefix + "/pay/demo/notify/order"
	appRow := payCreateAppWithNotifyURL(t, env, admin, "notify-e2e-app", demoURL)
	_, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-NOTIFY-E2E", "6.00")

	payRunNotifyJob(t, env)

	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 10 {
		t.Fatalf("demo 回调端点应答 code=0，任务应 10 成功，实际 %v", task["status"])
	}
	logs := payNotifyLogs(t, env, taskID)
	if len(logs) != 1 || payNum(logs[0]["status"]) != 10 {
		t.Fatalf("E2E 应有 1 条成功日志：%v", logs)
	}
	if !strings.Contains(payStr(logs[0]["response"]), `"code":0`) {
		t.Fatalf("日志摘要应记录 demo 端点应答：%q", payStr(logs[0]["response"]))
	}
}

// ---------- 回调任务管理端（pay:notify-task:query，spec §5/§4.11） ----------

// TestIntegrationPayNotifyTaskAdmin 管理端分页/详情（含日志）、普通角色 403、
// 跨租户 404。
func TestIntegrationPayNotifyTaskAdmin(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	merchant := newNotifyMerchant(t, []int{200, 200}, []string{
		`{"code":1001,"msg":"失败"}`, `{"code":0,"msg":"success"}`,
	})
	appRow := payCreateAppWithNotifyURL(t, env, admin, "notify-admin-app", merchant.srv.URL+"/pay/notify")
	_, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-NOTIFY-ADMIN", "9.99")
	payRunNotifyJob(t, env)
	payBackdateTask(t, env, taskID)
	payRunNotifyJob(t, env)

	// 分页：type/status 过滤。
	status, api := env.Request(t, http.MethodGet, "/pay/notify-task/page?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "回调任务分页", status, api)
	var page struct {
		Total int64 `json:"total"`
		List  []struct {
			ID          int64 `json:"id"`
			Type        int8  `json:"type"`
			Status      int8  `json:"status"`
			NotifyTimes int   `json:"notifyTimes"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析回调任务分页: %v", err)
	}
	if page.Total != 1 || len(page.List) != 1 || page.List[0].ID != taskID {
		t.Fatalf("回调任务分页应命中任务 %d：%+v", taskID, page)
	}
	if page.List[0].Status != 10 || page.List[0].NotifyTimes != 2 {
		t.Fatalf("任务应为两轮后成功（notifyTimes=2）：%+v", page.List[0])
	}
	status, api = env.Request(t, http.MethodGet, "/pay/notify-task/page?pageNo=1&pageSize=10&type=2", admin, nil)
	mustOK(t, "type 过滤", status, api)
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total != 0 {
		t.Fatalf("type=2 过滤应为空：%+v err=%v", page, err)
	}

	// 详情：任务 + 日志时间线（2 条 append-only）。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/notify-task/%d", taskID), admin, nil)
	mustOK(t, "回调任务详情", status, api)
	detail := parseJSON(t, string(api.Data))
	logs, ok := detail["logs"].([]any)
	if !ok || len(logs) != 2 {
		t.Fatalf("详情应携带 2 条执行日志：%s", api.Data)
	}
	first := logs[0].(map[string]any)
	if first["notifyTimes"].(float64) != 1 || first["status"].(float64) != 21 {
		t.Fatalf("第 1 条日志应为第 1 次且 21：%v", first)
	}

	// 状态枚举校验：非法过滤 400。
	status, _ = env.Request(t, http.MethodGet, "/pay/notify-task/page?pageNo=1&pageSize=10&status=99", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法状态过滤应 400，实际 HTTP %d", status)
	}

	// 普通角色（role id=2）未授 pay:notify-task:query → 403。
	demo := env.Login(t, "demo", "123456")
	status, _ = env.Request(t, http.MethodGet, "/pay/notify-task/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色回调任务分页应 403，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/notify-task/%d", taskID), demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色回调任务详情应 403，实际 HTTP %d", status)
	}

	// 跨租户 404。
	tenant2 := createTenantByAPI(t, env, admin, "回调任务越权租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	status, _ = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/notify-task/%d", taskID), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("跨租户回调任务详情应 404，实际 HTTP %d", status)
	}
}

// ---------- 订单超时关闭（payOrderTimeoutClose，spec §7） ----------

// TestIntegrationPayOrderTimeoutClose 过期关单：status=0 且 expire_time < now
// → 30 已关闭；已支付/已关闭不受影响；未到期不动。
func TestIntegrationPayOrderTimeoutClose(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := payCreateApp(t, env, admin, "timeout-app")
	payBindChannels(t, env, admin, appRow.ID, []string{"mock", "wallet"})

	// 准备钱包余额（wallet 支付成功路径的对照组）。
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: paymodel.WalletUserTypeAdmin, BizType: paymodel.WalletBizRecharge, Title: "超时关单准备", Price: money.FromCents(10_000),
	}); err != nil {
		t.Fatalf("充值准备: %v", err)
	}

	expiredID := payOrderID(t, payCreateOrder(t, env, admin, appRow.AppKey, "MO-TIMEOUT-EXP", "1.00"))
	paidID := payOrderID(t, payCreateOrder(t, env, admin, appRow.AppKey, "MO-TIMEOUT-PAID", "2.00"))
	futureID := payOrderID(t, payCreateOrder(t, env, admin, appRow.AppKey, "MO-TIMEOUT-FUTURE", "3.00"))

	// paid：wallet 支付成功（对照组）。
	status, api := paySubmit(t, env, admin, paidID, "wallet")
	mustOK(t, "wallet submit", status, api)

	// expired/paid 回拨过期时间；future 保持未到期。
	payBackdateExpire(t, env, expiredID)
	payBackdateExpire(t, env, paidID)

	// 直调超时关单任务（job seam）。
	if !env.App.Scheduler.IsSystemHandler(payservice.OrderTimeoutCloseJobHandlerName) {
		t.Fatalf("%s 应注册为系统级任务处理器", payservice.OrderTimeoutCloseJobHandlerName)
	}
	env.App.Scheduler.Execute(inframodel.Job{TenantID: 1, HandlerName: payservice.OrderTimeoutCloseJobHandlerName})

	assertOrderStatus(t, env, expiredID, 30, "过期待支付订单应被关闭")
	assertOrderStatus(t, env, paidID, 10, "已支付订单不受影响")
	assertOrderStatus(t, env, futureID, 0, "未到期订单不受影响")

	// 幂等：再跑一轮，已关闭订单不变（无二次更新副作用可观察的是状态稳定）。
	payBackdateExpire(t, env, futureID)
	env.App.Scheduler.Execute(inframodel.Job{TenantID: 1, HandlerName: payservice.OrderTimeoutCloseJobHandlerName})
	assertOrderStatus(t, env, futureID, 30, "第二轮应关闭新到期订单")
	assertOrderStatus(t, env, paidID, 10, "已支付订单仍不受影响")
}

func assertOrderStatus(t *testing.T, env *testutil.IntegrationEnv, orderID int64, want int8, phase string) {
	t.Helper()
	var st int8
	if err := env.DB.Raw("SELECT status FROM pay_order WHERE id = ?", orderID).Scan(&st).Error; err != nil {
		t.Fatalf("读订单 %d: %v", orderID, err)
	}
	if st != want {
		t.Fatalf("%s：订单 %d 状态应为 %d，实际 %d", phase, orderID, want, st)
	}
}

// ---------- 复用助手补充 ----------

// payAppIDByKey 按应用标识查应用 ID（绑定渠道等准备步骤用）。
func payAppIDByKey(t *testing.T, env *testutil.IntegrationEnv, token, appKey string) int64 {
	t.Helper()
	var id int64
	if err := env.DB.Raw("SELECT id FROM pay_app WHERE app_key = ?", appKey).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("查应用 %s: %v（id=%d）", appKey, err, id)
	}
	return id
}

// payCreateAppWithNotifyURL 创建指定回调地址的应用（回调域用例指向 httptest
// 商户或本服务 demo 端点）。
func payCreateAppWithNotifyURL(t *testing.T, env *testutil.IntegrationEnv, token, appKey, notifyURL string) payApp {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/app", token, map[string]any{
		"appKey": appKey, "name": "回调测试应用-" + appKey, "orderNotifyUrl": notifyURL,
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
