//go:build integration

// 考勤基础的 HTTP 集成用例（issue #60，docs/oa-mvp-spec.md §3.9/§4.10）：
// 班次 CRUD+启停（Slice A）、考勤组与组员/批量添加/唯一组约束（Slice B）、
// Web 打卡与多次保留/无组 400/IP+UA（Slice C）、权限码逐码与跨租户 404。
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t60，-p 1 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ---- 考勤测试基建（issue #60）----

// 菜单 ID（种子 527 段，grep 去重后取号并已报备协调台账）：
// 527 班次管理（oa:attendance:shift:manage）/ 530 考勤组管理
// （oa:attendance:group:manage）/ 529 我的考勤（员工自助页，登录即可用，无权限码）。
const (
	menuAttShift = int64(527)
	menuAttGroup = int64(530)
	menuAttPunch = int64(531)

	permAttShiftManage = "oa:attendance:shift:manage"
	permAttGroupManage = "oa:attendance:group:manage"
)

// attShiftRow 是班次行（页面/响应共读）。
type attShiftRow struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
	GraceMinutes int    `json:"graceMinutes"`
	Status       int8   `json:"status"`
	Sort         int    `json:"sort"`
}

// attCreateShift 新建班次，返回 (HTTP 状态, msg, 班次行)。
func attCreateShift(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int, string, attShiftRow) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/attendance/shift", token, body)
	var row attShiftRow
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &row); err != nil {
			t.Fatalf("解析班次创建响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, row
}

// attUpdateShift 更新班次，返回 (HTTP 状态, msg)。
func attUpdateShift(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, body map[string]any) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/attendance/shift/%d", id), token, body)
	return status, api.Msg
}

// attSetShiftStatus 启停班次，返回 (HTTP 状态, msg)。
func attSetShiftStatus(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, status int8) (int, string) {
	t.Helper()
	code, api := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/attendance/shift/%d/status", id), token, map[string]any{"status": status})
	return code, api.Msg
}

// attDeleteShift 删除班次，返回 (HTTP 状态, msg)。
func attDeleteShift(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	code, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/attendance/shift/%d", id), token, nil)
	return code, api.Msg
}

