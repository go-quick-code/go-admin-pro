//go:build integration

// 请假撤回/取消与可见范围的 HTTP 集成用例（issue #53，docs/oa-mvp-spec.md
// §4.6/§4.8/§5）：
//   - 撤回：首个审批动作前成功且额度不变；已有审批动作 409；
//   - 取消：开始日期前返还 used；开始当天/之后 400 且提示联系管理员；
//     重复取消 409；
//   - 可见范围：本人 / 部门负责人子树 / oa:leave:manage 全租户三档；
//     详情与附件遵循同一范围；跨租户一律 404；
//   - 权限码：撤回/取消按钮码 5295/5296 逐码 403，非发起人 404。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t53，-p 1 串行跑包）。
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

// ---------- 时间与额度辅助 ----------

// ensureLeaveBalancesForYears 为当前年份的前后一年补齐年假/调休额度：
// 用例日期按「今天」动态生成，跨年边界（12 月底/1 月初）也能取到额度行。
func ensureLeaveBalancesForYears(t *testing.T, le *leaveRequestEnv) {
	t.Helper()
	year := time.Now().In(le.env.Config.Location()).Year()
	for _, y := range []int{year - 1, year, year + 1} {
		status, _, _, msg := initBalances(t, le.env, le.admin, map[string]any{
			"year": y, "userIds": []int64{le.employeeID},
			"typeIds": []int64{le.annualTypeID, le.compTypeID},
		})
		if status != http.StatusOK || msg != "" {
			t.Fatalf("初始化 %d 年额度：HTTP %d msg %q", y, status, msg)
		}
	}
}

// upcomingWorkdays 返回从明天起的 count 个连续工作日（不含周末），
// 用于「开始日期未到」的窗口。
func upcomingWorkdays(t *testing.T, env *testutil.IntegrationEnv, count int) []time.Time {
	t.Helper()
	day := time.Now().In(env.Config.Location())
	out := make([]time.Time, 0, count)
	for len(out) < count {
		day = day.AddDate(0, 0, 1)
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			out = append(out, day)
		}
	}
	return out
}

// recentWorkdays 返回从今天（含）起的 count 个最近工作日（倒序），
// 用于「开始当天/已开始」的窗口。
func recentWorkdays(t *testing.T, env *testutil.IntegrationEnv, count int) []time.Time {
	t.Helper()
	day := time.Now().In(env.Config.Location())
	out := make([]time.Time, 0, count)
	for len(out) < count {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			out = append(out, day)
		}
		day = day.AddDate(0, 0, -1)
	}
	return out
}

// leaveDateKey 把日期格式化为 API 的 YYYY-MM-DD。
func leaveDateKey(day time.Time) string {
	return day.Format("2006-01-02")
}

// createAndSubmitLeave 创建并提交一整天（上午到下午）的申请，返回申请 ID。
func createAndSubmitLeave(t *testing.T, env *testutil.IntegrationEnv, le *leaveRequestEnv, day time.Time, reason string) int64 {
	t.Helper()
	start := leaveDateKey(day)
	status, draft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
		start, 0, start, 1, reason)
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建请假草稿（%s）：HTTP %d msg %q", start, status, msg)
	}
	id := int64(draft["id"].(float64))
	if status, _, msg := submitLeave(t, env, le.employee, id); status != http.StatusOK || msg != "" {
		t.Fatalf("提交请假（%s）：HTTP %d msg %q", start, status, msg)
	}
	return id
}

// approveBothNodes 依次由部门负责人与 HR 通过（两节点审批流）。
func approveBothNodes(t *testing.T, env *testutil.IntegrationEnv, le *leaveRequestEnv, id int64) {
	t.Helper()
	leaderTask := findMyTodoTask(t, env, le.leader, id, true)
	if got := approveLeaveTask(t, env, le.leader, int64(leaderTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("部门负责人通过：HTTP %d", got)
	}
	managerTask := findMyTodoTask(t, env, le.manager, id, true)
	if got := approveLeaveTask(t, env, le.manager, int64(managerTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("HR 通过：HTTP %d", got)
	}
}

// leaveAction 调用撤回/取消端点，返回 (HTTP 状态, 响应)。
func leaveAction(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, action string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost,
		fmt.Sprintf("/oa/leave/request/%d/%s", id, action), token, nil)
}

// leaveItemOf 把申请响应 data 解析为行视图（失败 Fatal）。
func leaveItemOf(t *testing.T, phase string, api testutil.APIResponse) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析%s响应: %v（data=%s）", phase, err, string(api.Data))
	}
	return row
}

// balanceUsed 读取员工某年度年假 used，找不到额度行时 Fatal。
func balanceUsed(t *testing.T, env *testutil.IntegrationEnv, token string, year int) string {
	t.Helper()
	balance := annualBalanceOf(t, env, token, year)
	used, _ := balance["used"].(string)
	return used
}

