//go:build integration

// 客户数据范围矩阵（issue #8）：5 种数据范围（对齐芋道 1–5，ADR 0004）在 CRM
// owner_dept_id / owner_user_id 列上生效；范围外详情/修改/删除一律 404。
// 写法参照 #5 的用户列表数据范围测试；谓词引擎语义（多角色并集、全部短路、
// 非法范围 400）已由 #5 覆盖，这里验证 CRM 的谓词翻译与可见性联动。
package crm_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// crmCustomerMenuID 是种子中的客户管理菜单 ID（含 crm:customer:query 权限码）。
const crmCustomerMenuID = 401

// setupScopeEnv 搭建标准观察场景（seed 部门 100 总公司 / 101 研发 / 102 市场 / 103 财务）：
//   - 研发一家客户（owner 研发销售，部门 101）
//   - 市场两家客户（owner 市场销售与观察者的同事，部门 102）
//   - 市场子部门「市场二组」一家客户（owner 子部门销售）
//   - 观察者（观察者角色）在部门 102，客户「观察者客户」归属本人
//
// 返回观察者用户名与其客户名，供各数据范围子测试断言。
func setupScopeEnv(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64, menuIDs ...int64) (observer, observerCustomer string) {
	t.Helper()
	rndDept := int64(101)
	mktDept := int64(102)

	createUserByAPI(t, env, admin, "owner_rnd", rndDept, nil)
	createUserByAPI(t, env, admin, "owner_mkt", mktDept, nil)
	createUserByAPI(t, env, admin, "owner_mkt2", mktDept, nil)
	subDept := createDeptByAPI(t, env, admin, "市场二组", mktDept)
	createUserByAPI(t, env, admin, "owner_sub", subDept, nil)

	for _, u := range []string{"owner_rnd", "owner_mkt", "owner_mkt2", "owner_sub"} {
		id := findUserIDByUsername(t, env, admin, u)
		createCustomer(t, env, admin, fmt.Sprintf("客户_of_%s", u), map[string]any{"ownerUserId": id})
	}

	if len(menuIDs) == 0 {
		menuIDs = []int64{crmCustomerMenuID}
	}
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "观察者角色", "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs, "menuIds": menuIDs,
	})
	observer = "observer_" + roleCode
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerCustomer = fmt.Sprintf("客户_of_%s", observer)
	observerID := findUserIDByUsername(t, env, admin, observer)
	createCustomer(t, env, admin, observerCustomer, map[string]any{"ownerUserId": observerID})
	return observer, observerCustomer
}

// TestIntegrationCustomerDataScopeMatrix 客户列表按 5 种数据范围过滤。
func TestIntegrationCustomerDataScopeMatrix(t *testing.T) {
	t.Run("全部数据", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer, _ := setupScopeEnv(t, env, admin, "ds_all", 1, nil)
		got := visibleCustomerNames(t, env, observer)
		assertCustomerSet(t, "全部数据", got, []string{
			"客户_of_owner_rnd", "客户_of_owner_mkt", "客户_of_owner_mkt2",
			"客户_of_owner_sub", "客户_of_" + observer,
		})
	})

	t.Run("自定义部门_精确集合不含子部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer, _ := setupScopeEnv(t, env, admin, "ds_custom", 2, []int64{101, 103})
		got := visibleCustomerNames(t, env, observer)
		// 仅 101、103 的客户可见：102（观察者本部门）与 101 的子部门不在集合内；
		// 自定义是精确集合，市场二组不是 101/103，不可见。
		assertCustomerSet(t, "自定义部门", got, []string{"客户_of_owner_rnd"},
			"客户_of_owner_mkt", "客户_of_owner_mkt2", "客户_of_owner_sub", "客户_of_"+observer)
	})

	t.Run("本部门", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer, observerCustomer := setupScopeEnv(t, env, admin, "ds_dept", 3, nil)
		got := visibleCustomerNames(t, env, observer)
		assertCustomerSet(t, "本部门", got, []string{
			"客户_of_owner_mkt", "客户_of_owner_mkt2", observerCustomer,
		}, "客户_of_owner_rnd", "客户_of_owner_sub")
	})

	t.Run("本部门及以下", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer, observerCustomer := setupScopeEnv(t, env, admin, "ds_deptsub", 4, nil)
		got := visibleCustomerNames(t, env, observer)
		assertCustomerSet(t, "本部门及以下", got, []string{
			"客户_of_owner_mkt", "客户_of_owner_mkt2", "客户_of_owner_sub", observerCustomer,
		}, "客户_of_owner_rnd")
	})

	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		observer, observerCustomer := setupScopeEnv(t, env, admin, "ds_self", 5, nil)
		got := visibleCustomerNames(t, env, observer)
		assertCustomerSet(t, "仅本人", got, []string{observerCustomer},
			"客户_of_owner_rnd", "客户_of_owner_mkt", "客户_of_owner_mkt2", "客户_of_owner_sub")
	})
}

// TestIntegrationCustomerDetailOutsideScope404 数据范围外的详情/修改/删除一律 404：
// 与列表可见性一致（看不见的即不存在），不泄露存在性。
// 观察者绑定完整按钮权限码（401/4011/4012/4013）——403（无权限码）已由
// TestIntegrationCustomerPermissionDenied 覆盖，这里验证「有权限码但范围外」。
func TestIntegrationCustomerDetailOutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer, observerCustomer := setupScopeEnv(t, env, admin, "ds_404", 5,
		nil, crmCustomerMenuID, 4011, 4012, 4013)
	observerToken := env.Login(t, observer, "123456")

	// 观察者自己可见自己的客户。
	list, total := pageCustomers(t, env, observerToken, "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("仅本人列表 total=%d，期望 1", total)
	}
	ownID := int64(list[0]["id"].(float64))

	// admin 建一个归属他人的客户，拿其 ID。
	otherID := findUserIDByUsername(t, env, admin, "owner_rnd")
	other := createCustomer(t, env, admin, "研发专属客户", map[string]any{"ownerUserId": otherID})
	otherID64 := int64(other["id"].(float64))

	// 详情 404；列表也看不到（not in list）。
	status, _ := getCustomer(t, env, observerToken, otherID64)
	if status != http.StatusNotFound {
		t.Fatalf("范围外详情：HTTP %d，期望 404", status)
	}
	// 修改 404；删除 404。
	status, _ = env.Request(t, http.MethodPut, "/crm/customer", observerToken, map[string]any{
		"id": otherID64, "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/customer/%d", otherID64), observerToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除：HTTP %d，期望 404", status)
	}

	// 对照：观察者范围内操作正常。
	status, _ = getCustomer(t, env, observerToken, ownID)
	if status != http.StatusOK {
		t.Fatalf("范围内详情：HTTP %d，期望 200", status)
	}
	if observerCustomer != "客户_of_"+observer {
		t.Fatalf("观察者客户名 %q 不符（自检）", observerCustomer)
	}
}
