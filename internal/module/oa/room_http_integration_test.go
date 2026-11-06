//go:build integration

// 会议室与预约的 HTTP 集成用例（issue #57，docs/oa-mvp-spec.md
// §4.11/§4.14）：
//   - 会议室 CRUD/启停；有未来有效预约时删除 409，取消后可删除；
//   - 预约时间范围与同房间重叠校验（相邻不算冲突、包含/交叉冲突 400）；
//   - 预约成功同事务生成 source=room_booking 的部门可见日程；取消同步取消；
//   - 会议提醒每 5 分钟窗口扫描、reminded_at 条件更新只发一次；
//   - 员工侧预约列表强制本人、非预约人取消 404、跨租户 404、权限码逐码；
//   - 菜单/任务种子（525/5251–5252、537、538；oaRoomBookingReminder 每 5 分钟）。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t57，-p 1 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 会议室菜单/按钮码（issue #57 种子）：525 管理、5251 维护、5252 预约、
// 538 管理端预约视图、539 员工入口「我的会议」（537 已被 #58 我的文档占用）。
const (
	menuRoomManage  = int64(525)
	btnRoomManage   = int64(5251)
	btnRoomBooking  = int64(5252)
	menuRoomBooking = int64(538)
	menuRoomMine    = int64(539)
)

// 测试统一使用 2030 年窗口，避免与当前时间/其他用例数据交叉。
const (
	roomBookingListStart = "2030-01-01 00:00:00"
	roomBookingListEnd   = "2030-02-01 00:00:00"
	roomReminderNow      = "2030-01-10 08:45:00"
)

// roomResp 是会议室响应的测试视图。
type roomResp struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Capacity int    `json:"capacity"`
	Status   int8   `json:"status"`
	Sort     int    `json:"sort"`
}

// roomBookingResp 是预约响应的测试视图。
type roomBookingResp struct {
	ID              int64  `json:"id"`
	RoomID          int64  `json:"roomId"`
	RoomName        string `json:"roomName"`
	RoomLocation    string `json:"roomLocation"`
	UserID          int64  `json:"userId"`
	UserName        string `json:"userName"`
	Title           string `json:"title"`
	StartTime       string `json:"startTime"`
	EndTime         string `json:"endTime"`
	Status          int8   `json:"status"`
	CalendarEventID *int64 `json:"calendarEventId"`
	RemindedAt      string `json:"remindedAt"`
}

// roomCalendarEventResp 是日程响应的最小测试视图（预约联动断言用）。
type roomCalendarEventResp struct {
	ID         int64  `json:"id"`
	CreatorID  int64  `json:"creatorId"`
	Title      string `json:"title"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	Visibility int8   `json:"visibility"`
	DeptID     int64  `json:"deptId"`
	Source     int8   `json:"source"`
	SourceID   *int64 `json:"sourceId"`
	Status     int8   `json:"status"`
}

// roomBody 构造会议室保存请求。
func roomBody(name, location string, capacity, sort int) map[string]any {
	return map[string]any{"name": name, "location": location, "capacity": capacity, "sort": sort}
}

// createRoomAPI 创建会议室并断言成功。
func createRoomAPI(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) roomResp {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/room", token, body)
	mustOK(t, "创建会议室", status, api)
	var out roomResp
	if err := json.Unmarshal(api.Data, &out); err != nil {
		t.Fatalf("解析会议室响应: %v（data=%s）", err, string(api.Data))
	}
	return out
}

// createRoomBookingAPI 创建预约，返回 (HTTP 状态, msg, 预约行)。
func createRoomBookingAPI(t *testing.T, env *testutil.IntegrationEnv, token string, roomID int64, title, start, end string) (int, string, roomBookingResp) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/room/booking", token, map[string]any{
		"roomId": roomID, "title": title, "startTime": start, "endTime": end,
	})
	var out roomBookingResp
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &out); err != nil {
			t.Fatalf("解析预约响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, out
}

// pageRoomBookingsAPI 查询预约分页，返回 (HTTP 状态, msg, 行列表, total)。
func pageRoomBookingsAPI(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, []roomBookingResp, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/room/booking/list"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil, 0
	}
	var page struct {
		List  []roomBookingResp `json:"list"`
		Total int64             `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析预约分页: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, page.List, page.Total
}

