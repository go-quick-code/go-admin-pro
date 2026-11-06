//go:build integration

// 请假申请与 bpm 审批的 HTTP 集成用例（issue #52，docs/oa-mvp-spec.md §4.8）：
//   - 提交 → 部门负责人待办可见 → 通过 → 额度扣减；
//   - 驳回/撤回不改额度；
//   - 半天方向、跨年、零工作日、槽位重叠、余额不足的拒绝与可读错误；
//   - 详情审批时间线与附件（上传/下载/提交后冻结）；
//   - 权限码逐码与租户隔离（跨租户/非本人一律 404）。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t52，-p 1 串行跑包）。
package oa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 请假申请测试的菜单/按钮码（菜单 529 + 5291–5296，issue #52/#53 种子）。
const (
	leaveRequestMenuID   = int64(529)
	leaveRequestCreate   = int64(5291)
	leaveRequestUpdate   = int64(5292)
	leaveRequestDelete   = int64(5293)
	leaveRequestSubmit   = int64(5294)
	leaveRequestWithdraw = int64(5295)
	leaveRequestCancel   = int64(5296)
	bpmTaskMenuID        = int64(512)
	bpmTaskHandleButton  = int64(5121)
	leaveManageMenuID    = int64(522)
	leaveApprovalType    = "oa_leave_approval"
)

// leaveRequestEnv 聚合一个用例的类型/用户/流程定义基础设施。
type leaveRequestEnv struct {
	env          *testutil.IntegrationEnv
	admin        string
	annualTypeID int64
	compTypeID   int64
	employeeID   int64
	employee     string
	leader       string
	manager      string
}

// createUserWithMenus 建角色 + 用户（指定部门），返回 (用户 ID, token)。
func createUserWithMenus(t *testing.T, env *testutil.IntegrationEnv, admin, username string, deptID int64, menuIDs []int64) (int64, string) {
	t.Helper()
	roleID := createRoleWithMenus(t, env, admin, "role_"+username, menuIDs)
	status, api := env.Request(t, http.MethodPost, "/system/user", admin, map[string]any{
		"username": username, "nickname": username, "password": "123456",
		"deptId": deptID, "status": 0, "roleIds": []int64{roleID},
	})
	mustOK(t, "创建用户 "+username, status, api)
	status, api = env.Request(t, http.MethodGet,
		"/system/user/page?username="+username+"&pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "查询用户 "+username, status, api)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("用户 %s 应唯一命中: %v（data=%s）", username, err, string(api.Data))
	}
	return page.List[0].ID, env.Login(t, username, "123456")
}

// createPublishedLeaveDefinition 创建并发布 oa_leave_approval 默认流程
// （节点 1 发起人部门负责人 → 节点 2 权限码 oa:leave:manage，spec §4.8）。
func createPublishedLeaveDefinition(t *testing.T, env *testutil.IntegrationEnv, admin string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/bpm/definition", admin, map[string]any{
		"key":          leaveApprovalType,
		"name":         "请假审批",
		"businessType": leaveApprovalType,
		"nodes": []map[string]any{
			{"name": "部门负责人审批", "assigneeType": "initiator_dept_leader"},
			{"name": "HR 审批", "assigneeType": "permission", "assigneeValue": "oa:leave:manage"},
		},
	})
	mustOK(t, "创建请假流程定义", status, api)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &created); err != nil {
		t.Fatalf("解析流程定义响应: %v（data=%s）", err, string(api.Data))
	}
	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/definition/%d/publish", created.ID), admin, nil)
	mustOK(t, "发布请假流程定义", status, api)
}