// attPageShifts 分页查询班次，返回 (HTTP 状态, msg, 行列表, total)。
func attPageShifts(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, []attShiftRow, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/shift"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil, 0
	}
	var page struct {
		List  []attShiftRow `json:"list"`
		Total int64         `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析班次分页响应: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, page.List, page.Total
}

// attGetShift 读取班次详情，返回 (HTTP 状态, msg, 班次行)。
func attGetShift(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string, attShiftRow) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/attendance/shift/%d", id), token, nil)
	var row attShiftRow
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &row); err != nil {
			t.Fatalf("解析班次详情响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, row
}

// TestIntegrationAttendanceShiftCRUD 班次全生命周期：创建（默认启用）、
// 详情、分页与筛选、更新、启停、删除；非法字段表驱动 400。
func TestIntegrationAttendanceShiftCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	t.Run("创建与回读", func(t *testing.T) {
		status, msg, row := attCreateShift(t, env, admin, map[string]any{
			"name": "标准班", "startTime": "09:00", "endTime": "18:00", "graceMinutes": 10, "sort": 3,
		})
		if status != http.StatusOK || msg != "" || row.ID <= 0 {
			t.Fatalf("创建班次：HTTP %d msg %q id %d", status, msg, row.ID)
		}
		if row.Name != "标准班" || row.StartTime != "09:00" || row.EndTime != "18:00" || row.GraceMinutes != 10 || row.Sort != 3 {
			t.Fatalf("创建响应字段不符: %+v", row)
		}
		if row.Status != 0 {
			t.Fatalf("新班次应默认启用（0），实际 %d", row.Status)
		}
		// 详情回读一致。
		status, msg, got := attGetShift(t, env, admin, row.ID)
		if status != http.StatusOK || got.ID != row.ID || got.Name != "标准班" {
			t.Fatalf("班次详情：HTTP %d msg %q row %+v", status, msg, got)
		}
	})

	t.Run("分页与筛选", func(t *testing.T) {
		// 追加两个班次（本用例环境独占库，计数从当前值起算）。
		_, _, early := attCreateShift(t, env, admin, map[string]any{
			"name": "早班", "startTime": "06:00", "endTime": "14:00", "graceMinutes": 5,
		})
		_, _, late := attCreateShift(t, env, admin, map[string]any{
			"name": "晚班", "startTime": "13:00", "endTime": "21:00", "graceMinutes": 0,
		})
		if early.ID <= 0 || late.ID <= 0 {
			t.Fatalf("辅助班次创建失败: %d %d", early.ID, late.ID)
		}
		// 停用晚班，供状态筛选断言。
		if code, msg := attSetShiftStatus(t, env, admin, late.ID, 1); code != http.StatusOK || msg != "" {
			t.Fatalf("停用晚班：HTTP %d msg %q", code, msg)
		}

		_, _, list, total := attPageShifts(t, env, admin, "?pageNo=1&pageSize=10")
		if total < 3 || len(list) < 3 {
			t.Fatalf("分页应含全部班次：total %d len %d", total, len(list))
		}
		// 名称模糊筛选。
		_, _, list, total = attPageShifts(t, env, admin, "?pageNo=1&pageSize=10&name=早班")
		if total != 1 || len(list) != 1 || list[0].Name != "早班" {
			t.Fatalf("名称筛选应只命中早班：total %d list %+v", total, list)
		}
		// 状态筛选：停用（1）只含晚班。
		_, _, list, total = attPageShifts(t, env, admin, "?pageNo=1&pageSize=10&status=1")
		if total != 1 || len(list) != 1 || list[0].Name != "晚班" {
			t.Fatalf("停用筛选应只命中晚班：total %d list %+v", total, list)
		}
		// 非法状态筛选值不炸接口（等同不过滤或空结果均可，只要求非 5xx）。
		status, _, _, _ := attPageShifts(t, env, admin, "?pageNo=1&pageSize=10&status=9")
		if status >= 500 {
			t.Fatalf("status=9 筛选不应 5xx，实际 %d", status)
		}
	})

	t.Run("更新班次", func(t *testing.T) {
		_, _, row := attCreateShift(t, env, admin, map[string]any{
			"name": "待改班次", "startTime": "08:00", "endTime": "17:00",
		})
		if code, msg := attUpdateShift(t, env, admin, row.ID, map[string]any{
			"name": "弹性班", "startTime": "09:30", "endTime": "18:30", "graceMinutes": 15, "sort": 7,
		}); code != http.StatusOK || msg != "" {
			t.Fatalf("更新班次：HTTP %d msg %q", code, msg)
		}
		_, _, got := attGetShift(t, env, admin, row.ID)
		if got.Name != "弹性班" || got.StartTime != "09:30" || got.EndTime != "18:30" || got.GraceMinutes != 15 || got.Sort != 7 {
			t.Fatalf("更新后字段不符: %+v", got)
		}
		// 更新不存在的班次 404。
		if code, _ := attUpdateShift(t, env, admin, 999999, map[string]any{
			"name": "幽灵", "startTime": "09:00", "endTime": "18:00",
		}); code != http.StatusNotFound {
			t.Fatalf("更新不存在班次应 404，实际 %d", code)
		}
	})

	t.Run("启停", func(t *testing.T) {
		_, _, row := attCreateShift(t, env, admin, map[string]any{
			"name": "启停班次", "startTime": "10:00", "endTime": "19:00",
		})
		if code, _ := attSetShiftStatus(t, env, admin, row.ID, 1); code != http.StatusOK {
			t.Fatalf("停用：HTTP %d", code)
		}
		if _, _, got := attGetShift(t, env, admin, row.ID); got.Status != 1 {
			t.Fatalf("停用后状态应 1，实际 %d", got.Status)
		}
		if code, _ := attSetShiftStatus(t, env, admin, row.ID, 0); code != http.StatusOK {
			t.Fatalf("重新启用：HTTP %d", code)
		}
		if _, _, got := attGetShift(t, env, admin, row.ID); got.Status != 0 {
			t.Fatalf("启用后状态应 0，实际 %d", got.Status)
		}
		// 非法状态值 400；不存在的班次 404。
		if code, _ := attSetShiftStatus(t, env, admin, row.ID, 2); code != http.StatusBadRequest {
			t.Fatalf("status=2 应 400，实际 %d", code)
		}
		if code, _ := attSetShiftStatus(t, env, admin, 999999, 1); code != http.StatusNotFound {
			t.Fatalf("启停不存在班次应 404，实际 %d", code)
		}
	})

	t.Run("删除与删除后不可读", func(t *testing.T) {
		_, _, row := attCreateShift(t, env, admin, map[string]any{
			"name": "待删班次", "startTime": "07:00", "endTime": "16:00",
		})
		if code, _ := attDeleteShift(t, env, admin, row.ID); code != http.StatusOK {
			t.Fatalf("删除：HTTP %d", code)
		}
		if code, _, _ := attGetShift(t, env, admin, row.ID); code != http.StatusNotFound {
			t.Fatalf("删除后详情应 404，实际 %d", code)
		}
		if code, _ := attDeleteShift(t, env, admin, row.ID); code != http.StatusNotFound {
			t.Fatalf("重复删除应 404，实际 %d", code)
		}
	})

	t.Run("非法字段表驱动400", func(t *testing.T) {
		cases := []struct {
			name string
			body map[string]any
		}{
			{"名称为空", map[string]any{"name": "", "startTime": "09:00", "endTime": "18:00"}},
			{"名称纯空格", map[string]any{"name": "   ", "startTime": "09:00", "endTime": "18:00"}},
			{"名称超长", map[string]any{"name": makeStringN(65), "startTime": "09:00", "endTime": "18:00"}},
			{"上班时间缺前导零", map[string]any{"name": "班次", "startTime": "9:00", "endTime": "18:00"}},
			{"上班时间带秒", map[string]any{"name": "班次", "startTime": "09:00:00", "endTime": "18:00"}},
			{"上班时间越界", map[string]any{"name": "班次", "startTime": "24:00", "endTime": "23:00"}},
			{"下班时间缺失", map[string]any{"name": "班次", "startTime": "09:00"}},
			{"下班等于上班", map[string]any{"name": "班次", "startTime": "09:00", "endTime": "09:00"}},
			{"下班早于上班跨天拒绝", map[string]any{"name": "夜班", "startTime": "22:00", "endTime": "06:00"}},
			{"宽限负数", map[string]any{"name": "班次", "startTime": "09:00", "endTime": "18:00", "graceMinutes": -1}},
			{"宽限超上限", map[string]any{"name": "班次", "startTime": "09:00", "endTime": "18:00", "graceMinutes": 1441}},
			{"排序负数", map[string]any{"name": "班次", "startTime": "09:00", "endTime": "18:00", "sort": -1}},
			{"排序超上限", map[string]any{"name": "班次", "startTime": "09:00", "endTime": "18:00", "sort": 10000}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				status, msg, _ := attCreateShift(t, env, admin, tc.body)
				if status != http.StatusBadRequest || msg == "" {
					t.Fatalf("%s 应 400 且有中文错误，实际 HTTP %d msg %q", tc.name, status, msg)
				}
			})
		}
		// 更新路径同套校验。
		_, _, row := attCreateShift(t, env, admin, map[string]any{
			"name": "更新校验班次", "startTime": "09:00", "endTime": "18:00",
		})
		status, _ := attUpdateShift(t, env, admin, row.ID, map[string]any{
			"name": "更新校验班次", "startTime": "18:00", "endTime": "09:00",
		})
		if status != http.StatusBadRequest {
			t.Fatalf("更新为跨天时段应 400，实际 %d", status)
		}
	})
}

// makeStringN 生成 n 个「班」字的字符串（名称超长用例）。
func makeStringN(n int) string {
	out := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, '班')
	}
	return string(out)
}

// ---------- Slice B：考勤组与组员（issue #60）----------

// attGroupRow 是考勤组行（详情含组员；分页仅带 memberCount）。
type attGroupRow struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	ShiftID     int64          `json:"shiftId"`
	Shift       *attShiftRow   `json:"shift"`
	Status      int8           `json:"status"`
	MemberCount int64          `json:"memberCount"`
	Members     []attMemberRow `json:"members"`
}

// attMemberRow 是组员条目（昵称经 system 网关回填）。
type attMemberRow struct {
	UserID   int64  `json:"userId"`
	Nickname string `json:"nickname"`
}

// attCreateGroup 新建考勤组，返回 (HTTP 状态, msg, 组行)。
func attCreateGroup(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int, string, attGroupRow) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/attendance/group", token, body)
	var row attGroupRow
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &row); err != nil {
			t.Fatalf("解析考勤组创建响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, row
}

// attGetGroup 读取考勤组详情（含组员），返回 (HTTP 状态, msg, 组行)。
func attGetGroup(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string, attGroupRow) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/attendance/group/%d", id), token, nil)
	var row attGroupRow
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &row); err != nil {
			t.Fatalf("解析考勤组详情响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, row
}

// attUpdateGroup 更新考勤组，返回 (HTTP 状态, msg)。
func attUpdateGroup(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, body map[string]any) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/attendance/group/%d", id), token, body)
	return status, api.Msg
}

// attSetGroupStatus 启停考勤组，返回 (HTTP 状态, msg)。
func attSetGroupStatus(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, status int8) (int, string) {
	t.Helper()
	code, api := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/attendance/group/%d/status", id), token, map[string]any{"status": status})
	return code, api.Msg
}

// attDeleteGroup 删除考勤组，返回 (HTTP 状态, msg)。
func attDeleteGroup(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	code, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/attendance/group/%d", id), token, nil)
	return code, api.Msg
}

// attPageGroups 分页查询考勤组，返回 (HTTP 状态, msg, 行列表, total)。
func attPageGroups(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, []attGroupRow, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/group"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil, 0
	}
	var page struct {
		List  []attGroupRow `json:"list"`
		Total int64         `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析考勤组分页响应: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, page.List, page.Total
}

