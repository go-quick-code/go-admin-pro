//go:build integration

// 监控面板集成测试（issue #84，spec §4.10/§5.8）：走真实 router + JWT/权限/
// 租户中间件 + 真实 MySQL，在 HTTP 契约上验证：
//   - overview 字段完整且数值合理（可控数据构造：直插 infra_api_log/
//     message_log 后断言精确值；请求自身的访问日志异步落库，断言前清空）；
//   - requests 按小时分桶（整点对齐、空桶不出现）、空数据返回空数组；
//   - 非平台租户即使持码也 403；无权限码（普通角色）403。
package infra_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// monitorBucketStats 是分桶断言用的聚合计数（测试侧小写别名）。
type monitorBucketStats struct {
	total        int64
	s2xx, s3xx   int64
	s4xx, s5xx   int64
	avgMs, maxMs int64
}

// monitorLogin 以指定租户登录（登录契约带 tenantId）。
func monitorLogin(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("登录租户 %d：HTTP %d code %d msg %q", tenantID, status, api.Code, api.Msg)
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}

// monitorCreateTenant 创建租户并回读 ID（新租户内置 admin/123456）。
func monitorCreateTenant(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建租户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=50&name="+name, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询租户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("租户 %s 应唯一命中: %v（data=%s）", name, err, string(api.Data))
	}
	return int64(page.List[0]["id"].(float64))
}

// monitorResetAPILog 清空访问日志并排干异步写队列：请求自身的访问日志在
// 响应完成后 1 秒内异步落库，精确断言前必须把「已落库 + 队列中」的日志
// 全部清掉，再插入可控数据，否则计数/耗时会被测试请求本身污染。
func monitorResetAPILog(t *testing.T, env *testutil.IntegrationEnv) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		if err := env.DB.Exec("DELETE FROM infra_api_log").Error; err != nil {
			t.Fatalf("清空 infra_api_log: %v", err)
		}
		if err := env.App.FlushAPILogs(ctx); err != nil {
			t.Fatalf("排干访问日志队列: %v", err)
		}
	}
	if err := env.DB.Exec("DELETE FROM infra_api_log").Error; err != nil {
		t.Fatalf("清空 infra_api_log: %v", err)
	}
}

// monitorInsertAPILog 直插一条访问日志（tenant.Skip 绕开租户插件；
// createdAt 由调用方给定以控制分桶归属）。
func monitorInsertAPILog(t *testing.T, env *testutil.IntegrationEnv, tenantID int64, createdAt time.Time, status int, durationMs int64) {
	t.Helper()
	err := env.DB.Exec(
		"INSERT INTO infra_api_log (created_at, tenant_id, user_id, username, request_method, request_path, response_status, duration_ms) "+
			"VALUES (?, ?, 0, 'monitor-test', 'GET', '/monitor-seed', ?, ?)",
		createdAt, tenantID, status, durationMs,
	).Error
	if err != nil {
		t.Fatalf("插入访问日志: %v", err)
	}
}

// monitorInsertMessageLog 直插一条消息发送日志（status：0 成功 1 失败）。
func monitorInsertMessageLog(t *testing.T, env *testutil.IntegrationEnv, channelType string, status int8, createdAt time.Time) {
	t.Helper()
	err := env.DB.Exec(
		"INSERT INTO message_log (created_at, tenant_id, channel_type, channel_id, template_code, recipient, title, content, status) "+
			"VALUES (?, 1, ?, 1, 'seed-code', 'monitor-test@example.com', '监控测试', 'content', ?)",
		createdAt, channelType, status,
	).Error
	if err != nil {
		t.Fatalf("插入消息日志: %v", err)
	}
}

// monitorSeedTraffic 插入 10 条可控访问日志（8×2xx、1×404、1×500；
// 耗时 100×8、50、9000，平均 985、最大 9000），分布在 cur 起的当前小时。
func monitorSeedTraffic(t *testing.T, env *testutil.IntegrationEnv, cur time.Time) {
	t.Helper()
	durations := []int64{100, 100, 100, 100, 100, 100, 100, 100, 50, 9000}
	statuses := []int{200, 200, 200, 200, 200, 200, 200, 200, 404, 500}
	for i, st := range statuses {
		monitorInsertAPILog(t, env, 1, cur.Add(time.Duration(i)*time.Second), st, durations[i])
	}
}

