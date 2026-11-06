//go:build integration

// 考勤结果与校正的 HTTP 集成用例（issue #61，docs/oa-mvp-spec.md §4.10）：
// 派生正确性（迟到/早退/迟到且早退/缺卡/休息/宽限/最早上班最晚下班）、重算
// 幂等、校正优先且保留原始打卡、可见范围（本人/部门子树/全租户）、权限码、
// 跨租户 404、打卡触发重算、每日任务生成前一日结果（直接调任务函数）。
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t61，-p 1 串行）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// ---- 考勤结果测试基建（issue #61）----

// 菜单 ID 与权限码（种子 533 段，grep 去重后取号并报备协调台账）：
// 533 考勤结果（查询）、5331 校正/重算、5332 全租户可见（oa:attendance:manage）。
const (
	menuAttResult        = int64(533)
	menuAttResultCorrect = int64(5331)
	menuAttResultAll     = int64(5332)

	permAttResultQuery   = "oa:attendance:result:query"
	permAttResultCorrect = "oa:attendance:result:correct"
	permAttManage        = "oa:attendance:manage"
)

// attResultRow 是考勤结果响应行（页面/断言共读）。
type attResultRow struct {
	ID                int64   `json:"id"`
	UserID            int64   `json:"userId"`
	Nickname          string  `json:"nickname"`
	WorkDate          string  `json:"workDate"`
	InTime            string  `json:"inTime"`
	OutTime           string  `json:"outTime"`
	LateMinutes       int     `json:"lateMinutes"`
	EarlyMinutes      int     `json:"earlyMinutes"`
	MissingIn         bool    `json:"missingIn"`
	MissingOut        bool    `json:"missingOut"`
	LeaveDays         float64 `json:"leaveDays"`
	Status            int8    `json:"status"`
	StatusName        string  `json:"statusName"`
	DerivedStatus     int8    `json:"derivedStatus"`
	DerivedStatusName string  `json:"derivedStatusName"`
	Corrected         bool    `json:"corrected"`
	OverrideStatus    *int8   `json:"overrideStatus"`
	OverrideRemark    string  `json:"overrideRemark"`
	ComputedAt        string  `json:"computedAt"`
}

// attInsertPunch 直接落一条指定时刻的打卡（测试需要可控时间；打卡 API 取
// 真实 now，只能覆盖「正常」路径）。tenantID 显式指定以支持跨租户用例。
func attInsertPunch(t *testing.T, env *testutil.IntegrationEnv, tenantID, userID int64, date string, hour, minute int, kind int8) {
	t.Helper()
	loc := env.Config.Location()
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		t.Fatalf("解析打卡日期 %q: %v", date, err)
	}
	// 打卡时刻按业务时区构造：MySQL 驱动按 DSN loc 落库，墙钟与业务时区一致。
	punchTime := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
	row := &oamodel.AttendancePunch{
		TenantID: tenantID, UserID: userID, PunchDate: day, PunchTime: punchTime,
		Type: kind, Source: oamodel.PunchSourceWeb, IP: "127.0.0.1",
	}
	if err := tenant.Skip(env.DB).Create(row).Error; err != nil {
		t.Fatalf("写入打卡（%s %02d:%02d type=%d）: %v", date, hour, minute, kind, err)
	}
}

// attPageResults 管理端结果分页，返回 (HTTP 状态, msg, 行列表, total)。
func attPageResults(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, []attResultRow, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/result/page"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil, 0
	}
	var page struct {
		List  []attResultRow `json:"list"`
		Total int64          `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析考勤结果分页: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, page.List, page.Total
}

// attPageMyResults 我的考勤结果分页。
func attPageMyResults(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, []attResultRow, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/result/mine"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil, 0
	}
	var page struct {
		List  []attResultRow `json:"list"`
		Total int64          `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析我的考勤结果分页: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, page.List, page.Total
}

// attRecompute 手工重算，返回 (HTTP 状态, msg, days)。
func attRecompute(t *testing.T, env *testutil.IntegrationEnv, token string, userID int64, start, end string) (int, string, int) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/attendance/result/recompute", token, map[string]any{
		"userId": userID, "startDate": start, "endDate": end,
	})
	var data struct {
		Days int `json:"days"`
	}
	if api.Code == 0 {
		if err := json.Unmarshal(api.Data, &data); err != nil {
			t.Fatalf("解析重算响应: %v（data=%s）", err, string(api.Data))
		}
	}
	return status, api.Msg, data.Days
}

// attCorrect 管理员校正，返回 (HTTP 状态, msg)。
func attCorrect(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, status int8, remark string) (int, string) {
	t.Helper()
	code, api := env.Request(t, http.MethodPut, fmt.Sprintf("/oa/attendance/result/%d/correct", id), token, map[string]any{
		"overrideStatus": status, "remark": remark,
	})
	return code, api.Msg
}

// attClearCorrect 撤销校正，返回 (HTTP 状态, msg)。
func attClearCorrect(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, string) {
	t.Helper()
	code, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/attendance/result/%d/correct", id), token, nil)
	return code, api.Msg
}

