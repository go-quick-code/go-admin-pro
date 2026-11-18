//go:build integration

// mall HTTP 集成测试公共助手（照 pay_http_helpers_integration_test.go 同构的
// 最小集合，issue #174）。
package mall_test

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

// parseJSON 解析响应 data 为 map。
func parseJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("解析响应 JSON: %v（raw=%s）", err, raw)
	}
	return out
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
			ID int64 `json:"id"`
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

// mallCreateMember 创建会员（断言成功返回响应 JSON）。
func mallCreateMember(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/member", token, body)
	mustOK(t, "创建会员", status, api)
	return parseJSON(t, string(api.Data))
}

// mallPageList 解析分页响应的 list 数组。
func mallPageList(t *testing.T, api testutil.APIResponse) []map[string]any {
	t.Helper()
	page := parseJSON(t, string(api.Data))
	list, ok := page["list"].([]any)
	if !ok {
		t.Fatalf("分页响应缺少 list：%s", api.Data)
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		out = append(out, item.(map[string]any))
	}
	return out
}
