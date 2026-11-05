//go:build integration

// 系统级任务处理器的平台租户限制（PR #39 审查 P0）：
// 公海回收与日志清理会跨租户执行，只有平台租户（种子租户 1）管理员可以创建、
// 修改、启用或手动运行这类任务；普通租户管理员持 super_admin（"*"）也只能
// 使用非系统级 handler，且处理器列表不展示系统级 handler。
package infra_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// systemJobHandler 是系统级 handler 名（公海回收，app 装配处 RegisterSystem）。
const systemJobHandler = "recyclePoolCustomers"

// createTenantAndLogin 以平台管理员身份创建租户并登录其内置 admin，
// 返回租户 ID 与 accessToken（新租户 admin 为 super_admin，持 "*"）。
func createTenantAndLogin(t *testing.T, env *testutil.IntegrationEnv, admin, name string) (int64, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建租户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?name="+name+"&pageNo=1&pageSize=10", admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询租户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("解析租户分页（应唯一命中）：err=%v data=%s", err, string(api.Data))
	}
	tenantID := page.List[0].ID

	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 %d 登录：HTTP %d code %d msg %q", tenantID, status, api.Code, api.Msg)
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析租户 %d 登录响应: %v（data=%s）", tenantID, err, string(api.Data))
	}
	return tenantID, data.AccessToken
}

// listJobHandlers 查询可注册的处理器列表。
func listJobHandlers(t *testing.T, env *testutil.IntegrationEnv, token string) []string {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/infra/job/handlers", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询任务处理器：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var names []string
	if err := json.Unmarshal(api.Data, &names); err != nil {
		t.Fatalf("解析处理器列表: %v（data=%s）", err, string(api.Data))
	}
	return names
}

// containsHandler 判断处理器名是否在列表中。
func containsHandler(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestIntegrationSystemJobHandlersPlatformOnly 系统级处理器只对平台租户开放：
//   - 平台管理员（租户 1）的处理器列表包含系统级 handler，可创建/运行；
//   - 租户 2 管理员的处理器列表不包含系统级 handler；
//   - 租户 2 创建/修改/启用/运行系统级 handler 一律 403（中文文案），
//     非系统级 handler 不受限制。
func TestIntegrationSystemJobHandlersPlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	platformHandlers := listJobHandlers(t, env, admin)
	if !containsHandler(platformHandlers, systemJobHandler) {
		t.Fatalf("平台租户的处理器列表应包含 %q，实际 %v", systemJobHandler, platformHandlers)
	}
	if !containsHandler(platformHandlers, "demoJob") {
		t.Fatalf("平台租户的处理器列表应包含 demoJob，实际 %v", platformHandlers)
	}

	tenant2ID, tenant2 := createTenantAndLogin(t, env, admin, "任务平台限制租户")
	tenant2Handlers := listJobHandlers(t, env, tenant2)
	if containsHandler(tenant2Handlers, systemJobHandler) {
		t.Fatalf("租户 %d 的处理器列表不应包含系统级 handler %q，实际 %v", tenant2ID, systemJobHandler, tenant2Handlers)
	}
	if !containsHandler(tenant2Handlers, "demoJob") {
		t.Fatalf("租户 %d 的处理器列表应保留非系统级 handler demoJob，实际 %v", tenant2ID, tenant2Handlers)
	}

	// 租户 2 创建系统级任务 → 403，文案明确「仅平台管理员」。
	status, api := env.Request(t, http.MethodPost, "/infra/job", tenant2, map[string]any{
		"name": "越权回收任务", "handlerName": systemJobHandler,
		"cronExpression": "0 0 3 * * *", "status": 1,
	})
	if status != http.StatusForbidden || !strings.Contains(api.Msg, "系统级任务仅平台管理员可操作") {
		t.Fatalf("租户 2 创建系统级任务：HTTP %d code %d msg %q，期望 403 且文案含「系统级任务仅平台管理员可操作」",
			status, api.Code, api.Msg)
	}

	// 租户 2 创建非系统级任务 → 成功（不受限制）。
	status, api = env.Request(t, http.MethodPost, "/infra/job", tenant2, map[string]any{
		"name": "租户2演示任务", "handlerName": "demoJob",
		"cronExpression": "0 */5 * * * *", "retryCount": 0, "status": 1,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 2 创建非系统级任务：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}
	tenant2JobID := findJobID(t, env, tenant2, "租户2演示任务")

	// 租户 2 把已有任务改成系统级 handler → 403。
	status, api = env.Request(t, http.MethodPut, "/infra/job", tenant2, map[string]any{
		"id": tenant2JobID, "name": "租户2演示任务", "handlerName": systemJobHandler,
		"cronExpression": "0 */5 * * * *", "retryCount": 0, "status": 1,
	})
	if status != http.StatusForbidden {
		t.Fatalf("租户 2 改任务为系统级 handler：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 租户 2 手动运行系统级任务 → 403。租户插件会隐藏其他租户的任务，
	// 这里直接为租户 2 插入一行系统级 handler 任务（模拟历史遗留/绕过入口）。
	insertJobForTenant(t, env, tenant2ID, "历史遗留回收任务", systemJobHandler)
	legacyJobID := findJobID(t, env, tenant2, "历史遗留回收任务")
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/infra/job/%d/run", legacyJobID), tenant2, nil)
	if status != http.StatusForbidden || !strings.Contains(api.Msg, "系统级任务仅平台管理员可操作") {
		t.Fatalf("租户 2 运行系统级任务：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 租户 2 启用系统级任务 → 403（启用后会被调度器自动执行）。
	status, api = env.Request(t, http.MethodPut, "/infra/job/status", tenant2, map[string]any{
		"id": legacyJobID, "status": 0,
	})
	if status != http.StatusForbidden {
		t.Fatalf("租户 2 启用系统级任务：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 平台管理员创建并运行系统级任务 → 成功。
	status, api = env.Request(t, http.MethodPost, "/infra/job", admin, map[string]any{
		"name": "平台回收任务", "handlerName": systemJobHandler,
		"cronExpression": "0 0 4 * * *", "retryCount": 0, "status": 1,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("平台管理员创建系统级任务：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}
	platformJobID := findJobID(t, env, admin, "平台回收任务")
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/infra/job/%d/run", platformJobID), admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("平台管理员运行系统级任务：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}
}

// findJobID 按名称从任务分页找到任务 ID（分页受租户插件过滤，只看当前租户）。
func findJobID(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet,
		"/infra/job/page?pageNo=1&pageSize=50&name="+url.QueryEscape(name), token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询任务 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var page struct {
		List []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析任务分页: %v（data=%s）", err, string(api.Data))
	}
	for _, job := range page.List {
		if job.Name == name {
			return job.ID
		}
	}
	t.Fatalf("未找到任务 %s（%s）", name, string(api.Data))
	return 0
}

// insertJobForTenant 直插一行任务（测试数据准备）：用于模拟修复前遗留的
// 租户级系统级 handler 任务，验证运行/启用入口的二次防线。
func insertJobForTenant(t *testing.T, env *testutil.IntegrationEnv, tenantID int64, name, handler string) {
	t.Helper()
	err := env.DB.Exec(
		"INSERT INTO infra_job (tenant_id, name, handler_name, handler_param, cron_expression, retry_count, status, created_at, updated_at) VALUES (?, ?, ?, '', '0 0 5 * * *', 0, 1, NOW(3), NOW(3))",
		tenantID, name, handler,
	).Error
	if err != nil {
		t.Fatalf("插入租户 %d 任务: %v", tenantID, err)
	}
}
