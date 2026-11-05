//go:build integration

package testutil_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// 本文件覆盖 issue #6 的平台加固验收点，全部打真实 /admin-api：
//   - CORS 白名单在真实路由上的装配；
//   - 用户创建 / 租户创建 / 角色菜单分配的事务性（错误注入 → 无残留）。

// pageTotal 请求分页接口并返回 total。
func pageTotal(t *testing.T, env *testutil.IntegrationEnv, token, path string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, path, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("GET %s: HTTP %d code %d msg %q", path, status, api.Code, api.Msg)
	}
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析分页响应: %v（data=%s）", err, string(api.Data))
	}
	return page.Total
}

// CORS 白名单装配：白名单来源返回 CORS 头，非白名单来源不返回。
func TestIntegrationCORSAllowlistOnRealRouter(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	origins := env.Config.App.CORS.AllowedOrigins
	if len(origins) == 0 {
		t.Fatal("集成配置应包含开发态 CORS 白名单（configs/config.yaml）")
	}
	allowed := origins[0]

	do := func(origin string) http.Header {
		req, err := http.NewRequest(http.MethodGet, env.Server.URL+"/healthz", nil)
		if err != nil {
			t.Fatalf("构造请求: %v", err)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := env.Server.Client().Do(req)
		if err != nil {
			t.Fatalf("请求 /healthz: %v", err)
		}
		defer resp.Body.Close()
		return resp.Header
	}

	if got := do(allowed).Get("Access-Control-Allow-Origin"); got != allowed {
		t.Fatalf("白名单 Origin %q 应回显 CORS 头，实际 %q", allowed, got)
	}
	if got := do("http://evil.example.com").Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("非白名单 Origin 不应返回 CORS 头，实际 %q", got)
	}
	if got := do("").Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("无 Origin 不应返回 CORS 头，实际 %q", got)
	}
}

// 创建用户时角色绑定失败（重复 roleId → system_user_role 主键冲突）：
// 整个创建回滚，用户不留残行。
func TestIntegrationCreateUserRollsBackOnRoleBindingFailure(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/system/user", token, map[string]any{
		"username": "txuser",
		"password": "123456",
		"nickname": "事务用户",
		"roleIds":  []int64{2, 2}, // 重复 roleId 触发主键冲突
		"postIds":  []int64{2},
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("注入失败应返回 HTTP 500，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	if got := pageTotal(t, env, token, "/system/user/page?pageNo=1&pageSize=10&username=txuser"); got != 0 {
		t.Fatalf("创建失败后不应残留用户行，实际 total=%d", got)
	}
}

// 创建用户成功路径：用户与角色、岗位绑定一起落库。
func TestIntegrationCreateUserCommitsAllSteps(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/system/user", token, map[string]any{
		"username": "integuser",
		"password": "123456",
		"nickname": "集成用户",
		"roleIds":  []int64{2},
		"postIds":  []int64{2},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建用户: HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	if got := pageTotal(t, env, token, "/system/user/page?pageNo=1&pageSize=10&username=integuser"); got != 1 {
		t.Fatalf("创建成功的用户应可见，实际 total=%d", got)
	}

	// 详情里应带上角色与岗位绑定（分页列表不含绑定，走详情接口）。
	_, api = env.Request(t, http.MethodGet, "/system/user/page?pageNo=1&pageSize=10&username=integuser", token, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v", err)
	}
	if len(page.List) != 1 || page.List[0].ID == 0 {
		t.Fatalf("用户列表应为 1 行，实际 %+v", page.List)
	}

	_, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/user/%d", page.List[0].ID), token, nil)
	var detail struct {
		RoleIDs []int64 `json:"roleIds"`
		PostIDs []int64 `json:"postIds"`
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析用户详情: %v（data=%s）", err, string(api.Data))
	}
	if len(detail.RoleIDs) != 1 || detail.RoleIDs[0] != 2 {
		t.Fatalf("用户应绑定角色 2，实际 %v", detail.RoleIDs)
	}
	if len(detail.PostIDs) != 1 || detail.PostIDs[0] != 2 {
		t.Fatalf("用户应绑定岗位 2，实际 %v", detail.PostIDs)
	}
}

