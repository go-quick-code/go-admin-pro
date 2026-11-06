//go:build integration

package infra_test

// API 访问日志集成测试（issue #80，spec §4.6/§5.5，ADR 0014）：
//   - 受保护接口（含 GET）经真实路由 + Auth 落库，Flush 后可见；
//   - 公开接口（登录/验证码）不记录；
//   - query 脱敏、request_id 读取/生成并回写响应头、UA 截断；
//   - 查询/按 ids 删除/清空 + 权限码 + 跨租户隔离 404；
//   - cleanAccessLogs 系统级任务按保留期删除。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// rawRequest 发起可带自定义请求头的请求（env.Request 不支持自定义头），
// 返回 HTTP 状态、响应头与统一响应信封。
func rawRequest(t *testing.T, env *testutil.IntegrationEnv, method, path, token string, headers map[string]string) (int, http.Header, testutil.APIResponse) {
	t.Helper()
	req, err := http.NewRequest(method, env.APIPrefixURL(path), nil)
	if err != nil {
		t.Fatalf("构造请求 %s %s: %v", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	var api testutil.APIResponse
	_ = json.Unmarshal(body, &api)
	return resp.StatusCode, resp.Header, api
}

func mustOKForAPILog(t *testing.T, label string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", label, status, api.Code, api.Msg)
	}
}

func decodePageForAPILog(t *testing.T, data json.RawMessage) ([]map[string]any, int64) {
	t.Helper()
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("解析分页响应: %v（data=%s）", err, string(data))
	}
	return page.List, page.Total
}

// pageAPILogs 分页查询访问日志。
func pageAPILogs(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/infra/api-log/page?pageNo=1&pageSize=100"+query, token, nil)
	mustOKForAPILog(t, "查询访问日志"+query, status, api)
	return decodePageForAPILog(t, api.Data)
}

// flushLogs 排干访问日志队列，保证请求产生的条目已落库。
func flushLogs(t *testing.T, env *testutil.IntegrationEnv) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := env.App.FlushAPILogs(ctx); err != nil {
		t.Fatalf("Flush 访问日志: %v", err)
	}
}

func findEntryByRequestID(list []map[string]any, rid string) map[string]any {
	for _, row := range list {
		if row["requestId"] == rid {
			return row
		}
	}
	return nil
}

