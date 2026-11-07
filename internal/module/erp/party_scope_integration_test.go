//go:build integration

// 仓库/客户/供应商数据范围集成测试（issue #89，docs/erp-mvp-spec.md §4.10 +
// ADR 0004）：三实体 owner = 创建人（恒有值），列表按 owner + 角色数据范围
// 过滤，范围外详情/修改/删除一律 404（不泄露存在性）。
// 谓词引擎语义（多角色并集、非法范围 400）已由 system/CRM 与 #88 产品域矩阵
// 测试覆盖，这里验证三新实体的谓词接线（全部 / 仅本人两端 + 范围外单行 404）。
package erp_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 三实体角色菜单（与 internal/seed/seed.go 700 段一致：菜单 + 增删改导出按钮）。
var (
	erpWarehouseScopeMenus = []int64{erpWarehouseMenuID, 7201, 7202, 7203, 7204}
	erpCustomerScopeMenus  = []int64{erpCustomerMenuID, 7401, 7402, 7403, 7404}
	erpSupplierScopeMenus  = []int64{erpSupplierMenuID, 7301, 7302, 7303, 7304}
)

// setupPartyScopeEnv 搭建三实体观察场景（seed 部门 101 研发 / 102 市场）：
//   - 研发用户各建一仓库/客户/供应商（owner 研发，部门 101）
//   - 市场用户各建一仓库/客户/供应商（owner 市场，部门 102）
//   - 观察者（观察者角色）在部门 102，自己再各建一条
//
// 返回观察者用户名。
func setupPartyScopeEnv(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int) string {
	t.Helper()
	rndDept := int64(101)
	mktDept := int64(102)

	// owner 角色只授三实体的查询/创建（owner 的数据范围不参与断言）。
	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP party owner 角色", "code": "erp_party_owner_role", "dataScope": 1,
		"menuIds": []int64{erpWarehouseMenuID, 7201, erpCustomerMenuID, 7401, erpSupplierMenuID, 7301},
	})
	createUserByAPI(t, env, admin, "party_owner_rnd", rndDept, []int64{ownerRoleID})
	createUserByAPI(t, env, admin, "party_owner_mkt", mktDept, []int64{ownerRoleID})

	for _, u := range []string{"party_owner_rnd", "party_owner_mkt"} {
		owner := env.Login(t, u, "123456")
		createWarehouse(t, env, owner, fmt.Sprintf("仓库_of_%s", u), nil)
		createCustomer(t, env, owner, fmt.Sprintf("客户_of_%s", u), nil)
		createSupplier(t, env, owner, fmt.Sprintf("供应商_of_%s", u), nil)
	}

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP party 观察者角色", "code": roleCode, "dataScope": dataScope,
		"menuIds": concatInt64(erpWarehouseScopeMenus, erpCustomerScopeMenus, erpSupplierScopeMenus),
	})
	observer := "observer_" + roleCode
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerToken := env.Login(t, observer, "123456")
	createWarehouse(t, env, observerToken, fmt.Sprintf("仓库_of_%s", observer), nil)
	createCustomer(t, env, observerToken, fmt.Sprintf("客户_of_%s", observer), nil)
	createSupplier(t, env, observerToken, fmt.Sprintf("供应商_of_%s", observer), nil)
	return observer
}

func concatInt64(lists ...[]int64) []int64 {
	out := make([]int64, 0)
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// partyVisibleNames 以指定账号登录并拉取各实体分页，返回「实体名→名称集合」。
func partyVisibleNames(t *testing.T, env *testutil.IntegrationEnv, username string) map[string]map[string]bool {
	t.Helper()
	token := env.Login(t, username, "123456")
	names := func(rows []map[string]any) map[string]bool {
		set := make(map[string]bool, len(rows))
		for _, row := range rows {
			set[row["name"].(string)] = true
		}
		return set
	}
	wh, _ := pageWarehouses(t, env, token, "")
	cus, _ := pageCustomers(t, env, token, "")
	sup, _ := pageSuppliers(t, env, token, "")
	return map[string]map[string]bool{
		"仓库":  names(wh),
		"客户":  names(cus),
		"供应商": names(sup),
	}
}

func assertPartyVisible(t *testing.T, phase, entity string, got map[string]bool, want []string, notWant ...string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s %s：可见集合缺少 %q，实际 %v", phase, entity, name, got)
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s %s：不应可见 %q，实际 %v", phase, entity, name, got)
		}
	}
}