// newLeaveRequestEnv 搭建：类型（年假 5 天 / 调休 0.5 天）、员工/部门负责人/HR
// 用户、部门负责人绑定与已发布流程定义。
func newLeaveRequestEnv(t *testing.T) *leaveRequestEnv {
	t.Helper()
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	annualID, _ := setupLeaveTypes(t, env, admin)
	status, compID, msg := createLeaveType(t, env, admin, map[string]any{
		"name": "调休", "code": "comp-time", "balanceManaged": true, "annualQuota": "0.5",
	})
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建调休类型：HTTP %d msg %q", status, msg)
	}

	employeeID, employee := createUserWithMenus(t, env, admin, "leave_emp", 101, []int64{
		leaveRequestMenuID, leaveRequestCreate, leaveRequestUpdate, leaveRequestDelete, leaveRequestSubmit,
		leaveRequestWithdraw, leaveRequestCancel, leaveMineMenuID,
	})
	leaderID, leader := createUserWithMenus(t, env, admin, "leave_leader", 101, []int64{
		bpmTaskMenuID, bpmTaskHandleButton, leaveRequestWithdraw, leaveRequestCancel,
	})
	_, manager := createUserWithMenus(t, env, admin, "leave_manager", 102, []int64{
		bpmTaskMenuID, bpmTaskHandleButton, leaveManageMenuID,
	})
	setDeptLeader(t, env, admin, 101, leaderID)

	if status, created, total, msg := initBalances(t, env, admin, map[string]any{
		"year": 2026, "userIds": []int64{employeeID}, "typeIds": []int64{annualID, compID},
	}); status != http.StatusOK || created != 2 || total != 2 || msg != "" {
		t.Fatalf("初始化额度：HTTP %d created %d total %d msg %q", status, created, total, msg)
	}
	createPublishedLeaveDefinition(t, env, admin)

	return &leaveRequestEnv{
		env: env, admin: admin, annualTypeID: annualID, compTypeID: compID,
		employeeID: employeeID, employee: employee, leader: leader, manager: manager,
	}
}

// ---------- HTTP 速记 ----------

// createLeaveDraft 创建草稿，返回 (HTTP 状态, 申请行, msg)。
func createLeaveDraft(t *testing.T, env *testutil.IntegrationEnv, token string, typeID int64, start string, startHalf int8, end string, endHalf int8, reason string) (int, map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/oa/leave/request", token, map[string]any{
		"leaveTypeId": typeID, "startDate": start, "startHalf": startHalf,
		"endDate": end, "endHalf": endHalf, "reason": reason,
	})
	if api.Code != 0 {
		return status, nil, api.Msg
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建请假响应: %v（data=%s）", err, string(api.Data))
	}
	return status, row, ""
}

// submitLeave 提交申请，返回 (HTTP 状态, 申请行, msg)。
func submitLeave(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, map[string]any, string) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/leave/request/%d/submit", id), token, nil)
	if api.Code != 0 {
		return status, nil, api.Msg
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析提交请假响应: %v（data=%s）", err, string(api.Data))
	}
	return status, row, ""
}

// getLeaveDetail 读取详情（原始响应，便于断言 404/403）。
func getLeaveDetail(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("/oa/leave/request/%d", id), token, nil)
}

// mustLeaveDetail 读取详情并断言成功，返回 data 行。
func mustLeaveDetail(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) map[string]any {
	t.Helper()
	status, api := getLeaveDetail(t, env, token, id)
	mustOK(t, "读取请假详情", status, api)
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析请假详情: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// findMyTodoTask 在我的待办里按业务 ID 找任务；required=false 时允许不存在。
func findMyTodoTask(t *testing.T, env *testutil.IntegrationEnv, token string, businessID int64, required bool) map[string]any {
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
		if row["businessType"] == leaveApprovalType && int64(row["businessId"].(float64)) == businessID {
			return row
		}
	}
	if required {
		t.Fatalf("待办中未找到业务 %s/%d：%+v", leaveApprovalType, businessID, page.List)
	}
	return nil
}

// approveLeaveTask 办理任务（通过/驳回），返回 HTTP 状态。
func approveLeaveTask(t *testing.T, env *testutil.IntegrationEnv, token string, taskID int64, action, comment string) int {
	t.Helper()
	path := fmt.Sprintf("/bpm/task/%d/approve", taskID)
	if action == "reject" {
		path = fmt.Sprintf("/bpm/task/%d/reject", taskID)
	}
	status, _ := env.Request(t, http.MethodPost, path, token, map[string]any{"comment": comment})
	return status
}