// attAddMembers 按用户批量添加组员，返回 (HTTP 状态, msg, added)。
func attAddMembers(t *testing.T, env *testutil.IntegrationEnv, token string, groupID int64, userIDs []int64) (int, string, int) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/attendance/group/%d/members", groupID), token, map[string]any{"userIds": userIDs})
	added := 0
	if api.Code == 0 {
		var data struct {
			Added int `json:"added"`
		}
		if err := json.Unmarshal(api.Data, &data); err != nil {
			t.Fatalf("解析添加组员响应: %v（data=%s）", err, string(api.Data))
		}
		added = data.Added
	}
	return status, api.Msg, added
}

// attAddMembersByDept 按部门批量添加组员，返回 (HTTP 状态, msg, added)。
func attAddMembersByDept(t *testing.T, env *testutil.IntegrationEnv, token string, groupID int64, deptIDs []int64) (int, string, int) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/attendance/group/%d/members/by-dept", groupID), token, map[string]any{"deptIds": deptIDs})
	added := 0
	if api.Code == 0 {
		var data struct {
			Added int `json:"added"`
		}
		if err := json.Unmarshal(api.Data, &data); err != nil {
			t.Fatalf("解析按部门添加组员响应: %v（data=%s）", err, string(api.Data))
		}
		added = data.Added
	}
	return status, api.Msg, added
}

// attRemoveMember 移出组员，返回 (HTTP 状态, msg)。
func attRemoveMember(t *testing.T, env *testutil.IntegrationEnv, token string, groupID, userID int64) (int, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/attendance/group/%d/members/%d", groupID, userID), token, nil)
	return status, api.Msg
}

// memberIDs 提取组员用户 ID（升序，详情响应顺序即 user_id ASC）。
func memberIDs(row attGroupRow) []int64 {
	ids := make([]int64, 0, len(row.Members))
	for _, m := range row.Members {
		ids = append(ids, m.UserID)
	}
	return ids
}

// mustEqualIDs 断言两个 ID 集合完全一致。
func mustEqualIDs(t *testing.T, label string, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s：got %v，want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s：got %v，want %v", label, got, want)
		}
	}
}