// monitorLocation 取服务端业务时区（配置 app.timezone），分桶期望值与
// 可控数据的整点计算都用它，与 ADR 0006 口径一致。
func monitorLocation(t *testing.T, env *testutil.IntegrationEnv) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(env.Config.App.Timezone)
	if err != nil {
		t.Fatalf("加载业务时区 %q: %v", env.Config.App.Timezone, err)
	}
	return loc
}

// TestIntegrationMonitorRequestsBuckets requests 按小时分桶：整点对齐、
// 各状态段计数、平均/最大耗时；窗口内无数据的小时不产生空桶。
func TestIntegrationMonitorRequestsBuckets(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	loc := monitorLocation(t, env)
	monitorResetAPILog(t, env)

	now := time.Now().In(loc)
	cur := now.Truncate(time.Hour)
	// 可控数据：当前小时 3 条（2×200、1×500），1 小时前 1 条 404，
	// 3 小时前 2 条 304。2 小时前刻意留空——空桶不应出现在结果里。
	monitorInsertAPILog(t, env, 1, cur.Add(5*time.Minute), 200, 100)
	monitorInsertAPILog(t, env, 1, cur.Add(6*time.Minute), 200, 300)
	monitorInsertAPILog(t, env, 1, cur.Add(7*time.Minute), 500, 3000)
	monitorInsertAPILog(t, env, 1, cur.Add(-1*time.Hour).Add(2*time.Minute), 404, 50)
	monitorInsertAPILog(t, env, 1, cur.Add(-3*time.Hour).Add(2*time.Minute), 304, 20)
	monitorInsertAPILog(t, env, 1, cur.Add(-3*time.Hour).Add(3*time.Minute), 304, 40)

	status, api := env.Request(t, http.MethodGet, "/infra/monitor/requests?hours=4", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 requests：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var resp struct {
		Hours   int `json:"hours"`
		Buckets []struct {
			Hour  string `json:"hour"`
			Total int64  `json:"total"`
			S2xx  int64  `json:"s2xx"`
			S3xx  int64  `json:"s3xx"`
			S4xx  int64  `json:"s4xx"`
			S5xx  int64  `json:"s5xx"`
			AvgMs int64  `json:"avgMs"`
			MaxMs int64  `json:"maxMs"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(api.Data, &resp); err != nil {
		t.Fatalf("解析 requests 响应: %v（data=%s）", err, string(api.Data))
	}
	if len(resp.Buckets) != 3 {
		t.Fatalf("应有 3 个非空桶（2 小时前留空不出现），实际 %d：%s", len(resp.Buckets), string(api.Data))
	}

	wantCur := cur.Format("2006-01-02 15:04")
	want1 := cur.Add(-1 * time.Hour).Format("2006-01-02 15:04")
	want3 := cur.Add(-3 * time.Hour).Format("2006-01-02 15:04")
	byHour := map[string]monitorBucketStats{}
	for _, b := range resp.Buckets {
		byHour[b.Hour] = monitorBucketStats{
			total: b.Total, s2xx: b.S2xx, s3xx: b.S3xx, s4xx: b.S4xx, s5xx: b.S5xx,
			avgMs: b.AvgMs, maxMs: b.MaxMs,
		}
	}
	for _, h := range []string{wantCur, want1, want3} {
		if _, ok := byHour[h]; !ok {
			t.Fatalf("缺少 %s 桶，实际桶：%v", h, keysOfMonitorBuckets(resp.Buckets))
		}
	}
	curB := byHour[wantCur]
	if curB.total != 3 || curB.s2xx != 2 || curB.s5xx != 1 || curB.s3xx != 0 || curB.s4xx != 0 {
		t.Fatalf("当前小时桶计数不符: %+v", curB)
	}
	if curB.maxMs != 3000 {
		t.Fatalf("当前小时桶 maxMs=%d，期望 3000", curB.maxMs)
	}
	// 平均：(100+300+3000)/3 ≈ 1133，四舍五入。
	if curB.avgMs < 1133 || curB.avgMs > 1134 {
		t.Fatalf("当前小时桶 avgMs=%d，期望 1133~1134", curB.avgMs)
	}
	b1 := byHour[want1]
	if b1.total != 1 || b1.s4xx != 1 || b1.maxMs != 50 || b1.avgMs != 50 {
		t.Fatalf("1 小时前桶不符: %+v", b1)
	}
	b3 := byHour[want3]
	if b3.total != 2 || b3.s3xx != 2 || b3.avgMs != 30 || b3.maxMs != 40 {
		t.Fatalf("3 小时前桶不符: %+v", b3)
	}
	if resp.Hours != 4 {
		t.Fatalf("回显 hours=4，实际 %d", resp.Hours)
	}
}

// TestIntegrationMonitorRequestsEmptyAndClamp 空数据返回空数组（非 null）
// 不报错；hours 越界收敛到 [1,168] 而不是 400。
func TestIntegrationMonitorRequestsEmptyAndClamp(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	monitorResetAPILog(t, env)

	for _, hours := range []string{"0", "-5", "999", ""} {
		// 每轮先重置：上一轮请求自身的访问日志约 1 秒后异步落库，
		// 会在窗口内留下非空桶，导致「空数组」断言抖动。
		monitorResetAPILog(t, env)
		path := "/infra/monitor/requests"
		if hours != "" {
			path += "?hours=" + hours
		}
		status, api := env.Request(t, http.MethodGet, path, token, nil)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("hours=%q 应 200（越界收敛），实际 HTTP %d code %d msg %q", hours, status, api.Code, api.Msg)
		}
		var resp struct {
			Hours   int             `json:"hours"`
			Buckets json.RawMessage `json:"buckets"`
		}
		if err := json.Unmarshal(api.Data, &resp); err != nil {
			t.Fatalf("解析响应: %v（data=%s）", err, string(api.Data))
		}
		if string(resp.Buckets) != "[]" {
			t.Fatalf("空数据 buckets 应为空数组，实际 %s", string(resp.Buckets))
		}
		var buckets []map[string]any
		if err := json.Unmarshal(resp.Buckets, &buckets); err != nil || buckets == nil {
			t.Fatalf("buckets 必须是可用的空数组（非 null）: %v", err)
		}
		if hours == "" && resp.Hours != 24 {
			t.Fatalf("缺省 hours 应为 24，实际 %d", resp.Hours)
		}
	}
}

// TestIntegrationMonitorOverview overview 字段完整且数值合理：直插可控
// 访问日志/消息日志后断言精确值，运行期指标（goroutine/堆/连接池）断言
// 合理区间。
func TestIntegrationMonitorOverview(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	loc := monitorLocation(t, env)
	monitorResetAPILog(t, env)

	// 起始基线：当前租户数/用户数（seed 只有平台租户）。
	baseline := monitorOverview(t, env, token)

	// 建一个租户（bootstrap 会带一个 admin 用户），租户数 +1、用户数 +1。
	monitorCreateTenant(t, env, token, "monitor-非平台租户")

	// 再次重置：基线查询与建租户请求自身的日志已异步落库，精确断言前
	// 全部清掉；此后到第二次 overview 查询之间只有原生 SQL 插入，不再
	// 产生新请求日志（overview 自身的日志在其响应完成后才入队）。
	monitorResetAPILog(t, env)

	// 可控数据：10 条访问日志（8×2xx、1×404、1×500；耗时 100×8、50、9000）。
	now := time.Now().In(loc)
	cur := now.Truncate(time.Hour)
	monitorSeedTraffic(t, env, cur)
	// 消息日志：sms 成功 2 失败 1，mail 成功 1 失败 1。
	monitorInsertMessageLog(t, env, "sms", 0, now.Add(-2*time.Hour))
	monitorInsertMessageLog(t, env, "sms", 0, now.Add(-1*time.Hour))
	monitorInsertMessageLog(t, env, "sms", 1, now.Add(-30*time.Minute))
	monitorInsertMessageLog(t, env, "mail", 0, now.Add(-20*time.Minute))
	monitorInsertMessageLog(t, env, "mail", 1, now.Add(-10*time.Minute))

	ov := monitorOverview(t, env, token)

	// —— 应用信息 ——
	if ov.App.Name == "" || ov.App.Env == "" || ov.App.GoVersion == "" || ov.App.StartTime == "" {
		t.Fatalf("应用信息不完整: %+v", ov.App)
	}
	if ov.App.Version == "" {
		t.Fatalf("应用版本不应为空（无构建信息时回退 dev）")
	}
	if ov.App.UptimeSeconds < 0 {
		t.Fatalf("运行时长不应为负: %d", ov.App.UptimeSeconds)
	}
	// —— 运行期 ——
	if ov.Runtime.Goroutines <= 0 || ov.Runtime.HeapBytes <= 0 || ov.Runtime.GCCount < 0 {
		t.Fatalf("运行期指标不合理: %+v", ov.Runtime)
	}
	// —— DB 连接池 ——
	if ov.DBPool.MaxOpen != 100 {
		t.Fatalf("连接池上限应取配置 max-open-conns=100，实际 %+v", ov.DBPool)
	}
	if ov.DBPool.Open <= 0 || ov.DBPool.InUse < 0 || ov.DBPool.Idle < 0 {
		t.Fatalf("连接池指标不合理: %+v", ov.DBPool)
	}
	// —— 调度器 ——
	if ov.Jobs.Total <= 0 || ov.Jobs.HandlerCount <= 0 {
		t.Fatalf("调度器指标不合理（seed 有默认任务行与内置 handler）: %+v", ov.Jobs)
	}
	if ov.Jobs.Enabled <= 0 || ov.Jobs.Enabled > ov.Jobs.Total {
		t.Fatalf("启用任务数不合理: %+v", ov.Jobs)
	}
	// —— 租户/用户 ——
	if got := ov.Tenants.Count; got != baseline.Tenants.Count+1 {
		t.Fatalf("租户数应 %d（基线 %d + 新建 1），实际 %d", baseline.Tenants.Count+1, baseline.Tenants.Count, got)
	}
	if got := ov.Tenants.Users; got != baseline.Tenants.Users+1 {
		t.Fatalf("用户数应 %d（基线 %d + 租户 bootstrap 1），实际 %d", baseline.Tenants.Users+1, baseline.Tenants.Users, got)
	}
	// —— 近 24h 流量（可控数据精确断言） ——
	if ov.Traffic.Total != 10 {
		t.Fatalf("近 24h 访问量应 10，实际 %d", ov.Traffic.Total)
	}
	if ov.Traffic.ErrorRate < 19.9 || ov.Traffic.ErrorRate > 20.1 {
		t.Fatalf("错误率应约 20%%（2/10），实际 %v", ov.Traffic.ErrorRate)
	}
	if ov.Traffic.MaxMs != 9000 {
		t.Fatalf("最大耗时应 9000，实际 %d", ov.Traffic.MaxMs)
	}
	if ov.Traffic.AvgMs < 984 || ov.Traffic.AvgMs > 986 {
		t.Fatalf("平均耗时应约 985（9850/10），实际 %d", ov.Traffic.AvgMs)
	}
	// —— 近 24h 消息 ——
	if ov.Messages.SmsSuccess != 2 || ov.Messages.SmsFailed != 1 || ov.Messages.MailSuccess != 1 || ov.Messages.MailFailed != 1 {
		t.Fatalf("消息统计不符: %+v", ov.Messages)
	}

	// —— requests 与 overview 同口径：total 汇总应等于访问量 ——
	// 先重置（overview 请求自身的日志约 1 秒后落库，会混进窗口计数；
	// reset 会把可控数据一并清掉，随后重插）再查询。
	monitorResetAPILog(t, env)
	monitorSeedTraffic(t, env, cur)
	status, api := env.Request(t, http.MethodGet, "/infra/monitor/requests?hours=24", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 requests：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var resp struct {
		Buckets []struct {
			Total int64 `json:"total"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal(api.Data, &resp); err != nil {
		t.Fatalf("解析 requests: %v", err)
	}
	var sum int64
	for _, b := range resp.Buckets {
		sum += b.Total
	}
	if sum != 10 {
		t.Fatalf("requests 24h 桶汇总应 10，实际 %d", sum)
	}
}

// TestIntegrationMonitorPlatformOnly 非平台租户即使持 infra:monitor:query
// 也 403（spec §4.10.3）。
func TestIntegrationMonitorPlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	tenantID := monitorCreateTenant(t, env, admin, "monitor-越权租户")
	tenantToken := monitorLogin(t, env, tenantID)

	for _, tc := range []struct{ name, path string }{
		{"overview", "/infra/monitor/overview"},
		{"requests", "/infra/monitor/requests?hours=24"},
	} {
		status, api := env.Request(t, http.MethodGet, tc.path, tenantToken, nil)
		if status != http.StatusForbidden {
			t.Fatalf("非平台租户 %s 应 403，实际 HTTP %d msg %q", tc.name, status, api.Msg)
		}
	}
}

// TestIntegrationMonitorPermission 平台租户内无权限码的普通角色（demo）403。
func TestIntegrationMonitorPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "demo", "123456")
	status, api := env.Request(t, http.MethodGet, "/infra/monitor/overview", token, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无权限码应 403，实际 HTTP %d msg %q", status, api.Msg)
	}
}

