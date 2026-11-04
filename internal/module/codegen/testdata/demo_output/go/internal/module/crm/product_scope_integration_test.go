//go:build integration

// 产品集成测试骨架（本文件由 codegen 生成（issue #10 样板）；矩阵模式参照 crm 客户的
// customer_scope_integration_test.go）：5 种数据范围（对齐芋道 1–5，
// ADR 0004）在 owner_dept_id / owner_user_id 列上生效，范围外详情/修改/删除一律 404。
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
const crmProductMenuID = int64(402)

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
		"name":  name,
		"price": "9.90",
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

// productScopeVisibleNames 以指定账号登录并拉取产品分页，返回可见产品名集合。
func productScopeVisibleNames(t *testing.T, env *testutil.IntegrationEnv, username string) map[string]bool {
	t.Helper()
	token := env.Login(t, username, "123456")
	list, _ := productScopePageProducts(t, env, token, "")
	set := make(map[string]bool, len(list))
	for _, row := range list {
		set[row["name"].(string)] = true
	}
	return set
}

// productScopeAssertSet 断言可见产品名集合完全匹配期望。
func productScopeAssertSet(t *testing.T, phase string, got map[string]bool, want []string, notWant ...string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s：可见集合缺少 %q，实际 %v", phase, name, got)
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s：不应可见 %q，实际 %v", phase, name, got)
		}
	}
}

// productScopeSetup 搭建标准观察场景（seed 部门 100 总公司 / 101 研发 / 102 市场 / 103 财务）：
//   - 研发一名销售，其产品在部门 101；
//   - 市场两名销售（含观察者的同事），其产品在部门 102；
//   - 市场子部门「市场二组」一名销售，其产品在子部门；
//   - 观察者（观察者角色）在部门 102，自己的产品归属本人。
//
// 返回观察者用户名，供各数据范围子测试断言。
func productScopeSetup(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64) string {
	t.Helper()
	rndDept, mktDept := int64(101), int64(102)
	productScopeCreateUser(t, env, admin, "owner_rnd_"+roleCode, rndDept, nil)
	productScopeCreateUser(t, env, admin, "owner_mkt_"+roleCode, mktDept, nil)
	productScopeCreateUser(t, env, admin, "owner_mkt2_"+roleCode, mktDept, nil)
	subDept := productScopeCreateDept(t, env, admin, "市场二组_"+roleCode, mktDept)
	productScopeCreateUser(t, env, admin, "owner_sub_"+roleCode, subDept, nil)

	for _, u := range []string{"owner_rnd_" + roleCode, "owner_mkt_" + roleCode, "owner_mkt2_" + roleCode, "owner_sub_" + roleCode} {
		id := productScopeFindUserID(t, env, admin, u)
		productScopeCreateProduct(t, env, admin, "产品_of_"+u, map[string]any{"ownerUserId": id})
	}

	roleID := productScopeCreateRole(t, env, admin, map[string]any{
		"name": "观察者角色_" + roleCode, "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs,
		"menuIds": []int64{402, 4021, 4022, 4023, 4024},
	})
	observer := "observer_" + roleCode
	productScopeCreateUser(t, env, admin, observer, mktDept, []int64{roleID})
	observerID := productScopeFindUserID(t, env, admin, observer)
	productScopeCreateProduct(t, env, admin, "产品_of_"+observer, map[string]any{"ownerUserId": observerID})
	return observer
}

