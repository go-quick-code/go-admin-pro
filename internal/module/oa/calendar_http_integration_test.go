//go:build integration

// 日程的 HTTP 集成用例（issue #56，docs/oa-mvp-spec.md §4.11）：
//   - CRUD：创建/编辑/取消（软取消后列表与详情都不再展示）；
//   - 三种可见性：私有仅创建人、部门按创建时部门快照及子树、公开全租户；
//   - 部门快照隔离：创建者调部门后历史部门日程仍归属原部门；
//   - 公开日程创建需要 oa:calendar:public（缺失 403）；
//   - 时间/可见性/区间校验（400）；
//   - 权限码逐码（query/create/update/delete）与跨租户 404。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t56，-p 1 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 日程菜单/按钮码（菜单 524 + 5241–5244，员工页 536；issue #56 种子）。
const (
	calendarMenuID     = int64(524)
	calendarCreate     = int64(5241)
	calendarUpdate     = int64(5242)
	calendarDelete     = int64(5243)
	calendarPublic     = int64(5244)
	calendarMineMenuID = int64(536)
)

// 测试统一使用 2030 年 1 月窗口，避免与当前时间/其他用例数据交叉。
const (
	calendarListStart = "2030-01-01 00:00:00"
	calendarListEnd   = "2030-02-01 00:00:00"
)