// monitorOverview 请求 overview 并解析为通用 map（测试侧不依赖响应 struct
// 的字段名拼写错误），返回按断言需要的读取器。
func monitorOverview(t *testing.T, env *testutil.IntegrationEnv, token string) monitorOverviewData {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/infra/monitor/overview", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 overview：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var data monitorOverviewData
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析 overview: %v（data=%s）", err, string(api.Data))
	}
	return data
}

type monitorOverviewData struct {
	App struct {
		Name          string `json:"name"`
		Env           string `json:"env"`
		Version       string `json:"version"`
		GoVersion     string `json:"goVersion"`
		StartTime     string `json:"startTime"`
		UptimeSeconds int64  `json:"uptimeSeconds"`
	} `json:"app"`
	Runtime struct {
		Goroutines int   `json:"goroutines"`
		HeapBytes  int64 `json:"heapBytes"`
		SysBytes   int64 `json:"sysBytes"`
		GCCount    int64 `json:"gcCount"`
	} `json:"runtime"`
	DBPool struct {
		MaxOpen    int   `json:"maxOpen"`
		Open       int   `json:"open"`
		InUse      int   `json:"inUse"`
		Idle       int   `json:"idle"`
		WaitCount  int64 `json:"waitCount"`
		WaitMillis int64 `json:"waitMillis"`
	} `json:"dbPool"`
	Jobs struct {
		Total        int64 `json:"total"`
		Enabled      int64 `json:"enabled"`
		HandlerCount int   `json:"handlerCount"`
	} `json:"jobs"`
	Tenants struct {
		Count int64 `json:"count"`
		Users int64 `json:"users"`
	} `json:"tenants"`
	Traffic struct {
		Total     int64   `json:"total"`
		ErrorRate float64 `json:"errorRate"`
		AvgMs     int64   `json:"avgMs"`
		MaxMs     int64   `json:"maxMs"`
	} `json:"traffic"`
	Messages struct {
		SmsSuccess  int64 `json:"smsSuccess"`
		SmsFailed   int64 `json:"smsFailed"`
		MailSuccess int64 `json:"mailSuccess"`
		MailFailed  int64 `json:"mailFailed"`
	} `json:"messages"`
}

// keysOfMonitorBuckets 提取桶标签列表（失败信息用）。
func keysOfMonitorBuckets(buckets []struct {
	Hour  string `json:"hour"`
	Total int64  `json:"total"`
	S2xx  int64  `json:"s2xx"`
	S3xx  int64  `json:"s3xx"`
	S4xx  int64  `json:"s4xx"`
	S5xx  int64  `json:"s5xx"`
	AvgMs int64  `json:"avgMs"`
	MaxMs int64  `json:"maxMs"`
}) []string {
	keys := make([]string, 0, len(buckets))
	for _, b := range buckets {
		keys = append(keys, fmt.Sprintf("%s(total=%d)", b.Hour, b.Total))
	}
	return keys
}