// attSetWorkday 设置单日工作日历（type：0 工作日 / 1 休息日 / 2 节假日）。
func attSetWorkday(t *testing.T, env *testutil.IntegrationEnv, token, date string, dayType int8) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, "/oa/workday", token, map[string]any{
		"date": date, "type": dayType, "remark": "考勤结果测试",
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("设置工作日历 %s => %d：HTTP %d code %d msg %q", date, dayType, status, api.Code, api.Msg)
	}
}

// attResultsByDate 把结果行按日期索引（同日该用户至多一行）。
func attResultsByDate(rows []attResultRow) map[string]attResultRow {
	out := make(map[string]attResultRow, len(rows))
	for _, row := range rows {
		out[row.WorkDate] = row
	}
	return out
}

// attResultSetup 建 09:00–18:00/宽限 10 的班次与考勤组，并把用户物化入组。
func attResultSetup(t *testing.T, env *testutil.IntegrationEnv, admin string, userIDs []int64) {
	t.Helper()
	_, _, shift := attCreateShift(t, env, admin, map[string]any{
		"name": "结果测试班", "startTime": "09:00", "endTime": "18:00", "graceMinutes": 10,
	})
	status, msg, group := attCreateGroup(t, env, admin, map[string]any{"name": "结果测试组", "shiftId": shift.ID})
	if status != http.StatusOK {
		t.Fatalf("建结果测试组：HTTP %d msg %q", status, msg)
	}
	if _, msg, added := attAddMembers(t, env, admin, group.ID, userIDs); added != len(userIDs) {
		t.Fatalf("结果测试组添加组员 %v 失败：added %d msg %q", userIDs, added, msg)
	}
}

