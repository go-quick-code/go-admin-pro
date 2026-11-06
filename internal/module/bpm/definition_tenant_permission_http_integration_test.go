//go:build integration

// 租户隔离与权限码（issue #44，spec §4.6）：跨租户 404 不泄露存在性；
// bpm:definition:* 权限码逐码控权。测试库 _t44。
package bpm_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// loginTenant 以指定租户登录（登录契约带 tenantId，同名账号跨租户共存）。
func loginTenant(t *testing.T, env *testutil.IntegrationEnv, username, password string, tenantID int64) string {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": username, "password": password, "tenantId": tenantID,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 %d 登录 %s：HTTP %d code %d msg %q", tenantID, username, status, api.Code, api.Msg)
	}
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	return data.AccessToken
}

// createTenantByAPI 创建租户并回读 ID（新租户内置 admin/123456 管理员）。
func createTenantByAPI(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建租户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?name="+name+"&pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询租户 %s：HTTP %d code %d", name, status, api.Code)
	}
	var page struct {
		List []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析租户分页: %v", err)
	}
	if len(page.List) != 1 {
		t.Fatalf("租户 %s 应唯一命中，实际 %d 条", name, len(page.List))
	}
	return page.List[0].ID
}

// TestIntegrationDefinitionTenantIsolation 跨租户：列表不可见、详情/发布/
// 停用/删除 404（不泄露存在性）；租户内数据互不串。
func TestIntegrationDefinitionTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	created := createDefinition(t, env, admin, "tenant_flow", demoBusinessType, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	id := int64(created["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "bpm_tenant_two")
	tenant2Token := loginTenant(t, env, "admin", "123456", tenant2)

	// 列表：租户 2 看不到租户 1 的定义（含 key 模糊筛选）。
	if _, total := pageDefinitions(t, env, tenant2Token, "&key=tenant_flow"); total != 0 {
		t.Fatalf("跨租户列表应为空，实际 total=%d", total)
	}
	// 详情/发布/停用/删除：404。
	status, _, msg := getDefinition(t, env, tenant2Token, id)
	mustFail(t, "跨租户详情", status, http.StatusNotFound, msg, "")
	status, msg = publishDefinition(t, env, tenant2Token, id)
	mustFail(t, "跨租户发布", status, http.StatusNotFound, msg, "")
	status, msg = disableDefinition(t, env, tenant2Token, id)
	mustFail(t, "跨租户停用", status, http.StatusNotFound, msg, "")
	status, msg = deleteDefinition(t, env, tenant2Token, id)
	mustFail(t, "跨租户删除", status, http.StatusNotFound, msg, "")

	// 租户 2 建同 key 定义互不冲突（版本号独立从 1 计）。
	tenant2Def := createDefinition(t, env, tenant2Token, "tenant_flow", demoBusinessType, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	if tenant2Def["version"].(float64) != 1 {
		t.Fatalf("租户 2 同 key 版本应从 1 计，实际 %v", tenant2Def["version"])
	}
	// 原租户数据未被影响：仍是草稿、可发布。
	if status, msg = publishDefinition(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("原租户发布：HTTP %d msg %q", status, msg)
	}
}

// TestIntegrationDefinitionPermissionCodes 权限码逐码控权（spec §4.6）：
// 仅查询码可读不可写；全量码可完成创建/编辑/发布/停用/删除；无码 403。
func TestIntegrationDefinitionPermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	registerTestHandler(t, env, demoBusinessType)

	// 菜单/按钮 ID：511 流程定义（查询码在菜单上），5111–5114 增/改/删/发布。
	const (
		menuDefinition = int64(511)
		btnCreate      = int64(5111)
		btnUpdate      = int64(5112)
		btnDelete      = int64(5113)
		btnPublish     = int64(5114)
	)
	createRole := func(name, code string, menuIDs []int64) int64 {
		t.Helper()
		status, api := env.Request(t, http.MethodPost, "/system/role", admin, map[string]any{
			"name": name, "code": code, "dataScope": 5, "status": 0, "menuIds": menuIDs,
		})
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("创建角色 %s：HTTP %d code %d msg %q", code, status, api.Code, api.Msg)
		}
		status, api = env.Request(t, http.MethodGet, "/system/role/page?code="+code+"&pageNo=1&pageSize=10", admin, nil)
		var page struct {
			List []struct {
				ID int64 `json:"id"`
			} `json:"list"`
		}
		if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
			t.Fatalf("查询角色 %s: %v（data=%s）", code, err, string(api.Data))
		}
		return page.List[0].ID
	}
	createUser := func(username string, roleID int64) string {
		t.Helper()
		status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
			"username": username, "nickname": username, "password": "123456",
			"deptId": 101, "status": 0, "roleIds": []int64{roleID},
		})
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("创建用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
		}
		return env.Login(t, username, "123456")
	}

	// 只读角色（bpm:definition:query）与全量角色（query+create+update+delete+publish）。
	reader := createUser("bpm_def_reader", createRole("bpm 只读角色", "bpm_def_read", []int64{menuDefinition}))
	manager := createUser("bpm_def_manager", createRole("bpm 管理角色", "bpm_def_manage",
		[]int64{menuDefinition, btnCreate, btnUpdate, btnDelete, btnPublish}))

	// 无任何 bpm 码的用户：列表 403。
	noPerm := createUser("bpm_def_none", createRole("bpm 无码角色", "bpm_def_none", []int64{1}))
	status, api := env.Request(t, http.MethodGet, "/bpm/definition/page?pageNo=1&pageSize=10", noPerm, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无码用户列表应 403，实际 HTTP %d code %d", status, api.Code)
	}

	// 只读：列表/详情可读，写路径全 403。
	if _, total := pageDefinitions(t, env, reader, ""); total < 0 {
		t.Fatal("不可达")
	}
	status, api = env.Request(t, http.MethodPost, "/bpm/definition", reader, map[string]any{
		"key": "perm_flow", "name": "无权创建", "businessType": demoBusinessType, "nodes": []map[string]any{node("审批", "initiator", "", 0)},
	})
	if status != http.StatusForbidden {
		t.Fatalf("只读用户创建应 403，实际 HTTP %d code %d", status, api.Code)
	}

	// 全量角色：创建 → 编辑 → 发布 → 停用 → 删除 全链可用。
	created := createDefinition(t, env, manager, "perm_flow", demoBusinessType, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	id := int64(created["id"].(float64))
	if status, msg := updateDefinition(t, env, manager, id, map[string]any{
		"name": "权限流-改", "nodes": []map[string]any{node("审批", "initiator", "", 0)}, "remark": "mgr",
	}); status != http.StatusOK {
		t.Fatalf("manager 编辑：HTTP %d msg %q", status, msg)
	}
	if status, msg := publishDefinition(t, env, manager, id); status != http.StatusOK {
		t.Fatalf("manager 发布：HTTP %d msg %q", status, msg)
	}
	if status, msg := disableDefinition(t, env, manager, id); status != http.StatusOK {
		t.Fatalf("manager 停用：HTTP %d msg %q", status, msg)
	}
	// 停用版本不可删（状态机约束优先于权限码），再建草稿验证删除权限。
	created2 := createDefinition(t, env, manager, "perm_flow2", demoBusinessType, []map[string]any{
		node("审批", "initiator", "", 0),
	})
	if status, msg := deleteDefinition(t, env, manager, int64(created2["id"].(float64))); status != http.StatusOK {
		t.Fatalf("manager 删除草稿：HTTP %d msg %q", status, msg)
	}
}