// TestIntegrationAttendanceGroupMembers 考勤组 CRUD 与组员物化：
// 绑定班次必须存在、按用户/部门批量添加、同批去重与非法用户过滤、
// 一个用户只属于一个组（唯一键 + upsert 改绑）、删除约束（有组员不可删、
// 有组绑定的班次不可删）。
func TestIntegrationAttendanceGroupMembers(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	_, _, shiftA := attCreateShift(t, env, admin, map[string]any{
		"name": "组测试早班", "startTime": "09:00", "endTime": "18:00", "graceMinutes": 5,
	})
	_, _, shiftB := attCreateShift(t, env, admin, map[string]any{
		"name": "组测试晚班", "startTime": "13:00", "endTime": "21:00",
	})

	t.Run("创建与详情", func(t *testing.T) {
		// 绑定不存在的班次 404。
		status, msg, _ := attCreateGroup(t, env, admin, map[string]any{"name": "幽灵组", "shiftId": 999999})
		if status != http.StatusNotFound || msg == "" {
			t.Fatalf("绑定不存在班次应 404，实际 HTTP %d msg %q", status, msg)
		}
		if status, msg, _ := attCreateGroup(t, env, admin, map[string]any{"name": "", "shiftId": shiftA.ID}); status != http.StatusBadRequest || msg == "" {
			t.Fatalf("空名称应 400，实际 HTTP %d msg %q", status, msg)
		}
		status, msg, group := attCreateGroup(t, env, admin, map[string]any{"name": "研发考勤组", "shiftId": shiftA.ID})
		if status != http.StatusOK || msg != "" || group.ID <= 0 {
			t.Fatalf("创建考勤组：HTTP %d msg %q id %d", status, msg, group.ID)
		}
		if group.Name != "研发考勤组" || group.ShiftID != shiftA.ID || group.Status != 0 {
			t.Fatalf("创建响应字段不符: %+v", group)
		}
		if group.Shift == nil || group.Shift.ID != shiftA.ID || group.Shift.StartTime != "09:00" {
			t.Fatalf("创建响应应带绑定班次摘要: %+v", group.Shift)
		}
		if group.MemberCount != 0 || len(group.Members) != 0 {
			t.Fatalf("新组应无组员: count %d members %+v", group.MemberCount, group.Members)
		}
		// 详情回读一致。
		if status, msg, got := attGetGroup(t, env, admin, group.ID); status != http.StatusOK || got.Name != "研发考勤组" || got.ShiftID != shiftA.ID {
			t.Fatalf("考勤组详情：HTTP %d msg %q row %+v", status, msg, got)
		}
	})

	t.Run("更新与分页筛选", func(t *testing.T) {
		_, _, group := attCreateGroup(t, env, admin, map[string]any{"name": "待改组", "shiftId": shiftA.ID})
		if status, msg := attUpdateGroup(t, env, admin, group.ID, map[string]any{"name": "已改组", "shiftId": shiftB.ID}); status != http.StatusOK || msg != "" {
			t.Fatalf("更新考勤组：HTTP %d msg %q", status, msg)
		}
		if _, _, got := attGetGroup(t, env, admin, group.ID); got.Name != "已改组" || got.ShiftID != shiftB.ID {
			t.Fatalf("更新后字段不符: %+v", got)
		}
		// 更新绑定不存在的班次 404；更新不存在的组 404。
		if status, _ := attUpdateGroup(t, env, admin, group.ID, map[string]any{"name": "已改组", "shiftId": 999999}); status != http.StatusNotFound {
			t.Fatalf("更新绑定不存在班次应 404，实际 %d", status)
		}
		if status, _ := attUpdateGroup(t, env, admin, 999999, map[string]any{"name": "幽灵", "shiftId": shiftA.ID}); status != http.StatusNotFound {
			t.Fatalf("更新不存在组应 404，实际 %d", status)
		}
		// 名称模糊筛选。
		_, _, list, total := attPageGroups(t, env, admin, "?pageNo=1&pageSize=10&name=已改组")
		if total != 1 || len(list) != 1 || list[0].Name != "已改组" {
			t.Fatalf("名称筛选应只命中已改组：total %d list %+v", total, list)
		}
		// 启停与状态筛选。
		if status, _ := attSetGroupStatus(t, env, admin, group.ID, 1); status != http.StatusOK {
			t.Fatalf("停用组：HTTP %d", status)
		}
		_, _, list, total = attPageGroups(t, env, admin, "?pageNo=1&pageSize=10&status=1")
		if total != 1 || len(list) != 1 || list[0].ID != group.ID {
			t.Fatalf("停用筛选应只命中该组：total %d list %+v", total, list)
		}
		if status, _ := attSetGroupStatus(t, env, admin, group.ID, 2); status != http.StatusBadRequest {
			t.Fatalf("非法状态应 400，实际 %d", status)
		}
	})

	t.Run("按用户批量添加与唯一组约束", func(t *testing.T) {
		_, _, groupA := attCreateGroup(t, env, admin, map[string]any{"name": "唯一组A", "shiftId": shiftA.ID})
		_, _, groupB := attCreateGroup(t, env, admin, map[string]any{"name": "唯一组B", "shiftId": shiftB.ID})

		// 同批重复 + 不存在用户：仅启用且存在的用户入组（admin=1、demo=2）。
		status, msg, added := attAddMembers(t, env, admin, groupA.ID, []int64{2, 1, 2, 999999})
		if status != http.StatusOK || msg != "" || added != 2 {
			t.Fatalf("按用户添加：HTTP %d msg %q added %d，期望 2", status, msg, added)
		}
		_, _, got := attGetGroup(t, env, admin, groupA.ID)
		mustEqualIDs(t, "组A组员", memberIDs(got), []int64{1, 2})
		if got.MemberCount != 2 || got.Members[0].Nickname == "" || got.Members[1].Nickname == "" {
			t.Fatalf("组员昵称应回填: %+v", got.Members)
		}
		// 分页行只带人数不带组员明细（详情才带 Members）。
		if _, _, list, _ := attPageGroups(t, env, admin, "?pageNo=1&pageSize=10&name=唯一组A"); len(list) != 1 || list[0].MemberCount != 2 || len(list[0].Members) != 0 {
			t.Fatalf("分页行应仅带 memberCount: %+v", list)
		}
		// 重复添加幂等（upsert 不产生重复行）。
		if _, _, added = attAddMembers(t, env, admin, groupA.ID, []int64{1, 2}); added != 2 {
			t.Fatalf("重复添加应仍报 added=2，实际 %d", added)
		}
		_, _, got = attGetGroup(t, env, admin, groupA.ID)
		if got.MemberCount != 2 {
			t.Fatalf("重复添加后组员应仍为 2，实际 %d", got.MemberCount)
		}
		// 一个用户只属于一个组：把 demo(2) 加到组B → 从组A 改绑。
		if _, _, added = attAddMembers(t, env, admin, groupB.ID, []int64{2}); added != 1 {
			t.Fatalf("改绑添加应报 added=1，实际 %d", added)
		}
		_, _, gotA := attGetGroup(t, env, admin, groupA.ID)
		_, _, gotB := attGetGroup(t, env, admin, groupB.ID)
		mustEqualIDs(t, "改绑后组A组员", memberIDs(gotA), []int64{1})
		mustEqualIDs(t, "改绑后组B组员", memberIDs(gotB), []int64{2})

		// 数据库唯一键 (tenant_id, user_id) 兜底：绕过应用直插同租户同用户被拒。
		if err := env.DB.Exec(
			"INSERT INTO `oa_attendance_group_user` (`tenant_id`, `group_id`, `user_id`) VALUES (1, ?, 2)", groupA.ID,
		).Error; err == nil {
			t.Fatal("直插同一用户到第二个组应被唯一键拒绝")
		}

		// 移出：成功一次，再次 404。
		if status, msg := attRemoveMember(t, env, admin, groupB.ID, 2); status != http.StatusOK || msg != "" {
			t.Fatalf("移出组员：HTTP %d msg %q", status, msg)
		}
		if status, _ := attRemoveMember(t, env, admin, groupB.ID, 2); status != http.StatusNotFound {
			t.Fatalf("重复移出应 404，实际 %d", status)
		}
		if _, _, gotB = attGetGroup(t, env, admin, groupB.ID); gotB.MemberCount != 0 {
			t.Fatalf("移出后组B应无组员，实际 %d", gotB.MemberCount)
		}
		// 再次入组：软删行被 upsert 复活改绑，唯一键 (tenant_id, user_id) 不产生第二行。
		if _, _, added = attAddMembers(t, env, admin, groupA.ID, []int64{2}); added != 1 {
			t.Fatalf("移出后再入组应 added=1，实际 %d", added)
		}
		_, _, gotA = attGetGroup(t, env, admin, groupA.ID)
		mustEqualIDs(t, "复活改绑后组A组员", memberIDs(gotA), []int64{1, 2})
	})

	t.Run("按部门批量添加", func(t *testing.T) {
		_, _, group := attCreateGroup(t, env, admin, map[string]any{"name": "部门导入组", "shiftId": shiftA.ID})
		// 部门 101（研发部）只有 demo(2)。
		status, msg, added := attAddMembersByDept(t, env, admin, group.ID, []int64{101})
		if status != http.StatusOK || msg != "" || added != 1 {
			t.Fatalf("按部门添加：HTTP %d msg %q added %d", status, msg, added)
		}
		_, _, got := attGetGroup(t, env, admin, group.ID)
		mustEqualIDs(t, "部门导入组员", memberIDs(got), []int64{2})
		// 空部门集合 400；不存在/无成员的部门 400。
		if status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/attendance/group/%d/members/by-dept", group.ID), admin, map[string]any{"deptIds": []int64{}}); status != http.StatusBadRequest || api.Msg == "" {
			t.Fatalf("空部门集合应 400，实际 HTTP %d msg %q", status, api.Msg)
		}
		if status, _, _ := attAddMembersByDept(t, env, admin, group.ID, []int64{999999}); status != http.StatusBadRequest {
			t.Fatalf("无成员部门应 400，实际 %d", status)
		}
		// 空用户集合 400。
		if status, _, _ := attAddMembers(t, env, admin, group.ID, []int64{}); status != http.StatusBadRequest {
			t.Fatalf("空用户集合应 400，实际 %d", status)
		}
	})

	t.Run("删除约束", func(t *testing.T) {
		_, _, shiftC := attCreateShift(t, env, admin, map[string]any{
			"name": "删除约束班", "startTime": "09:00", "endTime": "18:00",
		})
		_, _, group := attCreateGroup(t, env, admin, map[string]any{"name": "删除约束组", "shiftId": shiftC.ID})
		// 班次被组绑定时不可删（409）。
		if status, msg := attDeleteShift(t, env, admin, shiftC.ID); status != http.StatusConflict || msg == "" {
			t.Fatalf("绑定中的班次删除应 409，实际 HTTP %d msg %q", status, msg)
		}
		// 组内有成员时不可删（409）。
		if _, _, added := attAddMembers(t, env, admin, group.ID, []int64{1}); added != 1 {
			t.Fatalf("添加组员失败: added %d", added)
		}
		if status, msg := attDeleteGroup(t, env, admin, group.ID); status != http.StatusConflict || msg == "" {
			t.Fatalf("有组员的组删除应 409，实际 HTTP %d msg %q", status, msg)
		}
		// 移出成员后可删；删除后详情 404，绑定解除后班次可删。
		if status, _ := attRemoveMember(t, env, admin, group.ID, 1); status != http.StatusOK {
			t.Fatalf("移出组员：HTTP %d", status)
		}
		if status, msg := attDeleteGroup(t, env, admin, group.ID); status != http.StatusOK || msg != "" {
			t.Fatalf("删除空组：HTTP %d msg %q", status, msg)
		}
		if status, _, _ := attGetGroup(t, env, admin, group.ID); status != http.StatusNotFound {
			t.Fatalf("删除后详情应 404，实际 %d", status)
		}
		if status, msg := attDeleteShift(t, env, admin, shiftC.ID); status != http.StatusOK || msg != "" {
			t.Fatalf("解绑后删除班次：HTTP %d msg %q", status, msg)
		}
	})
}