// TestIntegrationAttendanceResultDerivation 派生主链：最早上班/最晚下班、
// 宽限边界、迟到/早退/迟到且早退/缺卡/休息、工作日历显式记录优先。
func TestIntegrationAttendanceResultDerivation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	const demoID = int64(2)
	attResultSetup(t, env, admin, []int64{demoID})

	// 2026-03-02 周一 ~ 03-10 周二（03-07/03-08 为周末）。
	in := oamodel.PunchTypeIn
	out := oamodel.PunchTypeOut
	// 正常：08:55 上班（早于班次）18:05 下班。
	attInsertPunch(t, env, 1, demoID, "2026-03-02", 8, 55, in)
	attInsertPunch(t, env, 1, demoID, "2026-03-02", 18, 5, out)
	// 迟到且早退：09:20（超宽限 10 分钟）/ 17:40（早退 10 分钟）。
	attInsertPunch(t, env, 1, demoID, "2026-03-03", 9, 20, in)
	attInsertPunch(t, env, 1, demoID, "2026-03-03", 17, 40, out)
	// 缺上班卡：只有下班卡。
	attInsertPunch(t, env, 1, demoID, "2026-03-04", 18, 0, out)
	// 缺下班卡：只有上班卡。
	attInsertPunch(t, env, 1, demoID, "2026-03-05", 9, 0, in)
	// 多次打卡：上班取最早 08:50、下班取最晚 18:10。
	attInsertPunch(t, env, 1, demoID, "2026-03-06", 9, 30, in)
	attInsertPunch(t, env, 1, demoID, "2026-03-06", 8, 50, in)
	attInsertPunch(t, env, 1, demoID, "2026-03-06", 17, 0, out)
	attInsertPunch(t, env, 1, demoID, "2026-03-06", 18, 10, out)
	// 周六有打卡：只判休息，不算迟到早退。
	attInsertPunch(t, env, 1, demoID, "2026-03-07", 9, 0, in)
	attInsertPunch(t, env, 1, demoID, "2026-03-07", 18, 0, out)
	// 2026-03-09 工作日无卡 → 双缺。
	// 2026-03-10 宽限上界：09:10 / 17:50 → 正常。
	attInsertPunch(t, env, 1, demoID, "2026-03-10", 9, 10, in)
	attInsertPunch(t, env, 1, demoID, "2026-03-10", 17, 50, out)

	status, msg, days := attRecompute(t, env, admin, demoID, "2026-03-02", "2026-03-10")
	if status != http.StatusOK || msg != "" || days != 9 {
		t.Fatalf("重算 9 天：HTTP %d msg %q days %d", status, msg, days)
	}

	_, _, rows, total := attPageResults(t, env, admin, "?pageNo=1&pageSize=50&userId=2&startDate=2026-03-02&endDate=2026-03-10")
	if total != 9 {
		t.Fatalf("结果行数应为 9，实际 %d", total)
	}
	got := attResultsByDate(rows)

	if row := got["2026-03-02"]; row.Status != oamodel.AttendanceResultNormal ||
		row.LateMinutes != 0 || row.EarlyMinutes != 0 ||
		row.InTime != "2026-03-02 08:55:00" || row.OutTime != "2026-03-02 18:05:00" {
		t.Fatalf("正常日结果不符：%+v", row)
	}
	if row := got["2026-03-03"]; row.Status != oamodel.AttendanceResultLateAndEarly ||
		row.LateMinutes != 10 || row.EarlyMinutes != 10 {
		t.Fatalf("迟到且早退结果不符：%+v", row)
	}
	if row := got["2026-03-04"]; row.Status != oamodel.AttendanceResultMissing || !row.MissingIn || row.MissingOut {
		t.Fatalf("缺上班卡结果不符：%+v", row)
	}
	if row := got["2026-03-05"]; row.Status != oamodel.AttendanceResultMissing || row.MissingIn || !row.MissingOut {
		t.Fatalf("缺下班卡结果不符：%+v", row)
	}
	if row := got["2026-03-06"]; row.Status != oamodel.AttendanceResultNormal ||
		row.InTime != "2026-03-06 08:50:00" || row.OutTime != "2026-03-06 18:10:00" {
		t.Fatalf("多次打卡应取最早上班/最晚下班：%+v", row)
	}
	if row := got["2026-03-07"]; row.Status != oamodel.AttendanceResultRest ||
		row.LateMinutes != 0 || row.EarlyMinutes != 0 || row.MissingIn || row.MissingOut {
		t.Fatalf("休息日有打卡也应只判休息：%+v", row)
	}
	if row := got["2026-03-08"]; row.Status != oamodel.AttendanceResultRest {
		t.Fatalf("周日应判休息：%+v", row)
	}
	if row := got["2026-03-09"]; row.Status != oamodel.AttendanceResultMissing || !row.MissingIn || !row.MissingOut {
		t.Fatalf("工作日无卡应双缺：%+v", row)
	}
	if row := got["2026-03-10"]; row.Status != oamodel.AttendanceResultNormal {
		t.Fatalf("宽限上界不应计迟到早退：%+v", row)
	}

	// 工作日历显式记录优先：把周六调休为工作日 → 有卡按正常；
	// 把周一定为休息日 → 有卡也只判休息。
	attSetWorkday(t, env, admin, "2026-03-07", 0)
	attSetWorkday(t, env, admin, "2026-03-02", 1)
	if status, msg, _ := attRecompute(t, env, admin, demoID, "2026-03-02", "2026-03-07"); status != http.StatusOK || msg != "" {
		t.Fatalf("按日历重算：HTTP %d msg %q", status, msg)
	}
	_, _, rows, _ = attPageResults(t, env, admin, "?pageNo=1&pageSize=50&userId=2&startDate=2026-03-02&endDate=2026-03-07")
	got = attResultsByDate(rows)
	if row := got["2026-03-07"]; row.Status != oamodel.AttendanceResultNormal {
		t.Fatalf("调休上班的周六有双卡应判正常：%+v", row)
	}
	if row := got["2026-03-02"]; row.Status != oamodel.AttendanceResultRest {
		t.Fatalf("显式休息日的周一应判休息：%+v", row)
	}

	// 非法日期/范围：400。
	if status, _, _ := attRecompute(t, env, admin, demoID, "2026-03-10", "2026-03-02"); status != http.StatusBadRequest {
		t.Fatalf("开始晚于结束应 400，实际 %d", status)
	}
	if status, _, _ := attRecompute(t, env, admin, demoID, "2026-3-2", "2026-03-03"); status != http.StatusBadRequest {
		t.Fatalf("非法日期格式应 400，实际 %d", status)
	}
}

