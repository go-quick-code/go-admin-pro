//go:build integration

// OA 模块骨架 HTTP 集成测试（issue #43，spec §2/§4.6）：
//   - 路由组挂载与鉴权（未登录 401，路由未挂载会是 404，两者可区分）；
//   - 可见范围 helper 的 HTTP 出口按 spec §4.6 优先级工作：
//     manage（超管通配 + 角色绑定按钮码）→ 部门负责人子树（含多部门并集）→ 仅本人；
//   - 资源登记外的类型 400；
//   - 跨租户隔离：可见集合只含本租户用户；
//   - 菜单种子 OA 根目录（500）下发且重复启动幂等。
//
// 主 seam：真实 router + 真实 MySQL（testutil），测试库走 _t43 后缀约定。
package oa_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/app"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// oaScopeMenuID 是测试专用的管理权限码按钮菜单 ID（仅存在于测试库，
// 不进种子文件；5099 落在 5000–5299 按钮段内、避开规格 §7.2 各模块已规划段）。
const oaScopeMenuID = int64(5099)

// oaManageCode 是测试专用按钮菜单携带的管理权限码（请假 manage）。
const oaManageCode = "oa:leave:manage"

// scopeResp 是 GET /oa/visibility-scope 的 data 结构。
type scopeResp struct {
	Resource string  `json:"resource"`
	All      bool    `json:"all"`
	UserIDs  []int64 `json:"userIds"`
}

// fetchScope 调用可见范围探测接口。
func fetchScope(t *testing.T, env *testutil.IntegrationEnv, token, resource string) (int, testutil.APIResponse, scopeResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/visibility-scope?resource="+resource, token, nil)
	var data scopeResp
	if api.Data != nil {
		if err := json.Unmarshal(api.Data, &data); err != nil {
			t.Fatalf("解析可见范围响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api, data
}

// mustOK 断言业务成功（HTTP 200 且 code 0）。
func mustOK(t *testing.T, phase string, status int, api testutil.APIResponse) {
	t.Helper()
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", phase, status, api.Code, api.Msg)
	}
}

// createUser 用 admin 创建启用用户并回读 ID。
func createUser(t *testing.T, env *testutil.IntegrationEnv, admin, username string, deptID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "password": "123456", "nickname": username,
		"deptId": deptID, "roleIds": []int64{}, "status": 0,
	})
	mustOK(t, "创建用户 "+username, status, api)
	status, api = env.Request(t, http.MethodGet,
		"/system/user/page?username="+username+"&pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "查询用户 "+username, status, api)
	var page struct {
		List []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("按用户名 %s 查询：total=%d，期望唯一命中", username, page.Total)
	}
	return page.List[0].ID
}

// createSubDept 在 parentID 下创建子部门并回读 ID。
func createSubDept(t *testing.T, env *testutil.IntegrationEnv, admin, name string, parentID int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/dept", admin, map[string]any{
		"name": name, "parentId": parentID, "status": 0,
	})
	mustOK(t, "创建部门 "+name, status, api)
	// 通过部门树回读 ID（递归查找，子部门可能在任意深度）。
	status, api = env.Request(t, http.MethodGet, "/system/dept/tree", admin, nil)
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

// setDeptLeader 把部门负责人设为指定用户。
func setDeptLeader(t *testing.T, env *testutil.IntegrationEnv, admin string, deptID, leaderUserID int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/system/dept/%d", deptID), admin, nil)
	mustOK(t, "读取部门", status, api)
	var dept struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		ParentID int64  `json:"parentId"`
		Sort     int    `json:"sort"`
		Status   int8   `json:"status"`
	}
	if err := json.Unmarshal(api.Data, &dept); err != nil {
		t.Fatalf("解析部门详情: %v（data=%s）", err, string(api.Data))
	}
	status, api = env.Request(t, http.MethodPut, "/system/dept", admin, map[string]any{
		"id": dept.ID, "name": dept.Name, "parentId": dept.ParentID, "sort": dept.Sort,
		"leaderUserId": leaderUserID, "status": dept.Status,
	})
	mustOK(t, "设置部门负责人", status, api)
}

