//go:build integration

// 租户管理仅平台租户可用（PR #39 残余风险 B）：租户管理员在自己的租户内
// 持有 super_admin（"*"），若不额外收口，可经 /system/tenant/* 管理平台租户。
// 平台租户（ID=1）管理员不受影响；super_admin 在本租户内的 system:* 权限不动。
package system_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createTenantForPlatformTest 用平台管理员创建租户并返回租户 ID。
func createTenantForPlatformTest(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()

	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0,
	})
	mustOK(t, "创建租户 "+name, status, api)

	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=50&name="+name, token, nil)
	mustOK(t, "查询租户 "+name, status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析租户分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 || page.List[0].ID <= 1 {
		t.Fatalf("应查到唯一新建租户 %s，实际 %+v", name, page.List)
	}
	return page.List[0].ID
}

// loginTenantAdminForPlatformTest 登录指定租户的内置管理员。
func loginTenantAdminForPlatformTest(t *testing.T, env *testutil.IntegrationEnv, tenantID int64) string {
	t.Helper()

	status, api := env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenantID,
	})
	mustOK(t, fmt.Sprintf("登录租户 %d 管理员", tenantID), status, api)
	var data struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	if data.AccessToken == "" {
		t.Fatalf("登录响应缺少 accessToken: %s", string(api.Data))
	}
	return data.AccessToken
}

// TestIntegrationTenantManagementPlatformOnly 非平台租户管理员调用租户 CRUD
// 一律 403；平台管理员（tenant 1）列表/创建/修改/删除正常。
func TestIntegrationTenantManagementPlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	// 平台管理员列表正常。
	status, api := env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10", platformAdmin, nil)
	mustOK(t, "平台管理员查询租户列表", status, api)

	tenantID := createTenantForPlatformTest(t, env, platformAdmin, "平台收口租户")
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	// 非平台租户管理员：所有租户管理接口 403（含只读列表）。
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{name: "列表", method: http.MethodGet, path: "/system/tenant/page?pageNo=1&pageSize=10"},
		{name: "详情", method: http.MethodGet, path: fmt.Sprintf("/system/tenant/%d", tenantID)},
		{name: "创建", method: http.MethodPost, path: "/system/tenant", body: map[string]any{"name": "越权租户", "status": 0}},
		{name: "修改", method: http.MethodPut, path: "/system/tenant", body: map[string]any{"id": tenantID, "name": "越权改名", "status": 0}},
		{name: "改状态", method: http.MethodPut, path: "/system/tenant/status", body: map[string]any{"id": tenantID, "status": 1}},
		{name: "删除", method: http.MethodDelete, path: fmt.Sprintf("/system/tenant/%d", tenantID)},
	} {
		status, api := env.Request(t, tc.method, tc.path, tenantAdmin, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("非平台租户管理员%s租户：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	// 平台管理员修改、删除正常（删除前清掉租户下用户，业务守卫要求先清理数据）。
	status, api = env.Request(t, http.MethodPut, "/system/tenant", platformAdmin, map[string]any{
		"id": tenantID, "name": "平台收口租户改名", "contactName": "新联系人", "status": 0,
	})
	mustOK(t, "平台管理员修改租户", status, api)

	status, api = env.Request(t, http.MethodPut, "/system/tenant/status", platformAdmin, map[string]any{
		"id": tenantID, "status": 1,
	})
	mustOK(t, "平台管理员修改租户状态", status, api)

	if err := env.DB.Exec("DELETE FROM system_user WHERE tenant_id = ?", tenantID).Error; err != nil {
		t.Fatalf("清理租户 %d 用户: %v", tenantID, err)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/tenant/%d", tenantID), platformAdmin, nil)
	mustOK(t, "平台管理员删除租户", status, api)
}

// TestIntegrationTenantManagementKeepsPlatformSelfService 平台租户内的
// system:* 权限保持不动：平台管理员仍可管理用户等本租户资源。
func TestIntegrationTenantManagementKeepsPlatformSelfService(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	platformAdmin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", platformAdmin, nil)
	mustOK(t, "平台管理员查询用户", status, api)
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10", platformAdmin, nil)
	mustOK(t, "平台管理员查询租户", status, api)
}