// ---------- Slice C：Web 打卡（issue #60）----------

// attPunchRow 是打卡记录行。
type attPunchRow struct {
	ID         int64  `json:"id"`
	Type       int8   `json:"type"`
	TypeName   string `json:"typeName"`
	Date       string `json:"date"`
	Time       string `json:"time"`
	Source     int8   `json:"source"`
	SourceName string `json:"sourceName"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
	GroupID    int64  `json:"groupId"`
	GroupName  string `json:"groupName"`
}

// attPunch 上班/下班打卡（kind = in/out），返回 (HTTP 状态, msg, 打卡行)。
func attPunch(t *testing.T, env *testutil.IntegrationEnv, token, kind string) (int, string, attPunchRow) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/attendance/punch/"+kind, token, map[string]any{})
	var row attPunchRow
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &row); err != nil {
			t.Fatalf("解析打卡响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, row
}

// attPunchWithUA 带自定义 User-Agent 打卡（原生请求，testutil.Request 不带 UA 头）。
func attPunchWithUA(t *testing.T, env *testutil.IntegrationEnv, token, kind, ua string) (int, testutil.APIResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/oa/attendance/punch/"+kind), nil)
	if err != nil {
		t.Fatalf("构造打卡请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", ua)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("发送打卡请求: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析打卡响应: %v", err)
	}
	return resp.StatusCode, api
}

// attPunchToday 读取今日打卡状态。
func attPunchToday(t *testing.T, env *testutil.IntegrationEnv, token string) (int, string, struct {
	Date    string         `json:"date"`
	Group   *attPunchGroup `json:"group"`
	Punches []attPunchRow  `json:"punches"`
}) {
	t.Helper()
	var out struct {
		Date    string         `json:"date"`
		Group   *attPunchGroup `json:"group"`
		Punches []attPunchRow  `json:"punches"`
	}
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/punch/today", token, nil)
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &out); err != nil {
			t.Fatalf("解析今日打卡响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, out
}

// attPunchGroup 是今日打卡状态里的组/班次信息。
type attPunchGroup struct {
	ID     int64        `json:"id"`
	Name   string       `json:"name"`
	Status int8         `json:"status"`
	Shift  *attShiftRow `json:"shift"`
}

// attPageMyPunches 我的打卡记录分页。
func attPageMyPunches(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, []attPunchRow, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/punch/page"+query, token, nil)
	if api.Code != 0 {
		return status, nil, 0
	}
	var page struct {
		List  []attPunchRow `json:"list"`
		Total int64         `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析我的打卡分页: %v（data=%s）", err, string(api.Data))
	}
	return status, page.List, page.Total
}

// attSetupPunchedUser 建班次+考勤组并把用户物化入组，返回组行。
func attSetupPunchedUser(t *testing.T, env *testutil.IntegrationEnv, admin string, userIDs []int64) attGroupRow {
	t.Helper()
	_, _, shift := attCreateShift(t, env, admin, map[string]any{
		"name": "打卡测试班", "startTime": "09:00", "endTime": "18:00", "graceMinutes": 5,
	})
	status, msg, group := attCreateGroup(t, env, admin, map[string]any{"name": "打卡测试组", "shiftId": shift.ID})
	if status != http.StatusOK {
		t.Fatalf("建打卡测试组：HTTP %d msg %q", status, msg)
	}
	if _, msg, added := attAddMembers(t, env, admin, group.ID, userIDs); added != len(userIDs) {
		t.Fatalf("打卡测试组添加组员 %v 失败：added %d msg %q", userIDs, added, msg)
	}
	return group
}