// annualBalanceOf 读取员工某年度年假额度行。
func annualBalanceOf(t *testing.T, env *testutil.IntegrationEnv, token string, year int) map[string]any {
	t.Helper()
	status, data, msg := myBalances(t, env, token, fmt.Sprintf("?year=%d", year))
	if status != http.StatusOK || msg != "" {
		t.Fatalf("读取我的额度：HTTP %d msg %q", status, msg)
	}
	item := mineItemOf(data, "annual")
	if item == nil {
		t.Fatalf("我的额度缺少 annual 类型: %+v", data)
	}
	balance, _ := item["balance"].(map[string]any)
	if balance == nil {
		t.Fatalf("annual 额度行缺失: %+v", item)
	}
	return balance
}

// uploadLeaveFile 以 multipart/form-data 上传请假附件。
func uploadLeaveFile(t *testing.T, env *testutil.IntegrationEnv, token string, id int64, filename string, content []byte) (int, testutil.APIResponse) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造 multipart 文件字段: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL(fmt.Sprintf("/oa/leave/request/%d/files", id)), &buf)
	if err != nil {
		t.Fatalf("构造上传请求: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("上传请假附件: %v", err)
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		t.Fatalf("解析上传响应: %v", err)
	}
	return resp.StatusCode, api
}

// downloadLeaveFile 按响应里的鉴权 URL 下载附件。
func downloadLeaveFile(t *testing.T, env *testutil.IntegrationEnv, token, fileURL string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.Server.URL+fileURL, nil)
	if err != nil {
		t.Fatalf("构造下载请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载请假附件: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取下载内容: %v", err)
	}
	return resp.StatusCode, body
}

// ---------- 主流程 ----------