// TestIntegrationPartyDataScope 三实体列表按数据范围过滤（owner = 创建人）：
// 全部数据可见全部；仅本人只见自己的。
func TestIntegrationPartyDataScope(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupPartyScopeEnv(t, env, admin, "erp_party_ds_all", 1)
		got := partyVisibleNames(t, env, observer)
		for _, entity := range []string{"仓库", "客户", "供应商"} {
			assertPartyVisible(t, "全部数据", entity, got[entity], []string{
				entity + "_of_party_owner_rnd", entity + "_of_party_owner_mkt", entity + "_of_" + observer,
			})
		}
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer := setupPartyScopeEnv(t, env, admin, "erp_party_ds_self", 5)
		got := partyVisibleNames(t, env, observer)
		for _, entity := range []string{"仓库", "客户", "供应商"} {
			assertPartyVisible(t, "仅本人", entity, got[entity], []string{entity + "_of_" + observer},
				entity+"_of_party_owner_rnd", entity+"_of_party_owner_mkt")
		}
	})
}

// TestIntegrationPartyOutOfScope404 范围外单行：仅本人观察者对他人仓库/客户/
// 供应商的详情/修改/删除一律 404（不泄露存在性，spec §4.10）。
func TestIntegrationPartyOutOfScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := setupPartyScopeEnv(t, env, admin, "erp_party_ds_404", 5)
	observerToken := env.Login(t, observer, "123456")

	// 取 owner 研发用户建的三条 ID。
	ownerToken := env.Login(t, "party_owner_rnd", "123456")
	wh, _ := pageWarehouses(t, env, ownerToken, "&name=仓库_of_party_owner_rnd")
	cus, _ := pageCustomers(t, env, ownerToken, "&name=客户_of_party_owner_rnd")
	sup, _ := pageSuppliers(t, env, ownerToken, "&name=供应商_of_party_owner_rnd")
	if len(wh) != 1 || len(cus) != 1 || len(sup) != 1 {
		t.Fatalf("owner 应看到自己的三条数据：%d/%d/%d", len(wh), len(cus), len(sup))
	}
	warehouseID := int64(wh[0]["id"].(float64))
	customerID := int64(cus[0]["id"].(float64))
	supplierID := int64(sup[0]["id"].(float64))

	// 详情 404。
	for _, path := range []string{
		fmt.Sprintf("/erp/warehouse/%d", warehouseID),
		fmt.Sprintf("/erp/customer/%d", customerID),
		fmt.Sprintf("/erp/supplier/%d", supplierID),
	} {
		status, _ := getJSON(t, env, observerToken, path)
		if status != http.StatusNotFound {
			t.Fatalf("范围外详情 %s：HTTP %d，期望 404", path, status)
		}
	}

	// 修改 404（PUT id-in-body 契约）。
	status, _ := env.Request(t, http.MethodPut, "/erp/customer", observerToken, map[string]any{
		"id": customerID, "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改客户：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPut, "/erp/warehouse", observerToken, map[string]any{
		"id": warehouseID, "name": "越权改名仓库",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改仓库：HTTP %d，期望 404", status)
	}

	// 删除 404。
	for _, path := range []string{
		fmt.Sprintf("/erp/warehouse/%d", warehouseID),
		fmt.Sprintf("/erp/customer/%d", customerID),
		fmt.Sprintf("/erp/supplier/%d", supplierID),
	} {
		status, _ = deleteByID(t, env, observerToken, path)
		if status != http.StatusNotFound {
			t.Fatalf("范围外删除 %s：HTTP %d，期望 404", path, status)
		}
	}
}

// TestIntegrationPartyCommonRoleNoPermission 内置普通角色（role id=2）默认无
// 三实体权限（spec §7：普通角色不授 ERP）。
func TestIntegrationPartyCommonRoleNoPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	// 先由 admin 造数据，保证「无权限 403」不是「无数据空列表」。
	createWarehouse(t, env, admin, "普通角色禁止仓库", nil)
	createCustomer(t, env, admin, "普通角色禁止客户", nil)
	createSupplier(t, env, admin, "普通角色禁止供应商", nil)

	common := env.Login(t, "demo", "123456")
	for _, path := range []string{
		"/erp/warehouse/page?pageNo=1&pageSize=10",
		"/erp/customer/page?pageNo=1&pageSize=10",
		"/erp/supplier/page?pageNo=1&pageSize=10",
	} {
		status, _ := env.Request(t, http.MethodGet, path, common, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色访问 %s：HTTP %d，期望 403", path, status)
		}
	}
}
