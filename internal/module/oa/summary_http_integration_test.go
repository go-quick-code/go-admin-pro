//go:build integration

// 请假联动与月度汇总的 HTTP 集成用例（issue #62，docs/oa-mvp-spec.md
// §4.8/§4.10）：
//   - 整天请假通过后该日不再计缺卡、状态=请假；半天请假记录 leave_days=0.5
//     且状态不变；
//   - 请假通过（bpm 提交后钩子）与取消（业务事务提交后）均触发受影响日期
//     的考勤重算；
//   - 月度汇总与每日结果逐项一致（生效状态口径）、空月份为零；
//   - 可见范围三档（本人/部门子树/全租户）、跨租户 404、菜单与权限码；
//   - 员工「我的考勤」本月汇总登录即可（强制本人）。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t62，-p 1 串行）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	oamodel "github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 月度汇总的菜单/按钮码（菜单 534，issue #62 种子；可见范围复用 5332）。
const (
	menuAttSummary = int64(534)
	permAttSummary = "oa:attendance:summary:query"
)

// attSummaryRow 是月度汇总响应行（与每日结果逐项对照用）。
type attSummaryRow struct {
	UserID         int64   `json:"userId"`
	Nickname       string  `json:"nickname"`
	Month          string  `json:"month"`
	Days           int     `json:"days"`
	WorkDays       int     `json:"workDays"`
	AttendanceDays int     `json:"attendanceDays"`
	Normal         int     `json:"normal"`
	Late           int     `json:"late"`
	Early          int     `json:"early"`
	LateAndEarly   int     `json:"lateAndEarly"`
	Missing        int     `json:"missing"`
	Rest           int     `json:"rest"`
	Leave          int     `json:"leave"`
	LeaveDays      float64 `json:"leaveDays"`
	LateMinutes    int     `json:"lateMinutes"`
	EarlyMinutes   int     `json:"earlyMinutes"`
}