// TestIntegrationAttendanceResultIdempotentAndCorrection 重算幂等 + 校正：
// 校正写入 override 且保留派生字段与原始打卡；重算不冲掉校正；撤销回落到派生。
func TestIntegrationAttendanceResultIdempotentAndCorrection(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	const demoID = int64(2)
	attResultSetup(t, env, admin, []int64{demoID})
	attInsertPunch(t, env, 1, demoID, "2026-03-03", 9, 30, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, demoID, "2026-03-03", 18, 0, oamodel.PunchTypeOut)

	for i := 0; i < 3; i++ {
		if status, msg, days := attRecompute(t, env, admin, demoID, "2026-03-03", "2026-03-03"); status != http.StatusOK || msg != "" || days != 1 {
			t.Fatalf("第 %d 次重算：HTTP %d msg %q days %d", i+1, status, msg, days)
		}
	}
	_, _, rows, total := attPageResults(t, env, admin, "?pageNo=1&pageSize=10&userId=2&startDate=2026-03-03&endDate=2026-03-03")
	if total != 1 || len(rows) != 1 {
		t.Fatalf("重算幂等应只有一行，实际 total=%d len=%d", total, len(rows))
	}
	derived := rows[0]
	if derived.Status != oamodel.AttendanceResultLate || derived.LateMinutes != 20 {
		t.Fatalf("派生结果应为迟到 20 分钟：%+v", derived)
	}

	// 校正为休息：override 优先，派生/打卡字段保留。
	if status, msg := attCorrect(t, env, admin, derived.ID, oamodel.AttendanceResultRest, "系统故障，人工核实"); status != http.StatusOK || msg != "" {
		t.Fatalf("校正：HTTP %d msg %q", status, msg)
	}
	_, _, rows, _ = attPageResults(t, env, admin, "?pageNo=1&pageSize=10&userId=2&startDate=2026-03-03&endDate=2026-03-03")
	corrected := rows[0]
	if !corrected.Corrected || corrected.Status != oamodel.AttendanceResultRest || corrected.StatusName != "休息" ||
		corrected.OverrideStatus == nil || *corrected.OverrideStatus != oamodel.AttendanceResultRest ||
		corrected.OverrideRemark != "系统故障，人工核实" {
		t.Fatalf("校正后生效状态不符：%+v", corrected)
	}
	if corrected.DerivedStatus != oamodel.AttendanceResultLate || corrected.LateMinutes != 20 ||
		corrected.InTime != "2026-03-03 09:30:00" || corrected.OutTime != "2026-03-03 18:00:00" {
		t.Fatalf("校正不得覆盖派生字段与原始打卡：%+v", corrected)
	}

	// 再次重算：派生字段刷新，校正仍在（override 不被 upsert 冲掉）。
	attInsertPunch(t, env, 1, demoID, "2026-03-03", 19, 0, oamodel.PunchTypeOut)
	if status, msg, _ := attRecompute(t, env, admin, demoID, "2026-03-03", "2026-03-03"); status != http.StatusOK || msg != "" {
		t.Fatalf("校正后重算：HTTP %d msg %q", status, msg)
	}
	_, _, rows, _ = attPageResults(t, env, admin, "?pageNo=1&pageSize=10&userId=2&startDate=2026-03-03&endDate=2026-03-03")
	after := rows[0]
	if !after.Corrected || after.Status != oamodel.AttendanceResultRest || after.DerivedStatus != oamodel.AttendanceResultLate {
		t.Fatalf("重算不应冲掉校正：%+v", after)
	}
	if after.OutTime != "2026-03-03 19:00:00" {
		t.Fatalf("重算应刷新最晚下班打卡：%+v", after)
	}

	// 校正参数校验：非法状态 / 空备注 400。
	if status, msg := attCorrect(t, env, admin, derived.ID, 9, "非法状态"); status != http.StatusBadRequest || msg == "" {
		t.Fatalf("非法校正状态应 400，实际 HTTP %d msg %q", status, msg)
	}
	if status, msg := attCorrect(t, env, admin, derived.ID, oamodel.AttendanceResultNormal, "   "); status != http.StatusBadRequest || msg == "" {
		t.Fatalf("空校正备注应 400，实际 HTTP %d msg %q", status, msg)
	}

	// 撤销校正：回落到派生值。
	if status, msg := attClearCorrect(t, env, admin, derived.ID); status != http.StatusOK || msg != "" {
		t.Fatalf("撤销校正：HTTP %d msg %q", status, msg)
	}
	_, _, rows, _ = attPageResults(t, env, admin, "?pageNo=1&pageSize=10&userId=2&startDate=2026-03-03&endDate=2026-03-03")
	if row := rows[0]; row.Corrected || row.Status != oamodel.AttendanceResultLate || row.OverrideStatus != nil {
		t.Fatalf("撤销校正后应回落到派生结果：%+v", row)
	}
}

