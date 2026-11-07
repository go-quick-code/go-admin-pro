//go:build integration

// 租户套餐绑定与权限语义（issue #70）：
// 平台租户保持通配；租户超管只拿到套餐内菜单/权限；换套餐与套餐改菜单都即时生效。
package system_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

type menuNode struct {
	ID       int64      `json:"id"`
	Children []menuNode `json:"children"`
}

func flattenMenuIDs(nodes []menuNode) []int64 {
	var ids []int64
	var walk func([]menuNode)
	walk = func(list []menuNode) {
		for _, node := range list {
			ids = append(ids, node.ID)
			walk(node.Children)
		}
	}
	walk(nodes)
	return ids
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func createTenantPackageForTest(t *testing.T, env *testutil.IntegrationEnv, token, name string, menuIDs []int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant-package", token, map[string]any{
		"name": name, "status": 0, "menuIds": menuIDs,
	})
	mustOK(t, "创建套餐 "+name, status, api)

	status, api = env.Request(t, http.MethodGet, "/system/tenant-package/page?pageNo=1&pageSize=50&name="+name, token, nil)
	mustOK(t, "查询套餐 "+name, status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析套餐分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 {
		t.Fatalf("应查到唯一套餐 %s，实际 %+v", name, page.List)
	}
	return page.List[0].ID
}

func createTenantWithPackageForTest(t *testing.T, env *testutil.IntegrationEnv, token, name string, packageID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name": name, "contactName": "联系人", "status": 0, "packageId": packageID,
	})
	mustOK(t, "创建租户 "+name, status, api)

	status, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=50&name="+name, token, nil)
	mustOK(t, "查询租户 "+name, status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析租户分页: %v（data=%s）", err, string(api.Data))
	}
	if len(page.List) != 1 || page.List[0].ID <= 1 {
		t.Fatalf("应查到唯一新建租户 %s，实际 %+v", name, page.List)
	}
	return page.List[0].ID
}

func userInfoOf(t *testing.T, env *testutil.IntegrationEnv, token string) ([]string, []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/auth/user-info", token, nil)
	mustOK(t, "查询当前用户信息", status, api)
	var info struct {
		Permissions []string   `json:"permissions"`
		Menus       []menuNode `json:"menus"`
	}
	if err := json.Unmarshal(api.Data, &info); err != nil {
		t.Fatalf("解析 user-info: %v（data=%s）", err, string(api.Data))
	}
	return info.Permissions, flattenMenuIDs(info.Menus)
}

