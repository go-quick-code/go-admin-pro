//go:build integration

package system_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 数据范围 HTTP 集成测试（issue #5）：
// 走 #3 基座的真实 router + 生产中间件（JWT/权限/租户）+ 真实 MySQL，
// 在 HTTP 契约上验证「非法 dataScope 返回 400 绝不回退」「自定义部门关联持久化」。

// mustOK 断言业务成功（HTTP 200 且 code 0）。
func mustOK(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// createRoleByAPI 用 admin 创建角色并回读 ID。
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

// getRoleRaw 回读角色详情原始 JSON。
func getRoleRaw(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/system/role/%d", id), token, nil)
	mustOK(t, "读取角色详情", status, api)
	var role map[string]any
	if err := json.Unmarshal(api.Data, &role); err != nil {
		t.Fatalf("解析角色详情: %v（data=%s）", err, string(api.Data))
	}
	return role
}

// TestIntegrationRoleRejectsInvalidDataScope 非法 dataScope 必须返回 400 且绝不落库（不回退「全部」）。
func TestIntegrationRoleRejectsInvalidDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	for _, bad := range []int{9, 0, -1} {
		status, api := env.Request(t, http.MethodPost, "/system/role", token, map[string]any{
			"name": "越权探测", "code": fmt.Sprintf("evil_%d", bad), "dataScope": bad,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("创建角色 dataScope=%d：HTTP %d code %d msg %q，期望 HTTP 400", bad, status, api.Code, api.Msg)
		}
		if api.Code == 0 || api.Msg == "" {
			t.Fatalf("创建角色 dataScope=%d：业务响应 %v，期望非零业务码与中文错误信息", bad, api)
		}
	}

	// 全部非法请求都不落库：按编码前缀查询不应有任何角色。
	status, api := env.Request(t, http.MethodGet, "/system/role/page?code=evil&pageNo=1&pageSize=10", token, nil)
	mustOK(t, "回查越权探测角色", status, api)
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析角色分页: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("非法 dataScope 的角色被落库（回退风险）：total=%d，期望 0", page.Total)
	}

	// 编辑既有角色同样拒绝非法值，且原值保持不变。
	roleID := createRoleByAPI(t, env, token, map[string]any{
		"name": "仅本人角色", "code": "scope_self", "dataScope": 5, "menuIds": []int64{101},
	})
	status, api = env.Request(t, http.MethodPut, "/system/role", token, map[string]any{
		"id": roleID, "name": "仅本人角色", "code": "scope_self", "dataScope": 9,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("编辑角色 dataScope=9：HTTP %d code %d msg %q，期望 HTTP 400", status, api.Code, api.Msg)
	}
	role := getRoleRaw(t, env, token, roleID)
	if got := int(role["dataScope"].(float64)); got != 5 {
		t.Fatalf("非法编辑后 dataScope=%d，期望保持 5（绝不回退「全部」）", got)
	}
}

// TestIntegrationRoleCustomDeptRoundTrip 自定义部门：deptIds 持久化、回显、随编辑替换。
func TestIntegrationRoleCustomDeptRoundTrip(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	roleID := createRoleByAPI(t, env, token, map[string]any{
		"name": "跨部门协作", "code": "scope_custom", "dataScope": 2,
		"deptIds": []int64{101, 102}, "menuIds": []int64{101},
	})

	role := getRoleRaw(t, env, token, roleID)
	if got := int(role["dataScope"].(float64)); got != 2 {
		t.Fatalf("dataScope=%v，期望 2（自定义部门）", role["dataScope"])
	}
	if got := fmt.Sprint(role["deptIds"]); got != "[101 102]" {
		t.Fatalf("deptIds=%v，期望 [101 102]", role["deptIds"])
	}

	// 编辑替换为新的部门集合。
	status, api := env.Request(t, http.MethodPut, "/system/role", token, map[string]any{
		"id": roleID, "name": "跨部门协作", "code": "scope_custom", "dataScope": 2,
		"deptIds": []int64{103}, "menuIds": []int64{101},
	})
	mustOK(t, "编辑角色部门集合", status, api)
	role = getRoleRaw(t, env, token, roleID)
	if got := fmt.Sprint(role["deptIds"]); got != "[103]" {
		t.Fatalf("编辑后 deptIds=%v，期望 [103]", role["deptIds"])
	}

	// 切换为「本部门」后部门集合不再是数据范围依据，接口原样保存不报错。
	status, api = env.Request(t, http.MethodPut, "/system/role", token, map[string]any{
		"id": roleID, "name": "跨部门协作", "code": "scope_custom", "dataScope": 3,
		"menuIds": []int64{101},
	})
	mustOK(t, "切换数据范围", status, api)
	role = getRoleRaw(t, env, token, roleID)
	if got := int(role["dataScope"].(float64)); got != 3 {
		t.Fatalf("dataScope=%v，期望 3", role["dataScope"])
	}
}

// ---------- 用户列表按数据范围过滤 ----------

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

// createUserByAPI 用 admin 创建启用用户并绑定角色。
func createUserByAPI(t *testing.T, env *testutil.IntegrationEnv, token, username string, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", token, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	mustOK(t, "创建用户 "+username, status, api)
}

// visibleUsernames 以指定账号登录并拉取用户分页，返回可见用户名集合。
func visibleUsernames(t *testing.T, env *testutil.IntegrationEnv, username string) map[string]bool {
	t.Helper()
	token := env.Login(t, username, "123456")
	status, api := env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=50", token, nil)
	mustOK(t, username+" 查询用户分页", status, api)
	var page struct {
		List []struct {
			Username string `json:"username"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v（data=%s）", err, string(api.Data))
	}
	set := make(map[string]bool, len(page.List))
	for _, u := range page.List {
		set[u.Username] = true
	}
	return set
}

func assertUserSet(t *testing.T, phase string, got map[string]bool, want []string, notWant ...string) {
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

// TestIntegrationUserPageDataScope 用户列表按 5 种数据范围过滤（引擎的首个验证场）。
// 每个子测试独立环境：seed 部门为 100 总公司 / 101 研发部 / 102 市场部 / 103 财务部，
// seed 用户为 admin(100)、demo(101)。
func TestIntegrationUserPageDataScope(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		token := env.Login(t, "admin", "123456")
		roleID := createRoleByAPI(t, env, token, map[string]any{
			"name": "全部数据", "code": "ds_all", "dataScope": 1, "menuIds": []int64{101},
		})
		createUserByAPI(t, env, token, "u_all", 102, []int64{roleID})
		got := visibleUsernames(t, env, "u_all")
		assertUserSet(t, "全部数据", got, []string{"admin", "demo", "u_all"})
	})

	t.Run("自定义部门_精确集合不含子部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		token := env.Login(t, "admin", "123456")
		subDept := createDeptByAPI(t, env, token, "研发一组", 101)
		roleID := createRoleByAPI(t, env, token, map[string]any{
			"name": "自定义部门", "code": "ds_custom", "dataScope": 2,
			"deptIds": []int64{101, 103}, "menuIds": []int64{101},
		})
		createUserByAPI(t, env, token, "u_custom", 102, []int64{roleID})
		createUserByAPI(t, env, token, "u_child", subDept, nil)
		createUserByAPI(t, env, token, "u_finance", 103, nil)
		got := visibleUsernames(t, env, "u_custom")
		// 101 研发（demo）与 103 财务可见；101 的子部门与观察者所在的 102 不可见。
		assertUserSet(t, "自定义部门", got, []string{"demo", "u_finance"},
			"admin", "u_custom", "u_child")
	})

	t.Run("本部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		token := env.Login(t, "admin", "123456")
		roleID := createRoleByAPI(t, env, token, map[string]any{
			"name": "本部门", "code": "ds_dept", "dataScope": 3, "menuIds": []int64{101},
		})
		createUserByAPI(t, env, token, "u_dept", 102, []int64{roleID})
		createUserByAPI(t, env, token, "u_mkt2", 102, nil)
		got := visibleUsernames(t, env, "u_dept")
		assertUserSet(t, "本部门", got, []string{"u_dept", "u_mkt2"}, "admin", "demo")
	})

	t.Run("本部门及以下", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		token := env.Login(t, "admin", "123456")
		subDept := createDeptByAPI(t, env, token, "市场二组", 102)
		roleID := createRoleByAPI(t, env, token, map[string]any{
			"name": "本部门及以下", "code": "ds_deptsub", "dataScope": 4, "menuIds": []int64{101},
		})
		createUserByAPI(t, env, token, "u_deptsub", 102, []int64{roleID})
		createUserByAPI(t, env, token, "u_mkt2", 102, nil)
		createUserByAPI(t, env, token, "u_mkt_sub", subDept, nil)
		got := visibleUsernames(t, env, "u_deptsub")
		assertUserSet(t, "本部门及以下", got, []string{"u_deptsub", "u_mkt2", "u_mkt_sub"},
			"admin", "demo")
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		token := env.Login(t, "admin", "123456")
		roleID := createRoleByAPI(t, env, token, map[string]any{
			"name": "仅本人", "code": "ds_self", "dataScope": 5, "menuIds": []int64{101},
		})
		createUserByAPI(t, env, token, "u_self", 102, []int64{roleID})
		createUserByAPI(t, env, token, "u_mkt2", 102, nil)
		got := visibleUsernames(t, env, "u_self")
		assertUserSet(t, "仅本人", got, []string{"u_self"}, "admin", "demo", "u_mkt2")
	})

}

// TestIntegrationUserPageMultiRoleUnion 多角色并集与「全部」短路。
func TestIntegrationUserPageMultiRoleUnion(t *testing.T) {
	t.Run("部门角色取并集", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		token := env.Login(t, "admin", "123456")
		subDept := createDeptByAPI(t, env, token, "研发一组", 101)
		customRole := createRoleByAPI(t, env, token, map[string]any{
			"name": "自定义101", "code": "ds_u_custom", "dataScope": 2,
			"deptIds": []int64{101}, "menuIds": []int64{101},
		})
		deptRole := createRoleByAPI(t, env, token, map[string]any{
			"name": "本部门102", "code": "ds_u_dept", "dataScope": 3, "menuIds": []int64{101},
		})
		createUserByAPI(t, env, token, "u_multi", 102, []int64{customRole, deptRole})
		createUserByAPI(t, env, token, "u_mkt2", 102, nil)
		createUserByAPI(t, env, token, "u_child", subDept, nil)
		got := visibleUsernames(t, env, "u_multi")
		// 自定义 {101} ∪ 本部门 {102}：demo(101) 与 102 的用户可见；101 的子部门不可见。
		assertUserSet(t, "多角色并集", got, []string{"demo", "u_multi", "u_mkt2"},
			"admin", "u_child")
	})

	t.Run("任一角色全部则短路", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		token := env.Login(t, "admin", "123456")
		allRole := createRoleByAPI(t, env, token, map[string]any{
			"name": "全部", "code": "ds_u_all", "dataScope": 1, "menuIds": []int64{101},
		})
		selfRole := createRoleByAPI(t, env, token, map[string]any{
			"name": "仅本人", "code": "ds_u_self", "dataScope": 5, "menuIds": []int64{101},
		})
		createUserByAPI(t, env, token, "u_mix", 103, []int64{selfRole, allRole})
		createUserByAPI(t, env, token, "u_finance2", 103, nil)
		got := visibleUsernames(t, env, "u_mix")
		assertUserSet(t, "全部短路", got, []string{"admin", "demo", "u_mix", "u_finance2"})
	})
}