// TestIntegrationLeaveRequestApprovalFlow 提交 → 待办 → 通过 → 扣额度；
// 驳回/撤回不改额度；详情时间线与附件状态一致。
func TestIntegrationLeaveRequestApprovalFlow(t *testing.T) {
	le := newLeaveRequestEnv(t)
	env := le.env

	// 1) 草稿 → 上传附件 → 提交（周五下午到周一上午 = 1.0 天，周末不计）。
	status, draft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
		"2026-11-06", 1, "2026-11-09", 0, "家里有事")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建草稿：HTTP %d msg %q", status, msg)
	}
	id := int64(draft["id"].(float64))
	if draft["status"].(float64) != 0 || draft["durationDays"] != "1.00" {
		t.Fatalf("草稿状态/时长不符：%+v", draft)
	}
	uploadStatus, uploadAPI := uploadLeaveFile(t, env, le.employee, id, "proof.txt", []byte("proof-content"))
	if uploadStatus != http.StatusOK || uploadAPI.Code != 0 {
		t.Fatalf("上传附件：HTTP %d code %d msg %q", uploadStatus, uploadAPI.Code, uploadAPI.Msg)
	}

	status, submitted, msg := submitLeave(t, env, le.employee, id)
	if status != http.StatusOK || msg != "" {
		t.Fatalf("提交请假：HTTP %d msg %q", status, msg)
	}
	if submitted["status"].(float64) != 1 || submitted["approvalInstanceId"] == nil {
		t.Fatalf("提交后应为待审且带实例 ID：%+v", submitted)
	}

	// 2) 部门负责人待办可见 → 通过；随后 HR（oa:leave:manage）待办出现 → 通过。
	leaderTask := findMyTodoTask(t, env, le.leader, id, true)
	if got := approveLeaveTask(t, env, le.leader, int64(leaderTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("部门负责人通过：HTTP %d", got)
	}
	managerTask := findMyTodoTask(t, env, le.manager, id, true)
	if got := approveLeaveTask(t, env, le.manager, int64(managerTask["id"].(float64)), "approve", "同意"); got != http.StatusOK {
		t.Fatalf("HR 通过：HTTP %d", got)
	}

	// 3) 详情：状态通过、时间线两条记录、附件仍可见且可下载。
	detail := mustLeaveDetail(t, env, le.employee, id)
	if detail["status"].(float64) != 2 {
		t.Fatalf("通过后状态应为 2：%+v", detail)
	}
	approval, _ := detail["approval"].(map[string]any)
	records, _ := approval["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("审批时间线应有 2 条记录，实际 %d：%+v", len(records), records)
	}
	files, _ := detail["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("详情应带 1 个附件，实际 %d", len(files))
	}
	file := files[0].(map[string]any)
	downloadStatus, body := downloadLeaveFile(t, env, le.employee, file["url"].(string))
	if downloadStatus != http.StatusOK || string(body) != "proof-content" {
		t.Fatalf("附件下载：HTTP %d body %q", downloadStatus, string(body))
	}
	// 提交后附件冻结：再上传 400。
	if status, _ := uploadLeaveFile(t, env, le.employee, id, "late.txt", []byte("late")); status != http.StatusBadRequest {
		t.Fatalf("通过后上传附件应 400，实际 HTTP %d", status)
	}

	// 4) 额度：5 - 1 = 4。
	balance := annualBalanceOf(t, env, le.employee, 2026)
	if balance["used"] != "1.00" || balance["available"] != "4.00" {
		t.Fatalf("通过后额度应为 used=1.00/available=4.00：%+v", balance)
	}

	// 5) 驳回不改额度。
	status, rejectDraft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
		"2026-11-10", 0, "2026-11-10", 0, "临时安排")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建驳回用例草稿：HTTP %d msg %q", status, msg)
	}
	rejectID := int64(rejectDraft["id"].(float64))
	if status, _, msg := submitLeave(t, env, le.employee, rejectID); status != http.StatusOK {
		t.Fatalf("提交驳回用例：HTTP %d msg %q", status, msg)
	}
	rejectTask := findMyTodoTask(t, env, le.leader, rejectID, true)
	if got := approveLeaveTask(t, env, le.leader, int64(rejectTask["id"].(float64)), "reject", "时间冲突"); got != http.StatusOK {
		t.Fatalf("驳回：HTTP %d", got)
	}
	rejected := mustLeaveDetail(t, env, le.employee, rejectID)
	if rejected["status"].(float64) != 3 || rejected["rejectReason"] != "时间冲突" {
		t.Fatalf("驳回后状态/原因不符：%+v", rejected)
	}
	if balance = annualBalanceOf(t, env, le.employee, 2026); balance["used"] != "1.00" {
		t.Fatalf("驳回不应改额度：%+v", balance)
	}

	// 6) 撤回不改额度（通用 bpm 撤回端点，发起人身份）。
	status, withdrawDraft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
		"2026-11-11", 0, "2026-11-11", 0, "待定")
	if status != http.StatusOK || msg != "" {
		t.Fatalf("创建撤信用例草稿：HTTP %d msg %q", status, msg)
	}
	withdrawID := int64(withdrawDraft["id"].(float64))
	status, withdrawSubmitted, msg := submitLeave(t, env, le.employee, withdrawID)
	if status != http.StatusOK || msg != "" {
		t.Fatalf("提交撤信用例：HTTP %d msg %q", status, msg)
	}
	instanceID := int64(withdrawSubmitted["approvalInstanceId"].(float64))
	status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/instance/%d/withdraw", instanceID), le.employee, map[string]any{"comment": "不请了"})
	mustOK(t, "撤回审批实例", status, api)
	withdrawn := mustLeaveDetail(t, env, le.employee, withdrawID)
	if withdrawn["status"].(float64) != 4 {
		t.Fatalf("撤回后状态应为 4：%+v", withdrawn)
	}
	if balance = annualBalanceOf(t, env, le.employee, 2026); balance["used"] != "1.00" {
		t.Fatalf("撤回不应改额度：%+v", balance)
	}

	// 7) 列表：本人 3 条（通过/驳回/撤回），状态过滤可用。
	status, api = env.Request(t, http.MethodGet, "/oa/leave/request/page?pageNo=1&pageSize=10", le.employee, nil)
	mustOK(t, "本人请假分页", status, api)
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析请假分页: %v", err)
	}
	if page.Total != 3 || len(page.List) != 3 {
		t.Fatalf("本人请假分页 total=%d len=%d，期望 3/3", page.Total, len(page.List))
	}
	status, api = env.Request(t, http.MethodGet, "/oa/leave/request/page?status=3", le.employee, nil)
	mustOK(t, "按状态过滤", status, api)
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total != 1 {
		t.Fatalf("status=3 过滤应 1 条: %v（data=%s）", err, string(api.Data))
	}
}

