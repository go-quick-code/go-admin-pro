//go:build integration

// pay HTTP 集成测试公共助手（与 erp_test 同构的最小集合，issue #131）。
package pay_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// mustOK 断言业务成功（HTTP 200 且 code 0）。
func mustOK(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// loginTenant 以指定租户登录（新租户内置 admin/123456）。
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
	mustOK(t, "创建租户 "+name, status, api)
	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?name="+name+"&pageNo=1&pageSize=10", token, nil)
	mustOK(t, "查询租户 "+name, status, api)
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