// createRoleWithMenus 创建绑定菜单按钮的角色并回读 ID。
func createRoleWithMenus(t *testing.T, env *testutil.IntegrationEnv, admin, code string, menuIDs []int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/role", admin, map[string]any{
		"name": "可见范围角色 " + code, "code": code, "dataScope": 5, "menuIds": menuIDs, "status": 0,
	})
	mustOK(t, "创建角色 "+code, status, api)
	status, api = env.Request(t, http.MethodGet,
		"/system/role/page?code="+code+"&pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "查询角色 "+code, status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
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

// assertScope 断言可见范围响应匹配期望（userIds 按集合比较；升序输出已由
// 纯函数表驱动测试锁定）。
func assertScope(t *testing.T, phase string, got scopeResp, wantResource string, wantAll bool, wantUserIDs []int64) {
	t.Helper()
	if got.Resource != wantResource {
		t.Fatalf("%s：resource=%q，期望 %q", phase, got.Resource, wantResource)
	}
	if got.All != wantAll {
		t.Fatalf("%s：all=%v，期望 %v", phase, got.All, wantAll)
	}
	if !wantAll {
		if len(got.UserIDs) != len(wantUserIDs) {
			t.Fatalf("%s：userIds=%v，期望 %v", phase, got.UserIDs, wantUserIDs)
		}
		want := make(map[int64]bool, len(wantUserIDs))
		for _, id := range wantUserIDs {
			want[id] = true
		}
		for _, id := range got.UserIDs {
			if !want[id] {
				t.Fatalf("%s：userIds=%v 含非期望成员 %d", phase, got.UserIDs, id)
			}
		}
	}
}

// TestIntegrationOARouteMountedAndGuarded OA 路由组挂载：未登录 401（路由
// 未挂载会是 404，两者可区分）；登录后可用；登记外的资源 400。
func TestIntegrationOARouteMountedAndGuarded(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 未登录：401，证明 /oa 路由组挂在鉴权链之后（未挂载会是 404）。
	status, api := env.Request(t, http.MethodGet, "/oa/visibility-scope?resource=leave", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("未登录访问 /oa/visibility-scope：HTTP %d code %d，期望 401", status, api.Code)
	}

	admin := env.Login(t, "admin", "123456")

	// super_admin 以 "*" 通配：任意登记资源都是全租户。
	status, api, data := fetchScope(t, env, admin, "leave")
	mustOK(t, "admin 查询 leave 可见范围", status, api)
	assertScope(t, "admin leave", data, "leave", true, nil)

	// 登记外资源（文档走目录 ACL，不经本 helper）与缺参：400。
	status, api = env.Request(t, http.MethodGet, "/oa/visibility-scope?resource=document", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("登记外资源 document：HTTP %d，期望 400", status)
	}
	status, api = env.Request(t, http.MethodGet, "/oa/visibility-scope", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("缺 resource 参数：HTTP %d，期望 400", status)
	}
}

// TestIntegrationOAVisibilityScopeMatrix 可见范围矩阵（spec §4.6）。
// 种子部门：100 总公司（101 研发 / 102 市场 / 103 财务）。
func TestIntegrationOAVisibilityScopeMatrix(t *testing.T) {
	t.Run("仅本人", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		plain := createUser(t, env, admin, "plain_user", 102)
		token := env.Login(t, "plain_user", "123456")

		status, api, data := fetchScope(t, env, token, "expense")
		mustOK(t, "查询 expense 可见范围", status, api)
		assertScope(t, "仅本人", data, "expense", false, []int64{plain})
	})

	t.Run("部门负责人_单部门子树_本人仍可见", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		subDept := createSubDept(t, env, admin, "市场二组", 102)
		member := createUser(t, env, admin, "market_member", subDept)
		// 负责人归属 102（不在被负责部门的子树内），本人单据仍要可见。
		leader := createUser(t, env, admin, "market_leader", 102)
		setDeptLeader(t, env, admin, subDept, leader)

		token := env.Login(t, "market_leader", "123456")
		status, api, data := fetchScope(t, env, token, "leave")
		mustOK(t, "查询 leave 可见范围", status, api)
		assertScope(t, "部门负责人", data, "leave", false, []int64{leader, member})
	})

	t.Run("部门负责人_多部门并集", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")
		subDept := createSubDept(t, env, admin, "市场二组", 102)
		finUser := createUser(t, env, admin, "fin_user", 103)
		subUser := createUser(t, env, admin, "sub_user", subDept)
		leader := createUser(t, env, admin, "double_leader", 101)
		setDeptLeader(t, env, admin, 103, leader)
		setDeptLeader(t, env, admin, subDept, leader)

		token := env.Login(t, "double_leader", "123456")
		status, api, data := fetchScope(t, env, token, "attendance")
		mustOK(t, "查询 attendance 可见范围", status, api)
		assertScope(t, "多部门并集", data, "attendance", false, []int64{leader, finUser, subUser})
	})

	t.Run("角色绑定manage_按资源生效", func(t *testing.T) {
		env := testutil.NewIntegrationEnv(t)
		admin := env.Login(t, "admin", "123456")

		// 测试库内插入携带 oa:leave:manage 的按钮菜单（生产种子由请假工单
		// 按规格 §7.2 下发，本工单只验证「按钮码 → 全租户」链路）。
		menu := sysmodel.Menu{
			BaseEntity: sysmodel.BaseEntity{ID: oaScopeMenuID}, Name: "请假管理（测试）",
			Permission: oaManageCode, Type: sysmodel.MenuTypeButton, ParentID: 500,
			Sort: 99, Visible: true, Status: sysmodel.StatusEnable,
		}
		if err := env.DB.Create(&menu).Error; err != nil {
			t.Fatalf("插入测试按钮菜单: %v", err)
		}
		roleID := createRoleWithMenus(t, env, admin, "oa_manage_test", []int64{oaScopeMenuID})
		manager := createUser(t, env, admin, "leave_manager", 102)
		status, api := env.Request(t, http.MethodPut, "/system/user", admin, map[string]any{
			"id": manager, "username": "leave_manager", "nickname": "leave_manager",
			"deptId": int64(102), "roleIds": []int64{roleID}, "status": 0,
		})
		mustOK(t, "绑定角色", status, api)

		token := env.Login(t, "leave_manager", "123456")
		// 持 oa:leave:manage → leave 全租户。
		status, api, data := fetchScope(t, env, token, "leave")
		mustOK(t, "查询 leave 可见范围", status, api)
		assertScope(t, "manage 命中", data, "leave", true, nil)
		// manage 按资源生效：expense 不命中 → 仅本人。
		status, api, data = fetchScope(t, env, token, "expense")
		mustOK(t, "查询 expense 可见范围", status, api)
		assertScope(t, "manage 不跨资源", data, "expense", false, []int64{manager})
	})
}