// calendarEventResp 是日程详情/保存响应的测试视图。
type calendarEventResp struct {
	ID          int64  `json:"id"`
	CreatorID   int64  `json:"creatorId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
	AllDay      bool   `json:"allDay"`
	Visibility  int8   `json:"visibility"`
	DeptID      int64  `json:"deptId"`
	Source      int8   `json:"source"`
	Status      int8   `json:"status"`
}

// calendarEventBody 构造日程保存请求。
func calendarEventBody(title, start, end string, allDay bool, visibility int8) map[string]any {
	return map[string]any{
		"title":       title,
		"description": title + " 描述",
		"startTime":   start,
		"endTime":     end,
		"allDay":      allDay,
		"visibility":  visibility,
	}
}

// createCalendarEventAPI 创建日程并断言成功，返回日程行。
func createCalendarEventAPI(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) calendarEventResp {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/calendar/event", token, body)
	mustOK(t, "创建日程", status, api)
	return decodeCalendarEvent(t, api)
}

// decodeCalendarEvent 解析日程响应。
func decodeCalendarEvent(t *testing.T, api testutil.APIResponse) calendarEventResp {
	t.Helper()
	var out calendarEventResp
	if err := json.Unmarshal(api.Data, &out); err != nil {
		t.Fatalf("解析日程响应: %v（data=%s）", err, string(api.Data))
	}
	return out
}

// getCalendarEventAPI 读日程详情；业务失败返回空行。
func getCalendarEventAPI(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse, calendarEventResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/calendar/event/%d", id), token, nil)
	if api.Code != 0 {
		return status, api, calendarEventResp{}
	}
	return status, api, decodeCalendarEvent(t, api)
}

// listCalendarEventsAPI 按窗口列日程；业务失败返回 nil。
func listCalendarEventsAPI(t *testing.T, env *testutil.IntegrationEnv, token, start, end string) (int, testutil.APIResponse, []calendarEventResp) {
	t.Helper()
	query := url.Values{}
	query.Set("startTime", start)
	query.Set("endTime", end)
	status, api := env.Request(t, http.MethodGet, "/oa/calendar/event/list?"+query.Encode(), token, nil)
	if api.Code != 0 {
		return status, api, nil
	}
	var rows []calendarEventResp
	if err := json.Unmarshal(api.Data, &rows); err != nil {
		t.Fatalf("解析日程列表: %v（data=%s）", err, string(api.Data))
	}
	return status, api, rows
}

// calendarTitles 提取列表中的标题集合（断言可见性用）。
func calendarTitles(rows []calendarEventResp) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[row.Title] = true
	}
	return out
}

// bindCalendarRole 给用户绑定角色并保留原部门（系统用户更新的最小请求体）。
func bindCalendarRole(t *testing.T, env *testutil.IntegrationEnv, admin, username string, userID, deptID, roleID int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, "/system/user", admin, map[string]any{
		"id": userID, "username": username, "nickname": username,
		"deptId": deptID, "roleIds": []int64{roleID}, "status": 0,
	})
	mustOK(t, "绑定日程角色 "+username, status, api)
}

// moveUserDept 调整用户部门（部门快照隔离用例：创建者调岗）。
func moveUserDept(t *testing.T, env *testutil.IntegrationEnv, admin, username string, userID, deptID int64, roleIDs []int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, "/system/user", admin, map[string]any{
		"id": userID, "username": username, "nickname": username,
		"deptId": deptID, "roleIds": roleIDs, "status": 0,
	})
	mustOK(t, "调整用户部门 "+username, status, api)
}

// TestIntegrationCalendarCRUD 创建 → 详情 → 编辑 → 列表 → 取消 → 列表/详情
// 不再展示；重复取消 404。
func TestIntegrationCalendarCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	created := createCalendarEventAPI(t, env, admin, calendarEventBody(
		"部门周会", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 1,
	))
	if created.ID <= 0 || created.CreatorID <= 0 {
		t.Fatalf("创建响应缺少 id/creatorId: %+v", created)
	}
	if created.Title != "部门周会" || created.StartTime != "2030-01-10 09:00:00" {
		t.Fatalf("创建回显不符: %+v", created)
	}
	if created.Status != 0 || created.Source != 0 {
		t.Fatalf("新建日程应为有效且手动来源: %+v", created)
	}

	status, api, detail := getCalendarEventAPI(t, env, admin, created.ID)
	mustOK(t, "日程详情", status, api)
	if detail.Title != created.Title || detail.Visibility != 1 {
		t.Fatalf("详情不符: %+v", detail)
	}

	// 编辑：标题/时间/全天/可见性。
	status, api = env.Request(t, http.MethodPut, "/oa/calendar/event", admin, map[string]any{
		"id": created.ID, "title": "部门周会（改）", "description": "",
		"startTime": "2030-01-11 00:00:00", "endTime": "2030-01-11 23:59:59",
		"allDay": true, "visibility": 0,
	})
	mustOK(t, "编辑日程", status, api)
	updated := decodeCalendarEvent(t, api)
	if updated.Title != "部门周会（改）" || !updated.AllDay || updated.Visibility != 0 {
		t.Fatalf("编辑后回显不符: %+v", updated)
	}
	if updated.StartTime != "2030-01-11 00:00:00" {
		t.Fatalf("编辑后开始时间 = %q", updated.StartTime)
	}

	status, api, rows := listCalendarEventsAPI(t, env, admin, calendarListStart, calendarListEnd)
	mustOK(t, "日程列表", status, api)
	if len(rows) != 1 || rows[0].ID != created.ID {
		t.Fatalf("列表应只含新建日程，得到 %+v", rows)
	}

	// 取消（软取消）：列表不再返回、详情 404、重复取消 404。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/calendar/event/%d", created.ID), admin, nil)
	mustOK(t, "取消日程", status, api)
	status, api, rows = listCalendarEventsAPI(t, env, admin, calendarListStart, calendarListEnd)
	mustOK(t, "取消后列表", status, api)
	if len(rows) != 0 {
		t.Fatalf("取消后列表应为空，得到 %+v", rows)
	}
	status, api, _ = getCalendarEventAPI(t, env, admin, created.ID)
	if status != http.StatusNotFound {
		t.Fatalf("取消后详情: HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/calendar/event/%d", created.ID), admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("重复取消: HTTP %d code %d，期望 404", status, api.Code)
	}
}

// TestIntegrationCalendarValidation 时间/可见性/区间/标题的 400 边界。
func TestIntegrationCalendarValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "结束等于开始", body: calendarEventBody("边界", "2030-01-10 09:00:00", "2030-01-10 09:00:00", false, 0)},
		{name: "结束早于开始", body: calendarEventBody("边界", "2030-01-10 10:00:00", "2030-01-10 09:00:00", false, 0)},
		{name: "非法可见性", body: calendarEventBody("边界", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 9)},
		{name: "非法时间格式", body: calendarEventBody("边界", "2030/01/10 09:00", "2030/01/10 10:00", false, 0)},
		{name: "空标题", body: map[string]any{"title": "  ", "startTime": "2030-01-10 09:00:00", "endTime": "2030-01-10 10:00:00"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, api := env.Request(t, http.MethodPost, "/oa/calendar/event", admin, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("%s: HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
			}
		})
	}

	// 列表区间必须成对且 ≤366 天。
	status, api := env.Request(t, http.MethodGet,
		"/oa/calendar/event/list?startTime="+url.QueryEscape("2030-01-01 00:00:00"), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("只给开始时间: HTTP %d code %d，期望 400", status, api.Code)
	}
	status, api = env.Request(t, http.MethodGet,
		"/oa/calendar/event/list?startTime="+url.QueryEscape("2030-01-01 00:00:00")+
			"&endTime="+url.QueryEscape("2032-01-01 00:00:00"), admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("区间超过一年: HTTP %d code %d，期望 400", status, api.Code)
	}
}

// TestIntegrationCalendarVisibility 三态可见性 + 部门快照隔离。
func TestIntegrationCalendarVisibility(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	roleID := createRoleWithMenus(t, env, admin, "calendar_staff", []int64{
		calendarMineMenuID, calendarCreate, calendarUpdate, calendarDelete,
	})
	ownerID := createUser(t, env, admin, "cal_owner", 102)
	sameDeptID := createUser(t, env, admin, "cal_same_dept", 102)
	otherDeptID := createUser(t, env, admin, "cal_other_dept", 101)
	newDeptID := createUser(t, env, admin, "cal_new_dept", 103)
	for _, u := range []struct {
		name string
		id   int64
		dept int64
	}{
		{"cal_owner", ownerID, 102},
		{"cal_same_dept", sameDeptID, 102},
		{"cal_other_dept", otherDeptID, 101},
		{"cal_new_dept", newDeptID, 103},
	} {
		bindCalendarRole(t, env, admin, u.name, u.id, u.dept, roleID)
	}
	owner := env.Login(t, "cal_owner", "123456")
	sameDept := env.Login(t, "cal_same_dept", "123456")
	otherDept := env.Login(t, "cal_other_dept", "123456")
	newDept := env.Login(t, "cal_new_dept", "123456")

	privateEvent := createCalendarEventAPI(t, env, owner, calendarEventBody(
		"私有日程", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 0,
	))
	deptEvent := createCalendarEventAPI(t, env, owner, calendarEventBody(
		"部门日程", "2030-01-10 11:00:00", "2030-01-10 12:00:00", false, 1,
	))
	if deptEvent.DeptID != 102 {
		t.Fatalf("部门日程 dept_id = %d，期望创建人部门快照 102", deptEvent.DeptID)
	}
	// 公开日程由管理员创建（super_admin 通配 oa:calendar:public）。
	publicEvent := createCalendarEventAPI(t, env, admin, calendarEventBody(
		"公开日程", "2030-01-10 14:00:00", "2030-01-10 15:00:00", false, 2,
	))

	// 列表可见性矩阵。
	status, api, rows := listCalendarEventsAPI(t, env, owner, calendarListStart, calendarListEnd)
	mustOK(t, "创建人列表", status, api)
	if titles := calendarTitles(rows); len(rows) != 3 || !titles["私有日程"] || !titles["部门日程"] || !titles["公开日程"] {
		t.Fatalf("创建人应看到全部 3 条，得到 %+v", rows)
	}
	status, api, rows = listCalendarEventsAPI(t, env, sameDept, calendarListStart, calendarListEnd)
	mustOK(t, "同部门列表", status, api)
	if titles := calendarTitles(rows); len(rows) != 2 || titles["私有日程"] || !titles["部门日程"] || !titles["公开日程"] {
		t.Fatalf("同部门用户应看到部门+公开 2 条，得到 %+v", rows)
	}
	status, api, rows = listCalendarEventsAPI(t, env, otherDept, calendarListStart, calendarListEnd)
	mustOK(t, "外部门列表", status, api)
	if titles := calendarTitles(rows); len(rows) != 1 || !titles["公开日程"] {
		t.Fatalf("外部门用户应只看到公开日程，得到 %+v", rows)
	}

	// 详情可见性：范围外一律 404。
	status, api, _ = getCalendarEventAPI(t, env, sameDept, privateEvent.ID)
	if status != http.StatusNotFound {
		t.Fatalf("同部门用户读私有日程: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, _ = getCalendarEventAPI(t, env, otherDept, deptEvent.ID)
	if status != http.StatusNotFound {
		t.Fatalf("外部门用户读部门日程: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, _ = getCalendarEventAPI(t, env, otherDept, publicEvent.ID)
	mustOK(t, "外部门用户读公开日程", status, api)

	// 部门快照隔离：创建者调到 103 后，原部门 102 的用户仍可见，
	// 新部门 103 的其他用户不可见；创建者本人仍可见自己的日程。
	moveUserDept(t, env, admin, "cal_owner", ownerID, 103, []int64{roleID})
	status, api, rows = listCalendarEventsAPI(t, env, sameDept, calendarListStart, calendarListEnd)
	mustOK(t, "调岗后原部门列表", status, api)
	if titles := calendarTitles(rows); !titles["部门日程"] {
		t.Fatalf("创建者调岗后原部门（102）应仍能看到部门日程，得到 %+v", rows)
	}
	status, api, rows = listCalendarEventsAPI(t, env, newDept, calendarListStart, calendarListEnd)
	mustOK(t, "新部门列表", status, api)
	if titles := calendarTitles(rows); titles["部门日程"] {
		t.Fatalf("新部门（103）用户不应看到按原快照归属 102 的日程，得到 %+v", rows)
	}
	owner = env.Login(t, "cal_owner", "123456")
	status, api, rows = listCalendarEventsAPI(t, env, owner, calendarListStart, calendarListEnd)
	mustOK(t, "调岗后创建人列表", status, api)
	if titles := calendarTitles(rows); !titles["部门日程"] {
		t.Fatalf("创建者调岗后仍应看到自己创建的部门日程，得到 %+v", rows)
	}
}

// TestIntegrationCalendarDeptAncestorVisibility 锁定部门日程的子树语义
// （PR #66 审查 P1-1）：部门日程对创建者部门及其**子树**可见，查看者侧按
// 部门祖先链判定。种子部门树 100 总公司 → 101/102/103，另建 101 的子部门
// 104。
func TestIntegrationCalendarDeptAncestorVisibility(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	roleID := createRoleWithMenus(t, env, admin, "calendar_ancestor", []int64{
		calendarMineMenuID, calendarCreate, calendarUpdate, calendarDelete,
	})
	dept104 := createSubDept(t, env, admin, "研发一组", 101)
	// 创建者/查看者：101（创建者）、101 子部门 104、兄弟部门 102/103、祖先 100。
	users := []struct {
		name   string
		deptID int64
	}{
		{"cal_anc_101", 101},
		{"cal_anc_104", dept104},
		{"cal_anc_102", 102},
		{"cal_anc_103", 103},
		{"cal_anc_100", 100},
	}
	tokens := make(map[string]string, len(users))
	for _, u := range users {
		id := createUser(t, env, admin, u.name, u.deptID)
		bindCalendarRole(t, env, admin, u.name, id, u.deptID, roleID)
		tokens[u.name] = env.Login(t, u.name, "123456")
	}
	creator101 := tokens["cal_anc_101"]

	// 101 建的部门日程：101 自己与子部门 104 可见；102/103（兄弟）与
	// 100（祖先）不可见——部门日程只向创建者部门的下级展开。
	dept101 := createCalendarEventAPI(t, env, creator101, calendarEventBody(
		"101 部门日程", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 1,
	))
	if dept101.DeptID != 101 {
		t.Fatalf("部门日程 dept_id = %d，期望创建人部门快照 101", dept101.DeptID)
	}
	cases := []struct {
		name string
		want bool
	}{
		{"cal_anc_101", true},
		{"cal_anc_104", true},
		{"cal_anc_102", false},
		{"cal_anc_103", false},
		{"cal_anc_100", false},
	}
	for _, tc := range cases {
		status, api, rows := listCalendarEventsAPI(t, env, tokens[tc.name], calendarListStart, calendarListEnd)
		mustOK(t, tc.name+" 列表", status, api)
		if got := calendarTitles(rows)["101 部门日程"]; got != tc.want {
			t.Fatalf("%s 对 101 部门日程可见性 = %v，期望 %v（rows=%+v）", tc.name, got, tc.want, rows)
		}
	}

	// 100 建的部门日程：全部下级 101（含 104）/102/103 均可见。
	dept100 := createCalendarEventAPI(t, env, tokens["cal_anc_100"], calendarEventBody(
		"100 部门日程", "2030-01-10 11:00:00", "2030-01-10 12:00:00", false, 1,
	))
	if dept100.DeptID != 100 {
		t.Fatalf("部门日程 dept_id = %d，期望创建人部门快照 100", dept100.DeptID)
	}
	for _, name := range []string{"cal_anc_101", "cal_anc_104", "cal_anc_102", "cal_anc_103", "cal_anc_100"} {
		status, api, rows := listCalendarEventsAPI(t, env, tokens[name], calendarListStart, calendarListEnd)
		mustOK(t, name+" 列表", status, api)
		if !calendarTitles(rows)["100 部门日程"] {
			t.Fatalf("%s 应能看到 100 建的部门日程（rows=%+v）", name, rows)
		}
	}

	// 私有/公开不受影响：私有仅创建人，公开对全部登录用户可见。
	private := createCalendarEventAPI(t, env, creator101, calendarEventBody(
		"101 私有日程", "2030-01-10 13:00:00", "2030-01-10 14:00:00", false, 0,
	))
	public := createCalendarEventAPI(t, env, admin, calendarEventBody(
		"公开日程", "2030-01-10 15:00:00", "2030-01-10 16:00:00", false, 2,
	))
	status, api, rows := listCalendarEventsAPI(t, env, tokens["cal_anc_104"], calendarListStart, calendarListEnd)
	mustOK(t, "子部门查看私有/公开", status, api)
	if titles := calendarTitles(rows); titles["101 私有日程"] {
		t.Fatalf("私有日程不应被子部门用户看到（rows=%+v）", rows)
	}
	for _, name := range []string{"cal_anc_100", "cal_anc_102", "cal_anc_103", "cal_anc_104"} {
		status, api, rows := listCalendarEventsAPI(t, env, tokens[name], calendarListStart, calendarListEnd)
		mustOK(t, name+" 列表", status, api)
		if !calendarTitles(rows)["公开日程"] {
			t.Fatalf("%s 应能看到公开日程（rows=%+v）", name, rows)
		}
	}
	status, api, _ = getCalendarEventAPI(t, env, tokens["cal_anc_104"], private.ID)
	if status != http.StatusNotFound {
		t.Fatalf("子部门读私有日程: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api, _ = getCalendarEventAPI(t, env, tokens["cal_anc_100"], public.ID)
	mustOK(t, "祖先部门读公开日程", status, api)
}

// TestIntegrationCalendarPermissionCodes 权限码逐码 + 公开日程缺少
// oa:calendar:public 的 403 + 非创建人不可编辑/取消。
func TestIntegrationCalendarPermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	plainID := createUser(t, env, admin, "cal_plain", 102)
	plain := env.Login(t, "cal_plain", "123456")

	// 无任何日程菜单：列表/创建 403。
	status, api := env.Request(t, http.MethodGet,
		"/oa/calendar/event/list?startTime="+url.QueryEscape(calendarListStart)+
			"&endTime="+url.QueryEscape(calendarListEnd), plain, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无查询码列表: HTTP %d code %d，期望 403", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, "/oa/calendar/event", plain,
		calendarEventBody("无权限", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 0))
	if status != http.StatusForbidden {
		t.Fatalf("无创建码创建: HTTP %d code %d，期望 403", status, api.Code)
	}

	// 授予查询/创建/编辑/取消（不含公开）。
	roleID := createRoleWithMenus(t, env, admin, "calendar_basic", []int64{
		calendarMineMenuID, calendarCreate, calendarUpdate, calendarDelete,
	})
	bindCalendarRole(t, env, admin, "cal_plain", plainID, 102, roleID)
	plain = env.Login(t, "cal_plain", "123456")

	// 公开日程缺少 oa:calendar:public → 403；私有/部门可创建。
	status, api = env.Request(t, http.MethodPost, "/oa/calendar/event", plain,
		calendarEventBody("公开越权", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 2))
	if status != http.StatusForbidden {
		t.Fatalf("无公开码创建公开日程: HTTP %d code %d，期望 403", status, api.Code)
	}
	privateEvent := createCalendarEventAPI(t, env, plain, calendarEventBody(
		"员工私有", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 0,
	))

	// 授予 oa:calendar:public 后创建公开日程成功。
	publicRoleID := createRoleWithMenus(t, env, admin, "calendar_public", []int64{calendarPublic})
	status, api = env.Request(t, http.MethodPut, "/system/user", admin, map[string]any{
		"id": plainID, "username": "cal_plain", "nickname": "cal_plain",
		"deptId": int64(102), "roleIds": []int64{roleID, publicRoleID}, "status": 0,
	})
	mustOK(t, "追加公开权限角色", status, api)
	plain = env.Login(t, "cal_plain", "123456")
	createCalendarEventAPI(t, env, plain, calendarEventBody(
		"员工公开", "2030-01-10 11:00:00", "2030-01-10 12:00:00", false, 2,
	))

	// 非创建人（有编辑/取消码）不可编辑或取消他人日程 → 404。
	otherID := createUser(t, env, admin, "cal_other_editor", 102)
	bindCalendarRole(t, env, admin, "cal_other_editor", otherID, 102, roleID)
	other := env.Login(t, "cal_other_editor", "123456")
	status, api = env.Request(t, http.MethodPut, "/oa/calendar/event", other, map[string]any{
		"id": privateEvent.ID, "title": "越权修改",
		"startTime": "2030-01-10 09:00:00", "endTime": "2030-01-10 10:00:00",
		"allDay": false, "visibility": 2,
	})
	if status != http.StatusNotFound {
		t.Fatalf("非创建人编辑: HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodDelete,
		fmt.Sprintf("/oa/calendar/event/%d", privateEvent.ID), other, nil)
	if status != http.StatusNotFound {
		t.Fatalf("非创建人取消: HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
}

// TestIntegrationCalendarTenantIsolation 跨租户：租户 2 看不到租户 1 的
// 公开日程（公开只在租户内），按 ID 读取 404。
func TestIntegrationCalendarTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	publicEvent := createCalendarEventAPI(t, env, admin, calendarEventBody(
		"租户一公开", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 2,
	))

	// 平台租户管理员创建租户 2（自动引导租户管理员 admin/123456）。
	status, api := env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": "租户二", "contactName": "联系人", "status": 0, "domain": "cal-t2.example.com",
	})
	mustOK(t, "创建租户", status, api)
	status, api = env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"tenantId": 2, "username": "admin", "password": "123456",
	})
	mustOK(t, "租户 2 登录", status, api)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &login); err != nil || login.AccessToken == "" {
		t.Fatalf("解析租户 2 登录响应: %v（data=%s）", err, string(api.Data))
	}
	t2 := login.AccessToken

	status, api, rows := listCalendarEventsAPI(t, env, t2, calendarListStart, calendarListEnd)
	mustOK(t, "租户 2 列表", status, api)
	if len(rows) != 0 {
		t.Fatalf("租户 2 不应看到租户 1 的日程，得到 %+v", rows)
	}
	status, api, _ = getCalendarEventAPI(t, env, t2, publicEvent.ID)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 跨租户读日程: HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	// 租户 2 管理员在自己的租户内创建公开日程成功（公开只在租户内生效）。
	createCalendarEventAPI(t, env, t2, calendarEventBody(
		"租户二公开", "2030-01-10 09:00:00", "2030-01-10 10:00:00", false, 2,
	))
}

// calendarMenuNode 是菜单树节点（按钮挂在菜单的 children 下）。
type calendarMenuNode struct {
	ID         int64              `json:"id"`
	ParentID   int64              `json:"parentId"`
	Name       string             `json:"name"`
	Permission string             `json:"permission"`
	Children   []calendarMenuNode `json:"children"`
}

// walkCalendarMenus 深度优先遍历菜单树（含按钮）。
func walkCalendarMenus(nodes []calendarMenuNode, fn func(calendarMenuNode)) {
	for _, node := range nodes {
		fn(node)
		walkCalendarMenus(node.Children, fn)
	}
}

// TestIntegrationCalendarSeedMenus 菜单种子：524/5241–5244 与 536 下发，
// 权限码与前端/后端约定一致（grep 去重后的号段）。
func TestIntegrationCalendarSeedMenus(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/system/menu/list", admin, nil)
	mustOK(t, "查询菜单列表", status, api)
	var menus []calendarMenuNode
	if err := json.Unmarshal(api.Data, &menus); err != nil {
		t.Fatalf("解析菜单列表: %v（data=%s）", err, string(api.Data))
	}
	want := map[int64]string{
		calendarMenuID:     "oa:calendar:query",
		calendarCreate:     "oa:calendar:create",
		calendarUpdate:     "oa:calendar:update",
		calendarDelete:     "oa:calendar:delete",
		calendarPublic:     "oa:calendar:public",
		calendarMineMenuID: "oa:calendar:query",
	}
	found := make(map[int64]string)
	walkCalendarMenus(menus, func(m calendarMenuNode) {
		if _, ok := want[m.ID]; ok {
			found[m.ID] = m.Permission
		}
	})
	if len(found) != len(want) {
		t.Fatalf("日程菜单缺失：找到 %v，期望 %v", found, want)
	}
	for id, permission := range want {
		if found[id] != permission {
			t.Fatalf("菜单 %d 权限码 = %q，期望 %q", id, found[id], permission)
		}
	}
}