// TestIntegrationLeaveRequestValidation 校验拒绝：半天方向、跨年、零工作日、
// 槽位重叠、余额不足，错误信息可读。
func TestIntegrationLeaveRequestValidation(t *testing.T) {
	le := newLeaveRequestEnv(t)
	env := le.env

	t.Run("同日PM到AM", func(t *testing.T) {
		status, _, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
			"2026-11-06", 1, "2026-11-06", 0, "非法方向")
		if status != http.StatusBadRequest || msg == "" {
			t.Fatalf("同日 PM→AM 应 400，实际 HTTP %d msg %q", status, msg)
		}
	})
	t.Run("跨自然年", func(t *testing.T) {
		status, _, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
			"2026-12-31", 0, "2027-01-04", 0, "跨年")
		if status != http.StatusBadRequest || msg == "" {
			t.Fatalf("跨年应 400，实际 HTTP %d msg %q", status, msg)
		}
	})
	t.Run("零工作日", func(t *testing.T) {
		status, draft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
			"2026-11-07", 0, "2026-11-08", 1, "周末")
		if status != http.StatusOK || msg != "" {
			t.Fatalf("草稿允许零工作日：HTTP %d msg %q", status, msg)
		}
		id := int64(draft["id"].(float64))
		status, _, msg = submitLeave(t, env, le.employee, id)
		if status != http.StatusBadRequest || msg == "" {
			t.Fatalf("零工作日提交应 400，实际 HTTP %d msg %q", status, msg)
		}
	})
	t.Run("槽位重叠", func(t *testing.T) {
		status, first, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
			"2026-11-18", 0, "2026-11-18", 0, "上午")
		if status != http.StatusOK || msg != "" {
			t.Fatalf("创建首个申请：HTTP %d msg %q", status, msg)
		}
		if status, _, msg := submitLeave(t, env, le.employee, int64(first["id"].(float64))); status != http.StatusOK {
			t.Fatalf("提交首个申请：HTTP %d msg %q", status, msg)
		}
		status, second, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
			"2026-11-18", 0, "2026-11-18", 0, "同日同半天")
		if status != http.StatusOK || msg != "" {
			t.Fatalf("创建重叠申请：HTTP %d msg %q", status, msg)
		}
		status, _, msg = submitLeave(t, env, le.employee, int64(second["id"].(float64)))
		if status != http.StatusConflict || msg == "" {
			t.Fatalf("槽位重叠应 409，实际 HTTP %d msg %q", status, msg)
		}
		// 相邻半天不算重叠：同日下午可提交。
		status, adjacent, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
			"2026-11-18", 1, "2026-11-18", 1, "同日下午")
		if status != http.StatusOK || msg != "" {
			t.Fatalf("创建相邻半天申请：HTTP %d msg %q", status, msg)
		}
		if status, _, msg := submitLeave(t, env, le.employee, int64(adjacent["id"].(float64))); status != http.StatusOK {
			t.Fatalf("相邻半天应可提交：HTTP %d msg %q", status, msg)
		}
	})
	t.Run("余额不足", func(t *testing.T) {
		status, draft, msg := createLeaveDraft(t, env, le.employee, le.compTypeID,
			"2026-11-19", 0, "2026-11-19", 1, "调休一天")
		if status != http.StatusOK || msg != "" {
			t.Fatalf("创建调休申请：HTTP %d msg %q", status, msg)
		}
		status, _, msg = submitLeave(t, env, le.employee, int64(draft["id"].(float64)))
		if status != http.StatusBadRequest || msg == "" {
			t.Fatalf("余额不足应 400，实际 HTTP %d msg %q", status, msg)
		}
	})
}