// TestIntegrationOAVisibilityCrossTenant 跨租户隔离：租户 2 普通用户的可见
// 集合只含本租户用户，租户 1 的用户 ID 不可能进入；租户 2 管理员的「全租户」
// 也只指本租户（manage 语义按登录租户生效，spec §4.6）。
func TestIntegrationOAVisibilityCrossTenant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 平台租户管理员创建租户 2（自动引导租户管理员 admin/123456）。
	status, api := env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": "租户二", "contactName": "联系人", "status": 0, "domain": "t2.example.com",
	})
	mustOK(t, "创建租户", status, api)

	// 租户 2 管理员登录。
	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"tenantId": 2, "username": "admin", "password": "123456",
	})
	mustOK(t, "租户 2 登录", status, api)
	var login struct {
		AccessToken string `json:"accessToken"`
		UserID      int64  `json:"userId"`
	}
	if err := json.Unmarshal(api.Data, &login); err != nil {
		t.Fatalf("解析登录响应: %v（data=%s）", err, string(api.Data))
	}
	if login.AccessToken == "" || login.UserID <= 0 {
		t.Fatalf("登录响应缺少 token/userId: %s", string(api.Data))
	}

	// 租户 2 管理员在本租户内创建普通用户（不挂任何角色）。
	status, api = env.Request(t, http.MethodPost, "/system/user", login.AccessToken, map[string]any{
		"username": "t2_plain", "password": "123456", "nickname": "t2_plain",
		"roleIds": []int64{}, "status": 0,
	})
	mustOK(t, "租户 2 创建普通用户", status, api)
	status, api = env.Request(t, http.MethodGet,
		"/system/user/page?username=t2_plain&pageNo=1&pageSize=10", login.AccessToken, nil)
	mustOK(t, "租户 2 查询用户", status, api)
	var page struct {
		List []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("租户 2 查询 t2_plain：total=%d，期望唯一命中", page.Total)
	}
	t2PlainID := page.List[0].ID

	// 租户 2 管理员：本租户 super_admin → 本租户全量（all=true）。
	status, api, data := fetchScope(t, env, login.AccessToken, "leave")
	mustOK(t, "租户 2 管理员查询可见范围", status, api)
	assertScope(t, "租户 2 管理员", data, "leave", true, nil)

	// 租户 2 普通用户：仅本人，且可见集合不含租户 1 的任何用户 ID
	//（1=admin、2=demo 及后续创建的租户 1 用户；跨租户业务 ID 在消费方 404）。
	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"tenantId": 2, "username": "t2_plain", "password": "123456",
	})
	mustOK(t, "租户 2 普通用户登录", status, api)
	var t2Login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &t2Login); err != nil || t2Login.AccessToken == "" {
		t.Fatalf("解析租户 2 普通用户登录响应: %v（data=%s）", err, string(api.Data))
	}
	status, api, data = fetchScope(t, env, t2Login.AccessToken, "leave")
	mustOK(t, "租户 2 普通用户查询可见范围", status, api)
	assertScope(t, "跨租户隔离", data, "leave", false, []int64{t2PlainID})
	for _, foreign := range []int64{1, 2, adminUserID(t, env, admin)} {
		for _, id := range data.UserIDs {
			if id == foreign {
				t.Fatalf("跨租户用户 %d 不应出现在可见集合 %v", foreign, data.UserIDs)
			}
		}
	}
}