// TestIntegrationAPILogRecordsProtectedOnly 覆盖：受保护 GET 落库（含路由模板/
// 脱敏 query/request_id 回写与入库/UA 截断），公开接口不落库，过滤条件可用。
func TestIntegrationAPILogRecordsProtectedOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 公开接口：登录（上面刚做过）不产生访问日志——中间件只挂 protected 组。
	// 受保护 GET，带敏感 query 与自定义 request_id。
	customRID := "it-rid-" + fmt.Sprint(time.Now().UnixNano())
	status, header, _ := rawRequest(t, env, http.MethodGet,
		"/system/user/page?pageNo=1&pageSize=5&token=abc123", admin,
		map[string]string{"X-Request-Id": customRID, "User-Agent": strings.Repeat("it-agent/", 60) + "END"})
	if status != http.StatusOK {
		t.Fatalf("受保护 GET：HTTP %d", status)
	}
	if got := header.Get("X-Request-Id"); got != customRID {
		t.Fatalf("request_id 应回写响应头，期望 %q 实际 %q", customRID, got)
	}

	// 未带 X-Request-Id：服务端生成 UUID 并回写。
	status, header, api := rawRequest(t, env, http.MethodGet, "/infra/config/page?pageNo=1&pageSize=5", admin, nil)
	mustOKForAPILog(t, "受保护 GET（生成 request_id）", status, api)
	generatedRID := header.Get("X-Request-Id")
	if len(generatedRID) != 36 || strings.Count(generatedRID, "-") != 4 {
		t.Fatalf("未带头时应生成 UUID，实际 %q", generatedRID)
	}

	flushLogs(t, env)
	list, total := pageAPILogs(t, env, admin, "")
	if total < 2 || len(list) < 2 {
		t.Fatalf("访问日志 total=%d，期望至少 2 条", total)
	}

	// 公开接口不记录：任何条目的路由模板/原始 path 都不是登录或验证码。
	for _, row := range list {
		p, _ := row["requestPath"].(string)
		if strings.Contains(p, "/system/auth/") || p == "/healthz" {
			t.Fatalf("公开接口不应记录：%v", row)
		}
	}

	// 自定义 request_id 的条目：路由模板 + 脱敏 query + 用户/租户 + UA 截断。
	entry := findEntryByRequestID(list, customRID)
	if entry == nil {
		t.Fatalf("自定义 request_id 的条目未落库：%v", customRID)
	}
	if entry["requestPath"] != "/admin-api/system/user/page" {
		t.Fatalf("应记录路由模板，实际 %v", entry["requestPath"])
	}
	if entry["requestMethod"] != "GET" {
		t.Fatalf("应记录 GET，实际 %v", entry["requestMethod"])
	}
	if q, _ := entry["requestQuery"].(string); q != "pageNo=1&pageSize=5&token=***" {
		t.Fatalf("query 应脱敏，实际 %q", q)
	}
	if entry["responseStatus"].(float64) != 200 {
		t.Fatalf("状态码不符：%v", entry["responseStatus"])
	}
	if entry["username"] != "admin" || entry["tenantId"].(float64) != 1 {
		t.Fatalf("用户/租户不符：%v", entry)
	}
	if ua, _ := entry["userAgent"].(string); len(ua) != 500 {
		t.Fatalf("UA 应截断 500，实际 %d", len(ua))
	}
	if entry["clientIp"] == "" || entry["createTime"] == nil {
		t.Fatalf("IP/时间缺失：%v", entry)
	}

	// 生成 request_id 的条目也存在（回写值与入库值一致）。
	if findEntryByRequestID(list, generatedRID) == nil {
		t.Fatalf("生成 request_id 的条目未落库：%v", generatedRID)
	}

	// 过滤：路径 / 用户 / 状态 / 时间段。
	if _, n := pageAPILogs(t, env, admin, "&path=/system/user/page"); n < 1 {
		t.Fatalf("按路径过滤应命中")
	}
	if _, n := pageAPILogs(t, env, admin, "&path=/no-such-path"); n != 0 {
		t.Fatalf("按路径过滤不应命中不存在路径")
	}
	if _, n := pageAPILogs(t, env, admin, "&username=adm"); n < 1 {
		t.Fatalf("按用户模糊过滤应命中")
	}
	if _, n := pageAPILogs(t, env, admin, "&status=200"); n < 1 {
		t.Fatalf("按状态 200 过滤应命中")
	}
	if _, n := pageAPILogs(t, env, admin, "&status=418"); n != 0 {
		t.Fatalf("状态 418 不应命中")
	}
	if _, n := pageAPILogs(t, env, admin, "&beginTime="+url.QueryEscape("2030-01-01 00:00:00")); n != 0 {
		t.Fatalf("未来时间段不应命中")
	}
	if _, n := pageAPILogs(t, env, admin, "&beginTime="+url.QueryEscape(time.Now().Add(-24*time.Hour).Format("2006-01-02 15:04:05"))); n < 1 {
		t.Fatalf("过去一天起的时间段应命中")
	}
}