// TestIntegrationAttendanceResultVisibility 可见范围（spec §4.6）：
// 无管理权限看本人；部门负责人看子树；oa:attendance:manage 看全租户；
// 越出范围的 userId 过滤返回空页；跨租户不可见/校正 404。
func TestIntegrationAttendanceResultVisibility(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	queryRole := attCreateRole(t, env, admin, "考勤结果查询角色", "att_result_query_role", []int64{menuAttResult})
	manageRole := attCreateRole(t, env, admin, "考勤结果全租户角色", "att_result_manage_role",
		[]int64{menuAttResult, menuAttResultCorrect, menuAttResultAll})
	noneRole := attCreateRole(t, env, admin, "考勤结果无码角色", "att_result_none_role", []int64{1})

	leadID, lead := attCreateUserInDept(t, env, admin, "res_lead", 102, queryRole)
	peerID, peer := attCreateUserInDept(t, env, admin, "res_peer", 102, noneRole)
	outID, out := attCreateUserInDept(t, env, admin, "res_out", 103, noneRole)
	hrID, hr := attCreateUserInDept(t, env, admin, "res_hr", 103, manageRole)
	// 部门 102 的负责人：可见子树 = 102 内全部用户（lead + peer，含本人）。
	if status, api := env.Request(t, http.MethodPut, "/system/dept", admin, map[string]any{
		"id": 102, "name": "市场部", "parentId": 100, "sort": 2, "leaderUserId": leadID, "status": 0,
	}); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("设置部门负责人：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	attResultSetup(t, env, admin, []int64{leadID, peerID, outID, hrID})
	// 同一工作日给四个用户各一条正常结果。
	for _, uid := range []int64{leadID, peerID, outID, hrID} {
		attInsertPunch(t, env, 1, uid, "2026-03-03", 9, 0, oamodel.PunchTypeIn)
		attInsertPunch(t, env, 1, uid, "2026-03-03", 18, 0, oamodel.PunchTypeOut)
		if status, msg, _ := attRecompute(t, env, admin, uid, "2026-03-03", "2026-03-03"); status != http.StatusOK || msg != "" {
			t.Fatalf("重算用户 %d：HTTP %d msg %q", uid, status, msg)
		}
	}

	// 部门负责人：只看 102 子树（lead + peer），看不到 103 的 out/hr。
	_, _, rows, total := attPageResults(t, env, lead, "?pageNo=1&pageSize=50&startDate=2026-03-03&endDate=2026-03-03")
	if total != 2 {
		t.Fatalf("负责人应看到 2 行（子树），实际 %d：%+v", total, rows)
	}
	seen := map[int64]bool{}
	for _, row := range rows {
		seen[row.UserID] = true
	}
	if !seen[leadID] || !seen[peerID] || seen[outID] || seen[hrID] {
		t.Fatalf("负责人可见集合不符：%+v", seen)
	}
	// 明确过滤范围外用户 → 空页（不泄露存在性）。
	if _, _, _, total := attPageResults(t, env, lead, fmt.Sprintf("?pageNo=1&pageSize=50&userId=%d&startDate=2026-03-03&endDate=2026-03-03", outID)); total != 0 {
		t.Fatalf("负责人查范围外用户应为空页，实际 %d", total)
	}

	// 手工重算与列表同一可见范围（PR #66 审查 P2-4）：负责人给子树内用户
	// 重算成功，范围外 404；全租户管理员可重算任意用户。给负责人补校正权限
	// 码（重算接口挂 oa:attendance:result:correct）。
	correctRole := attCreateRole(t, env, admin, "考勤结果校正角色", "att_result_correct_vis",
		[]int64{menuAttResult, menuAttResultCorrect})
	if status, api := env.Request(t, http.MethodPut, "/system/user", admin, map[string]any{
		"id": leadID, "username": "res_lead", "nickname": "res_lead",
		"deptId": int64(102), "roleIds": []int64{queryRole, correctRole}, "status": 0,
	}); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("给负责人追加校正权限：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	lead = env.Login(t, "res_lead", "123456")
	if status, msg, days := attRecompute(t, env, lead, peerID, "2026-03-03", "2026-03-03"); status != http.StatusOK || msg != "" || days != 1 {
		t.Fatalf("负责人重算子部门用户：HTTP %d msg %q days %d，期望 200/1", status, msg, days)
	}
	if status, _, _ := attRecompute(t, env, lead, outID, "2026-03-03", "2026-03-03"); status != http.StatusNotFound {
		t.Fatalf("负责人重算范围外用户应 404，实际 %d", status)
	}
	if status, msg, days := attRecompute(t, env, hr, outID, "2026-03-03", "2026-03-03"); status != http.StatusOK || msg != "" || days != 1 {
		t.Fatalf("全租户管理员重算任意用户：HTTP %d msg %q days %d，期望 200/1", status, msg, days)
	}

	// 普通员工：admin 结果页仅授予 531 时 403；仅本人（有 533 查询码但不是负责人）。
	if status, _, _, _ := attPageResults(t, env, peer, "?pageNo=1&pageSize=10"); status != http.StatusForbidden {
		t.Fatalf("无查询码用户应 403，实际 %d", status)
	}
	_, _, mine, mineTotal := attPageMyResults(t, env, peer, "?pageNo=1&pageSize=50&startDate=2026-03-03&endDate=2026-03-03")
	if mineTotal != 1 || len(mine) != 1 || mine[0].UserID != peerID {
		t.Fatalf("我的考勤结果应只含本人：total=%d rows=%+v", mineTotal, mine)
	}
	// out 用户没有 533 码，但 /mine 登录即可用。
	if status, _, _, total := attPageMyResults(t, env, out, "?pageNo=1&pageSize=50"); status != http.StatusOK || total != 1 {
		t.Fatalf("我的考勤结果登录即可用：HTTP %d total %d", status, total)
	}

	// 全租户：5332 授予 oa:attendance:manage 后可见全部（admin 无结果不影响）。
	_, _, rows, total = attPageResults(t, env, hr, "?pageNo=1&pageSize=100&startDate=2026-03-03&endDate=2026-03-03")
	if total != 4 {
		t.Fatalf("全租户角色应看到 4 行，实际 %d：%+v", total, rows)
	}
	if len(rows) > 0 && rows[0].Nickname == "" {
		t.Fatalf("管理端列表应回填昵称：%+v", rows[0])
	}

	// 跨租户：租户 2 看不到租户 1 的结果，跨租户校正/撤销 404。
	tenant2 := createOATenant(t, env, admin, "att_result_isolate_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	if _, _, _, total := attPageResults(t, env, token2, "?pageNo=1&pageSize=100"); total != 0 {
		t.Fatalf("跨租户结果列表应为空，实际 %d", total)
	}
	_, _, rows, _ = attPageResults(t, env, hr, "?pageNo=1&pageSize=1&startDate=2026-03-03&endDate=2026-03-03")
	firstID := rows[0].ID
	if status, _ := attCorrect(t, env, token2, firstID, oamodel.AttendanceResultNormal, "越权校正"); status != http.StatusNotFound {
		t.Fatalf("跨租户校正应 404，实际 %d", status)
	}
	if status, _ := attClearCorrect(t, env, token2, firstID); status != http.StatusNotFound {
		t.Fatalf("跨租户撤销校正应 404，实际 %d", status)
	}
	// 跨租户手工重算：用户不属于租户 2 → 404（不泄露存在性）。
	if status, _, _ := attRecompute(t, env, token2, leadID, "2026-03-03", "2026-03-03"); status != http.StatusNotFound {
		t.Fatalf("跨租户重算应 404，实际 %d", status)
	}
}

// attUserIDByUsername 按用户名解析当前租户内的用户 ID（登录用户 ID 辅助）。
func attUserIDByUsername(t *testing.T, env *testutil.IntegrationEnv, token, username string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
	}
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("用户 %s 应唯一命中: %v（data=%s）", username, err, string(api.Data))
	}
	return page.List[0].ID
}

