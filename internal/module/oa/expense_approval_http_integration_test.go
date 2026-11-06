//go:build integration

// 报销提交与 bpm 审批的 HTTP 集成用例（issue #55，docs/oa-mvp-spec.md §4.9）：
//   - 提交：草稿/已驳回可提交（其余 409）；事务内生成 oa_expense_approval 实例，
//     部门负责人待办可见；审批人解析不到 400 且单据保持草稿；
//   - 通过 → 已通过终态、明细不可再改；驳回 → 已驳回 + 原因（按列宽截断），
//     可修改重提；撤回 → 撤回；
//   - 单号/金额/部门写入实例标题与摘要快照；
//   - 详情时间线与实例一致（GetInstanceDetailForBusiness）；
//   - 可见范围：本人 / 部门负责人子树 / oa:expense:manage 全租户；跨租户 404；
//   - 权限码：提交 5234 逐码 403，非发起人 404；
//   - 默认流程种子：oa_expense_approval v1 已发布、重复 seed 不新增不覆盖。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t55，-p 1 串行跑包）。
package oa_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// expenseApprovalType 是报销审批的 bpm 业务类型/默认流程 key（spec §4.9）。
const expenseApprovalType = "oa_expense_approval"

// expenseApprovalEnv 聚合报销审批用例的用户与部门基础设施。
type expenseApprovalEnv struct {
	env        *testutil.IntegrationEnv
	admin      string
	employeeID int64
	employee   string // 研发部（101），持创建/编辑/删除/提交码
	leader     string // 研发部负责人，持待办码
	manager    string // 市场部，持 oa:expense:manage（财务/管理员全租户）
}

// newExpenseApprovalEnv 搭建：报销用户三角色 + 部门 101 负责人。
// 默认流程 oa_expense_approval 已由 seed 写入（tenant 1 已发布 v1）。
func newExpenseApprovalEnv(t *testing.T) *expenseApprovalEnv {
	t.Helper()
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	employeeID, employee := createUserWithMenus(t, env, admin, "exp_emp", 101, []int64{
		expenseMineMenuID, expenseCreate, expenseUpdate, expenseDelete, expenseSubmit,
	})
	leaderID, leader := createUserWithMenus(t, env, admin, "exp_leader", 101, []int64{
		bpmTaskMenuID, bpmTaskHandleButton,
	})
	_, manager := createUserWithMenus(t, env, admin, "exp_manager", 102, []int64{
		bpmTaskMenuID, bpmTaskHandleButton, expenseMenuID, expenseManage,
	})
	setDeptLeader(t, env, admin, 101, leaderID)
	return &expenseApprovalEnv{
		env: env, admin: admin, employeeID: employeeID,
		employee: employee, leader: leader, manager: manager,
	}
}

// ---------- HTTP 速记 ----------

// submitExpenseClaim 提交报销单（原始响应，便于断言 400/404/409）。
func submitExpenseClaim(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, fmt.Sprintf("/oa/expense/claim/%d/submit", id), token, nil)
}

// mustSubmitExpenseClaim 提交并断言成功，返回详情行。
func mustSubmitExpenseClaim(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) map[string]any {
	t.Helper()
	status, api := submitExpenseClaim(t, env, token, id)
	mustOK(t, "提交报销单", status, api)
	return expenseDetailMap(t, api)
}

// expenseDetailMap 把详情响应 data 解析为 map（失败 Fatal）。
func expenseDetailMap(t *testing.T, api testutil.APIResponse) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析报销详情: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// mustExpenseDetail 读取报销详情并断言成功。
func mustExpenseDetail(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/expense/claim/%d", id), token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("读取报销详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析报销详情: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// findExpenseTodoTask 在待办里按报销单 ID 找任务；required=false 允许不存在。
func findExpenseTodoTask(t *testing.T, env *testutil.IntegrationEnv, token string, claimID int64, required bool) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/bpm/task/page?tab=todo&pageNo=1&pageSize=50", token, nil)
	mustOK(t, "查询我的待办", status, api)
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析待办分页: %v（data=%s）", err, string(api.Data))
	}
	for _, row := range page.List {
		if row["businessType"] == expenseApprovalType && int64(row["businessId"].(float64)) == claimID {
			return row
		}
	}
	if required {
		t.Fatalf("待办中未找到报销单 %d：%+v", claimID, page.List)
	}
	return nil
}

