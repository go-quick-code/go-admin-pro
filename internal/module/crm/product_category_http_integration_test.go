//go:build integration

// 产品分类 HTTP 集成测试（issue #11）：走真实 router + 生产中间件（JWT/权限/租户插件）
// + 真实 MySQL，在 HTTP 契约上验证树查询（sort 排序、嵌套）、改父的环校验、
// 删除约束（有子分类/有产品禁止删）与租户隔离。
// 产品是租户级共享资源，无数据范围，不做 5 范围矩阵（对照 customer_scope_integration_test.go）。
package crm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// crmProductCategoryMenuID 是种子中的产品分类菜单 ID（含 crm:product-category:query 权限码）；
// 4067–4070 是查询/新增/修改/删除按钮 ID（与 seed.go 对齐）。
const crmProductCategoryMenuID = int64(4062)

// createProductCategory 创建产品分类并返回落库后的完整 JSON（extra 覆盖 parentId/sort/status 等）。
func createProductCategory(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra ...map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for _, m := range extra {
		for k, v := range m {
			payload[k] = v
		}
	}
	status, api := env.Request(t, http.MethodPost, "/crm/product-category", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建产品分类 %q：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建分类响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// categoryTreeNode 是分类树节点（children 由服务端组装）。
type categoryTreeNode struct {
	ID       int64              `json:"id"`
	ParentID int64              `json:"parentId"`
	Name     string             `json:"name"`
	Sort     int                `json:"sort"`
	Status   int                `json:"status"`
	Children []categoryTreeNode `json:"children"`
}

// getProductCategoryTree 查询分类树。
func getProductCategoryTree(t *testing.T, env *testutil.IntegrationEnv, token string) []categoryTreeNode {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/product-category/tree", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询产品分类树：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var tree []categoryTreeNode
	if err := json.Unmarshal(api.Data, &tree); err != nil {
		t.Fatalf("解析分类树: %v（data=%s）", err, string(api.Data))
	}
	return tree
}

// findCategoryInTree 在树中按 ID 找节点。
func findCategoryInTree(tree []categoryTreeNode, id int64) (categoryTreeNode, bool) {
	for _, n := range tree {
		if n.ID == id {
			return n, true
		}
		if got, ok := findCategoryInTree(n.Children, id); ok {
			return got, true
		}
	}
	return categoryTreeNode{}, false
}

// TestIntegrationProductCategoryTreeLifecycle 分类树全生命周期：
// 三级嵌套 + sort 排序 → 改名/停用 → 改父环校验（400）→ 删除约束（子分类 1801 / 产品 1802）
// → 叶子先删后父可删。
func TestIntegrationProductCategoryTreeLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 根 A(sort 1)、根 B(sort 0)、A 的子 C、C 的孙 D：树应按 sort 排根 [B A]。
	rootA := createProductCategory(t, env, admin, "办公设备", map[string]any{"sort": 1})
	rootB := createProductCategory(t, env, admin, "耗材", map[string]any{"sort": 0})
	idA := int64(rootA["id"].(float64))
	idB := int64(rootB["id"].(float64))
	child := createProductCategory(t, env, admin, "打印机", map[string]any{"parentId": idA, "sort": 1})
	idC := int64(child["id"].(float64))
	grand := createProductCategory(t, env, admin, "激光打印机", map[string]any{"parentId": idC, "sort": 2})
	idD := int64(grand["id"].(float64))

	tree := getProductCategoryTree(t, env, admin)
	if len(tree) != 2 || tree[0].ID != idB || tree[1].ID != idA {
		t.Fatalf("根应按 sort 为 [耗材 办公设备]，实际 %+v", tree)
	}
	nodeC, ok := findCategoryInTree(tree, idC)
	if !ok || len(nodeC.Children) != 1 || nodeC.Children[0].ID != idD {
		t.Fatalf("树应为 A→C→D 嵌套，实际 %+v", tree)
	}

	// 改名 + 停用：回读一致（status 0 启用 / 1 停用）。
	status, api := env.Request(t, http.MethodPut, "/crm/product-category", admin, map[string]any{
		"id": idD, "name": "激光打印机Pro", "parentId": idC, "sort": 2, "status": 1,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("修改分类：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	tree = getProductCategoryTree(t, env, admin)
	if n, _ := findCategoryInTree(tree, idD); n.Name != "激光打印机Pro" || n.Status != 1 {
		t.Fatalf("改名停用未生效：%+v", n)
	}

	// 改父环校验：把 A 挂到自己的孙 D 下 → 400；把 A 挂到自己 → 400。
	for _, tc := range []struct {
		name     string
		parentID int64
		want     string
	}{
		{"父挂到自己的子孙", idD, "上级分类"},
		{"父挂到自己", idA, "上级分类不能是自己"},
	} {
		status, api = env.Request(t, http.MethodPut, "/crm/product-category", admin, map[string]any{
			"id": idA, "name": "办公设备", "parentId": tc.parentID, "sort": 1, "status": 0,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
	}

	// 删除约束：A 有子 → 业务码 1801；C 有子 → 1801。
	for _, id := range []int64{idA, idC} {
		status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product-category/%d", id), admin, nil)
		if status != http.StatusOK || api.Code != 1801 {
			t.Fatalf("删除有子分类的分类 %d：HTTP %d code %d msg %q，期望 HTTP 200 code 1801", id, status, api.Code, api.Msg)
		}
	}

	// 分类下有产品 → 1802（产品 CRUD 助手在本文件包内共享）。
	product := createProduct(t, env, admin, "激光打印机 X1", map[string]any{"categoryId": idD})
	productID := int64(product["id"].(float64))
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product-category/%d", idD), admin, nil)
	if status != http.StatusOK || api.Code != 1802 {
		t.Fatalf("删除有产品的分类：HTTP %d code %d msg %q，期望 HTTP 200 code 1802", status, api.Code, api.Msg)
	}

	// 先删产品再删叶子：C、A 依序可删（删完子后约束解除）。
	deleteProduct(t, env, admin, productID)
	for _, id := range []int64{idD, idC, idA, idB} {
		status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product-category/%d", id), admin, nil)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("删除分类 %d：HTTP %d code %d msg %q，期望成功", id, status, api.Code, api.Msg)
		}
	}
	if tree := getProductCategoryTree(t, env, admin); len(tree) != 0 {
		t.Fatalf("全删后树应为空，实际 %+v", tree)
	}
}

// TestIntegrationProductCategoryTenantIsolation 分类树租户隔离：
// 租户 2 树为空、跨租户改/删 404、租户 2 建的同名分类与租户 1 互不可见。
func TestIntegrationProductCategoryTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cat1 := createProductCategory(t, env, admin, "租户一分类", nil)
	id1 := int64(cat1["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "分类隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	if tree := getProductCategoryTree(t, env, token2); len(tree) != 0 {
		t.Fatalf("租户 2 分类树应为空，实际 %+v", tree)
	}
	// 租户 2 建同名分类（不同租户允许重名），互不串扰。
	createProductCategory(t, env, token2, "租户一分类", nil)

	// 跨租户修改/删除：404（不泄露存在性）。
	status, _ := env.Request(t, http.MethodPut, "/crm/product-category", token2, map[string]any{
		"id": id1, "name": "越权改名", "parentId": 0, "sort": 0, "status": 0,
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 分类：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/product-category/%d", id1), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 分类：HTTP %d，期望 404", status)
	}

	// 各自树里只有自己的一条。
	if tree := getProductCategoryTree(t, env, token2); len(tree) != 1 || tree[0].ID == id1 {
		t.Fatalf("租户 2 树应只有自己的同名分类，实际 %+v", tree)
	}
	if tree := getProductCategoryTree(t, env, admin); len(tree) != 1 || tree[0].ID != id1 {
		t.Fatalf("租户 1 分类树应不受影响，实际 %+v", tree)
	}
}

// TestIntegrationProductCategoryPermissionDenied 分类接口的按钮码 403：
// 只绑分类查询（4062+4067）的用户，新增/修改/删除全部 403（普通 CRUD，无本人放行特例）。
func TestIntegrationProductCategoryPermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cat := createProductCategory(t, env, admin, "权限宿主分类", nil)
	id := int64(cat["id"].(float64))

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅分类查询角色", "code": "cat_query_only", "dataScope": 1,
		"menuIds": []int64{crmProductCategoryMenuID, 4067},
	})
	createUserByAPI(t, env, admin, "cat_query_user", 102, []int64{roleID})
	readonly := env.Login(t, "cat_query_user", "123456")

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"新增", http.MethodPost, "/crm/product-category", map[string]any{"name": "越权分类"}},
		{"修改", http.MethodPut, "/crm/product-category", map[string]any{"id": id, "name": "越权改名", "parentId": 0}},
		{"删除", http.MethodDelete, fmt.Sprintf("/crm/product-category/%d", id), nil},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, readonly, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("分类%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}
}
