//go:build integration

// 产品集成测试骨架（本文件由 codegen 生成（issue #10 样板）；矩阵模式参照 crm 客户的
// customer_scope_integration_test.go）：CRUD 生命周期冒烟。
//
// 前置（生成包 INTEGRATION.md 的接线步骤全部完成后才可运行）：
// 迁移 + TenantTables 登记 + 路由注册 + 菜单/权限码种子；
// 测试库经 GAP_TEST_DATABASE_DSN 指向独立库，用 make test-integration 运行。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// crmProductMenuID 是种子中的产品菜单 ID（含 crm:product:query 权限码）。
// 取值来自实体元信息的 menu 配置，与 seed/menus.append.go 片段一致。
const crmProductMenuID = int64(4061)

// productMustOK 断言业务成功（HTTP 200 且 code 0）。
func productMustOK(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// productScopeCreateUser 用 admin 创建启用用户并绑定角色。
func productScopeCreateUser(t *testing.T, env *testutil.IntegrationEnv, token, username string, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", token, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	productMustOK(t, "创建用户 "+username, status, api)
}

// productScopeFindUserID 按用户名精确回查用户 ID。
func productScopeFindUserID(t *testing.T, env *testutil.IntegrationEnv, token, username string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", token, nil)
	productMustOK(t, "查询用户 "+username, status, api)
	var page struct {
		List []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v（data=%s）", err, string(api.Data))
	}
	for _, u := range page.List {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("用户 %s 精确回查未命中", username)
	return 0
}

// productScopeCreateRole 用 admin 创建角色（数据范围 + 菜单绑定）并回读 ID。
func productScopeCreateRole(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/role", token, payload)
	productMustOK(t, fmt.Sprintf("创建角色 %v", payload["code"]), status, api)

	status, api = env.Request(t, http.MethodGet, "/system/role/page?code="+fmt.Sprint(payload["code"])+"&pageNo=1&pageSize=10", token, nil)
	productMustOK(t, "查询角色 "+fmt.Sprint(payload["code"]), status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析角色分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 {
		t.Fatalf("按编码 %s 查询角色：命中 %d 条，期望唯一", payload["code"], len(page.List))
	}
	return page.List[0].ID
}

// productScopeTreeNode 是部门树节点（回查测试创建的子部门用）。
type productScopeTreeNode struct {
	ID       int64                  `json:"id"`
	Name     string                 `json:"name"`
	Children []productScopeTreeNode `json:"children"`
}

// productScopeCreateDept 用 admin 在 parentID 下创建子部门并回读 ID。
func productScopeCreateDept(t *testing.T, env *testutil.IntegrationEnv, token, name string, parentID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/dept", token, map[string]any{
		"name": name, "parentId": parentID,
	})
	productMustOK(t, "创建部门 "+name, status, api)

	status, api = env.Request(t, http.MethodGet, "/system/dept/tree", token, nil)
	productMustOK(t, "查询部门树", status, api)
	var tree []productScopeTreeNode
	if err := json.Unmarshal(api.Data, &tree); err != nil {
		t.Fatalf("解析部门树: %v（data=%s）", err, string(api.Data))
	}
	var find func(nodes []productScopeTreeNode) (int64, bool)
	find = func(nodes []productScopeTreeNode) (int64, bool) {
		for _, n := range nodes {
			if n.Name == name {
				return n.ID, true
			}
			if id, ok := find(n.Children); ok {
				return id, true
			}
		}
		return 0, false
	}
	if id, ok := find(tree); ok {
		return id
	}
	t.Fatalf("部门树中未找到 %s（父部门 %d）", name, parentID)
	return 0
}

// productScopeCreateProduct 用指定账号创建产品（必填字段填占位值，extra 覆盖任意字段）。
func productScopeCreateProduct(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{
		"name":       name,
		"categoryId": 1,
		"code":       "占位",
		"price":      "9.90",
	}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/crm/product", token, payload)
	productMustOK(t, "创建产品 "+name, status, api)
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建产品响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// productScopePageProducts 拉取产品分页（query 追加在固定分页参数后）。
func productScopePageProducts(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/product/page?pageNo=1&pageSize=50"+query, token, nil)
	productMustOK(t, "产品分页", status, api)
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析产品分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// productScopeGetProduct 查询产品详情，返回 HTTP 状态码（404 时不解析响应体）。
func productScopeGetProduct(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) int {
	t.Helper()
	status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/crm/product/%d", id), token, nil)
	return status
}

// TestIntegrationProductCRUDSmoke 非 owner 实体的 CRUD 冒烟骨架：
// 创建 → 分页 → 详情 → 修改 → 删除 → 详情 404（数据范围矩阵不适用于本实体）。
func TestIntegrationProductCRUDSmoke(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	row := productScopeCreateProduct(t, env, admin, "产品_冒烟", nil)
	id := int64(row["id"].(float64))

	list, total := productScopePageProducts(t, env, admin, "")
	if total < 1 || len(list) < 1 {
		t.Fatalf("分页应至少 1 条，实际 total=%d", total)
	}

	if status := productScopeGetProduct(t, env, admin, id); status != http.StatusOK {
		t.Fatalf("详情：HTTP %d，期望 200", status)
	}

	status, _ := env.Request(t, http.MethodPut, "/crm/product", admin, map[string]any{
		"id":         id,
		"name":       "产品_改名",
		"categoryId": 1,
		"code":       "占位",
		"price":      "9.90",
	})
	if status != http.StatusOK {
		t.Fatalf("修改：HTTP %d，期望 200", status)
	}

	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", id), admin, nil)
	if status != http.StatusOK {
		t.Fatalf("删除：HTTP %d，期望 200", status)
	}
	if status := productScopeGetProduct(t, env, admin, id); status != http.StatusNotFound {
		t.Fatalf("删除后详情：HTTP %d，期望 404", status)
	}
}