// TestIntegrationProductDataScopeMatrix 产品列表按 5 种数据范围过滤。
func TestIntegrationProductDataScopeMatrix(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := productScopeSetup(t, env, admin, "product_ds_all", 1, nil)
		got := productScopeVisibleNames(t, env, observer)
		productScopeAssertSet(t, "全部数据", got, []string{
			"产品_of_owner_rnd_product_ds_all",
			"产品_of_owner_mkt_product_ds_all",
			"产品_of_owner_mkt2_product_ds_all",
			"产品_of_owner_sub_product_ds_all",
			"产品_of_observer_product_ds_all",
		})
	})

	t.Run("自定义部门_精确集合不含子部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := productScopeSetup(t, env, admin, "product_ds_custom", 2, []int64{101, 103})
		got := productScopeVisibleNames(t, env, observer)
		// 仅 101、103 的产品可见：102（观察者本部门）与 101 的子部门不在集合内。
		productScopeAssertSet(t, "自定义部门", got, []string{
			"产品_of_owner_rnd_product_ds_custom",
		},
			"产品_of_owner_mkt_product_ds_custom",
			"产品_of_owner_mkt2_product_ds_custom",
			"产品_of_owner_sub_product_ds_custom",
			"产品_of_observer_product_ds_custom")
	})

	t.Run("本部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := productScopeSetup(t, env, admin, "product_ds_dept", 3, nil)
		got := productScopeVisibleNames(t, env, observer)
		productScopeAssertSet(t, "本部门", got, []string{
			"产品_of_owner_mkt_product_ds_dept",
			"产品_of_owner_mkt2_product_ds_dept",
			"产品_of_observer_product_ds_dept",
		},
			"产品_of_owner_rnd_product_ds_dept",
			"产品_of_owner_sub_product_ds_dept")
	})

	t.Run("本部门及以下", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := productScopeSetup(t, env, admin, "product_ds_deptsub", 4, nil)
		got := productScopeVisibleNames(t, env, observer)
		productScopeAssertSet(t, "本部门及以下", got, []string{
			"产品_of_owner_mkt_product_ds_deptsub",
			"产品_of_owner_mkt2_product_ds_deptsub",
			"产品_of_owner_sub_product_ds_deptsub",
			"产品_of_observer_product_ds_deptsub",
		},
			"产品_of_owner_rnd_product_ds_deptsub")
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := productScopeSetup(t, env, admin, "product_ds_self", 5, nil)
		got := productScopeVisibleNames(t, env, observer)
		productScopeAssertSet(t, "仅本人", got, []string{
			"产品_of_observer_product_ds_self",
		},
			"产品_of_owner_rnd_product_ds_self",
			"产品_of_owner_mkt_product_ds_self",
			"产品_of_owner_mkt2_product_ds_self",
			"产品_of_owner_sub_product_ds_self")
	})
}

// TestIntegrationProductOutsideScope404 数据范围外的详情/修改/删除一律 404：
// 观察者绑定完整按钮权限码——403（无权限码）由权限中间件语义覆盖，这里验证
// 「有权限码但范围外」；与列表可见性一致（看不见的即不存在，不泄露存在性）。
func TestIntegrationProductOutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := productScopeSetup(t, env, admin, "product_ds_404", 5, nil)
	observerToken := env.Login(t, observer, "123456")

	// 观察者自己可见自己的产品。
	list, total := productScopePageProducts(t, env, observerToken, "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("仅本人列表 total=%d，期望 1", total)
	}
	ownID := int64(list[0]["id"].(float64))

	// admin 建一个归属他人的产品，拿其 ID。
	otherID := productScopeFindUserID(t, env, admin, "owner_rnd_product_ds_404")
	other := productScopeCreateProduct(t, env, admin, "产品_研发专属",
		map[string]any{"ownerUserId": otherID})
	otherID64 := int64(other["id"].(float64))

	// 详情 404。
	if status := productScopeGetProduct(t, env, observerToken, otherID64); status != http.StatusNotFound {
		t.Fatalf("范围外详情：HTTP %d，期望 404", status)
	}
	// 修改 404。
	status, _ := env.Request(t, http.MethodPut, "/crm/product", observerToken, map[string]any{
		"id":    otherID64,
		"name":  "产品越权改名",
		"price": "9.90",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改：HTTP %d，期望 404", status)
	}
	// 删除 404。
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product/%d", otherID64), observerToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除：HTTP %d，期望 404", status)
	}

	// 对照：观察者范围内操作正常。
	if status := productScopeGetProduct(t, env, observerToken, ownID); status != http.StatusOK {
		t.Fatalf("范围内详情：HTTP %d，期望 200", status)
	}
}