// attCreateUserInDept 创建指定部门的用户并登录，返回 (用户 ID, token)。
func attCreateUserInDept(t *testing.T, env *testutil.IntegrationEnv, admin, username string, deptID, roleID int64) (int64, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "nickname": username, "password": "123456",
		"deptId": deptID, "status": 0, "roleIds": []int64{roleID},
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

// TestIntegrationAttendanceResultPermissionCodes 菜单种子（533/5331/5332）与
// 权限码逐码控权：查询码可看列表、校正码可写（仅本人范围）、无码 403。
func TestIntegrationAttendanceResultPermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	cases := []struct {
		id        int64
		name      string
		perm      string
		component string
	}{
		{menuAttResult, "考勤结果", permAttResultQuery, "oa/attendance/result"},
		{menuAttResultCorrect, "考勤校正", permAttResultCorrect, ""},
		{menuAttResultAll, "全租户可见", permAttManage, ""},
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
		}
		if err := json.Unmarshal(api.Data, &menu); err != nil {
			t.Fatalf("解析菜单 %d: %v", tc.id, err)
		}
		if menu.ID != tc.id || menu.Name != tc.name || menu.Permission != tc.perm {
			t.Fatalf("菜单 %d 种子不符：%+v", tc.id, menu)
		}
		if tc.component != "" && (menu.ParentID != 500 || menu.Component != tc.component) {
			t.Fatalf("菜单 %d 层级/组件不符：%+v", tc.id, menu)
		}
		if tc.component == "" && menu.ParentID != menuAttResult {
			t.Fatalf("按钮 %d 应挂在 533 下：%+v", tc.id, menu)
		}
	}

	queryRole := attCreateRole(t, env, admin, "结果只读角色", "att_result_readonly", []int64{menuAttResult})
	correctRole := attCreateRole(t, env, admin, "结果校正角色", "att_result_correct", []int64{menuAttResult, menuAttResultCorrect})
	noneRole := attCreateRole(t, env, admin, "结果无码角色", "att_result_none", []int64{1})

	readID, readToken := attCreateUserInDept(t, env, admin, "res_read", 103, queryRole)
	correctID, correctToken := attCreateUserInDept(t, env, admin, "res_correct", 103, correctRole)
	noneID, noneToken := attCreateUserInDept(t, env, admin, "res_none", 103, noneRole)
	attResultSetup(t, env, admin, []int64{readID, correctID, noneID})
	for _, uid := range []int64{readID, correctID, noneID} {
		attInsertPunch(t, env, 1, uid, "2026-03-04", 9, 0, oamodel.PunchTypeIn)
		attInsertPunch(t, env, 1, uid, "2026-03-04", 18, 0, oamodel.PunchTypeOut)
		if status, msg, _ := attRecompute(t, env, admin, uid, "2026-03-04", "2026-03-04"); status != http.StatusOK || msg != "" {
			t.Fatalf("重算用户 %d：HTTP %d msg %q", uid, status, msg)
		}
	}

	// 只读码：可查列表；校正/重算 403。
	if status, _, _, total := attPageResults(t, env, readToken, "?pageNo=1&pageSize=10&startDate=2026-03-04&endDate=2026-03-04"); status != http.StatusOK || total != 1 {
		t.Fatalf("只读码查列表：HTTP %d total %d", status, total)
	}
	if status, _, _ := attRecompute(t, env, readToken, readID, "2026-03-04", "2026-03-04"); status != http.StatusForbidden {
		t.Fatalf("只读码重算应 403，实际 %d", status)
	}
	// 无码：列表 403，但 /mine 登录即可用。
	if status, _, _, _ := attPageResults(t, env, noneToken, "?pageNo=1&pageSize=10"); status != http.StatusForbidden {
		t.Fatalf("无码查列表应 403，实际 %d", status)
	}
	if status, _, _, total := attPageMyResults(t, env, noneToken, "?pageNo=1&pageSize=10"); status != http.StatusOK || total != 1 {
		t.Fatalf("无码用户我的结果：HTTP %d total %d", status, total)
	}

	// 校正码：只能校正可见范围内的行（本人），校正他人 404。
	_, _, rows, _ := attPageResults(t, env, correctToken, "?pageNo=1&pageSize=10&startDate=2026-03-04&endDate=2026-03-04")
	if len(rows) != 1 || rows[0].UserID != correctID {
		t.Fatalf("校正码用户列表应仅本人：%+v", rows)
	}
	if status, msg := attCorrect(t, env, correctToken, rows[0].ID, oamodel.AttendanceResultNormal, "本人校正"); status != http.StatusOK || msg != "" {
		t.Fatalf("校正本人：HTTP %d msg %q", status, msg)
	}
	if status, _, _, _ := attPageResults(t, env, admin, fmt.Sprintf("?pageNo=1&pageSize=10&userId=%d&startDate=2026-03-04&endDate=2026-03-04", readID)); status != http.StatusOK {
		t.Fatalf("admin 查询：HTTP %d", status)
	}
	_, _, readRows, _ := attPageResults(t, env, admin, fmt.Sprintf("?pageNo=1&pageSize=10&userId=%d&startDate=2026-03-04&endDate=2026-03-04", readID))
	if status, _ := attCorrect(t, env, correctToken, readRows[0].ID, oamodel.AttendanceResultNormal, "越权校正"); status != http.StatusNotFound {
		t.Fatalf("校正范围外行应 404，实际 %d", status)
	}

	// 重算码：可重算（本身不做可见范围校验，按用户 + 日期写结果）。
	if status, msg, days := attRecompute(t, env, correctToken, correctID, "2026-03-04", "2026-03-04"); status != http.StatusOK || msg != "" || days != 1 {
		t.Fatalf("校正码重算：HTTP %d msg %q days %d", status, msg, days)
	}
}