// TestIntegrationAttendancePunch 打卡主链：无考勤组 400、同一天多次均保留、
// 记录 IP/User-Agent、今日状态与我的打卡记录、组停用 400、未登录 401。
func TestIntegrationAttendancePunch(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")
	loc := env.Config.Location()
	wantDate := time.Now().In(loc).Format("2006-01-02")

	t.Run("无考勤组打卡400", func(t *testing.T) {
		// demo 未加入任何组（种子默认组无组员）。
		status, msg, _ := attPunch(t, env, demo, "in")
		if status != http.StatusBadRequest || !strings.Contains(msg, "考勤组") {
			t.Fatalf("无组打卡应 400 且提示考勤组，实际 HTTP %d msg %q", status, msg)
		}
		// 未登录 401。
		if status, _, _ := attPunch(t, env, "", "in"); status != http.StatusUnauthorized {
			t.Fatalf("未登录打卡应 401，实际 %d", status)
		}
		// 今日状态不报错：group 为 null、punches 为空数组。
		status, _, today := attPunchToday(t, env, demo)
		if status != http.StatusOK || today.Date != wantDate || today.Group != nil || len(today.Punches) != 0 {
			t.Fatalf("未入组今日状态：HTTP %d date %q group %+v punches %d", status, today.Date, today.Group, len(today.Punches))
		}
	})

	group := attSetupPunchedUser(t, env, admin, []int64{2})

	t.Run("多次打卡均保留", func(t *testing.T) {
		status, msg, first := attPunch(t, env, demo, "in")
		if status != http.StatusOK || msg != "" || first.ID <= 0 {
			t.Fatalf("上班打卡：HTTP %d msg %q id %d", status, msg, first.ID)
		}
		if first.Type != 0 || first.TypeName != "上班" || first.Source != 0 || first.SourceName != "Web" {
			t.Fatalf("上班打卡字段不符: %+v", first)
		}
		if first.Date != wantDate || first.GroupID != group.ID || first.GroupName != "打卡测试组" {
			t.Fatalf("打卡日期/所属组不符: %+v", first)
		}
		if _, err := time.Parse(time.RFC3339, first.Time); err != nil {
			t.Fatalf("打卡时刻应为 RFC3339: %q（%v）", first.Time, err)
		}
		if first.IP == "" {
			t.Fatalf("打卡应记录 IP: %+v", first)
		}
		// 同一天多次：下班、再上班都保留（不覆盖）。
		status, _, second := attPunch(t, env, demo, "out")
		if status != http.StatusOK || second.Type != 1 || second.ID == first.ID {
			t.Fatalf("下班打卡：HTTP %d row %+v", status, second)
		}
		status, _, third := attPunch(t, env, demo, "in")
		if status != http.StatusOK || third.Type != 0 || third.ID == first.ID || third.ID == second.ID {
			t.Fatalf("再次上班打卡：HTTP %d row %+v", status, third)
		}

		// 今日状态：组信息 + 三条打卡（升序）。
		status, _, today := attPunchToday(t, env, demo)
		if status != http.StatusOK || today.Date != wantDate || today.Group == nil {
			t.Fatalf("今日状态：HTTP %d date %q group %+v", status, today.Date, today.Group)
		}
		if today.Group.ID != group.ID || today.Group.Name != "打卡测试组" || today.Group.Status != 0 {
			t.Fatalf("今日状态组信息不符: %+v", today.Group)
		}
		if today.Group.Shift == nil || today.Group.Shift.StartTime != "09:00" || today.Group.Shift.GraceMinutes != 5 {
			t.Fatalf("今日状态班次信息不符: %+v", today.Group.Shift)
		}
		if len(today.Punches) != 3 {
			t.Fatalf("今日应保留 3 条打卡，实际 %d", len(today.Punches))
		}
		if today.Punches[0].ID != first.ID || today.Punches[1].ID != second.ID || today.Punches[2].ID != third.ID {
			t.Fatalf("今日打卡应按时刻升序: %+v", today.Punches)
		}

		// 我的打卡记录：总数 3，时间降序（最新在前）。
		status, list, total := attPageMyPunches(t, env, demo, "?pageNo=1&pageSize=10")
		if status != http.StatusOK || total != 3 || len(list) != 3 {
			t.Fatalf("我的打卡记录：HTTP %d total %d len %d", status, total, len(list))
		}
		if list[0].ID != third.ID || list[2].ID != first.ID {
			t.Fatalf("我的打卡记录应时间降序: %+v", list)
		}
		// 日期区间筛选：今天命中，昨天为空。
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		_, _, total = attPageMyPunches(t, env, demo, "?pageNo=1&pageSize=10&startDate="+wantDate+"&endDate="+wantDate)
		if total != 3 {
			t.Fatalf("当日筛选应 3 条，实际 %d", total)
		}
		_, _, total = attPageMyPunches(t, env, demo, "?pageNo=1&pageSize=10&startDate="+yesterday+"&endDate="+yesterday)
		if total != 0 {
			t.Fatalf("昨日筛选应为空，实际 %d", total)
		}

		// spec §5 统一入口 POST /oa/attendance/punch（body.type）与动作路径等价；
		// 非法 type 400 且不落记录。
		status, api := env.Request(t, http.MethodPost, "/oa/attendance/punch", demo, map[string]any{"type": 1})
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("统一入口打卡：HTTP %d code %d msg %q", status, api.Code, api.Msg)
		}
		var unified attPunchRow
		if err := json.Unmarshal(api.Data, &unified); err != nil || unified.Type != 1 {
			t.Fatalf("统一入口打卡响应: %v（data=%s）", err, string(api.Data))
		}
		if status, _ := env.Request(t, http.MethodPost, "/oa/attendance/punch", demo, map[string]any{"type": 2}); status != http.StatusBadRequest {
			t.Fatalf("非法打卡类型应 400，实际 %d", status)
		}
		if _, _, total = attPageMyPunches(t, env, demo, "?pageNo=1&pageSize=10"); total != 4 {
			t.Fatalf("统一入口打卡后应 4 条记录，实际 %d", total)
		}
	})

	t.Run("记录UserAgent", func(t *testing.T) {
		ua := "AttendanceIntegrationTest/1.0"
		status, api := attPunchWithUA(t, env, demo, "out", ua)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("带 UA 打卡：HTTP %d code %d msg %q", status, api.Code, api.Msg)
		}
		var row attPunchRow
		if err := json.Unmarshal(api.Data, &row); err != nil {
			t.Fatalf("解析打卡响应: %v", err)
		}
		if row.UserAgent != ua {
			t.Fatalf("User-Agent 应记录 %q，实际 %q", ua, row.UserAgent)
		}
		// 我的记录里也能读到。
		_, list, _ := attPageMyPunches(t, env, demo, "?pageNo=1&pageSize=10")
		if len(list) == 0 || list[0].UserAgent != ua {
			t.Fatalf("打卡记录应保留 User-Agent: %+v", list)
		}
	})

	t.Run("组停用打卡400", func(t *testing.T) {
		if status, _ := attSetGroupStatus(t, env, admin, group.ID, 1); status != http.StatusOK {
			t.Fatalf("停用组：HTTP %d", status)
		}
		status, msg, _ := attPunch(t, env, demo, "in")
		if status != http.StatusBadRequest || !strings.Contains(msg, "停用") {
			t.Fatalf("组停用打卡应 400 且提示停用，实际 HTTP %d msg %q", status, msg)
		}
		// 今日状态仍可读，组状态标 1（页面据此禁用按钮）。
		_, _, today := attPunchToday(t, env, demo)
		if today.Group == nil || today.Group.Status != 1 {
			t.Fatalf("停用组今日状态应带 status=1: %+v", today.Group)
		}
		// 重新启用后可打卡。
		if status, _ := attSetGroupStatus(t, env, admin, group.ID, 0); status != http.StatusOK {
			t.Fatalf("启用组：HTTP %d", status)
		}
		if status, msg, _ := attPunch(t, env, demo, "in"); status != http.StatusOK || msg != "" {
			t.Fatalf("重新启用后打卡：HTTP %d msg %q", status, msg)
		}
	})

	t.Run("管理员未入组仍400", func(t *testing.T) {
		status, msg, _ := attPunch(t, env, admin, "in")
		if status != http.StatusBadRequest || !strings.Contains(msg, "考勤组") {
			t.Fatalf("管理员未入组打卡应 400，实际 HTTP %d msg %q", status, msg)
		}
	})
}

