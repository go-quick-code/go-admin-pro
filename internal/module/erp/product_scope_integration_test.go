//go:build integration

// ERP 数据范围矩阵（issue #88，docs/erp-mvp-spec.md §4.10 + ADR 0004）：
// ERP 主数据 owner = 创建人（恒有值），列表/导出按 owner + 角色数据范围过滤
// （与 CRM 行为一致）。5 种数据范围在 owner_user_id/owner_dept_id 列上生效；
// 谓词引擎语义（多角色并集、全部短路、非法范围 400）已由 system/CRM 测试覆盖，
// 这里验证 ERP 的谓词翻译与可见性联动。
package erp_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// erpScopeRoleMenus 是观察者角色绑定的 ERP 菜单/按钮（产品查询/创建/修改/删除/
// 导出——修改/删除按钮码也要授予：403（无权限码）与 404（范围外）分层，
// 这里只验证「有权限码但范围外」的 404，与 crm 数据范围测试同口径）。
var erpScopeRoleMenus = []int64{erpProductMenuID, 7121, 7122, 7123, 7124}

// setupErpScopeEnv 搭建标准观察场景（seed 部门 100 总公司 / 101 研发 / 102 市场 / 103 财务）：
//   - 研发一家产品（owner 研发用户，部门 101）
//   - 市场两家产品（部门 102）
//   - 市场子部门「市场二组」一家产品
//   - 观察者（观察者角色）在部门 102，自己创建的产品归属本人
//
// 返回观察者用户名，供各数据范围子测试断言。
func setupErpScopeEnv(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64) string {
	t.Helper()
	rndDept := int64(101)
	mktDept := int64(102)

	// 主数据（分类/单位）由 admin 建好（owner=admin）；各 owner 用户创建的产品
	// 引用它们（存在性校验不受数据范围约束，spec §4.10 只约束列表/导出可见性）。
	category := createCategory(t, env, admin, "范围测试分类", nil)
	unit := createUnit(t, env, admin, "范围测试单位", nil)
	categoryID := int64(category["id"].(float64))
	unitID := int64(unit["id"].(float64))

	// owner 角色只授产品查询/创建（各 owner 用户登录后创建自己的产品；
	// 观察者的可见性才是被测对象，owner 的数据范围不参与断言）。编码固定且不
	// 含观察者编码（findRoleIDByCode 按 code LIKE 匹配，避免二次命中）。
	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP owner 角色", "code": "erp_owner_role", "dataScope": 1,
		"menuIds": []int64{erpProductMenuID, 7121},
	})
	subDept := createDeptByAPI(t, env, admin, "市场二组", mktDept)
	createUserByAPI(t, env, admin, "owner_rnd", rndDept, []int64{ownerRoleID})
	createUserByAPI(t, env, admin, "owner_mkt", mktDept, []int64{ownerRoleID})
	createUserByAPI(t, env, admin, "owner_mkt2", mktDept, []int64{ownerRoleID})
	createUserByAPI(t, env, admin, "owner_sub", subDept, []int64{ownerRoleID})

	for _, u := range []string{"owner_rnd", "owner_mkt", "owner_mkt2", "owner_sub"} {
		owner := env.Login(t, u, "123456")
		createProduct(t, env, owner, fmt.Sprintf("产品_of_%s", u), map[string]any{
			"categoryId": categoryID, "unitId": unitID,
		})
	}

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP观察者角色", "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs, "menuIds": erpScopeRoleMenus,
	})
	observer := "observer_" + roleCode
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerToken := env.Login(t, observer, "123456")
	createProduct(t, env, observerToken, fmt.Sprintf("产品_of_%s", observer), map[string]any{
		"categoryId": categoryID, "unitId": unitID,
	})
	return observer
}

// visibleProductNames 以指定账号登录并拉取产品分页，返回可见产品名集合。
func visibleProductNames(t *testing.T, env *testutil.IntegrationEnv, username string) map[string]bool {
	t.Helper()
	token := env.Login(t, username, "123456")
	list, _ := pageProducts(t, env, token, "")
	set := make(map[string]bool, len(list))
	for _, row := range list {
		set[row["name"].(string)] = true
	}
	return set
}

func assertProductSet(t *testing.T, phase string, got map[string]bool, want []string, notWant ...string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s：可见集合缺少 %q，实际 %v", phase, name, keysOfProduct(got))
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s：不应可见 %q，实际 %v", phase, name, keysOfProduct(got))
		}
	}
}

func keysOfProduct(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	return names
}

