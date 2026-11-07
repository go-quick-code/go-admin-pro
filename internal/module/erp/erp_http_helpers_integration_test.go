//go:build integration

// ERP HTTP 集成测试公共助手（issue #88）：与 crm_test 同构的最小集合。
// 走 testutil 基座的真实 router + 生产中间件（JWT/权限/租户插件）+ 真实 MySQL，
// 在 HTTP 契约上验证 CRUD/引用约束/数据范围/租户隔离/导出。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ERP 菜单/按钮 ID（与 internal/seed/seed.go 的 700 段一致）。
const (
	erpCategoryMenuID = int64(710)
	erpUnitMenuID     = int64(711)
	erpProductMenuID  = int64(712)
)

// mustOK 断言业务成功（HTTP 200 且 code 0）。
func mustOK(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// ---------- 产品分类 ----------

// createCategory 创建产品分类并断言成功，返回落库后的完整分类 JSON。
func createCategory(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/erp/product-category", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建分类 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建分类响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// listCategories 查询分类全量列表（GET /erp/product-category/list，前端组树契约）。
func listCategories(t *testing.T, env *testutil.IntegrationEnv, token string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/product-category/list", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询分类列表：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var list []map[string]any
	if err := json.Unmarshal(api.Data, &list); err != nil {
		t.Fatalf("解析分类列表: %v（data=%s）", err, string(api.Data))
	}
	return list
}

// getJSON GET 详情并返回 HTTP 状态与业务信封。
func getJSON(t *testing.T, env *testutil.IntegrationEnv, token, path string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, path, token, nil)
}

// deleteByID 按 ID 发起 DELETE（path 形如 /erp/product-category/%d）。
func deleteByID(t *testing.T, env *testutil.IntegrationEnv, token, path string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodDelete, path, token, nil)
}

// createRoleByAPI 用 admin 创建角色（可带自定义部门与菜单绑定）并回读 ID。
func createRoleByAPI(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/role", token, payload)
	mustOK(t, fmt.Sprintf("创建角色 %v", payload["code"]), status, api)
	return findRoleIDByCode(t, env, token, fmt.Sprint(payload["code"]))
}

// findRoleIDByCode 按角色编码分页查询并返回唯一命中的 ID。
func findRoleIDByCode(t *testing.T, env *testutil.IntegrationEnv, token, code string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/role/page?code="+code+"&pageNo=1&pageSize=10", token, nil)
	mustOK(t, "查询角色 "+code, status, api)
	var page struct {
		List []struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析角色分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("按编码 %s 查询角色：total=%d，期望唯一命中", code, page.Total)
	}
	return page.List[0].ID
}

// createUserByAPI 用 admin 创建启用用户并绑定角色。
func createUserByAPI(t *testing.T, env *testutil.IntegrationEnv, token, username string, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", token, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	mustOK(t, "创建用户 "+username, status, api)
}

type treeNode struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Children []treeNode `json:"children"`
}

// createDeptByAPI 用 admin 在 parentID 下创建子部门并回读 ID。
func createDeptByAPI(t *testing.T, env *testutil.IntegrationEnv, token, name string, parentID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/dept", token, map[string]any{
		"name": name, "parentId": parentID,
	})
	mustOK(t, "创建部门 "+name, status, api)

	status, api = env.Request(t, http.MethodGet, "/system/dept/tree", token, nil)
	mustOK(t, "查询部门树", status, api)
	var tree []treeNode
	if err := json.Unmarshal(api.Data, &tree); err != nil {
		t.Fatalf("解析部门树: %v（data=%s）", err, string(api.Data))
	}
	if id, ok := findTreeNode(tree, name); ok {
		return id
	}
	t.Fatalf("部门树中未找到 %s（父部门 %d）", name, parentID)
	return 0
}

// findTreeNode 在树中按名称递归查找（测试创建的节点名在每个环境内唯一）。
func findTreeNode(nodes []treeNode, name string) (int64, bool) {
	for _, n := range nodes {
		if n.Name == name {
			return n.ID, true
		}
		if id, ok := findTreeNode(n.Children, name); ok {
			return id, true
		}
	}
	return 0, false
}

// findUserIDByUsername 按用户名回读用户 ID。
func findUserIDByUsername(t *testing.T, env *testutil.IntegrationEnv, token, username string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=50&username="+username, token, nil)
	mustOK(t, "查询用户 "+username, status, api)
	var page struct {
		List []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v", err)
	}
	for _, row := range page.List {
		if row.Username == username {
			return row.ID
		}
	}
	t.Fatalf("用户 %s 未找到", username)
	return 0
}

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