// actExpenseTask 办理审批任务（approve/reject），返回 HTTP 状态。
func actExpenseTask(t *testing.T, env *testutil.IntegrationEnv, token string, taskID int64, action, comment string) int {
	t.Helper()
	path := fmt.Sprintf("/bpm/task/%d/approve", taskID)
	if action == "reject" {
		path = fmt.Sprintf("/bpm/task/%d/reject", taskID)
	}
	status, _ := env.Request(t, http.MethodPost, path, token, map[string]any{"comment": comment})
	return status
}

// approveExpenseBothNodes 依次由部门负责人与财务（manage 权限）通过。
func approveExpenseBothNodes(t *testing.T, env *testutil.IntegrationEnv, exp *expenseApprovalEnv, claimID int64) {
	t.Helper()
	leaderTask := findExpenseTodoTask(t, env, exp.leader, claimID, true)
	if got := actExpenseTask(t, env, exp.leader, int64(leaderTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("部门负责人通过：HTTP %d", got)
	}
	managerTask := findExpenseTodoTask(t, env, exp.manager, claimID, true)
	if got := actExpenseTask(t, env, exp.manager, int64(managerTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("财务通过：HTTP %d", got)
	}
}

// instanceOf 取详情里的审批实例视图（无实例 Fatal）。
func instanceOf(t *testing.T, detail map[string]any) map[string]any {
	t.Helper()
	approval, _ := detail["approval"].(map[string]any)
	inst, _ := approval["instance"].(map[string]any)
	if inst == nil {
		t.Fatalf("详情缺少审批实例：%+v", detail)
	}
	return inst
}

// expenseClaimNoFor 是测试侧独立实现的展示单号（BX-%06d），用于断言快照。
func expenseClaimNoFor(id int64) string {
	return fmt.Sprintf("BX-%06d", id)
}

// createExpenseClaim 创建一张标准报销单（100.00 + 28.50 = 128.50）。
func createExpenseClaim(t *testing.T, env *testutil.IntegrationEnv, token, title string) int64 {
	t.Helper()
	return createExpenseClaimAPI(t, env, token, expenseClaimBody(title, "客户拜访差旅",
		expenseItemBody("交通", "100.00", "2026-10-01", "高铁"),
		expenseItemBody("餐饮", "28.50", "2026-10-01", "工作餐"),
	))
}

// ---------- 提交 → 通过 ----------

// TestIntegrationExpenseSubmitAndApprove 主流程：提交生成实例（单号/金额/部门
// 快照）→ 部门负责人待办 → 财务待办 → 通过终态；通过后明细/删除/重提一律 409；
// 详情时间线与实例状态一致。
func TestIntegrationExpenseSubmitAndApprove(t *testing.T) {
	exp := newExpenseApprovalEnv(t)
	env := exp.env
	claimID := createExpenseClaim(t, env, exp.employee, "十月客户拜访差旅")

	// 提交前：草稿无实例；待办为空。
	draft := mustExpenseDetail(t, env, exp.employee, claimID)
	if draft["approval"] != nil || draft["approvalInstanceId"] != nil {
		t.Fatalf("草稿不应有审批实例：%+v", draft)
	}
	if task := findExpenseTodoTask(t, env, exp.leader, claimID, false); task != nil {
		t.Fatalf("提交前不应有部门负责人待办：%+v", task)
	}

	submitted := mustSubmitExpenseClaim(t, env, exp.employee, claimID)
	if submitted["status"].(float64) != 1 {
		t.Fatalf("提交后状态应为 1（待审）：%+v", submitted)
	}
	instanceID := int64(submitted["approvalInstanceId"].(float64))
	if instanceID <= 0 {
		t.Fatalf("提交后应写入审批实例 ID：%+v", submitted)
	}

	// 快照：标题含单号与标题，摘要含单号/部门/金额。
	inst := instanceOf(t, submitted)
	claimNo := expenseClaimNoFor(claimID)
	title, _ := inst["title"].(string)
	summary, _ := inst["summary"].(string)
	if !strings.Contains(title, claimNo) || !strings.Contains(title, "十月客户拜访差旅") {
		t.Fatalf("实例标题应含单号与标题：%q", title)
	}
	for _, want := range []string{claimNo, "研发部", "128.50"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("实例摘要 %q 应包含 %q", summary, want)
		}
	}
	if inst["businessType"] != expenseApprovalType || int64(inst["businessId"].(float64)) != claimID {
		t.Fatalf("实例业务标识不符：%+v", inst)
	}

	// 待办：仅部门负责人可见；财务节点尚未创建。
	leaderTask := findExpenseTodoTask(t, env, exp.leader, claimID, true)
	if leaderTask["nodeName"] != "部门负责人审批" {
		t.Fatalf("首个待办应为部门负责人审批：%+v", leaderTask)
	}
	if task := findExpenseTodoTask(t, env, exp.manager, claimID, false); task != nil {
		t.Fatalf("节点 1 未办理前财务不应有待办：%+v", task)
	}
	// 重复提交 409。
	if status, api := submitExpenseClaim(t, env, exp.employee, claimID); status != http.StatusConflict {
		t.Fatalf("待审重复提交应 409，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 部门负责人通过 → 财务待办出现。
	if got := actExpenseTask(t, env, exp.leader, int64(leaderTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("部门负责人通过：HTTP %d", got)
	}
	if task := findExpenseTodoTask(t, env, exp.leader, claimID, false); task != nil {
		t.Fatalf("通过后部门负责人待办应消失：%+v", task)
	}
	managerTask := findExpenseTodoTask(t, env, exp.manager, claimID, true)
	if managerTask["nodeName"] != "财务审批" {
		t.Fatalf("第二个待办应为财务审批：%+v", managerTask)
	}
	if got := actExpenseTask(t, env, exp.manager, int64(managerTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("财务通过：HTTP %d", got)
	}

	// 终态：报销单通过；实例通过；时间线两条通过记录。
	approved := mustExpenseDetail(t, env, exp.employee, claimID)
	if approved["status"].(float64) != 2 {
		t.Fatalf("通过后报销单状态应为 2：%+v", approved)
	}
	if approved["rejectReason"] != "" {
		t.Fatalf("通过后不应残留驳回原因：%+v", approved)
	}
	approvedInst := instanceOf(t, approved)
	if approvedInst["status"].(float64) != 1 || approvedInst["endedAt"] == nil {
		t.Fatalf("审批实例应为通过且已结束：%+v", approvedInst)
	}
	approval, _ := approved["approval"].(map[string]any)
	records, _ := approval["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("时间线应有两条通过记录：%+v", records)
	}
	for _, raw := range records {
		rec, _ := raw.(map[string]any)
		if rec["action"].(float64) != 1 {
			t.Fatalf("时间线动作应全部为通过（1）：%+v", rec)
		}
	}

	// 通过即终态：明细编辑/删除/重提/附件上传一律 409。
	body := expenseClaimBody("改标题", "改事由", expenseItemBody("办公", "1.00", "2026-10-02", ""))
	body["id"] = claimID
	if status, api := env.Request(t, http.MethodPut, "/oa/expense/claim", exp.employee, body); status != http.StatusConflict {
		t.Fatalf("通过后修改明细应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
	if status, api := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/expense/claim/%d", claimID), exp.employee, nil); status != http.StatusConflict {
		t.Fatalf("通过后删除应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
	if status, api := submitExpenseClaim(t, env, exp.employee, claimID); status != http.StatusConflict {
		t.Fatalf("通过后重提应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
	if status, api := uploadExpenseFile(t, env, exp.employee, claimID, "late.txt", []byte("late")); status != http.StatusConflict || api.Code == 0 {
		t.Fatalf("通过后上传附件应 409，实际 HTTP %d code %d", status, api.Code)
	}
	// 待办全部消化。
	if task := findExpenseTodoTask(t, env, exp.manager, claimID, false); task != nil {
		t.Fatalf("通过后财务待办应消失：%+v", task)
	}
}

// ---------- 驳回 / 重提 ----------

// TestIntegrationExpenseRejectAndResubmit 驳回：回「已驳回」+ 原因（按 200 字
// 截断）；可编辑并重提，重提清空原因并生成新实例。
func TestIntegrationExpenseRejectAndResubmit(t *testing.T) {
	exp := newExpenseApprovalEnv(t)
	env := exp.env
	claimID := createExpenseClaim(t, env, exp.employee, "驳回用例")
	mustSubmitExpenseClaim(t, env, exp.employee, claimID)
	firstInstance := int64(mustExpenseDetail(t, env, exp.employee, claimID)["approvalInstanceId"].(float64))

	leaderTask := findExpenseTodoTask(t, env, exp.leader, claimID, true)
	longReason := strings.Repeat("票据不合规", 60) // 300 字，超过列宽 200
	if got := actExpenseTask(t, env, exp.leader, int64(leaderTask["id"].(float64)), "reject", longReason); got != http.StatusOK {
		t.Fatalf("部门负责人驳回：HTTP %d", got)
	}

	rejected := mustExpenseDetail(t, env, exp.employee, claimID)
	if rejected["status"].(float64) != 3 {
		t.Fatalf("驳回后状态应为 3：%+v", rejected)
	}
	reason, _ := rejected["rejectReason"].(string)
	if n := utf8.RuneCountInString(reason); n != 200 {
		t.Fatalf("驳回原因应截断到 200 字，实际 %d 字：%q", n, reason)
	}
	if !strings.HasPrefix(reason, "票据不合规") {
		t.Fatalf("驳回原因应保留可读前缀：%q", reason)
	}
	if inst := instanceOf(t, rejected); inst["status"].(float64) != 2 {
		t.Fatalf("审批实例应为驳回（2）：%+v", inst)
	}
	// 驳回结束实例：财务无待办。
	if task := findExpenseTodoTask(t, env, exp.manager, claimID, false); task != nil {
		t.Fatalf("驳回后财务不应有待办：%+v", task)
	}

	// 已驳回可编辑（明细替换 + 合计重算）。
	updated := updateExpenseClaimAPI(t, env, exp.employee, claimID,
		expenseClaimBody("驳回后修改", "补充发票", expenseItemBody("交通", "88.00", "2026-10-03", "改签")))
	if updated.Status != 3 || updated.TotalAmount != "88.00" {
		t.Fatalf("已驳回可编辑且合计重算：%+v", updated)
	}

	// 重提：状态回待审、原因清空、实例换新。
	resubmitted := mustSubmitExpenseClaim(t, env, exp.employee, claimID)
	if resubmitted["status"].(float64) != 1 || resubmitted["rejectReason"] != "" {
		t.Fatalf("重提后应为待审且清空驳回原因：%+v", resubmitted)
	}
	secondInstance := int64(resubmitted["approvalInstanceId"].(float64))
	if secondInstance == firstInstance {
		t.Fatalf("重提应生成新实例（旧 %d）", firstInstance)
	}
	if inst := instanceOf(t, resubmitted); inst["status"].(float64) != 0 {
		t.Fatalf("重提后新实例应为运行中：%+v", inst)
	}
	if !strings.Contains(summaryOf(t, resubmitted), "88.00") {
		t.Fatalf("重提快照金额应为修改后的 88.00：%q", summaryOf(t, resubmitted))
	}
	// 重提后部门负责人可再次办理。
	if task := findExpenseTodoTask(t, env, exp.leader, claimID, true); int64(task["id"].(float64)) <= 0 {
		t.Fatalf("重提后应重新生成部门负责人待办：%+v", task)
	}
}

// summaryOf 取实例摘要文本。
func summaryOf(t *testing.T, detail map[string]any) string {
	t.Helper()
	summary, _ := instanceOf(t, detail)["summary"].(string)
	return summary
}

// ---------- 撤回 ----------

// TestIntegrationExpenseWithdraw 撤回：首个审批动作前撤回 → 状态 4、实例撤回、
// 待办消失；已有审批动作后撤回 409 且状态保持待审。
func TestIntegrationExpenseWithdraw(t *testing.T) {
	exp := newExpenseApprovalEnv(t)
	env := exp.env

	t.Run("首个审批前撤回", func(t *testing.T) {
		claimID := createExpenseClaim(t, env, exp.employee, "撤回报销")
		submitted := mustSubmitExpenseClaim(t, env, exp.employee, claimID)
		instanceID := int64(submitted["approvalInstanceId"].(float64))
		findExpenseTodoTask(t, env, exp.leader, claimID, true)

		status, api := env.Request(t, http.MethodPost,
			fmt.Sprintf("/bpm/instance/%d/withdraw", instanceID), exp.employee, map[string]any{"comment": "不报了"})
		mustOK(t, "发起人撤回", status, api)

		detail := mustExpenseDetail(t, env, exp.employee, claimID)
		if detail["status"].(float64) != 4 {
			t.Fatalf("撤回后状态应为 4：%+v", detail)
		}
		inst := instanceOf(t, detail)
		if inst["status"].(float64) != 3 {
			t.Fatalf("审批实例应为撤回（3）：%+v", inst)
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
			t.Fatalf("时间线应包含撤回记录：%+v", records)
		}
		if task := findExpenseTodoTask(t, env, exp.leader, claimID, false); task != nil {
			t.Fatalf("撤回后待办应消失：%+v", task)
		}
		// 撤回落 ExpenseStatusWithdrawn（4，与 #54 状态常量/前端筛选一致），
		// 不属于可重提窗口（仅草稿/已驳回可提交，守卫见 ValidateExpenseAction）。
		if status, api := submitExpenseClaim(t, env, exp.employee, claimID); status != http.StatusConflict {
			t.Fatalf("撤回后重提应 409，实际 HTTP %d msg %q", status, api.Msg)
		}
	})

	t.Run("已有审批动作后撤回409", func(t *testing.T) {
		claimID := createExpenseClaim(t, env, exp.employee, "已办理撤回")
		submitted := mustSubmitExpenseClaim(t, env, exp.employee, claimID)
		instanceID := int64(submitted["approvalInstanceId"].(float64))
		leaderTask := findExpenseTodoTask(t, env, exp.leader, claimID, true)
		if got := actExpenseTask(t, env, exp.leader, int64(leaderTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
			t.Fatalf("部门负责人通过：HTTP %d", got)
		}
		status, api := env.Request(t, http.MethodPost,
			fmt.Sprintf("/bpm/instance/%d/withdraw", instanceID), exp.employee, map[string]any{"comment": ""})
		if status != http.StatusConflict {
			t.Fatalf("首个节点已办理后撤回应 409，实际 HTTP %d msg %q", status, api.Msg)
		}
		if detail := mustExpenseDetail(t, env, exp.employee, claimID); detail["status"].(float64) != 1 {
			t.Fatalf("撤回失败后状态应保持待审：%+v", detail)
		}
	})
}

// ---------- 可见范围 / 租户隔离 ----------

// TestIntegrationExpenseVisibilityAndTenant 三档可见范围（本人 / 部门负责人
// 子树 / oa:expense:manage 全租户）对列表与详情一致生效；跨租户 404。
func TestIntegrationExpenseVisibilityAndTenant(t *testing.T) {
	exp := newExpenseApprovalEnv(t)
	env := exp.env

	// 员工：1 条待审（有审批时间线）；财务部局外人：1 条草稿。
	employeeClaim := createExpenseClaim(t, env, exp.employee, "员工报销")
	mustSubmitExpenseClaim(t, env, exp.employee, employeeClaim)

	outsiderID, outsider := createUserWithMenus(t, env, exp.admin, "exp_outsider", 103, []int64{
		expenseMineMenuID, expenseCreate, expenseUpdate, expenseDelete, expenseSubmit,
	})
	outsiderClaim := createExpenseClaim(t, env, outsider, "局外人报销")
	_ = outsiderID

	// 列表范围：员工 1；负责人（101 子树）1；财务 manage 2；局外人 1。
	assertExpensePageTotal(t, env, exp.employee, 1, "本人列表")
	assertExpensePageTotal(t, env, exp.leader, 1, "部门负责人列表")
	assertExpensePageTotal(t, env, exp.manager, 2, "财务全租户列表")
	assertExpensePageTotal(t, env, outsider, 1, "局外人列表")

	// 详情：负责人可见下属且能看时间线；局外人 404；财务可见全部。
	leaderDetail := mustExpenseDetail(t, env, exp.leader, employeeClaim)
	if leaderDetail["approval"] == nil {
		t.Fatalf("部门负责人应看到下属报销的审批时间线：%+v", leaderDetail)
	}
	if status, _ := getExpenseClaimAPI(t, env, exp.leader, outsiderClaim); status != http.StatusNotFound {
		t.Fatalf("负责人读子树外详情应 404，实际 HTTP %d", status)
	}
	if status, detail := getExpenseClaimAPI(t, env, exp.manager, outsiderClaim); status != http.StatusOK || detail.UserID != outsiderID {
		t.Fatalf("财务读全租户详情应 200 且 userId=%d：HTTP %d %+v", outsiderID, status, detail)
	}
	if status, _ := getExpenseClaimAPI(t, env, outsider, employeeClaim); status != http.StatusNotFound {
		t.Fatalf("局外人读他人详情应 404，实际 HTTP %d", status)
	}
	// 附件列表同样按可见范围收口。
	if status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/expense/claim/%d/files", employeeClaim), outsider, nil); status != http.StatusNotFound {
		t.Fatalf("局外人读他人附件应 404，实际 HTTP %d", status)
	}

	// 跨租户：列表为空、详情/提交 404。
	tenant2 := createOATenant(t, env, exp.admin, "expense_scope_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	assertExpensePageTotal(t, env, token2, 0, "租户 2 列表")
	if status, _ := getExpenseClaimAPI(t, env, token2, employeeClaim); status != http.StatusNotFound {
		t.Fatalf("跨租户详情应 404，实际 HTTP %d", status)
	}
	if status, _ := submitExpenseClaim(t, env, token2, employeeClaim); status != http.StatusNotFound {
		t.Fatalf("跨租户提交应 404，实际 HTTP %d", status)
	}
}

// assertExpensePageTotal 断言报销分页 total 与条数。
func assertExpensePageTotal(t *testing.T, env *testutil.IntegrationEnv, token string, want int64, phase string) {
	t.Helper()
	status, total, rows := pageExpenseClaimsAPI(t, env, token, "")
	if status != http.StatusOK {
		t.Fatalf("%s：HTTP %d", phase, status)
	}
	if total != want || int64(len(rows)) != want {
		t.Fatalf("%s total=%d len=%d，期望 %d", phase, total, len(rows), want)
	}
}

// ---------- 权限码 / 归属 ----------

// TestIntegrationExpenseSubmitPermissions 提交逐码与归属：无 5234 403；
// 持码但非发起人 404；审批人无法经报销组接口代替发起人提交。
func TestIntegrationExpenseSubmitPermissions(t *testing.T) {
	exp := newExpenseApprovalEnv(t)
	env := exp.env
	claimID := createExpenseClaim(t, env, exp.employee, "权限用例")

	// 无提交码用户（可建草稿，但只授予登录）→ 403。
	_, noPerm := createUserWithMenus(t, env, exp.admin, "exp_no_submit", 101, []int64{expenseMineMenuID})
	if status, api := submitExpenseClaim(t, env, noPerm, claimID); status != http.StatusForbidden {
		t.Fatalf("无 oa:expense:submit 提交应 403，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 持提交码但非发起人 → 404（可见范围不放宽写动作）。
	_, outsider := createUserWithMenus(t, env, exp.admin, "exp_submit_other", 102, []int64{
		expenseMineMenuID, expenseCreate, expenseSubmit,
	})
	if status, api := submitExpenseClaim(t, env, outsider, claimID); status != http.StatusNotFound {
		t.Fatalf("非发起人提交应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 审批人（部门负责人）未持提交码 → 403。
	if status, _ := submitExpenseClaim(t, env, exp.leader, claimID); status != http.StatusForbidden {
		t.Fatalf("负责人无提交码应 403，实际 HTTP %d", status)
	}
	// 被拒的提交不改变状态。
	if detail := mustExpenseDetail(t, env, exp.employee, claimID); detail["status"].(float64) != 0 {
		t.Fatalf("被拒的提交不得改变状态：%+v", detail)
	}
}

// TestIntegrationExpenseSubmitWithoutDeptLeader 审批人解析不到 400：部门（及其
// 祖先）没有负责人时整笔提交回滚，单据保持草稿且无实例。
func TestIntegrationExpenseSubmitWithoutDeptLeader(t *testing.T) {
	exp := newExpenseApprovalEnv(t)
	env := exp.env
	// 财务部（103）与总公司（100）均无负责人（用例只设置了 101）。
	_, orphan := createUserWithMenus(t, env, exp.admin, "exp_orphan", 103, []int64{
		expenseMineMenuID, expenseCreate, expenseSubmit,
	})
	claimID := createExpenseClaim(t, env, orphan, "无负责人报销")

	status, api := submitExpenseClaim(t, env, orphan, claimID)
	if status != http.StatusBadRequest {
		t.Fatalf("解析不到审批人应 400，实际 HTTP %d msg %q", status, api.Msg)
	}
	if !strings.Contains(api.Msg, "解析不到审批人") {
		t.Fatalf("400 提示应说明审批人解析失败：%q", api.Msg)
	}
	detail := mustExpenseDetail(t, env, orphan, claimID)
	if detail["status"].(float64) != 0 || detail["approvalInstanceId"] != nil || detail["approval"] != nil {
		t.Fatalf("提交失败应回滚到草稿且无实例：%+v", detail)
	}
}

// ---------- 默认流程种子 ----------

// TestIntegrationExpenseDefaultDefinitionSeed 默认流程种子：oa_expense_approval
// v1 已发布（节点 1 部门负责人、节点 2 oa:expense:manage）；重复 seed 不新增、
// 不覆盖租户已维护的内容。
func TestIntegrationExpenseDefaultDefinitionSeed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	def := mustExpenseDefinition(t, env, admin)
	if def.Version != 1 || def.Status != 1 || def.Key != expenseApprovalType ||
		def.BusinessType != expenseApprovalType {
		t.Fatalf("默认报销流程应为已发布 v1：%+v", def)
	}
	if len(def.Nodes) != 2 ||
		def.Nodes[0].AssigneeType != bpmmodel.AssigneeInitiatorDeptLeader ||
		def.Nodes[1].AssigneeType != bpmmodel.AssigneePermission ||
		def.Nodes[1].AssigneeValue != "oa:expense:manage" {
		t.Fatalf("默认报销流程节点不符：%+v", def.Nodes)
	}

	// 租户已维护（改名）后重复 seed：不新增版本、不覆盖。
	if err := tenant.Skip(env.DB).Model(&bpmmodel.ProcessDefinition{}).
		Where("tenant_id = ? AND `key` = ? AND version = ?", 1, expenseApprovalType, 1).
		Update("name", "租户自定义报销流程").Error; err != nil {
		t.Fatalf("模拟租户维护流程定义: %v", err)
	}
	if err := seed.Run(env.DB, false); err != nil {
		t.Fatalf("重复 seed: %v", err)
	}
	again := mustExpenseDefinition(t, env, admin)
	if again.Name != "租户自定义报销流程" {
		t.Fatalf("重复 seed 不应覆盖租户维护的名称：%q", again.Name)
	}
	if again.Version != 1 {
		t.Fatalf("重复 seed 不应新增版本：%+v", again)
	}
}

// definitionRow 是流程定义分页行的测试视图。
type definitionRow struct {
	ID           int64  `json:"id"`
	Key          string `json:"key"`
	Version      int    `json:"version"`
	Name         string `json:"name"`
	BusinessType string `json:"businessType"`
	Status       int8   `json:"status"`
	Nodes        []struct {
		Name          string `json:"name"`
		AssigneeType  string `json:"assigneeType"`
		AssigneeValue string `json:"assigneeValue"`
	} `json:"nodes"`
}

// mustExpenseDefinition 读 oa_expense_approval 的定义分页并断言恰好一条。
func mustExpenseDefinition(t *testing.T, env *testutil.IntegrationEnv, token string) definitionRow {
	t.Helper()
	status, api := env.Request(t, http.MethodGet,
		"/bpm/definition/page?businessType="+expenseApprovalType+"&pageNo=1&pageSize=10", token, nil)
	mustOK(t, "查询报销流程定义", status, api)
	var page struct {
		List  []definitionRow `json:"list"`
		Total int64           `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析流程定义分页: %v（data=%s）", err, string(api.Data))
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("报销默认流程应恰好 1 条：total=%d list=%+v", page.Total, page.List)
	}
	return page.List[0]
}