// TestIntegrationAttendanceResultPunchTrigger 打卡后立即重算当日结果：
// 今日显式标记为工作日后，上班卡 → 缺下班卡，下班卡 → 正常。
func TestIntegrationAttendanceResultPunchTrigger(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")
	const demoID = int64(2)
	// 全天班次 + 宽限 1440 分钟：打卡 API 取真实 now，任意时刻都不计迟到/早退。
	_, _, shift := attCreateShift(t, env, admin, map[string]any{
		"name": "打卡触发班", "startTime": "00:00", "endTime": "23:59", "graceMinutes": 1440,
	})
	_, _, group := attCreateGroup(t, env, admin, map[string]any{"name": "打卡触发组", "shiftId": shift.ID})
	if _, msg, added := attAddMembers(t, env, admin, group.ID, []int64{demoID}); added != 1 {
		t.Fatalf("打卡触发组添加组员失败：added %d msg %q", added, msg)
	}

	today := time.Now().In(env.Config.Location()).Format("2006-01-02")
	attSetWorkday(t, env, admin, today, 0)

	if status, msg, _ := attPunch(t, env, demo, "in"); status != http.StatusOK || msg != "" {
		t.Fatalf("上班打卡：HTTP %d msg %q", status, msg)
	}
	query := fmt.Sprintf("?pageNo=1&pageSize=10&startDate=%s&endDate=%s", today, today)
	_, _, rows, total := attPageMyResults(t, env, demo, query)
	if total != 1 || len(rows) != 1 {
		t.Fatalf("打卡后应生成当日结果：total=%d rows=%+v", total, rows)
	}
	if rows[0].Status != oamodel.AttendanceResultMissing || rows[0].MissingIn || !rows[0].MissingOut {
		t.Fatalf("只有上班卡应判缺下班卡：%+v", rows[0])
	}

	if status, msg, _ := attPunch(t, env, demo, "out"); status != http.StatusOK || msg != "" {
		t.Fatalf("下班打卡：HTTP %d msg %q", status, msg)
	}
	_, _, rows, _ = attPageMyResults(t, env, demo, query)
	if total != 1 || len(rows) != 1 || rows[0].Status != oamodel.AttendanceResultNormal {
		t.Fatalf("上下班卡齐全应判正常：total=%d rows=%+v", total, rows)
	}
}