// TestIntegrationProductDataScopeMatrix 产品列表按 5 种数据范围过滤（owner = 创建人）。
func TestIntegrationProductDataScopeMatrix(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupErpScopeEnv(t, env, admin, "erp_ds_all", 1, nil)
		got := visibleProductNames(t, env, observer)
		assertProductSet(t, "全部数据", got, []string{
			"产品_of_owner_rnd", "产品_of_owner_mkt", "产品_of_owner_mkt2",
			"产品_of_owner_sub", "产品_of_" + observer,
		})
	})

	t.Run("本部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupErpScopeEnv(t, env, admin, "erp_ds_dept", 3, nil)
		got := visibleProductNames(t, env, observer)
		assertProductSet(t, "本部门", got, []string{
			"产品_of_owner_mkt", "产品_of_owner_mkt2", "产品_of_" + observer,
		}, "产品_of_owner_rnd", "产品_of_owner_sub")
	})

	t.Run("本部门及以下", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupErpScopeEnv(t, env, admin, "erp_ds_deptsub", 4, nil)
		got := visibleProductNames(t, env, observer)
		assertProductSet(t, "本部门及以下", got, []string{
			"产品_of_owner_mkt", "产品_of_owner_mkt2", "产品_of_owner_sub", "产品_of_" + observer,
		}, "产品_of_owner_rnd")
	})

	t.Run("自定义部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		// 自定义精确集合 {101, 103}：观察者在 102，可见 101（研发）的产品；
		// 102（含观察者本人，owner 是本人但部门不在集合——IncludeSelf 不开）不可见。
		observer := setupErpScopeEnv(t, env, admin, "erp_ds_custom", 2, []int64{101, 103})
		got := visibleProductNames(t, env, observer)
		assertProductSet(t, "自定义部门", got, []string{"产品_of_owner_rnd"},
			"产品_of_owner_mkt", "产品_of_owner_mkt2", "产品_of_owner_sub", "产品_of_"+observer)
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupErpScopeEnv(t, env, admin, "erp_ds_self", 5, nil)
		got := visibleProductNames(t, env, observer)
		assertProductSet(t, "仅本人", got, []string{"产品_of_" + observer},
			"产品_of_owner_rnd", "产品_of_owner_mkt", "产品_of_owner_mkt2", "产品_of_owner_sub")
	})
}

// TestIntegrationProductExportScopeExport 导出与列表同一数据范围口径（spec §4.10）：
// 仅本人角色导出只见自己的产品；范围外详情/修改/删除一律 404（不泄露存在性）。
func TestIntegrationProductExportScopeExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := setupErpScopeEnv(t, env, admin, "erp_ds_export", 5, nil)

	// 导出与列表同口径：仅本人导出 1 行。
	_, rows := getExportRows(t, env, env.Login(t, observer, "123456"), "/erp/product/export-excel")
	if len(rows) != 1 {
		t.Fatalf("仅本人导出应含 1 行，实际 %d 行：%v", len(rows), rows)
	}

	// 范围外详情/修改/删除一律 404：owner_rnd 的产品对仅本人观察者不可见。
	ownerRnd := env.Login(t, "owner_rnd", "123456")
	list, _ := pageProducts(t, env, ownerRnd, "&name=产品_of_owner_rnd")
	if len(list) != 1 {
		t.Fatalf("owner_rnd 应看到自己的产品，实际 %d 条", len(list))
	}
	targetID := int64(list[0]["id"].(float64))
	observerToken := env.Login(t, observer, "123456")

	status, _ := getJSON(t, env, observerToken, fmt.Sprintf("/erp/product/%d", targetID))
	if status != http.StatusNotFound {
		t.Fatalf("范围外详情：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPut, "/erp/product", observerToken, map[string]any{
		"id": targetID, "name": "越权改名", "categoryId": 1, "unitId": 1,
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, observerToken, fmt.Sprintf("/erp/product/%d", targetID))
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除：HTTP %d，期望 404", status)
	}
}

// TestIntegrationCommonRoleNoErpPermission 内置普通角色（role id=2）默认无任何
// ERP 权限（spec §7：财务与库存敏感，仅管理员与后续自定义角色）。
func TestIntegrationCommonRoleNoErpPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	// 先由 admin 造一条数据，保证「无权限 403」不是「无数据空列表」。
	createCategory(t, env, admin, "普通角色禁止分类", nil)

	common := env.Login(t, "demo", "123456")
	status, _ := env.Request(t, http.MethodGet, "/erp/product-category/list", common, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色访问分类列表：HTTP %d，期望 403", status)
	}
	status, _ = env.Request(t, http.MethodGet, "/erp/product-unit/page?pageNo=1&pageSize=10", common, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色访问单位分页：HTTP %d，期望 403", status)
	}
	status, _ = env.Request(t, http.MethodGet, "/erp/product/page?pageNo=1&pageSize=10", common, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色访问产品分页：HTTP %d，期望 403", status)
	}
}
