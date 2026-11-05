//go:build integration

// 集成测试共享助手：复用 system 模块 HTTP 契约搭建角色/用户/部门，供数据范围矩阵使用。
// 写法参照 #5 的用户列表数据范围测试（internal/module/system/datascope_http_integration_test.go）。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// currentDateKey 返回当天 yyyyMMdd（与服务端 DateKey 同格式，仅测试断言用）。
func currentDateKey() string {
	return time.Now().Format("20060102")
}

// mustOK 断言业务成功（HTTP 200 且 code 0）。
func mustOK(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// createRoleByAPI 用 admin 创建角色（可带自定义部门与 CRM 菜单绑定）并回读 ID。
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

type treeNode struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Children []treeNode `json:"children"`
}

// findTreeNode 在部门树中按名称递归查找（测试创建的部门名在每个环境内唯一）。
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

// visibleCustomerNames 以指定账号登录并拉取客户分页，返回可见客户名集合。
func visibleCustomerNames(t *testing.T, env *testutil.IntegrationEnv, username string) map[string]bool {
	t.Helper()
	token := env.Login(t, username, "123456")
	list, _ := pageCustomers(t, env, token, "")
	set := make(map[string]bool, len(list))
	for _, row := range list {
		set[row["name"].(string)] = true
	}
	return set
}

// assertCustomerSet 断言可见客户名集合完全匹配期望。
func assertCustomerSet(t *testing.T, phase string, got map[string]bool, want []string, notWant ...string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s：可见集合缺少 %q，实际 %v", phase, name, keysOf(got))
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s：不应可见 %q，实际 %v", phase, name, keysOf(got))
		}
	}
}

func keysOf(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	return names
}