// TestIntegrationLeaveRequestPermissionAndTenant 权限码逐码、非本人 404 与
// 租户隔离（spec §4.6：跨租户业务 ID 一律 404，不泄露存在性）。
func TestIntegrationLeaveRequestPermissionAndTenant(t *testing.T) {
	le := newLeaveRequestEnv(t)
	env := le.env

	// 一条已完成申请（用于非本人/跨租户读取断言）。
	_, draft, msg := createLeaveDraft(t, env, le.employee, le.annualTypeID,
		"2026-11-12", 0, "2026-11-12", 0, "权限用例")
	if msg != "" {
		t.Fatalf("创建权限用例申请：msg %q", msg)
	}
	requestID := int64(draft["id"].(float64))
	if status, _, msg := submitLeave(t, env, le.employee, requestID); status != http.StatusOK {
		t.Fatalf("提交权限用例申请：HTTP %d msg %q", status, msg)
	}

	// 无码用户：查询登录即可，写动作全 403。
	noPermRole := createRoleWithMenus(t, env, le.admin, "leave_no_perm", []int64{1})
	noPerm := oaLoginNewUser(t, env, le.admin, "leave_no_perm_u", noPermRole)
	if status, _ := env.Request(t, http.MethodGet, "/oa/leave/request/page", noPerm, nil); status != http.StatusOK {
		t.Fatalf("无码用户本人列表应 200，实际 HTTP %d", status)
	}
	status, _ := env.Request(t, http.MethodPost, "/oa/leave/request", noPerm, map[string]any{
		"leaveTypeId": le.annualTypeID, "startDate": "2026-11-13", "endDate": "2026-11-13", "reason": "x",
	})
	if status != http.StatusForbidden {
		t.Fatalf("无码创建应 403，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodPost, fmt.Sprintf("/oa/leave/request/%d/submit", requestID), noPerm, nil); status != http.StatusForbidden {
		t.Fatalf("无码提交应 403，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/oa/leave/request/%d", requestID), noPerm, nil); status != http.StatusForbidden {
		t.Fatalf("无码删除应 403，实际 HTTP %d", status)
	}

	// 可见范围（issue #53，spec §4.6）：持 oa:leave:manage 的 HR 可读全租户，
	// 所以本人以外「可见」不再一律 404；范围外（普通用户读他人）才是 404。
	if status, _ := getLeaveDetail(t, env, le.manager, requestID); status != http.StatusOK {
		t.Fatalf("持 oa:leave:manage 的 HR 读详情应 200，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/leave/request/%d/files", requestID), le.manager, nil); status != http.StatusOK {
		t.Fatalf("持 oa:leave:manage 的 HR 读附件应 200，实际 HTTP %d", status)
	}
	if status, _ := getLeaveDetail(t, env, noPerm, requestID); status != http.StatusNotFound {
		t.Fatalf("范围外普通用户读详情应 404，实际 HTTP %d", status)
	}
	if status, _ := env.Request(t, http.MethodGet, fmt.Sprintf("/oa/leave/request/%d/files", requestID), noPerm, nil); status != http.StatusNotFound {
		t.Fatalf("范围外普通用户读附件应 404，实际 HTTP %d", status)
	}

	// 租户隔离：租户 2 列表为空，跨租户详情 404。
	tenant2 := createOATenant(t, env, le.admin, "leave_iso_t2")
	token2 := loginOATenant(t, env, "admin", "123456", tenant2)
	status, api := env.Request(t, http.MethodGet, "/oa/leave/request/page?pageNo=1&pageSize=10", token2, nil)
	mustOK(t, "租户 2 请假分页", status, api)
	var page struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total != 0 {
		t.Fatalf("租户 2 请假分页应为空: %v（data=%s）", err, string(api.Data))
	}
	if status, _ := getLeaveDetail(t, env, token2, requestID); status != http.StatusNotFound {
		t.Fatalf("跨租户读详情应 404，实际 HTTP %d", status)
	}
}