// TestIntegrationAttendanceResultDailyJob 每日任务（直接调任务函数，不睡时钟）：
// 系统级 handler 按启用租户迭代生成指定日期/前一日结果；handler_param 可回填。
func TestIntegrationAttendanceResultDailyJob(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	const demoID = int64(2)

	if !env.App.Scheduler.IsSystemHandler(oaservice.AttendanceDailyResultHandlerName) {
		t.Fatalf("%s 应注册为系统级任务处理器", oaservice.AttendanceDailyResultHandlerName)
	}

	// 租户 1：00:00–23:59 宽限 1440 的班次，保证任意时刻打卡都判正常。
	_, _, shift := attCreateShift(t, env, admin, map[string]any{
		"name": "日任务班", "startTime": "00:00", "endTime": "23:59", "graceMinutes": 1440,
	})
	_, _, group := attCreateGroup(t, env, admin, map[string]any{"name": "日任务组", "shiftId": shift.ID})
	if _, msg, added := attAddMembers(t, env, admin, group.ID, []int64{demoID}); added != 1 {
		t.Fatalf("日任务组添加租户 1 组员失败：added %d msg %q", added, msg)
	}

	loc := env.Config.Location()
	now := time.Now().In(loc)
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	attInsertPunch(t, env, 1, demoID, yesterday, 0, 1, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, demoID, yesterday, 23, 58, oamodel.PunchTypeOut)

	// 租户 2：独立班次/组员/打卡，验证任务按租户迭代。
	tenant2 := createOATenant(t, env, admin, "att_result_job_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	_, _, shift2 := attCreateShift(t, env, token2, map[string]any{
		"name": "日任务班", "startTime": "00:00", "endTime": "23:59", "graceMinutes": 1440,
	})
	_, _, group2 := attCreateGroup(t, env, token2, map[string]any{"name": "日任务组", "shiftId": shift2.ID})
	// 租户 2 管理员入组并打卡（新租户 bootstrap 的 admin 用户 ID 由接口解析）。
	tenant2AdminID := attUserIDByUsername(t, env, token2, "admin")
	if _, msg, added := attAddMembers(t, env, token2, group2.ID, []int64{tenant2AdminID}); added != 1 {
		t.Fatalf("日任务组添加租户 2 组员失败：added %d msg %q", added, msg)
	}
	attInsertPunch(t, env, tenant2, tenant2AdminID, yesterday, 0, 1, oamodel.PunchTypeIn)
	attInsertPunch(t, env, tenant2, tenant2AdminID, yesterday, 23, 58, oamodel.PunchTypeOut)

	// 直接调任务函数：空 param = 业务时区前一日；系统级 handler 迭代全部启用租户。
	env.App.Scheduler.Execute(inframodel.Job{
		TenantID: 1, HandlerName: oaservice.AttendanceDailyResultHandlerName,
	})

	// 周末只判休息，工作日双卡正常（按真实时钟的昨日星期判定）。
	yesterdayDate, err := time.ParseInLocation("2006-01-02", yesterday, time.UTC)
	if err != nil {
		t.Fatalf("解析昨日: %v", err)
	}
	wantStatus := oamodel.AttendanceResultNormal
	if wd := yesterdayDate.Weekday(); wd == time.Saturday || wd == time.Sunday {
		wantStatus = oamodel.AttendanceResultRest
	}
	for _, tc := range []struct {
		tenantID int64
		userID   int64
	}{
		{1, demoID},
		{tenant2, tenant2AdminID},
	} {
		var row oamodel.AttendanceResult
		if err := tenant.Skip(env.DB).
			Where("tenant_id = ? AND user_id = ? AND `work_date` = ?", tc.tenantID, tc.userID, yesterday).
			First(&row).Error; err != nil {
			t.Fatalf("租户 %d 用户 %d 昨日（%s）结果未生成: %v", tc.tenantID, tc.userID, yesterday, err)
		}
		if row.Status != wantStatus {
			t.Fatalf("租户 %d 用户 %d 昨日结果状态应为 %d，实际 %d", tc.tenantID, tc.userID, wantStatus, row.Status)
		}
	}

	// handler_param 指定日期回填（显式工作日 → 正常）。
	env.App.Scheduler.Execute(inframodel.Job{
		TenantID: 1, HandlerName: oaservice.AttendanceDailyResultHandlerName, HandlerParam: "2026-03-02",
	})
	attInsertPunch(t, env, 1, demoID, "2026-03-02", 9, 0, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, demoID, "2026-03-02", 18, 0, oamodel.PunchTypeOut)
	env.App.Scheduler.Execute(inframodel.Job{
		TenantID: 1, HandlerName: oaservice.AttendanceDailyResultHandlerName, HandlerParam: "2026-03-02",
	})
	var row oamodel.AttendanceResult
	if err := tenant.Skip(env.DB).
		Where("tenant_id = 1 AND user_id = ? AND `work_date` = ?", demoID, "2026-03-02").
		First(&row).Error; err != nil {
		t.Fatalf("按 param 回填结果未生成: %v", err)
	}
	if row.Status != oamodel.AttendanceResultNormal {
		t.Fatalf("2026-03-02 双卡结果应为正常，实际 %d", row.Status)
	}

	// 非法 param 让任务失败（handler 返回错误，便于 infra_job 日志定位）。
	env.App.Scheduler.Execute(inframodel.Job{
		TenantID: 1, HandlerName: oaservice.AttendanceDailyResultHandlerName, HandlerParam: "2026-3-2",
	})
	var logRow inframodel.JobLog
	if err := tenant.Skip(env.DB).Where("handler_name = ? AND result = 1", oaservice.AttendanceDailyResultHandlerName).
		Order("id DESC").First(&logRow).Error; err != nil {
		t.Fatalf("非法 param 应写入失败任务日志: %v", err)
	}
}
