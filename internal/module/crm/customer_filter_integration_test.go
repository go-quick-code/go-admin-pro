//go:build integration

// 客户筛选、权限码 403 与租户隔离（issue #8）。
package crm_test

import (
	"encoding/json"
	"fmt"
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

// TestIntegrationCustomerPageFilters 列表筛选：名称模糊、状态、负责人、部门。
func TestIntegrationCustomerPageFilters(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 两位销售（研发 101 / 市场 102），客户分别归属并快照其部门。
	createUserByAPI(t, env, admin, "seller_rnd", 101, nil)
	createUserByAPI(t, env, admin, "seller_mkt", 102, nil)
	rnd := findUserIDByUsername(t, env, admin, "seller_rnd")
	mkt := findUserIDByUsername(t, env, admin, "seller_mkt")

	createCustomer(t, env, admin, "华东制造集团", map[string]any{"ownerUserId": rnd, "status": 1})
	createCustomer(t, env, admin, "华南零售集团", map[string]any{"ownerUserId": mkt, "status": 2})
	createCustomer(t, env, admin, "华西制造有限公司", map[string]any{"ownerUserId": mkt, "status": 3})

	// 名称模糊匹配。
	if _, total := pageCustomers(t, env, admin, "&name=制造"); total != 2 {
		t.Fatalf("按名称模糊「制造」total=%d，期望 2", total)
	}
	// 状态精确匹配。
	if _, total := pageCustomers(t, env, admin, "&status=2"); total != 1 {
		t.Fatalf("按状态 2 total=%d，期望 1", total)
	}
	// 负责人精确匹配。
	if _, total := pageCustomers(t, env, admin, fmt.Sprintf("&ownerUserId=%d", rnd)); total != 1 {
		t.Fatalf("按负责人 seller_rnd total=%d，期望 1", total)
	}
	// 负责人部门快照匹配：102 市场部有两家。
	if _, total := pageCustomers(t, env, admin, "&ownerDeptId=102"); total != 2 {
		t.Fatalf("按部门 102 total=%d，期望 2", total)
	}
	// 组合筛选：名称 + 状态。
	if _, total := pageCustomers(t, env, admin, "&name=集团&status=1"); total != 1 {
		t.Fatalf("组合筛选 total=%d，期望 1", total)
	}
}

// findUserIDByUsername 按用户名分页查询并返回唯一命中的用户 ID。
func findUserIDByUsername(t *testing.T, env *testutil.IntegrationEnv, token, username string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询用户 %s：HTTP %d code %d", username, status, api.Code)
	}
	var page struct {
		List []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v", err)
	}
	for _, u := range page.List {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("用户 %s 精确回查未命中（LIKE 筛选返回 %d 条）", username, len(page.List))
	return 0
}

// TestIntegrationCustomerPermissionDenied 无权限码一律 403：
// demo 账号只有仪表盘与用户页面权限，未绑定任何 CRM 菜单。
func TestIntegrationCustomerPermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	createCustomer(t, env, admin, "存在但不可见的客户", nil)

	demo := env.Login(t, "demo", "123456")

	// 列表、详情、新建、修改、删除全部 403，且不泄露客户存在性（详情 403 而非 404）。
	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "分页", method: http.MethodGet, path: "/crm/customer/page?pageNo=1&pageSize=10"},
		{name: "详情", method: http.MethodGet, path: "/crm/customer/1"},
		{name: "新建", method: http.MethodPost, path: "/crm/customer", body: map[string]any{"name": "越权客户"}},
		{name: "修改", method: http.MethodPut, path: "/crm/customer", body: map[string]any{"id": 1, "name": "越权改名"}},
		{name: "删除", method: http.MethodDelete, path: "/crm/customer/1"},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, demo, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	// admin 有全部权限码，对照可正常访问。
	status, api := env.Request(t, http.MethodGet, "/crm/customer/page?pageNo=1&pageSize=10", admin, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 查询分页：HTTP %d code %d", status, api.Code)
	}
}

// TestIntegrationCustomerTenantIsolation 租户隔离：两个租户互不可见，
// 编号序列按租户独立发放，跨租户访问详情 404。
func TestIntegrationCustomerTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	first := createCustomer(t, env, admin, "租户一客户", nil)
	no1 := first["customerNo"].(string)

	tenant2 := createTenantByAPI(t, env, admin, "隔离测试租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 租户 2 看不到租户 1 的客户。
	_, total := pageCustomers(t, env, token2, "")
	if total != 0 {
		t.Fatalf("租户 2 初始 total=%d，期望 0（租户隔离）", total)
	}
	status, _ := getCustomer(t, env, token2, int64(first["id"].(float64)))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 访问租户 1 客户详情：HTTP %d，期望 404", status)
	}

	// 租户 2 自己的编号序列从 0001 开始，与租户 1 同日编号互不干扰。
	own := createCustomer(t, env, token2, "租户二客户", nil)
	no2 := own["customerNo"].(string)
	if want := "KH" + env.DateKey() + "0001"; no2 != want {
		t.Fatalf("租户 2 首号应为 %s，实际 %s（序列按租户独立）", want, no2)
	}
	if no2 == no1 {
		// 允许同形：序列按 (tenant_id, biz_type, date_key) 独立发放，
		// 两租户同日各自的 0001 字符串相同不构成冲突。
		t.Logf("两租户同日编号同为 %s（序列按租户独立，互不干扰）", no2)
	}

	// 租户 1 仍只看到自己的客户，看不到租户 2 的。
	_, total = pageCustomers(t, env, admin, "")
	if total != 1 {
		t.Fatalf("租户 1 total=%d，期望 1", total)
	}

	// 租户 2 的 admin 也不能改租户 1 的客户（更新走租户过滤，找不到即 404）。
	status, _ = env.Request(t, http.MethodPut, "/crm/customer", token2, map[string]any{
		"id": first["id"], "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 客户：HTTP %d，期望 404", status)
	}
}
