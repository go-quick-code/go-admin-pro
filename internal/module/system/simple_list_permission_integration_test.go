//go:build integration

// simple-list 权限收口（PR #39 残余风险 C）：用户/角色/岗位简表不再对任意
// 登录用户开放，分别要求 system:user:simple-list / system:role:simple-list /
// system:post:simple-list。演示销售角色（sales/sales_manager）需要用户简表
// 做负责人下拉，seed 已把该权限码绑定到演示角色。
package system_test

import (
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// assertSimpleListStatus 请求简表接口并断言状态码。
func assertSimpleListStatus(t *testing.T, env *testutil.IntegrationEnv, token, path string, want int) {
	t.Helper()

	status, api := env.Request(t, http.MethodGet, path, token, nil)
	if status != want {
		t.Fatalf("GET %s：HTTP %d code %d msg %q，期望 HTTP %d", path, status, api.Code, api.Msg, want)
	}
}

// TestIntegrationSimpleListPermission 无权限码 403；演示销售可读用户简表；
// 平台管理员全部可读；角色/岗位简表同样收口。
func TestIntegrationSimpleListPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 演示数据提供 sales/sales_manager 角色与 sales1 账号。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("写入 CRM 演示数据: %v", err)
	}
	sales1 := env.Login(t, "sales1", "123456")

	// 演示销售：用户简表 200（负责人下拉），角色/岗位简表未授权 → 403。
	assertSimpleListStatus(t, env, sales1, "/system/user/simple-list", http.StatusOK)
	assertSimpleListStatus(t, env, sales1, "/system/role/simple-list", http.StatusForbidden)
	assertSimpleListStatus(t, env, sales1, "/system/post/simple-list", http.StatusForbidden)

	// 普通角色演示账号（role 2）：seed 已绑定三条简表按钮码（用户管理页渲染所需）。
	demo := env.Login(t, "demo", "123456")
	assertSimpleListStatus(t, env, demo, "/system/user/simple-list", http.StatusOK)
	assertSimpleListStatus(t, env, demo, "/system/role/simple-list", http.StatusOK)
	assertSimpleListStatus(t, env, demo, "/system/post/simple-list", http.StatusOK)

	// 自定义角色只绑用户管理页面（101）、没有简表按钮码：三条简表仍 403。
	noPermRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "简表无权限角色", "code": "simple_list_noperm", "dataScope": 5, "menuIds": []int64{101},
	})
	createUserByAPI(t, env, admin, "simple_list_noperm", 102, []int64{noPermRole})
	noPerm := env.Login(t, "simple_list_noperm", "123456")
	assertSimpleListStatus(t, env, noPerm, "/system/user/simple-list", http.StatusForbidden)
	assertSimpleListStatus(t, env, noPerm, "/system/role/simple-list", http.StatusForbidden)
	assertSimpleListStatus(t, env, noPerm, "/system/post/simple-list", http.StatusForbidden)

	// 平台管理员持有 "*"：三条简表都可读。
	assertSimpleListStatus(t, env, admin, "/system/user/simple-list", http.StatusOK)
	assertSimpleListStatus(t, env, admin, "/system/role/simple-list", http.StatusOK)
	assertSimpleListStatus(t, env, admin, "/system/post/simple-list", http.StatusOK)
}