// ---------- 权限码与租户隔离（issue #60，spec §4.6）----------

// attCreateRole 创建角色并回读 ID（仅授予 menuIDs 携带的权限码）。
func attCreateRole(t *testing.T, env *testutil.IntegrationEnv, admin, name, code string, menuIDs []int64) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/role", admin, map[string]any{
		"name": name, "code": code, "dataScope": 5, "status": 0, "menuIds": menuIDs,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建角色 %s：HTTP %d code %d msg %q", code, status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/role/page?code="+code+"&pageNo=1&pageSize=10", admin, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("查询角色 %s: %v（data=%s）", code, err, string(api.Data))
	}
	return page.List[0].ID
}

// attCreateUser 创建用户（归属研发部 101）并登录，返回 (用户 ID, token)。
func attCreateUser(t *testing.T, env *testutil.IntegrationEnv, admin, username string, roleID int64) (int64, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "nickname": username, "password": "123456",
		"deptId": 101, "status": 0, "roleIds": []int64{roleID},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", admin, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("查询用户 %s: %v（data=%s）", username, err, string(api.Data))
	}
	return page.List[0].ID, env.Login(t, username, "123456")
}

// TestIntegrationAttendanceMenus 菜单种子（527/530/531）与权限码落库：
// 管理端两页各带独立维护码，员工自助页无码且可见。
func TestIntegrationAttendanceMenus(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cases := []struct {
		id        int64
		name      string
		perm      string
		component string
	}{
		{menuAttShift, "班次管理", permAttShiftManage, "oa/attendance/shift"},
		{menuAttGroup, "考勤组管理", permAttGroupManage, "oa/attendance/group"},
		{menuAttPunch, "我的考勤", "", "oa/attendance/punch"},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/system/menu/%d", tc.id), admin, nil)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("菜单 %d：HTTP %d code %d msg %q", tc.id, status, api.Code, api.Msg)
		}
		var menu struct {
			ID         int64  `json:"id"`
			ParentID   int64  `json:"parentId"`
			Name       string `json:"name"`
			Component  string `json:"component"`
			Permission string `json:"permission"`
			Visible    bool   `json:"visible"`
		}
		if err := json.Unmarshal(api.Data, &menu); err != nil {
			t.Fatalf("解析菜单 %d: %v", tc.id, err)
		}
		if menu.ID != tc.id || menu.Name != tc.name || menu.ParentID != 500 || menu.Component != tc.component || menu.Permission != tc.perm || !menu.Visible {
			t.Fatalf("菜单 %d 种子不符: %+v", tc.id, menu)
		}
	}
}