// TestIntegrationAPILogDeleteClearPermissionAndIsolation 覆盖：按 ids 删除、
// 清空、权限码 403、跨租户隔离 404。
func TestIntegrationAPILogDeleteClearPermissionAndIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 产生两条本租户日志。
	for i := 0; i < 2; i++ {
		status, _, _ := rawRequest(t, env, http.MethodGet,
			fmt.Sprintf("/infra/config/page?pageNo=1&pageSize=5&mark=del-%d", i), admin, nil)
		if status != http.StatusOK {
			t.Fatalf("产生日志的请求失败：HTTP %d", status)
		}
	}
	flushLogs(t, env)
	list, total := pageAPILogs(t, env, admin, "&path=/infra/config/page")
	if total < 2 {
		t.Fatalf("应至少 2 条 config/page 日志，实际 %d", total)
	}
	firstID := int64(list[0]["id"].(float64))
	secondID := int64(list[1]["id"].(float64))

	// 无权限用户：只挂仪表盘菜单的角色，访问日志查询/删除都 403。
	roleID := createRoleForAPILog(t, env, admin, "访问日志无权角色", "no-api-log-role", []int64{1})
	createUserForAPILog(t, env, admin, "nologuser", 101, []int64{roleID})
	limited := env.Login(t, "nologuser", "123456")
	if status, _ := env.Request(t, http.MethodGet, "/infra/api-log/page?pageNo=1&pageSize=10", limited, nil); status != http.StatusForbidden {
		t.Fatalf("无权限用户查询访问日志：HTTP %d，期望 403", status)
	}
	if status, _ := env.Request(t, http.MethodDelete, "/infra/api-log/clear", limited, nil); status != http.StatusForbidden {
		t.Fatalf("无权限用户清空访问日志：HTTP %d，期望 403", status)
	}

	// 跨租户：租户 2 看不到租户 1 的日志；按 ids 删除含跨租户 ID 时 404 不删。
	tenant2 := createTenantForAPILog(t, env, admin, "访问日志隔离租户")
	tenant2Token := loginTenantForAPILog(t, env, tenant2)
	if _, n := pageAPILogs(t, env, tenant2Token, ""); n != 0 {
		t.Fatalf("租户 2 初始访问日志应为 0，实际 %d", n)
	}
	status, api := env.Request(t, http.MethodDelete, "/infra/api-log", tenant2Token, map[string]any{"ids": []int64{firstID, secondID}})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 日志：HTTP %d，期望 404", status)
	}
	_ = api
	// 404 不删：租户 1 的行原样保留。
	if _, n := pageAPILogs(t, env, admin, "&path=/infra/config/page"); n < 2 {
		t.Fatalf("404 后租户 1 日志应保留，实际 %d", n)
	}

	// 租户 2 清空：只清自己（0 条），不影响租户 1。
	status, api = env.Request(t, http.MethodDelete, "/infra/api-log/clear", tenant2Token, nil)
	mustOKForAPILog(t, "租户 2 清空", status, api)
	if _, n := pageAPILogs(t, env, admin, "&path=/infra/config/page"); n < 2 {
		t.Fatalf("租户 2 清空不应影响租户 1，实际 %d", n)
	}

	// 本租户按 ids 删除 + 清空。
	status, api = env.Request(t, http.MethodDelete, "/infra/api-log", admin, map[string]any{"ids": []int64{firstID, secondID}})
	mustOKForAPILog(t, "按 ids 删除", status, api)
	if _, n := pageAPILogs(t, env, admin, "&path=/infra/config/page"); n != 0 {
		t.Fatalf("按 ids 删除后应为 0，实际 %d", n)
	}
	// 再产生一条并清空。
	rawRequest(t, env, http.MethodGet, "/infra/config/page?pageNo=1", admin, nil)
	flushLogs(t, env)
	status, api = env.Request(t, http.MethodDelete, "/infra/api-log/clear", admin, nil)
	mustOKForAPILog(t, "清空", status, api)
	if _, n := pageAPILogs(t, env, admin, ""); n != 0 {
		t.Fatalf("清空后应为 0，实际 %d", n)
	}
}