// listRoomCalendarEvents 读取日程列表（窗口固定 2030 年 1 月）。
func listRoomCalendarEvents(t *testing.T, env *testutil.IntegrationEnv, token string) []roomCalendarEventResp {
	t.Helper()
	query := url.Values{}
	query.Set("startTime", roomBookingListStart)
	query.Set("endTime", roomBookingListEnd)
	status, api := env.Request(t, http.MethodGet, "/oa/calendar/event/list?"+query.Encode(), token, nil)
	mustOK(t, "查询日程列表", status, api)
	var rows []roomCalendarEventResp
	if err := json.Unmarshal(api.Data, &rows); err != nil {
		t.Fatalf("解析日程列表: %v（data=%s）", err, string(api.Data))
	}
	return rows
}

// TestIntegrationRoomCRUDAndDeleteConstraint 会议室全生命周期 + 删除约束：
// 有未来有效预约时删除 409；取消后可删除；停用后不能再预约。
func TestIntegrationRoomCRUDAndDeleteConstraint(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	room := createRoomAPI(t, env, admin, roomBody("大会议室", "3 层东侧", 20, 1))
	if room.ID <= 0 || room.Status != 0 {
		t.Fatalf("新建会议室应为启用: %+v", room)
	}

	// 详情/分页/更新/启停。
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/room/%d", room.ID), admin, nil)
	mustOK(t, "会议室详情", status, api)
	status, api = env.Request(t, http.MethodGet, "/oa/room/page?pageNo=1&pageSize=10&name=大会议", admin, nil)
	mustOK(t, "会议室分页", status, api)
	var page struct {
		List  []roomResp `json:"list"`
		Total int64      `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析会议室分页: %v", err)
	}
	if page.Total != 1 || len(page.List) != 1 || page.List[0].Name != "大会议室" {
		t.Fatalf("名称模糊筛选应只命中大会议室: %+v", page)
	}
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/oa/room/%d", room.ID), admin,
		roomBody("大会议室（改）", "3 层西侧", 30, 2))
	mustOK(t, "更新会议室", status, api)
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/oa/room/%d/status", room.ID), admin, map[string]any{"status": 1})
	mustOK(t, "停用会议室", status, api)
	status, api = env.Request(t, http.MethodPost, "/oa/room/booking", admin, map[string]any{
		"roomId": room.ID, "title": "停用后预约",
		"startTime": "2030-01-10 09:00:00", "endTime": "2030-01-10 10:00:00",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("停用会议室预约: HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/oa/room/%d/status", room.ID), admin, map[string]any{"status": 0})
	mustOK(t, "重新启用会议室", status, api)

	// 未来有效预约阻塞删除（409）；取消后可删除。
	_, _, booking := createRoomBookingAPI(t, env, admin, room.ID, "周会", "2030-01-10 09:00:00", "2030-01-10 10:00:00")
	if booking.ID <= 0 {
		t.Fatalf("预约创建失败: %+v", booking)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/room/%d", room.ID), admin, nil)
	if status != http.StatusConflict || api.Msg == "" {
		t.Fatalf("有未来预约删除会议室: HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/oa/room/booking/%d/cancel", booking.ID), admin, nil)
	mustOK(t, "取消预约", status, api)
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/room/%d", room.ID), admin, nil)
	mustOK(t, "取消后删除会议室", status, api)
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/oa/room/%d", room.ID), admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情: HTTP %d code %d，期望 404", status, api.Code)
	}
}

// TestIntegrationRoomBookingConflict 时间范围校验与重叠规则：相邻不算冲突，
// 包含/交叉/相同冲突 400；取消后同一时段可重新预约。
func TestIntegrationRoomBookingConflict(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	room := createRoomAPI(t, env, admin, roomBody("小会议室", "2 层", 8, 0))

	status, msg, first := createRoomBookingAPI(t, env, admin, room.ID, "第一场", "2030-01-10 09:00:00", "2030-01-10 10:00:00")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建首个预约：HTTP %d msg %q", status, msg)
	}
	if first.ID <= 0 || first.RoomName != "小会议室" || first.UserID <= 0 {
		t.Fatalf("预约回显缺少房间/预约人: %+v", first)
	}

	status, msg, _ = createRoomBookingAPI(t, env, admin, room.ID, "相同时段", "2030-01-10 09:00:00", "2030-01-10 10:00:00")
	if status != http.StatusBadRequest || msg == "" {
		t.Fatalf("相同时段: HTTP %d msg %q，期望 400", status, msg)
	}
	status, msg, _ = createRoomBookingAPI(t, env, admin, room.ID, "交叉", "2030-01-10 09:30:00", "2030-01-10 10:30:00")
	if status != http.StatusBadRequest || msg == "" {
		t.Fatalf("交叉时段: HTTP %d msg %q，期望 400", status, msg)
	}
	status, msg, _ = createRoomBookingAPI(t, env, admin, room.ID, "包含", "2030-01-10 08:00:00", "2030-01-10 12:00:00")
	if status != http.StatusBadRequest || msg == "" {
		t.Fatalf("包含时段: HTTP %d msg %q，期望 400", status, msg)
	}
	status, msg, adjacent := createRoomBookingAPI(t, env, admin, room.ID, "相邻", "2030-01-10 10:00:00", "2030-01-10 11:00:00")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("相邻时段：HTTP %d msg %q", status, msg)
	}
	if adjacent.ID <= 0 {
		t.Fatalf("相邻预约创建失败")
	}
	// 结束不晚于开始 400。
	status, msg, _ = createRoomBookingAPI(t, env, admin, room.ID, "非法时间", "2030-01-10 10:00:00", "2030-01-10 10:00:00")
	if status != http.StatusBadRequest || msg == "" {
		t.Fatalf("结束等于开始: HTTP %d msg %q，期望 400", status, msg)
	}

	// 取消首个预约后同一时段可重新预约（相邻的 10:00–11:00 仍在，但与
	// 09:00–10:00 相邻不算冲突）。
	status, resp := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/room/booking/%d/cancel", first.ID), admin, nil)
	mustOK(t, "取消首个预约", status, resp)
	status, msg, rebooked := createRoomBookingAPI(t, env, admin, room.ID, "重新预约", "2030-01-10 09:00:00", "2030-01-10 10:00:00")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("取消后重新预约：HTTP %d msg %q", status, msg)
	}
	if rebooked.ID <= 0 {
		t.Fatalf("取消后重新预约失败")
	}
}

// TestIntegrationRoomBookingCalendarAndCancelSync 预约同事务生成部门可见
// 日程（source=1、sourceId=预约 ID、deptId=预约人部门快照）；取消预约同步
// 取消日程（列表不再返回、详情 404）。
func TestIntegrationRoomBookingCalendarAndCancelSync(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	roleID := createRoleWithMenus(t, env, admin, "room_staff", []int64{
		menuRoomMine, btnRoomBooking, 536, calendarUpdate, calendarDelete, // 我的会议 + 预约 + 日程查询/编辑/取消
	})
	ownerID := createUser(t, env, admin, "room_owner", 102)
	sameDeptID := createUser(t, env, admin, "room_same_dept", 102)
	otherDeptID := createUser(t, env, admin, "room_other_dept", 101)
	bindCalendarRole(t, env, admin, "room_owner", ownerID, 102, roleID)
	bindCalendarRole(t, env, admin, "room_same_dept", sameDeptID, 102, roleID)
	bindCalendarRole(t, env, admin, "room_other_dept", otherDeptID, 101, roleID)
	owner := env.Login(t, "room_owner", "123456")
	sameDept := env.Login(t, "room_same_dept", "123456")
	otherDept := env.Login(t, "room_other_dept", "123456")

	room := createRoomAPI(t, env, admin, roomBody("项目会议室", "5 层", 12, 0))
	status, msg, booking := createRoomBookingAPI(t, env, owner, room.ID,
		"项目周会", "2030-01-10 14:00:00", "2030-01-10 15:00:00")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("员工预约：HTTP %d msg %q", status, msg)
	}
	if booking.CalendarEventID == nil || *booking.CalendarEventID <= 0 {
		t.Fatalf("预约响应缺少 calendarEventId: %+v", booking)
	}

	events := listRoomCalendarEvents(t, env, owner)
	if len(events) != 1 {
		t.Fatalf("预约应生成 1 条日程，得到 %+v", events)
	}
	event := events[0]
	if event.Source != 1 || event.SourceID == nil || *event.SourceID != booking.ID {
		t.Fatalf("日程来源应为预约（source=1, sourceId=%d），得到 %+v", booking.ID, event)
	}
	if event.Visibility != 1 || event.DeptID != 102 {
		t.Fatalf("日程应为部门可见且 dept_id=102 快照，得到 %+v", event)
	}
	if event.CreatorID != ownerID || event.Title != "项目周会" {
		t.Fatalf("日程创建人/标题不符: %+v", event)
	}

	// 同部门可见、外部门不可见（部门可见性生效）。
	if titles := roomCalendarTitles(listRoomCalendarEvents(t, env, sameDept)); !titles["项目周会"] {
		t.Fatalf("同部门用户应看到预约生成的日程")
	}
	if titles := roomCalendarTitles(listRoomCalendarEvents(t, env, otherDept)); titles["项目周会"] {
		t.Fatalf("外部门用户不应看到部门日程")
	}

	// 日历接口不得编辑/取消预约生成的日程（PR #66 审查 P2-5）：创建人
	// 即使持有 oa:calendar:update/delete 也返回 409，只允许会议室取消路径。
	status, api := env.Request(t, http.MethodPut, "/oa/calendar/event", owner, map[string]any{
		"id": event.ID, "title": "越权改名", "description": "",
		"startTime": "2030-01-10 14:00:00", "endTime": "2030-01-10 15:00:00",
		"allDay": false, "visibility": 0,
	})
	if status != http.StatusConflict {
		t.Fatalf("日历接口编辑预约日程: HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/calendar/event/%d", event.ID), owner, nil)
	if status != http.StatusConflict {
		t.Fatalf("日历接口取消预约日程: HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}

	// 取消预约：预约状态取消，日程同步取消（列表空、详情 404）。
	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/oa/room/booking/%d/cancel", booking.ID), owner, nil)
	mustOK(t, "取消预约", status, api)
	if events := listRoomCalendarEvents(t, env, owner); len(events) != 0 {
		t.Fatalf("取消预约后日程列表应为空，得到 %+v", events)
	}
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/oa/calendar/event/%d", *booking.CalendarEventID), owner, nil)
	if status != http.StatusNotFound {
		t.Fatalf("取消后日程详情: HTTP %d code %d，期望 404", status, api.Code)
	}

	// 员工侧列表（无 oa:room:manage）强制本人：管理员创建的预约不可见。
	_, _, _ = createRoomBookingAPI(t, env, admin, room.ID, "管理员会议", "2030-01-10 16:00:00", "2030-01-10 17:00:00")
	status, msg, list, total := pageRoomBookingsAPI(t, env, owner,
		"?pageNo=1&pageSize=10&date=2030-01-10&userId=1")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("员工预约列表：HTTP %d msg %q", status, msg)
	}
	if total != 1 || len(list) != 1 || list[0].UserID != ownerID {
		t.Fatalf("员工侧应只看到本人 1 条预约（忽略 userId=1 越权），得到 total=%d list=%+v", total, list)
	}
}

// roomCalendarTitles 提取日程标题集合。
func roomCalendarTitles(rows []roomCalendarEventResp) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[row.Title] = true
	}
	return out
}

// TestIntegrationRoomReminderOnce 会议提醒窗口与只发一次：
// 15 分钟整点进入提醒、超过 15 分钟不发；重复执行不重复写通知。
func TestIntegrationRoomReminderOnce(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	room := createRoomAPI(t, env, admin, roomBody("提醒会议室", "1 层", 6, 0))

	_, _, booking := createRoomBookingAPI(t, env, admin, room.ID, "提醒会议", "2030-01-10 09:00:00", "2030-01-10 09:30:00")

	// 取种子任务（同时锁定 handler/cron/默认启用）。
	var job inframodel.Job
	if err := tenant.Skip(env.DB).Where("`handler_name` = ?", oaservice.RoomBookingReminderHandlerName).First(&job).Error; err != nil {
		t.Fatalf("查询会议提醒任务种子: %v", err)
	}
	if job.CronExpression != "0 */5 * * * *" || job.Status != inframodel.JobStatusRunning {
		t.Fatalf("会议提醒任务种子不符: cron=%q status=%d", job.CronExpression, job.Status)
	}

	reminderPage := func() (int64, []struct {
		BizType string `json:"bizType"`
		BizID   int64  `json:"bizId"`
		Route   string `json:"route"`
	}) {
		t.Helper()
		status, api := env.Request(t, http.MethodGet, "/notify/message/page?type=meeting_reminder&pageNo=1&pageSize=10", admin, nil)
		mustOK(t, "查询会议提醒通知", status, api)
		var page struct {
			List []struct {
				BizType string `json:"bizType"`
				BizID   int64  `json:"bizId"`
				Route   string `json:"route"`
			} `json:"list"`
			Total int64 `json:"total"`
		}
		if err := json.Unmarshal(api.Data, &page); err != nil {
			t.Fatalf("解析通知分页: %v", err)
		}
		return page.Total, page.List
	}
	reminderTotal := func() int64 {
		t.Helper()
		total, _ := reminderPage()
		return total
	}

	runReminder := func(now string) {
		t.Helper()
		job.HandlerParam = now
		env.App.Scheduler.Execute(job)
	}

	// 距开始 15 分 1 秒（08:44:59）：不发。
	runReminder("2030-01-10 08:44:59")
	if got := reminderTotal(); got != 0 {
		t.Fatalf("窗口外不应提醒，通知数 %d", got)
	}
	// 15 分钟整点（08:45:00）：发且只发 1 条，业务定位与跳转路由正确。
	runReminder(roomReminderNow)
	if total, list := reminderPage(); total != 1 {
		t.Fatalf("15 分钟整点应发 1 条提醒，实际 %d", total)
	} else if len(list) != 1 || list[0].BizType != "oa_room_booking" || list[0].BizID != booking.ID || list[0].Route != "/oa/room-mine" {
		t.Fatalf("提醒通知业务定位不符: total=%d list=%+v", total, list)
	}
	// 重复执行（08:50）：reminded_at 条件更新保证不重复发。
	runReminder("2030-01-10 08:50:00")
	if got := reminderTotal(); got != 1 {
		t.Fatalf("重复执行不应重复提醒，通知数 %d", got)
	}

	// 预约行 reminded_at 已回填。
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/room/booking/%d", booking.ID), admin, nil)
	mustOK(t, "预约详情", status, api)
	var detail roomBookingResp
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析预约详情: %v", err)
	}
	if detail.RemindedAt == "" {
		t.Fatalf("预约 reminded_at 应已回填: %+v", detail)
	}

	// 第二场（次日）在窗口内才提醒：08:44:59 不发、08:45:00 发。
	_, _, next := createRoomBookingAPI(t, env, admin, room.ID, "次日会议", "2030-01-11 09:00:00", "2030-01-11 09:30:00")
	runReminder("2030-01-11 08:44:59")
	if got := reminderTotal(); got != 1 {
		t.Fatalf("次日窗口外不应提醒，通知数 %d", got)
	}
	runReminder("2030-01-11 08:45:00")
	if got := reminderTotal(); got != 2 {
		t.Fatalf("次日窗口内应提醒，通知数 %d", got)
	}
	_ = next
}

// TestIntegrationRoomPermissionAndTenantIsolation 权限码逐码、非预约人取消 404、
// 跨租户 404 与租户内列表隔离。
func TestIntegrationRoomPermissionAndTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	room := createRoomAPI(t, env, admin, roomBody("隔离会议室", "6 层", 10, 0))
	_, _, booking := createRoomBookingAPI(t, env, admin, room.ID, "隔离会议", "2030-01-10 09:00:00", "2030-01-10 10:00:00")

	plainID := createUser(t, env, admin, "room_plain", 102)
	plain := env.Login(t, "room_plain", "123456")

	// 无任何会议室菜单：列表/创建/预约全部 403。
	status, api := env.Request(t, http.MethodGet, "/oa/room/list", plain, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无查询码读会议室: HTTP %d code %d，期望 403", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, "/oa/room", plain, roomBody("越权", "", 1, 0))
	if status != http.StatusForbidden {
		t.Fatalf("无管理码建会议室: HTTP %d code %d，期望 403", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, "/oa/room/booking", plain, map[string]any{
		"roomId": room.ID, "title": "越权预约",
		"startTime": "2030-01-10 11:00:00", "endTime": "2030-01-10 12:00:00",
	})
	if status != http.StatusForbidden {
		t.Fatalf("无预约码建预约: HTTP %d code %d，期望 403", status, api.Code)
	}

	// 仅授予查询：可看会议室/预约，不能建会议室，也不能取消他人预约（404）。
	queryRole := createRoleWithMenus(t, env, admin, "room_query", []int64{menuRoomBooking})
	bindCalendarRole(t, env, admin, "room_plain", plainID, 102, queryRole)
	plain = env.Login(t, "room_plain", "123456")
	status, api = env.Request(t, http.MethodGet, "/oa/room/list", plain, nil)
	mustOK(t, "仅查询码读会议室", status, api)
	status, api = env.Request(t, http.MethodPost, "/oa/room", plain, roomBody("越权", "", 1, 0))
	if status != http.StatusForbidden {
		t.Fatalf("仅查询码建会议室: HTTP %d code %d，期望 403", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/oa/room/booking/%d/cancel", booking.ID), plain, nil)
	if status != http.StatusNotFound {
		t.Fatalf("非预约人取消: HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}

	// 跨租户：租户 2 看不到租户 1 的会议室/预约。
	status, api = env.Request(t, http.MethodPost, "/system/tenant", admin, map[string]any{
		"name": "租户二", "contactName": "联系人", "status": 0, "domain": "room-t2.example.com",
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
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/oa/room/%d", room.ID), t2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 跨租户读会议室: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/oa/room/booking/%d", booking.ID), t2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 跨租户读预约: HTTP %d code %d，期望 404", status, api.Code)
	}
	status, msg, list, total := pageRoomBookingsAPI(t, env, t2, "?pageNo=1&pageSize=10")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("租户 2 预约列表：HTTP %d msg %q", status, msg)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("租户 2 不应看到租户 1 的预约: total=%d list=%+v", total, list)
	}
}

// TestIntegrationRoomSeedMenusAndJob 菜单种子 525/5251–5252、537、538 与
// 权限码，以及会议提醒任务种子（每 5 分钟、默认启用）。
func TestIntegrationRoomSeedMenusAndJob(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, "/system/menu/list", admin, nil)
	mustOK(t, "查询菜单列表", status, api)
	var menus []calendarMenuNode
	if err := json.Unmarshal(api.Data, &menus); err != nil {
		t.Fatalf("解析菜单列表: %v（data=%s）", err, string(api.Data))
	}
	want := map[int64]string{
		menuRoomManage:  "oa:room:query",
		btnRoomManage:   "oa:room:manage",
		btnRoomBooking:  "oa:room:booking",
		menuRoomBooking: "oa:room:query",
		menuRoomMine:    "oa:room:query",
	}
	found := make(map[int64]string)
	walkCalendarMenus(menus, func(m calendarMenuNode) {
		if _, ok := want[m.ID]; ok {
			found[m.ID] = m.Permission
		}
	})
	if len(found) != len(want) {
		t.Fatalf("会议室菜单缺失：找到 %v，期望 %v", found, want)
	}
	for id, permission := range want {
		if found[id] != permission {
			t.Fatalf("菜单 %d 权限码 = %q，期望 %q", id, found[id], permission)
		}
	}

	// 任务种子：handler 已注册为系统级、cron 每 5 分钟、默认启用。
	var job inframodel.Job
	if err := tenant.Skip(env.DB).Where("`handler_name` = ?", oaservice.RoomBookingReminderHandlerName).First(&job).Error; err != nil {
		t.Fatalf("查询会议提醒任务种子: %v", err)
	}
	if job.CronExpression != "0 */5 * * * *" {
		t.Fatalf("会议提醒 cron = %q，期望每 5 分钟", job.CronExpression)
	}
	if job.Status != inframodel.JobStatusRunning {
		t.Fatalf("会议提醒任务应默认启用，status=%d", job.Status)
	}
	if !env.App.Scheduler.IsSystemHandler(oaservice.RoomBookingReminderHandlerName) {
		t.Fatalf("会议提醒 handler 应注册为系统级（跨租户）")
	}
}