// TestIntegrationAttendancePermissionCodes 权限码逐码控权（spec §5/§7.2）：
// 527 班次管理（oa:attendance:shift:manage）、530 考勤组管理
// （oa:attendance:group:manage）各自独立；无码 403；打卡以登录身份收口
// （无码用户入组后仍可打卡，与 bpm 待办动作同款）。
func TestIntegrationAttendancePermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	shiftOnlyID, shiftOnly := attCreateUser(t, env, admin, "att_shift_only",
		attCreateRole(t, env, admin, "考勤班次角色", "att_shift_role", []int64{menuAttShift}))
	groupOnlyID, groupOnly := attCreateUser(t, env, admin, "att_group_only",
		attCreateRole(t, env, admin, "考勤组角色", "att_group_role", []int64{menuAttGroup}))
	noPermID, noPerm := attCreateUser(t, env, admin, "att_none",
		attCreateRole(t, env, admin, "考勤无码角色", "att_none_role", []int64{1}))

	// 班次码：班次可读写，考勤组 403。
	if status, _, _, _ := attPageShifts(t, env, shiftOnly, "?pageNo=1&pageSize=10"); status != http.StatusOK {
		t.Fatalf("班次码用户读班次应 200，实际 %d", status)
	}
	if status, msg, _ := attCreateShift(t, env, shiftOnly, map[string]any{"name": "班次码班次", "startTime": "09:00", "endTime": "18:00"}); status != http.StatusOK || msg != "" {
		t.Fatalf("班次码用户建班次：HTTP %d msg %q", status, msg)
	}
	if status, _, _, _ := attPageGroups(t, env, shiftOnly, "?pageNo=1&pageSize=10"); status != http.StatusForbidden {
		t.Fatalf("班次码用户读考勤组应 403，实际 %d", status)
	}
	if status, _, _ := attCreateGroup(t, env, shiftOnly, map[string]any{"name": "越权组", "shiftId": 1}); status != http.StatusForbidden {
		t.Fatalf("班次码用户建考勤组应 403，实际 %d", status)
	}

	// 考勤组码：组可读写，班次 403。
	if status, _, _, _ := attPageGroups(t, env, groupOnly, "?pageNo=1&pageSize=10"); status != http.StatusOK {
		t.Fatalf("组码用户读考勤组应 200，实际 %d", status)
	}
	if status, _, _ := attCreateGroup(t, env, groupOnly, map[string]any{"name": "组码组", "shiftId": 1}); status != http.StatusOK {
		t.Fatalf("组码用户建组：HTTP %d", status)
	}
	if status, _, _, _ := attPageShifts(t, env, groupOnly, "?pageNo=1&pageSize=10"); status != http.StatusForbidden {
		t.Fatalf("组码用户读班次应 403，实际 %d", status)
	}

	// 无码：两域均 403。
	if status, _, _, _ := attPageShifts(t, env, noPerm, "?pageNo=1&pageSize=10"); status != http.StatusForbidden {
		t.Fatalf("无码用户读班次应 403，实际 %d", status)
	}
	if status, _, _, _ := attPageGroups(t, env, noPerm, "?pageNo=1&pageSize=10"); status != http.StatusForbidden {
		t.Fatalf("无码用户读考勤组应 403，实际 %d", status)
	}

	// 打卡登录即可用：管理员把三个用户都加入组，无码用户也能打卡。
	group := attSetupPunchedUser(t, env, admin, []int64{shiftOnlyID, groupOnlyID, noPermID})
	if status, msg, _ := attPunch(t, env, noPerm, "in"); status != http.StatusOK || msg != "" {
		t.Fatalf("无码用户（已入组）打卡应 200，实际 HTTP %d msg %q", status, msg)
	}
	if status, _, today := attPunchToday(t, env, noPerm); status != http.StatusOK || today.Group == nil || today.Group.ID != group.ID {
		t.Fatalf("无码用户今日状态：HTTP %d group %+v", status, today.Group)
	}
}

// TestIntegrationAttendanceTenantIsolation 跨租户：班次/考勤组详情、修改、
// 删除、组员添加 404（不泄露存在性）；列表不可见；租户内数据互不串。
func TestIntegrationAttendanceTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	_, _, shift := attCreateShift(t, env, admin, map[string]any{
		"name": "隔离班次", "startTime": "09:00", "endTime": "18:00",
	})
	_, _, group := attCreateGroup(t, env, admin, map[string]any{"name": "隔离考勤组", "shiftId": shift.ID})
	if _, _, added := attAddMembers(t, env, admin, group.ID, []int64{2}); added != 1 {
		t.Fatalf("租户 1 添加组员失败: added %d", added)
	}

	tenant2 := createOATenant(t, env, admin, "attendance_isolate_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)

	// 列表不可见。
	if _, _, _, total := attPageShifts(t, env, token2, "?pageNo=1&pageSize=10&name=隔离班次"); total != 0 {
		t.Fatalf("跨租户班次列表应为空，实际 total=%d", total)
	}
	if _, _, _, total := attPageGroups(t, env, token2, "?pageNo=1&pageSize=10&name=隔离考勤组"); total != 0 {
		t.Fatalf("跨租户考勤组列表应为空，实际 total=%d", total)
	}
	// 详情/修改/启停/删除/组员操作全部 404。
	if status, _, _ := attGetShift(t, env, token2, shift.ID); status != http.StatusNotFound {
		t.Fatalf("跨租户班次详情应 404，实际 %d", status)
	}
	if status, _ := attUpdateShift(t, env, token2, shift.ID, map[string]any{"name": "越权", "startTime": "09:00", "endTime": "18:00"}); status != http.StatusNotFound {
		t.Fatalf("跨租户班次修改应 404，实际 %d", status)
	}
	if status, _ := attSetShiftStatus(t, env, token2, shift.ID, 1); status != http.StatusNotFound {
		t.Fatalf("跨租户班次启停应 404，实际 %d", status)
	}
	if status, _ := attDeleteShift(t, env, token2, shift.ID); status != http.StatusNotFound {
		t.Fatalf("跨租户班次删除应 404，实际 %d", status)
	}
	if status, _, _ := attGetGroup(t, env, token2, group.ID); status != http.StatusNotFound {
		t.Fatalf("跨租户考勤组详情应 404，实际 %d", status)
	}
	if status, _ := attUpdateGroup(t, env, token2, group.ID, map[string]any{"name": "越权", "shiftId": 1}); status != http.StatusNotFound {
		t.Fatalf("跨租户考勤组修改应 404，实际 %d", status)
	}
	if status, _ := attSetGroupStatus(t, env, token2, group.ID, 1); status != http.StatusNotFound {
		t.Fatalf("跨租户考勤组启停应 404，实际 %d", status)
	}
	if status, _ := attDeleteGroup(t, env, token2, group.ID); status != http.StatusNotFound {
		t.Fatalf("跨租户考勤组删除应 404，实际 %d", status)
	}
	if status, _, _ := attAddMembers(t, env, token2, group.ID, []int64{1}); status != http.StatusNotFound {
		t.Fatalf("跨租户添加组员应 404，实际 %d", status)
	}
	if status, _ := attRemoveMember(t, env, token2, group.ID, 2); status != http.StatusNotFound {
		t.Fatalf("跨租户移出组员应 404，实际 %d", status)
	}

	// 租户 2 可建同名班次/组，互不冲突。
	status, msg, shift2 := attCreateShift(t, env, token2, map[string]any{
		"name": "隔离班次", "startTime": "08:00", "endTime": "17:00",
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("租户 2 建同名班次：HTTP %d msg %q", status, msg)
	}
	if status, msg, group2 := attCreateGroup(t, env, token2, map[string]any{"name": "隔离考勤组", "shiftId": shift2.ID}); status != http.StatusOK || msg != "" || group2.ID == group.ID {
		t.Fatalf("租户 2 建同名考勤组：HTTP %d msg %q id %d", status, msg, group2.ID)
	}

	// 租户 1 数据未被影响：组员仍只有 demo。
	_, _, got := attGetGroup(t, env, admin, group.ID)
	mustEqualIDs(t, "租户 1 组员", memberIDs(got), []int64{2})
}