// 创建租户时引导流程失败（注入：system_user 插入一律失败）：
// 事务整体回滚，租户、部门、角色、账号都不残留。
func TestIntegrationCreateTenantRollsBackOnBootstrapFailure(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	// 注入点：引导流程的最后一步（创建租户管理员）失败，
	// 旧的补偿式删除实现会留下部门/角色残留，事务实现则全部回滚。
	if err := env.DB.Callback().Create().Before("gorm:create").Register("test:fail_user", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_user" {
			_ = tx.AddError(fmt.Errorf("注入：租户管理员创建失败"))
		}
	}); err != nil {
		t.Fatalf("注册注入回调: %v", err)
	}

	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name":        "注入租户",
		"contactName": "联系人",
		"status":      0,
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("注入失败应返回 HTTP 500，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	for table, label := range map[string]string{
		"system_tenant": "租户",
		"system_dept":   "部门",
		"system_role":   "角色",
		"system_user":   "用户",
	} {
		var total int64
		q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE tenant_id > 1", table)
		if table == "system_tenant" {
			q = "SELECT COUNT(*) FROM system_tenant WHERE name = '注入租户'"
		}
		if err := env.DB.Raw(q).Scan(&total).Error; err != nil {
			t.Fatalf("统计%s残留: %v", label, err)
		}
		if total != 0 {
			t.Fatalf("引导失败后不应残留%s（%s），实际 %d 行", label, table, total)
		}
	}
}

// 创建租户成功路径：引导数据完整落库，新租户管理员可以直接登录。
func TestIntegrationCreateTenantBootstrapsAndAdminCanLogin(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/system/tenant", token, map[string]any{
		"name":        "集成租户",
		"contactName": "联系人",
		"status":      0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建租户: HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	var page struct {
		List []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	_, api = env.Request(t, http.MethodGet, "/system/tenant/page?pageNo=1&pageSize=10&name=%E9%9B%86%E6%88%90%E7%A7%9F%E6%88%B7", token, nil)
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析租户分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("新租户应可查到，实际 total=%d list=%+v", page.Total, page.List)
	}
	tenantID := page.List[0].ID

	// 新租户管理员登录：证明部门/角色/账号引导完整提交。
	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin",
		"password": "123456",
		"tenantId": tenantID,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("新租户管理员登录: HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// 角色菜单分配失败（重复 menuId → system_role_menu 主键冲突）：
// 角色本身的修改一并回滚，菜单绑定保持原状。
func TestIntegrationUpdateRoleRollsBackOnMenuAssignmentFailure(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/system/role", token, map[string]any{
		"name":      "集成角色",
		"code":      "integ_role",
		"dataScope": 1, // 数据范围非法值会被 400 拒绝（issue #5），这里取合法值
		"menuIds":   []int64{101},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建角色: HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	_, api = env.Request(t, http.MethodGet, "/system/role/page?pageNo=1&pageSize=10&code=integ_role", token, nil)
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析角色分页: %v", err)
	}
	if len(page.List) != 1 || page.List[0].ID == 0 {
		t.Fatalf("角色应可查到，实际 %+v", page.List)
	}
	roleID := page.List[0].ID

	status, api = env.Request(t, http.MethodPut, "/system/role", token, map[string]any{
		"id":        roleID,
		"name":      "集成角色改名",
		"code":      "integ_role",
		"dataScope": 1,
		"menuIds":   []int64{1011, 1011}, // 重复 menuId 触发主键冲突
	})
	if status != http.StatusInternalServerError {
		t.Fatalf("注入失败应返回 HTTP 500，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	_, api = env.Request(t, http.MethodGet, fmt.Sprintf("/system/role/%d", roleID), token, nil)
	var role struct {
		Name    string  `json:"name"`
		MenuIDs []int64 `json:"menuIds"`
	}
	if err := json.Unmarshal(api.Data, &role); err != nil {
		t.Fatalf("解析角色详情: %v（data=%s）", err, string(api.Data))
	}
	if role.Name != "集成角色" {
		t.Fatalf("菜单分配失败后角色名应回滚，实际 %q", role.Name)
	}
	if len(role.MenuIDs) != 1 || role.MenuIDs[0] != 101 {
		t.Fatalf("菜单绑定应保持 [101]，实际 %v", role.MenuIDs)
	}
}