// attPageSummaries 管理端月度汇总分页，返回 (HTTP 状态, msg, 行列表, total)。
func attPageSummaries(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, []attSummaryRow, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/summary/page"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil, 0
	}
	var page struct {
		List  []attSummaryRow `json:"list"`
		Total int64           `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析考勤汇总分页: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, page.List, page.Total
}

// attSummaryOfUser 单用户月度汇总，返回 (HTTP 状态, msg, 行)。
func attSummaryOfUser(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, *attSummaryRow) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/summary/user"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil
	}
	var row attSummaryRow
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析用户考勤汇总: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, &row
}

// attMySummary 我的月度汇总（强制本人），返回 (HTTP 状态, msg, 行)。
func attMySummary(t *testing.T, env *testutil.IntegrationEnv, token, query string) (int, string, *attSummaryRow) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/attendance/summary/mine"+query, token, nil)
	if api.Code != 0 {
		return status, api.Msg, nil
	}
	var row attSummaryRow
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析我的考勤汇总: %v（data=%s）", err, string(api.Data))
	}
	return status, api.Msg, &row
}

// summaryDate 解析 YYYY-MM-DD 字面量（UTC 零点，与 DateKey 口径一致）。
func summaryDate(t *testing.T, s string) time.Time {
	t.Helper()
	day, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		t.Fatalf("解析测试日期 %q: %v", s, err)
	}
	return day
}

// attResultRowOf 读取某用户指定日期的结果行（缺失直接 Fatal）。
func attResultRowOf(t *testing.T, env *testutil.IntegrationEnv, admin string, userID int64, date string) attResultRow {
	t.Helper()
	_, _, rows, total := attPageResults(t, env, admin,
		fmt.Sprintf("?pageNo=1&pageSize=10&userId=%d&startDate=%s&endDate=%s", userID, date, date))
	if total != 1 || len(rows) != 1 {
		t.Fatalf("用户 %d 在 %s 应有 1 行结果，实际 total=%d rows=%+v", userID, date, total, rows)
	}
	return rows[0]
}

// TestIntegrationAttendanceSummaryMonthMatchesDailyResults 月度汇总与每日结果
// 逐项一致：正常/迟到/早退/迟到且早退/缺卡/休息计数、出勤天数、迟到早退
// 分钟合计；空月份为零值。
func TestIntegrationAttendanceSummaryMonthMatchesDailyResults(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	userID := createUser(t, env, admin, "sum_consistency", 101)
	userToken := env.Login(t, "sum_consistency", "123456")
	attResultSetup(t, env, admin, []int64{userID})

	// 2026-03：03-02 正常、03-03 迟到且早退（宽限 10 → 迟到/早退各 10）、
	// 03-04 缺上班卡、03-05 缺下班卡、03-06 正常；其余为周末/无卡工作日。
	attInsertPunch(t, env, 1, userID, "2026-03-02", 9, 0, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, userID, "2026-03-02", 18, 0, oamodel.PunchTypeOut)
	attInsertPunch(t, env, 1, userID, "2026-03-03", 9, 20, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, userID, "2026-03-03", 17, 40, oamodel.PunchTypeOut)
	attInsertPunch(t, env, 1, userID, "2026-03-04", 18, 0, oamodel.PunchTypeOut)
	attInsertPunch(t, env, 1, userID, "2026-03-05", 9, 0, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, userID, "2026-03-06", 9, 0, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, userID, "2026-03-06", 18, 0, oamodel.PunchTypeOut)

	if status, msg, days := attRecompute(t, env, admin, userID, "2026-03-01", "2026-03-31"); status != http.StatusOK || msg != "" || days != 31 {
		t.Fatalf("整月重算：HTTP %d msg %q days %d", status, msg, days)
	}

	// 每日结果（生效状态）与测试内独立计数。
	_, _, daily, dailyTotal := attPageResults(t, env, admin,
		fmt.Sprintf("?pageNo=1&pageSize=50&userId=%d&startDate=2026-03-01&endDate=2026-03-31", userID))
	if dailyTotal != 31 {
		t.Fatalf("整月应有 31 行每日结果，实际 %d", dailyTotal)
	}
	var normal, late, early, lateAndEarly, missing, rest, leaveCount int
	var leaveDays float64
	var lateMinutes, earlyMinutes int
	for _, row := range daily {
		switch row.Status {
		case oamodel.AttendanceResultNormal:
			normal++
		case oamodel.AttendanceResultLate:
			late++
		case oamodel.AttendanceResultEarly:
			early++
		case oamodel.AttendanceResultLateAndEarly:
			lateAndEarly++
		case oamodel.AttendanceResultMissing:
			missing++
		case oamodel.AttendanceResultRest:
			rest++
		case oamodel.AttendanceResultLeave:
			leaveCount++
		}
		leaveDays += row.LeaveDays
		lateMinutes += row.LateMinutes
		earlyMinutes += row.EarlyMinutes
	}

	status, msg, summary := attSummaryOfUser(t, env, admin,
		fmt.Sprintf("?month=2026-03&userId=%d", userID))
	if status != http.StatusOK || msg != "" || summary == nil {
		t.Fatalf("用户月度汇总：HTTP %d msg %q summary=%+v", status, msg, summary)
	}
	want := attSummaryRow{
		UserID: userID, Nickname: "sum_consistency", Month: "2026-03",
		Days: 31, WorkDays: 22, AttendanceDays: 3,
		Normal: 2, Late: 0, Early: 0, LateAndEarly: 1, Missing: 19, Rest: 9,
		Leave: 0, LeaveDays: 0, LateMinutes: 10, EarlyMinutes: 10,
	}
	if *summary != want {
		t.Fatalf("月度汇总口径不符：\n got=%+v\nwant=%+v", *summary, want)
	}
	// 与每日结果逐项对照（测试内独立计数，防止口径漂移）。
	gotCounts := []int{summary.Normal, summary.Late, summary.Early, summary.LateAndEarly,
		summary.Missing, summary.Rest, summary.Leave}
	wantCounts := []int{normal, late, early, lateAndEarly, missing, rest, leaveCount}
	for i := range gotCounts {
		if gotCounts[i] != wantCounts[i] {
			t.Fatalf("汇总第 %d 项与每日结果不一致：summary=%v daily=%v", i, gotCounts, wantCounts)
		}
	}
	if summary.LeaveDays != leaveDays || summary.LateMinutes != lateMinutes || summary.EarlyMinutes != earlyMinutes {
		t.Fatalf("请假天数/迟到早退分钟与每日结果不一致：summary=%+v daily(leave=%v late=%d early=%d)",
			*summary, leaveDays, lateMinutes, earlyMinutes)
	}
	if summary.AttendanceDays != normal+late+early+lateAndEarly {
		t.Fatalf("出勤天数应等于正常+迟到+早退+迟到且早退：%+v", *summary)
	}

	// 管理端分页：按用户分页，昵称回填。
	_, _, list, total := attPageSummaries(t, env, admin, "?month=2026-03&pageNo=1&pageSize=10")
	if total != 1 || len(list) != 1 {
		t.Fatalf("汇总分页应有 1 个用户，实际 total=%d list=%+v", total, list)
	}
	if list[0].UserID != userID || list[0].Nickname == "" {
		t.Fatalf("汇总分页行不符：%+v", list[0])
	}

	// 员工本人：登录即可读本月汇总（与用户汇总同一口径）。
	status, msg, mine := attMySummary(t, env, userToken, "?month=2026-03")
	if status != http.StatusOK || msg != "" || mine == nil || mine.UserID != userID {
		t.Fatalf("我的月度汇总：HTTP %d msg %q row=%+v", status, msg, mine)
	}
	if mine.Days != summary.Days || mine.AttendanceDays != summary.AttendanceDays ||
		mine.Missing != summary.Missing || mine.Rest != summary.Rest {
		t.Fatalf("我的汇总与用户汇总不一致：mine=%+v user=%+v", *mine, *summary)
	}

	// 空月份：无结果行时全部为零（仅回显 userId/month）。
	_, _, empty := attSummaryOfUser(t, env, admin, fmt.Sprintf("?month=2026-04&userId=%d", userID))
	if empty == nil || empty.Month != "2026-04" || empty.Days != 0 || empty.WorkDays != 0 ||
		empty.AttendanceDays != 0 || empty.LeaveDays != 0 || empty.Normal != 0 {
		t.Fatalf("空月份应为零值：%+v", empty)
	}

	// 月份格式非法：400。
	if status, _, _, _ := attPageSummaries(t, env, admin, "?month=2026-3"); status != http.StatusBadRequest {
		t.Fatalf("非法月份格式应 400，实际 %d", status)
	}
	// 缺省月份（当月）：登录即可，返回本月（无论有无数据都不报错）。
	if status, msg, mine := attMySummary(t, env, userToken, ""); status != http.StatusOK || msg != "" || mine == nil {
		t.Fatalf("缺省月份我的汇总：HTTP %d msg %q row=%+v", status, msg, mine)
	}
}

// TestIntegrationLeaveAttendanceFullDayLinked 整天请假通过后：该日不再计缺卡、
// 状态=请假、leave_days=1，且审批提交后自动重算（无需手工重算）。
func TestIntegrationLeaveAttendanceFullDayLinked(t *testing.T) {
	le := newLeaveRequestEnv(t)
	env := le.env
	const dateKey = "2026-03-02"
	attResultSetup(t, env, le.admin, []int64{le.employeeID})
	attInsertPunch(t, env, 1, le.employeeID, dateKey, 9, 0, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, le.employeeID, dateKey, 18, 0, oamodel.PunchTypeOut)
	if status, msg, _ := attRecompute(t, env, le.admin, le.employeeID, dateKey, dateKey); status != http.StatusOK || msg != "" {
		t.Fatalf("基线重算：HTTP %d msg %q", status, msg)
	}
	baseline := attResultRowOf(t, env, le.admin, le.employeeID, dateKey)
	if baseline.Status != oamodel.AttendanceResultNormal || baseline.LeaveDays != 0 {
		t.Fatalf("基线应为正常且无请假：%+v", baseline)
	}

	id := createAndSubmitLeave(t, env, le, summaryDate(t, dateKey), "整天请假联动")
	approveBothNodes(t, env, le, id)

	// 审批通过后 bpm 提交后钩子已重算：状态=请假、leave_days=1、不再计缺卡。
	after := attResultRowOf(t, env, le.admin, le.employeeID, dateKey)
	if after.Status != oamodel.AttendanceResultLeave || after.StatusName != "请假" {
		t.Fatalf("整天请假后状态应为请假：%+v", after)
	}
	if after.LeaveDays != 1 {
		t.Fatalf("整天请假 leave_days 应为 1：%+v", after)
	}
	if after.MissingIn || after.MissingOut {
		t.Fatalf("整天请假当天不再计缺卡：%+v", after)
	}

	// 月度汇总同步体现请假天数。
	_, _, summary := attSummaryOfUser(t, env, le.admin,
		fmt.Sprintf("?month=2026-03&userId=%d", le.employeeID))
	if summary == nil || summary.Leave != 1 || summary.LeaveDays != 1 || summary.AttendanceDays != 0 {
		t.Fatalf("整天请假后月度汇总不符：%+v", summary)
	}
}

// TestIntegrationLeaveAttendanceHalfDay 半天请假：结果行 leave_days=0.5，
// 状态保持派生结果（有双卡 → 正常），请假天数按 0.5 计入月度汇总。
func TestIntegrationLeaveAttendanceHalfDay(t *testing.T) {
	le := newLeaveRequestEnv(t)
	env := le.env
	const dateKey = "2026-03-03"
	attResultSetup(t, env, le.admin, []int64{le.employeeID})
	attInsertPunch(t, env, 1, le.employeeID, dateKey, 9, 0, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, le.employeeID, dateKey, 18, 0, oamodel.PunchTypeOut)
	if status, msg, _ := attRecompute(t, env, le.admin, le.employeeID, dateKey, dateKey); status != http.StatusOK || msg != "" {
		t.Fatalf("基线重算：HTTP %d msg %q", status, msg)
	}

	// 当天上午半天请假（0=上午）。
	status, draft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID, dateKey, 0, dateKey, 0, "上午半天假")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建半天请假：HTTP %d msg %q", status, msg)
	}
	id := int64(draft["id"].(float64))
	if status, _, msg := submitLeave(t, env, le.employee, id); status != http.StatusOK || msg != "" {
		t.Fatalf("提交半天请假：HTTP %d msg %q", status, msg)
	}
	approveBothNodes(t, env, le, id)

	after := attResultRowOf(t, env, le.admin, le.employeeID, dateKey)
	if after.LeaveDays != 0.5 {
		t.Fatalf("半天请假 leave_days 应为 0.5：%+v", after)
	}
	if after.Status != oamodel.AttendanceResultNormal {
		t.Fatalf("半天请假不改变日期级状态（双卡应为正常）：%+v", after)
	}

	_, _, summary := attSummaryOfUser(t, env, le.admin,
		fmt.Sprintf("?month=2026-03&userId=%d", le.employeeID))
	if summary == nil || summary.LeaveDays != 0.5 || summary.Leave != 0 || summary.Normal != 1 {
		t.Fatalf("半天请假月度汇总不符：%+v", summary)
	}
}

// TestIntegrationLeaveAttendanceRecomputeOnCancel 通过/取消均触发重算：
// 未来工作日的整天请假通过 → 请假；取消（开始日期前）→ 回到正常。
func TestIntegrationLeaveAttendanceRecomputeOnCancel(t *testing.T) {
	le := newLeaveRequestEnv(t)
	env := le.env
	ensureLeaveBalancesForYears(t, le)
	attResultSetup(t, env, le.admin, []int64{le.employeeID})

	day := upcomingWorkdays(t, env, 1)[0]
	dateKey := leaveDateKey(day)
	attInsertPunch(t, env, 1, le.employeeID, dateKey, 9, 0, oamodel.PunchTypeIn)
	attInsertPunch(t, env, 1, le.employeeID, dateKey, 18, 0, oamodel.PunchTypeOut)
	if status, msg, _ := attRecompute(t, env, le.admin, le.employeeID, dateKey, dateKey); status != http.StatusOK || msg != "" {
		t.Fatalf("基线重算：HTTP %d msg %q", status, msg)
	}
	baseline := attResultRowOf(t, env, le.admin, le.employeeID, dateKey)
	wantBaseline := oamodel.AttendanceResultNormal
	if baseline.Status != wantBaseline {
		t.Fatalf("基线应为正常：%+v", baseline)
	}

	id := createAndSubmitLeave(t, env, le, day, "未来整天请假")
	approveBothNodes(t, env, le, id)
	approved := attResultRowOf(t, env, le.admin, le.employeeID, dateKey)
	if approved.Status != oamodel.AttendanceResultLeave || approved.LeaveDays != 1 {
		t.Fatalf("通过后应为请假：%+v", approved)
	}

	// 取消（开始日期未到）：业务事务提交后重算 → 回到正常、请假天数归零。
	status, api := leaveAction(t, env, le.employee, id, "cancel")
	mustOK(t, "取消请假", status, api)
	cancelled := attResultRowOf(t, env, le.admin, le.employeeID, dateKey)
	if cancelled.Status != oamodel.AttendanceResultNormal || cancelled.LeaveDays != 0 {
		t.Fatalf("取消后应重算回正常且请假归零：%+v", cancelled)
	}
	if cancelled.InTime == "" || cancelled.OutTime == "" {
		t.Fatalf("取消重算不应丢失原始打卡：%+v", cancelled)
	}
}

// TestIntegrationAttendanceSummaryVisibility 可见范围三档（spec §4.6/§4.10）：
// 部门负责人看子树 ∪ 本人、oa:attendance:manage 看全租户、普通员工越权为空；
// 单用户查询范围外 404；跨租户列表为空、跨租户用户 404。
func TestIntegrationAttendanceSummaryVisibility(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	queryRole := attCreateRole(t, env, admin, "考勤汇总查询角色", "att_summary_query_role", []int64{menuAttSummary})
	manageRole := attCreateRole(t, env, admin, "考勤汇总全租户角色", "att_summary_manage_role",
		[]int64{menuAttResult, menuAttSummary, menuAttResultAll})
	noneRole := attCreateRole(t, env, admin, "考勤汇总无码角色", "att_summary_none_role", []int64{1})

	leadID, lead := attCreateUserInDept(t, env, admin, "sum_lead", 102, queryRole)
	peerID, peer := attCreateUserInDept(t, env, admin, "sum_peer", 102, noneRole)
	outID, out := attCreateUserInDept(t, env, admin, "sum_out", 103, noneRole)
	hrID, hr := attCreateUserInDept(t, env, admin, "sum_hr", 103, manageRole)
	if status, api := env.Request(t, http.MethodPut, "/system/dept", admin, map[string]any{
		"id": 102, "name": "市场部", "parentId": 100, "sort": 2, "leaderUserId": leadID, "status": 0,
	}); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("设置部门负责人：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	attResultSetup(t, env, admin, []int64{leadID, peerID, outID, hrID})
	for _, uid := range []int64{leadID, peerID, outID, hrID} {
		attInsertPunch(t, env, 1, uid, "2026-03-03", 9, 0, oamodel.PunchTypeIn)
		attInsertPunch(t, env, 1, uid, "2026-03-03", 18, 0, oamodel.PunchTypeOut)
		if status, msg, _ := attRecompute(t, env, admin, uid, "2026-03-03", "2026-03-03"); status != http.StatusOK || msg != "" {
			t.Fatalf("重算用户 %d：HTTP %d msg %q", uid, status, msg)
		}
	}

	// 部门负责人：只看 102 子树（lead + peer）。
	_, _, list, total := attPageSummaries(t, env, lead, "?month=2026-03&pageNo=1&pageSize=50")
	if total != 2 {
		t.Fatalf("负责人应看到 2 个用户的汇总，实际 %d：%+v", total, list)
	}
	seen := map[int64]bool{}
	for _, row := range list {
		seen[row.UserID] = true
	}
	if !seen[leadID] || !seen[peerID] || seen[outID] || seen[hrID] {
		t.Fatalf("负责人可见集合不符：%+v", seen)
	}
	// 明确过滤范围外用户 → 空页；单用户查询范围外 → 404。
	if _, _, _, total := attPageSummaries(t, env, lead,
		fmt.Sprintf("?month=2026-03&userId=%d", outID)); total != 0 {
		t.Fatalf("负责人查范围外用户应为空页，实际 %d", total)
	}
	if status, _, _ := attSummaryOfUser(t, env, lead,
		fmt.Sprintf("?month=2026-03&userId=%d", outID)); status != http.StatusNotFound {
		t.Fatalf("负责人查范围外用户汇总应 404，实际 %d", status)
	}

	// 无查询码：汇总分页/单用户 403，但「我的汇总」登录即可用。
	if status, _, _, _ := attPageSummaries(t, env, peer, "?month=2026-03"); status != http.StatusForbidden {
		t.Fatalf("无查询码用户分页应 403，实际 %d", status)
	}
	if status, msg, mine := attMySummary(t, env, peer, "?month=2026-03"); status != http.StatusOK || msg != "" || mine == nil || mine.UserID != peerID {
		t.Fatalf("无查询码用户我的汇总：HTTP %d msg %q row=%+v", status, msg, mine)
	}
	// 无码用户即便登录可读本人，也不能用单用户接口查他人（403 由权限码拦截）。
	if status, _, _ := attSummaryOfUser(t, env, peer, fmt.Sprintf("?month=2026-03&userId=%d", leadID)); status != http.StatusForbidden {
		t.Fatalf("无查询码用户单用户查询应 403，实际 %d", status)
	}

	// 全租户（oa:attendance:manage）：可见全部 4 个用户。
	_, _, list, total = attPageSummaries(t, env, hr, "?month=2026-03&pageNo=1&pageSize=100")
	if total != 4 {
		t.Fatalf("全租户角色应看到 4 个用户，实际 %d：%+v", total, list)
	}

	// 跨租户：列表为空；跨租户用户 404（即便对方是超级管理员也不泄露）。
	tenant2 := createOATenant(t, env, admin, "att_summary_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	if _, _, _, total := attPageSummaries(t, env, token2, "?month=2026-03"); total != 0 {
		t.Fatalf("跨租户汇总列表应为空，实际 %d", total)
	}
	if status, _, _ := attSummaryOfUser(t, env, token2,
		fmt.Sprintf("?month=2026-03&userId=%d", leadID)); status != http.StatusNotFound {
		t.Fatalf("跨租户用户汇总应 404，实际 %d", status)
	}
	if status, msg, mine := attMySummary(t, env, token2, ""); status != http.StatusOK || msg != "" || mine == nil || mine.Days != 0 {
		t.Fatalf("跨租户我的汇总应为空：HTTP %d msg %q row=%+v", status, msg, mine)
	}

	// 租户 1 的普通员工（无 534 码）也能通过「我的汇总」读到本人数据。
	if status, msg, mine := attMySummary(t, env, out, "?month=2026-03"); status != http.StatusOK || msg != "" || mine == nil || mine.UserID != outID {
		t.Fatalf("普通员工我的汇总：HTTP %d msg %q row=%+v", status, msg, mine)
	}
}

// TestIntegrationAttendanceSummaryPermissionCodes 菜单种子（534）与权限码
// oa:attendance:summary:query 逐码控权：挂 500 下、组件/权限码正确；
// 有码可查、无码 403；「我的汇总」登录即可。
func TestIntegrationAttendanceSummaryPermissionCodes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/system/menu/%d", menuAttSummary), admin, nil)
	mustOK(t, "读取考勤汇总菜单", status, api)
	var menu struct {
		ID         int64  `json:"id"`
		ParentID   int64  `json:"parentId"`
		Name       string `json:"name"`
		Component  string `json:"component"`
		Permission string `json:"permission"`
	}
	if err := json.Unmarshal(api.Data, &menu); err != nil {
		t.Fatalf("解析菜单 %d: %v", menuAttSummary, err)
	}
	if menu.ID != menuAttSummary || menu.ParentID != 500 || menu.Name != "考勤汇总" ||
		menu.Component != "oa/attendance/summary" || menu.Permission != permAttSummary {
		t.Fatalf("菜单 %d 种子不符：%+v", menuAttSummary, menu)
	}

	queryRole := attCreateRole(t, env, admin, "汇总只读角色", "att_summary_readonly", []int64{menuAttSummary})
	noneRole := attCreateRole(t, env, admin, "汇总无码角色", "att_summary_none", []int64{1})
	readID, readToken := attCreateUserInDept(t, env, admin, "sum_read", 103, queryRole)
	noneID, noneToken := attCreateUserInDept(t, env, admin, "sum_none", 103, noneRole)
	attResultSetup(t, env, admin, []int64{readID, noneID})
	for _, uid := range []int64{readID, noneID} {
		attInsertPunch(t, env, 1, uid, "2026-03-04", 9, 0, oamodel.PunchTypeIn)
		attInsertPunch(t, env, 1, uid, "2026-03-04", 18, 0, oamodel.PunchTypeOut)
		if status, msg, _ := attRecompute(t, env, admin, uid, "2026-03-04", "2026-03-04"); status != http.StatusOK || msg != "" {
			t.Fatalf("重算用户 %d：HTTP %d msg %q", uid, status, msg)
		}
	}

	// 有码：分页只含本人（非部门负责人），单用户查本人可用。
	if status, msg, list, total := attPageSummaries(t, env, readToken, "?month=2026-03"); status != http.StatusOK || msg != "" {
		t.Fatalf("只读码查汇总：HTTP %d msg %q", status, msg)
	} else if total != 1 || len(list) != 1 || list[0].UserID != readID {
		t.Fatalf("只读码汇总应仅本人：total=%d list=%+v", total, list)
	}
	if status, msg, row := attSummaryOfUser(t, env, readToken,
		fmt.Sprintf("?month=2026-03&userId=%d", readID)); status != http.StatusOK || msg != "" || row == nil || row.UserID != readID {
		t.Fatalf("只读码查本人汇总：HTTP %d msg %q row=%+v", status, msg, row)
	}

	// 无码：分页 403；我的汇总 200。
	if status, _, _, _ := attPageSummaries(t, env, noneToken, "?month=2026-03"); status != http.StatusForbidden {
		t.Fatalf("无码查汇总应 403，实际 %d", status)
	}
	if status, msg, row := attMySummary(t, env, noneToken, "?month=2026-03"); status != http.StatusOK || msg != "" || row == nil || row.UserID != noneID {
		t.Fatalf("无码我的汇总：HTTP %d msg %q row=%+v", status, msg, row)
	}
}