// TestIntegrationAPILogCleanJobByRetention 覆盖：cleanAccessLogs 系统级任务
// 按保留期删除（老行删、新行留），并验证 handler_param 天数覆盖。
func TestIntegrationAPILogCleanJobByRetention(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 直插两条：40 天前（超默认保留期 30 天）与 1 天前。
	old := time.Now().AddDate(0, 0, -40)
	fresh := time.Now().AddDate(0, 0, -1)
	if err := env.DB.Exec(
		"INSERT INTO infra_api_log (created_at, tenant_id, user_id, username, request_method, request_path, request_query, response_status, duration_ms, client_ip, user_agent, request_id) "+
			"VALUES (?, 1, 1, 'admin', 'GET', '/admin-api/legacy', '', 200, 1, '127.0.0.1', 'job-old', 'job-old')", old).Error; err != nil {
		t.Fatalf("插入过期日志: %v", err)
	}
	if err := env.DB.Exec(
		"INSERT INTO infra_api_log (created_at, tenant_id, user_id, username, request_method, request_path, request_query, response_status, duration_ms, client_ip, user_agent, request_id) "+
			"VALUES (?, 1, 1, 'admin', 'GET', '/admin-api/legacy', '', 200, 1, '127.0.0.1', 'job-fresh', 'job-fresh')", fresh).Error; err != nil {
		t.Fatalf("插入新鲜日志: %v", err)
	}

	// 种子的系统级任务行（每日 03:00，启用）。
	var jobID int64
	if err := env.DB.Raw("SELECT id FROM infra_job WHERE handler_name = 'cleanAccessLogs' LIMIT 1").Scan(&jobID).Error; err != nil || jobID == 0 {
		t.Fatalf("种子 cleanAccessLogs 任务缺失: %v（id=%d）", err, jobID)
	}

	// 平台管理员手动运行（RunJob 异步，轮询等待结果）。
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/infra/job/%d/run", jobID), admin, nil)
	mustOKForAPILog(t, "运行清理任务", status, api)

	deadline := time.Now().Add(10 * time.Second)
	for {
		var oldCount, freshCount int64
		// 直查 DB（系统级视角），与清理动作同口径（tenant.Skip）。
		env.DB.Raw("SELECT COUNT(*) FROM infra_api_log WHERE request_id = 'job-old'").Scan(&oldCount)
		env.DB.Raw("SELECT COUNT(*) FROM infra_api_log WHERE request_id = 'job-fresh'").Scan(&freshCount)
		if oldCount == 0 && freshCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("清理任务未按保留期删除：old=%d fresh=%d", oldCount, freshCount)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// handler_param 天数覆盖：把任务行 handler_param 改为 2 天——
	// 3 天前行删除，1 天前的新鲜行保留。
	if err := env.DB.Exec(
		"INSERT INTO infra_api_log (created_at, tenant_id, user_id, username, request_method, request_path, request_query, response_status, duration_ms, client_ip, user_agent, request_id) "+
			"VALUES (?, 1, 1, 'admin', 'GET', '/admin-api/legacy', '', 200, 1, '127.0.0.1', 'job-3d', 'job-3d')",
		time.Now().AddDate(0, 0, -3)).Error; err != nil {
		t.Fatalf("插入 3 天前日志: %v", err)
	}
	if err := env.DB.Exec("UPDATE infra_job SET handler_param = '2' WHERE id = ?", jobID).Error; err != nil {
		t.Fatalf("改写 handler_param: %v", err)
	}
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/infra/job/%d/run", jobID), admin, nil)
	mustOKForAPILog(t, "按 param 天数运行清理任务", status, api)

	deadline = time.Now().Add(10 * time.Second)
	for {
		var threeDay, freshCount int64
		env.DB.Raw("SELECT COUNT(*) FROM infra_api_log WHERE request_id = 'job-3d'").Scan(&threeDay)
		env.DB.Raw("SELECT COUNT(*) FROM infra_api_log WHERE request_id = 'job-fresh'").Scan(&freshCount)
		if threeDay == 0 && freshCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("param 天数覆盖未生效：3d=%d fresh=%d", threeDay, freshCount)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ---- 辅助：建角色/用户/租户（与 message/file 集成测试同款 HTTP 契约）----

func createRoleForAPILog(t *testing.T, env *testutil.IntegrationEnv, admin, name, code string, menuIDs []int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/role", admin, map[string]any{
		"name": name, "code": code, "dataScope": 1, "status": 0, "menuIds": menuIDs,
	})
	mustOKForAPILog(t, "创建角色 "+code, status, api)
	status, api = env.Request(t, http.MethodGet, "/system/role/page?code="+code+"&pageNo=1&pageSize=10", admin, nil)
	mustOKForAPILog(t, "查询角色 "+code, status, api)
	list, total := decodePageForAPILog(t, api.Data)
	if total != 1 || len(list) != 1 {
		t.Fatalf("按编码 %s 查询角色：total=%d，期望唯一命中", code, total)
	}
	return int64(list[0]["id"].(float64))
}

func createUserForAPILog(t *testing.T, env *testutil.IntegrationEnv, admin, username string, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	mustOKForAPILog(t, "创建用户 "+username, status, api)
}

func createTenantForAPILog(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	mustOKForAPILog(t, "创建租户 "+name, status, api)
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10&name="+name, token, nil)
	mustOKForAPILog(t, "查询租户 "+name, status, api)
	list, total := decodePageForAPILog(t, api.Data)
	if total != 1 || len(list) != 1 {
		t.Fatalf("租户 %s 应唯一命中，实际 total=%d", name, total)
	}
	return int64(list[0]["id"].(float64))
}

func loginTenantForAPILog(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	mustOKForAPILog(t, fmt.Sprintf("登录租户 %d", tenantID), status, api)
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}