func TestIntegrationTenantPackageBindingAndSwitch(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 套餐只包含系统管理目录 + 用户管理菜单（按钮由绑定自动展开）。
	pkgID := createTenantPackageForTest(t, env, admin, "用户套餐", []int64{100, 101})
	tenantID := createTenantWithPackageForTest(t, env, admin, "绑定套餐租户", pkgID)
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	permissions, menus := userInfoOf(t, env, tenantAdmin)
	if containsString(permissions, "*") {
		t.Fatalf("租户超管不应再获得通配权限：%+v", permissions)
	}
	if !containsString(permissions, "system:user:query") {
		t.Fatalf("套餐内权限 system:user:query 缺失：%+v", permissions)
	}
	if !containsString(permissions, "system:user:create") {
		t.Fatalf("菜单按钮权限应随菜单展开：%+v", permissions)
	}
	if containsString(permissions, "system:role:query") {
		t.Fatalf("套餐外权限 system:role:query 不应出现：%+v", permissions)
	}
	if !containsID(menus, 100) || !containsID(menus, 101) || containsID(menus, 102) {
		t.Fatalf("租户菜单应只有套餐内目录/菜单，实际 %+v", menus)
	}

	status, api := env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", tenantAdmin, nil)
	mustOK(t, "租户超管访问套餐内用户列表", status, api)
	status, api = env.Request(t, http.MethodGet, "/system/role/page?pageNo=1&pageSize=10", tenantAdmin, nil)
	if status != http.StatusForbidden {
		t.Fatalf("套餐外角色列表：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 平台超管保持通配。
	platformPermissions, platformMenus := userInfoOf(t, env, admin)
	if !containsString(platformPermissions, "*") {
		t.Fatalf("平台超管应保留通配权限：%+v", platformPermissions)
	}
	if !containsID(platformMenus, 102) {
		t.Fatalf("平台超管应看到全部菜单：%+v", platformMenus)
	}

	// 换成只有目录的套餐：用户管理权限立即消失（缓存被清）。
	onlyDirPkg := createTenantPackageForTest(t, env, admin, "仅目录套餐", []int64{100})
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/tenant/%d", tenantID), admin, nil)
	mustOK(t, "读取租户详情", status, api)
	var tenant struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(api.Data, &tenant); err != nil {
		t.Fatalf("解析租户详情: %v", err)
	}
	status, api = env.Request(t, http.MethodPut, "/system/tenant", admin, map[string]any{
		"id": tenantID, "name": tenant.Name, "status": 0, "packageId": onlyDirPkg,
	})
	mustOK(t, "换套餐", status, api)

	status, api = env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", tenantAdmin, nil)
	if status != http.StatusForbidden {
		t.Fatalf("换套餐后套餐外用户列表：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 套餐改菜单：重新加回用户管理，已绑定租户的菜单同步刷新。
	status, api = env.Request(t, http.MethodPut, "/system/tenant-package", admin, map[string]any{
		"id": onlyDirPkg, "name": "仅目录套餐", "status": 0, "menuIds": []int64{100, 101},
	})
	mustOK(t, "修改套餐菜单", status, api)
	status, api = env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10", tenantAdmin, nil)
	mustOK(t, "套餐改菜单后访问用户列表", status, api)
}

func TestIntegrationTenantWithoutPackageKeepsAllMenus(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	tenantID := createTenantForPlatformTest(t, env, admin, "未绑套餐租户")
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	permissions, menus := userInfoOf(t, env, tenantAdmin)
	if containsString(permissions, "*") {
		t.Fatalf("租户超管不应获得通配权限：%+v", permissions)
	}
	if !containsID(menus, 102) || !containsID(menus, 103) {
		t.Fatalf("未绑套餐租户应保留全部菜单：%+v", menus)
	}
	status, api := env.Request(t, http.MethodGet, "/system/role/page?pageNo=1&pageSize=10", tenantAdmin, nil)
	mustOK(t, "未绑套餐租户访问角色列表", status, api)
}

func TestIntegrationTenantMenuWritesPlatformOnly(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 套餐包含菜单管理，租户超管有 system:menu:* 权限，但全局菜单写操作仍应 403。
	pkgID := createTenantPackageForTest(t, env, admin, "含菜单管理套餐", []int64{100, 103})
	tenantID := createTenantWithPackageForTest(t, env, admin, "菜单写越权租户", pkgID)
	tenantAdmin := loginTenantAdminForPlatformTest(t, env, tenantID)

	status, api := env.Request(t, http.MethodPost, "/system/menu", tenantAdmin, map[string]any{
		"name": "租户私建菜单", "type": 2, "parentId": 0, "sort": 99,
	})
	if status != http.StatusForbidden {
		t.Fatalf("租户超管创建全局菜单：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	// 平台超管仍可创建并删除。
	status, api = env.Request(t, http.MethodPost, "/system/menu", admin, map[string]any{
		"name": "平台测试菜单", "type": 2, "parentId": 0, "sort": 99,
	})
	mustOK(t, "平台超管创建菜单", status, api)
	status, api = env.Request(t, http.MethodGet, "/system/menu/list", admin, nil)
	mustOK(t, "平台超管查询菜单", status, api)
	var menus []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(api.Data, &menus); err != nil {
		t.Fatalf("解析菜单列表: %v", err)
	}
	var createdID int64
	for _, menu := range menus {
		if menu.Name == "平台测试菜单" {
			createdID = menu.ID
		}
	}
	if createdID == 0 {
		t.Fatalf("未找到刚创建的菜单")
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/system/menu/%d", createdID), admin, nil)
	mustOK(t, "平台超管删除菜单", status, api)
}