// ---------- 撤回 ----------

// TestIntegrationLeaveWithdrawWindow 撤回窗口：待审且首个审批动作前成功，
// 额度不变；已有通过动作后 409，状态与额度保持不变；重复撤回 409。
func TestIntegrationLeaveWithdrawWindow(t *testing.T) {
	le := newLeaveRequestEnv(t)
	ensureLeaveBalancesForYears(t, le)
	env := le.env
	days := upcomingWorkdays(t, env, 2)

	t.Run("首个审批前撤回_额度不变", func(t *testing.T) {
		id := createAndSubmitLeave(t, env, le, days[0], "撤回用例")
		year := days[0].Year()
		if used := balanceUsed(t, env, le.employee, year); used != "0.00" {
			t.Fatalf("待审时 used 应为 0.00：%s", used)
		}

		status, api := leaveAction(t, env, le.employee, id, "withdraw")
		mustOK(t, "发起人撤回", status, api)
		row := leaveItemOf(t, "撤回", api)
		if row["status"].(float64) != 4 {
			t.Fatalf("撤回后状态应为 4：%+v", row)
		}
		if used := balanceUsed(t, env, le.employee, year); used != "0.00" {
			t.Fatalf("撤回不得改额度，used=%s", used)
		}

		// 详情时间线出现撤回记录（action=3），且审批实例已结束。
		detail := mustLeaveDetail(t, env, le.employee, id)
		if detail["status"].(float64) != 4 {
			t.Fatalf("详情状态应为 4：%+v", detail)
		}
		approval, _ := detail["approval"].(map[string]any)
		records, _ := approval["records"].([]any)
		hasWithdraw := false
		for _, raw := range records {
			if rec, _ := raw.(map[string]any); rec != nil && rec["action"].(float64) == 3 {
				hasWithdraw = true
			}
		}
		if !hasWithdraw {
			t.Fatalf("审批时间线应包含撤回记录：%+v", records)
		}
		// 待办已消化。
		if task := findMyTodoTask(t, env, le.leader, id, false); task != nil {
			t.Fatalf("撤回后部门负责人待办应消失：%+v", task)
		}
		// 重复撤回 409。
		if status, _ := leaveAction(t, env, le.employee, id, "withdraw"); status != http.StatusConflict {
			t.Fatalf("重复撤回应 409，实际 HTTP %d", status)
		}
	})

	t.Run("已有审批动作后不可撤回", func(t *testing.T) {
		id := createAndSubmitLeave(t, env, le, days[1], "撤回窗口")
		leaderTask := findMyTodoTask(t, env, le.leader, id, true)
		if got := approveLeaveTask(t, env, le.leader, int64(leaderTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
			t.Fatalf("部门负责人通过：HTTP %d", got)
		}
		status, api := leaveAction(t, env, le.employee, id, "withdraw")
		if status != http.StatusConflict {
			t.Fatalf("首个节点已办理后撤回应 409，实际 HTTP %d msg %q", status, api.Msg)
		}
		detail := mustLeaveDetail(t, env, le.employee, id)
		if detail["status"].(float64) != 1 {
			t.Fatalf("撤回失败后状态应保持待审：%+v", detail)
		}
	})
}

// ---------- 取消 ----------

// TestIntegrationLeaveCancelWindow 取消窗口：开始日期前返还 used；
// 开始当天/之后返回可读 400（提示联系管理员）；重复取消 409。
func TestIntegrationLeaveCancelWindow(t *testing.T) {
	le := newLeaveRequestEnv(t)
	ensureLeaveBalancesForYears(t, le)
	env := le.env
	future := upcomingWorkdays(t, env, 1)
	recent := recentWorkdays(t, env, 2)

	t.Run("开始前取消_返还额度", func(t *testing.T) {
		id := createAndSubmitLeave(t, env, le, future[0], "取消用例")
		approveBothNodes(t, env, le, id)
		year := future[0].Year()
		if used := balanceUsed(t, env, le.employee, year); used != "1.00" {
			t.Fatalf("通过后 used 应为 1.00：%s", used)
		}

		status, api := leaveAction(t, env, le.employee, id, "cancel")
		mustOK(t, "发起人取消", status, api)
		row := leaveItemOf(t, "取消", api)
		if row["status"].(float64) != 5 {
			t.Fatalf("取消后状态应为 5：%+v", row)
		}
		balance := annualBalanceOf(t, env, le.employee, year)
		if balance["used"] != "0.00" || balance["available"] != "5.00" {
			t.Fatalf("取消后应返还额度（used=0.00/available=5.00）：%+v", balance)
		}
		if status, _ := leaveAction(t, env, le.employee, id, "cancel"); status != http.StatusConflict {
			t.Fatalf("重复取消应 409，实际 HTTP %d", status)
		}
	})

	t.Run("开始当天取消_400提示联系管理员", func(t *testing.T) {
		id := createAndSubmitLeave(t, env, le, recent[0], "当天取消")
		approveBothNodes(t, env, le, id)
		year := recent[0].Year()
		if used := balanceUsed(t, env, le.employee, year); used != "1.00" {
			t.Fatalf("通过后 used 应为 1.00：%s", used)
		}

		status, api := leaveAction(t, env, le.employee, id, "cancel")
		if status != http.StatusBadRequest {
			t.Fatalf("开始当天取消应 400，实际 HTTP %d msg %q", status, api.Msg)
		}
		if !strings.Contains(api.Msg, "联系管理员") {
			t.Fatalf("400 提示应引导联系管理员：%q", api.Msg)
		}
		detail := mustLeaveDetail(t, env, le.employee, id)
		if detail["status"].(float64) != 2 {
			t.Fatalf("取消被拒后状态应保持通过：%+v", detail)
		}
		if used := balanceUsed(t, env, le.employee, year); used != "1.00" {
			t.Fatalf("取消被拒不得返还额度，used=%s", used)
		}
	})

	t.Run("开始后取消_400", func(t *testing.T) {
		id := createAndSubmitLeave(t, env, le, recent[1], "已开始取消")
		approveBothNodes(t, env, le, id)
		status, api := leaveAction(t, env, le.employee, id, "cancel")
		if status != http.StatusBadRequest {
			t.Fatalf("开始后取消应 400，实际 HTTP %d msg %q", status, api.Msg)
		}
		if !strings.Contains(api.Msg, "联系管理员") {
			t.Fatalf("400 提示应引导联系管理员：%q", api.Msg)
		}
	})
}

// ---------- 权限与发起人归属 ----------

// TestIntegrationLeaveActionPermissionAndOwnership 撤回/取消逐码：
// 无码 403；持码但非发起人 404（可见范围只放宽读，不放宽写）。
func TestIntegrationLeaveActionPermissionAndOwnership(t *testing.T) {
	le := newLeaveRequestEnv(t)
	ensureLeaveBalancesForYears(t, le)
	env := le.env
	days := upcomingWorkdays(t, env, 2)

	// 无码用户（仅登录）调用撤回/取消 → 路由权限 403。
	noPermRole := createRoleWithMenus(t, env, le.admin, "leave_action_no_perm", []int64{1})
	noPerm := oaLoginNewUser(t, env, le.admin, "leave_action_no_perm_u", noPermRole)
	pendingID := createAndSubmitLeave(t, env, le, days[0], "权限用例")
	if status, _ := leaveAction(t, env, noPerm, pendingID, "withdraw"); status != http.StatusForbidden {
		t.Fatalf("无 withdraw 码撤回应 403，实际 HTTP %d", status)
	}
	if status, _ := leaveAction(t, env, noPerm, pendingID, "cancel"); status != http.StatusForbidden {
		t.Fatalf("无 cancel 码取消应 403，实际 HTTP %d", status)
	}

	// 部门负责人持 5295/5296 且可见下属数据，但撤回/取消仅发起人 → 404。
	if status, api := leaveAction(t, env, le.leader, pendingID, "withdraw"); status != http.StatusNotFound {
		t.Fatalf("非发起人撤回应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	approvedID := createAndSubmitLeave(t, env, le, days[1], "归属用例")
	approveBothNodes(t, env, le, approvedID)
	if status, api := leaveAction(t, env, le.leader, approvedID, "cancel"); status != http.StatusNotFound {
		t.Fatalf("非发起人取消应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 非发起人的误操作不得改变状态。
	if detail := mustLeaveDetail(t, env, le.employee, approvedID); detail["status"].(float64) != 2 {
		t.Fatalf("非发起人取消被拒后状态应保持通过：%+v", detail)
	}
}

// ---------- 可见范围 ----------

// TestIntegrationLeaveVisibilityScope 三档可见范围（本人 / 部门负责人子树 /
// oa:leave:manage 全租户）对列表、详情与附件一致生效；跨租户 404。
func TestIntegrationLeaveVisibilityScope(t *testing.T) {
	le := newLeaveRequestEnv(t)
	ensureLeaveBalancesForYears(t, le)
	env := le.env
	days := upcomingWorkdays(t, env, 3)

	// 员工：一条带附件的草稿 + 一条待审（详情有审批时间线）。
	start := leaveDateKey(days[0])
	status, draft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
		start, 0, start, 1, "可见范围草稿")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建员工草稿：HTTP %d msg %q", status, msg)
	}
	draftID := int64(draft["id"].(float64))
	if uploadStatus, uploadAPI := uploadLeaveFile(t, env, le.employee, draftID, "scope.txt", []byte("scope-file")); uploadStatus != http.StatusOK || uploadAPI.Code != 0 {
		t.Fatalf("上传员工附件：HTTP %d code %d msg %q", uploadStatus, uploadAPI.Code, uploadAPI.Msg)
	}
	pendingID := createAndSubmitLeave(t, env, le, days[1], "可见范围待审")

	// 部门 103 的局外人：仅自己的一条草稿（需创建码）。
	outsiderID, outsider := createUserWithMenus(t, env, le.admin, "leave_scope_outsider", 103,
		[]int64{leaveRequestMenuID, leaveRequestCreate})
	outStart := leaveDateKey(days[2])
	status, outDraft, msg := createLeaveDraft(t, env, outsider, le.annualTypeID,
		outStart, 0, outStart, 1, "局外人草稿")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建局外人草稿：HTTP %d msg %q", status, msg)
	}
	outID := int64(outDraft["id"].(float64))

	// 列表范围：员工 2 条；负责人（部门 101 子树）2 条；HR manage 3 条；局外人 1 条。
	assertLeavePageTotal(t, env, le.employee, 2, "本人列表")
	assertLeavePageTotal(t, env, le.leader, 2, "部门负责人列表")
	assertLeavePageTotal(t, env, le.manager, 3, "HR 全租户列表")
	assertLeavePageTotal(t, env, outsider, 1, "局外人列表")

	// 详情：负责人可见下属且能看审批时间线；局外人 404；HR 可见全部。
	leaderDetail := mustLeaveDetail(t, env, le.leader, pendingID)
	if leaderDetail["approval"] == nil {
		t.Fatalf("部门负责人应看到下属申请的审批时间线：%+v", leaderDetail)
	}
	if status, _ := getLeaveDetail(t, env, le.leader, outID); status != http.StatusNotFound {
		t.Fatalf("部门负责人读子树外详情应 404，实际 HTTP %d", status)
	}
	if detail := mustLeaveDetail(t, env, le.manager, outID); int64(detail["userId"].(float64)) != outsiderID {
		t.Fatalf("HR 详情 userId 不符：%+v", detail)
	}
	if status, _ := getLeaveDetail(t, env, outsider, draftID); status != http.StatusNotFound {
		t.Fatalf("局外人读他人详情应 404，实际 HTTP %d", status)
	}

	// 附件：负责人/HR 可列出并下载；局外人 404。
	leaderDetail = mustLeaveDetail(t, env, le.leader, draftID)
	files, _ := leaderDetail["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("负责人应看到 1 个附件：%+v", leaderDetail["files"])
	}
	fileURL := files[0].(map[string]any)["url"].(string)
	if downloadStatus, body := downloadLeaveFile(t, env, le.leader, fileURL); downloadStatus != http.StatusOK || string(body) != "scope-file" {
		t.Fatalf("负责人下载附件：HTTP %d body %q", downloadStatus, string(body))
	}
	if status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/leave/request/%d/files", draftID), outsider, nil); status != http.StatusNotFound {
		t.Fatalf("局外人读附件列表应 404，实际 HTTP %d", status)
	}
	if status, _ := downloadLeaveFile(t, env, outsider, fileURL); status != http.StatusNotFound {
		t.Fatalf("局外人下载附件应 404，实际 HTTP %d", status)
	}
	if detail := mustLeaveDetail(t, env, le.manager, draftID); len(detail["files"].([]any)) != 1 {
		t.Fatalf("HR 应看到申请附件：%+v", detail["files"])
	}

	// 跨租户：列表为空、详情/附件 404。
	tenant2 := createOATenant(t, env, le.admin, "leave_scope_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	assertLeavePageTotal(t, env, token2, 0, "租户 2 列表")
	if status, _ := getLeaveDetail(t, env, token2, pendingID); status != http.StatusNotFound {
		t.Fatalf("跨租户详情应 404，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/leave/request/%d/files", draftID), token2, nil); status != http.StatusNotFound {
		t.Fatalf("跨租户附件应 404，实际 HTTP %d", status)
	}
}

// assertLeavePageTotal 断言请假分页 total。
func assertLeavePageTotal(t *testing.T, env *testutil.IntegrationEnv, token string, want int64, phase string) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/oa/leave/request/page?pageNo=1&pageSize=50", token, nil)
	mustOK(t, phase, status, api)
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析%s分页: %v（data=%s）", phase, err, string(api.Data))
	}
	if page.Total != want || int64(len(page.List)) != want {
		t.Fatalf("%s total=%d len=%d，期望 %d", phase, page.Total, len(page.List), want)
	}
}