// adminUserID 回读租户 1 内置管理员（admin）的用户 ID，供跨租户断言使用。
func adminUserID(t *testing.T, env *testutil.IntegrationEnv, admin string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet,
		"/system/user/page?username=admin&pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "查询内置管理员", status, api)
	var page struct {
		List []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"list"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析用户分页: %v（data=%s）", err, string(api.Data))
	}
	for _, u := range page.List {
		if u.Username == "admin" {
			return u.ID
		}
	}
	t.Fatalf("未找到内置管理员 admin")
	return 0
}

// TestIntegrationOASeedMenuIdempotent OA 根菜单（500）由 seed 下发，重复
// 启动幂等（同一测试库再次装配应用，菜单不重复、seed 不报错）。
func TestIntegrationOASeedMenuIdempotent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	countMenu500 := func(t *testing.T) int64 {
		t.Helper()
		var total int64
		if err := env.DB.Raw("SELECT COUNT(*) FROM system_menu WHERE id = ?", 500).Scan(&total).Error; err != nil {
			t.Fatalf("统计 OA 根菜单: %v", err)
		}
		return total
	}
	if got := countMenu500(t); got != 1 {
		t.Fatalf("OA 根菜单数量 = %d，期望 1", got)
	}

	// 菜单接口可读（管理员）。
	status, api := env.Request(t, http.MethodGet, "/system/menu/list", admin, nil)
	mustOK(t, "查询菜单列表", status, api)
	var menus []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Type       int8   `json:"type"`
		Path       string `json:"path"`
		Permission string `json:"permission"`
	}
	if err := json.Unmarshal(api.Data, &menus); err != nil {
		t.Fatalf("解析菜单列表: %v（data=%s）", err, string(api.Data))
	}
	var oaRoot *struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Type       int8   `json:"type"`
		Path       string `json:"path"`
		Permission string `json:"permission"`
	}
	for i := range menus {
		if menus[i].ID == 500 {
			oaRoot = &menus[i]
			break
		}
	}
	if oaRoot == nil {
		t.Fatalf("菜单列表缺少 OA 根目录（id=500）")
	}
	if oaRoot.Name != "OA" || oaRoot.Type != 1 || oaRoot.Path != "/oa" || oaRoot.Permission != "" {
		t.Fatalf("OA 根菜单字段不符: %+v（期望 name=OA type=1(目录) path=/oa permission 空）", oaRoot)
	}

	// 重复启动：同一测试库再次走生产装配（seed 再跑一遍），菜单不重复。
	a2, err := app.New(env.Config, slog.New(slog.NewTextHandler(io.Discard,
		&slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatalf("重复装配应用: %v", err)
	}
	if got := countMenu500(t); got != 1 {
		t.Fatalf("重复启动后 OA 根菜单数量 = %d，期望 1（幂等）", got)
	}
	_ = a2.Close()
}
