//go:build integration

// bpm 运行与通用待办的 HTTP 集成用例（issue #46，spec #42 §4.2/§4.3/§4.4/§4.13）。
//
// seam 布局：
//   - 发起 = 程序化 seam（spec §5 不设 POST /bpm/instance）：业务模块在自己的
//     提交事务里经 env.App.BPM.StartInstance / StartInstanceInTx 发起，测试以
//     真实装配的 Service（租户 + 登录主体上下文）驱动，生产同构；
//   - 办理/查询 = HTTP seam（真实 router + Auth + 租户插件 + 权限中间件）；
//   - 通知/已读联动 = notify HTTP seam（/notify/message/*）。
//
// 并发用例确定性：候选池先办者胜出依赖实例行锁串行化（锁序实例→任务），
// 断言「恰好一个成功 + 其余 409 + 恰好一条通过记录」，不依赖调度顺序。
//
// 测试库走 GAP_TEST_DATABASE_DSN（协调约定用 go_admin_pro_test_t46 后缀）：
//
//	GAP_TEST_DATABASE_DSN='root:123456@tcp(127.0.0.1:3306)/go_admin_pro_test_t46?charset=utf8mb4&parseTime=True' \
//	  go test -tags=integration -p 1 ./internal/module/bpm/
package bpm_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// runtimeBusinessType 是运行时用例的业务类型；fake handler 记录回调并把
// 「业务状态」写进定义行的 remark 列——真实业务回写由 #47/#52/#55 落自己的表，
// 这里复用本包可见的表证明「handler 的 DB 写与审批状态同事务回滚」。
const runtimeBusinessType = "test_runtime_business"

// runtimeRoleMenus 是测试角色绑定的菜单：通用待办页 512 + 办理 5121 +
// 实例查询 5122 + 通知查询 5123（notify:message:query，普通用户通知可用）。
var runtimeRoleMenus = []int64{512, 5121, 5122, 5123}

// runtimeFakeHandler 是 BusinessHandler 测试替身：记录调用轨迹、可注入失败、
// 以定义行 remark 模拟业务状态落库（事务内写，随整笔回滚）。
type runtimeFakeHandler struct {
	mu            sync.Mutex
	markerDefID   int64
	failApprove   bool
	failReject    bool
	failWithdraw  bool
	calls         []string
	afterApproved []int64
}

func (h *runtimeFakeHandler) OnApproved(_ context.Context, inst *model.ProcessInstance, tx *gorm.DB) error {
	h.mu.Lock()
	h.calls = append(h.calls, fmt.Sprintf("approved:%d", inst.ID))
	fail := h.failApprove
	h.mu.Unlock()
	if err := tx.Model(&model.ProcessDefinition{}).Where("id = ?", h.markerDefID).Update("remark", "biz-approved").Error; err != nil {
		return err
	}
	if fail {
		return errors.New("handler 注入失败：approve")
	}
	return nil
}

func (h *runtimeFakeHandler) OnRejected(_ context.Context, inst *model.ProcessInstance, tx *gorm.DB, reason string) error {
	h.mu.Lock()
	h.calls = append(h.calls, fmt.Sprintf("rejected:%d:%s", inst.ID, reason))
	fail := h.failReject
	h.mu.Unlock()
	if err := tx.Model(&model.ProcessDefinition{}).Where("id = ?", h.markerDefID).Update("remark", "biz-rejected").Error; err != nil {
		return err
	}
	if fail {
		return errors.New("handler 注入失败：reject")
	}
	return nil
}

func (h *runtimeFakeHandler) OnWithdrawn(_ context.Context, inst *model.ProcessInstance, tx *gorm.DB) error {
	h.mu.Lock()
	h.calls = append(h.calls, fmt.Sprintf("withdrawn:%d", inst.ID))
	fail := h.failWithdraw
	h.mu.Unlock()
	if err := tx.Model(&model.ProcessDefinition{}).Where("id = ?", h.markerDefID).Update("remark", "biz-withdrawn").Error; err != nil {
		return err
	}
	if fail {
		return errors.New("handler 注入失败：withdraw")
	}
	return nil
}

func (h *runtimeFakeHandler) callCount(prefix string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if len(c) >= len(prefix) && c[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

// AfterApproved 实现 ApprovalCommitHook：只应在实例最终通过后调用一次
// （PR #66 审查 P2-1）。记录实例 ID 供多节点用例断言。
func (h *runtimeFakeHandler) AfterApproved(_ context.Context, inst *model.ProcessInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterApproved = append(h.afterApproved, inst.ID)
}

// afterApprovedCount 返回提交后钩子的调用次数。
func (h *runtimeFakeHandler) afterApprovedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.afterApproved)
}

// runtimeEnv 聚合一个用例的角色/用户/handler 基础设施。
type runtimeEnv struct {
	env      *testutil.IntegrationEnv
	handler  *runtimeFakeHandler
	roleID   int64
	adminTok string
}

// newRuntimeEnv 建角色（512 段菜单）与 fake handler 注册。
func newRuntimeEnv(t *testing.T) *runtimeEnv {
	t.Helper()
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/system/role", admin, map[string]any{
		"name": "bpm 运行测试角色", "code": "bpm_runtime_role", "dataScope": 5, "status": 0,
		"menuIds": runtimeRoleMenus,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建测试角色：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/system/role/page?code=bpm_runtime_role&pageNo=1&pageSize=10", admin, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("查询测试角色: %v（data=%s）", err, string(api.Data))
	}
	re := &runtimeEnv{env: env, handler: &runtimeFakeHandler{}, adminTok: admin}
	re.roleID = page.List[0].ID
	// 注册 fake handler（spec §4.4：未注册 business_type 不允许发布/发起）。
	env.App.BPM.RegisterBusinessHandler(runtimeBusinessType, re.handler)
	return re
}

// createRole 建角色并回读 ID（menuIds 可空——发布期仅校验角色存在，spec §4.2）。
func (re *runtimeEnv) createRole(t *testing.T, code, name string) int64 {
	t.Helper()
	status, api := re.env.Request(t, http.MethodPost, "/system/role", re.adminTok, map[string]any{
		"name": name, "code": code, "dataScope": 5, "status": 0, "menuIds": []int64{},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建角色 %s：HTTP %d code %d msg %q", code, status, api.Code, api.Msg)
	}
	status, api = re.env.Request(t, http.MethodGet, "/system/role/page?code="+code+"&pageNo=1&pageSize=10", re.adminTok, nil)
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

// createUser 建启用用户并授予测试角色，返回 (ID, token)。
func (re *runtimeEnv) createUser(t *testing.T, username string, roleIDs []int64) (int64, string) {
	t.Helper()
	status, api := re.env.Request(t, http.MethodPost, "/system/user", re.adminTok, map[string]any{
		"username": username, "nickname": username + "-昵称", "password": "123456",
		"deptId": 101, "roleIds": roleIDs,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
	}
	status, api = re.env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", re.adminTok, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("查询用户 %s: %v（data=%s）", username, err, string(api.Data))
	}
	return page.List[0].ID, re.env.Login(t, username, "123456")
}

// createDef 建「单节点 role 规则」定义并发布，返回定义 ID。
func (re *runtimeEnv) createDef(t *testing.T, key string, nodes []map[string]any) int64 {
	t.Helper()
	return re.createDefWithBiz(t, key, runtimeBusinessType, nodes)
}

func (re *runtimeEnv) createDefWithBiz(t *testing.T, key, businessType string, nodes []map[string]any) int64 {
	t.Helper()
	status, api := re.env.Request(t, http.MethodPost, "/bpm/definition", re.adminTok, map[string]any{
		"key": key, "name": "运行-" + key, "businessType": businessType, "nodes": nodes,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建定义 %s：HTTP %d code %d msg %q", key, status, api.Code, api.Msg)
	}
	var data struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析定义响应: %v", err)
	}
	status, api = re.env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/definition/%d/publish", data.ID), re.adminTok, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("发布定义 %s：HTTP %d code %d msg %q", key, status, api.Code, api.Msg)
	}
	return data.ID
}

// start 由发起人经程序化 seam 发起实例（生产里业务模块在自己的提交事务里这样调用）。
func (re *runtimeEnv) start(t *testing.T, initiatorID, defID, businessID int64, title, summary string) *model.ProcessInstance {
	t.Helper()
	ctx := re.userCtx(initiatorID)
	inst, err := re.env.App.BPM.StartInstance(ctx, bpmservice.StartInstanceInput{
		DefinitionID: defID, BusinessID: businessID, Title: title, Summary: summary,
	})
	if err != nil {
		t.Fatalf("发起实例: %v", err)
	}
	return inst
}

// startWantStatus 发起并断言失败状态码（400 无审批人/停用定义等）。
func (re *runtimeEnv) startWantStatus(t *testing.T, initiatorID, defID, businessID int64, title string, wantHTTP int) {
	t.Helper()
	ctx := re.userCtx(initiatorID)
	_, err := re.env.App.BPM.StartInstance(ctx, bpmservice.StartInstanceInput{
		DefinitionID: defID, BusinessID: businessID, Title: title,
	})
	if err == nil {
		t.Fatalf("期望发起失败（HTTP %d），实际成功", wantHTTP)
	}
	xe, ok := xerr.As(err)
	if !ok || xe.Status != wantHTTP {
		t.Fatalf("期望发起失败 HTTP %d，实际 %v", wantHTTP, err)
	}
}

func (re *runtimeEnv) userCtx(userID int64) context.Context {
	return tenant.WithTenantID(
		authn.With(context.Background(), authn.Principal{UserID: userID, TenantID: tenant.DefaultID}),
		tenant.DefaultID,
	)
}

// ---------- HTTP 速记 ----------

func (re *runtimeEnv) listTasks(t *testing.T, token, tab string) []map[string]any {
	t.Helper()
	status, api := re.env.Request(t, http.MethodGet, "/bpm/task/page?tab="+tab+"&pageNo=1&pageSize=50", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询我的任务 tab=%s：HTTP %d code %d msg %q", tab, status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析任务分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List
}

func (re *runtimeEnv) taskIDOf(t *testing.T, rows []map[string]any, instanceID int64) int64 {
	t.Helper()
	for _, row := range rows {
		if int64(row["instanceId"].(float64)) == instanceID {
			return int64(row["id"].(float64))
		}
	}
	t.Fatalf("任务列表中没有实例 %d 的任务（共 %d 行）", instanceID, len(rows))
	return 0
}

func (re *runtimeEnv) approve(t *testing.T, token string, taskID int64, comment string) (int, testutil.APIResponse) {
	t.Helper()
	return re.env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/task/%d/approve", taskID), token, map[string]any{"comment": comment})
}

func (re *runtimeEnv) reject(t *testing.T, token string, taskID int64, comment string) (int, testutil.APIResponse) {
	t.Helper()
	return re.env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/task/%d/reject", taskID), token, map[string]any{"comment": comment})
}

func (re *runtimeEnv) transfer(t *testing.T, token string, taskID, toUserID int64, comment string) (int, testutil.APIResponse) {
	t.Helper()
	return re.env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/task/%d/transfer", taskID), token, map[string]any{"toUserId": toUserID, "comment": comment})
}

func (re *runtimeEnv) withdraw(t *testing.T, token string, instanceID int64, comment string) (int, testutil.APIResponse) {
	t.Helper()
	return re.env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/instance/%d/withdraw", instanceID), token, map[string]any{"comment": comment})
}

func (re *runtimeEnv) instanceDetail(t *testing.T, token string, instanceID int64) (int, map[string]any) {
	t.Helper()
	status, api := re.env.Request(t, http.MethodGet, fmt.Sprintf("/bpm/instance/%d", instanceID), token, nil)
	var data map[string]any
	if status == http.StatusOK && api.Code == 0 {
		if err := json.Unmarshal(api.Data, &data); err != nil {
			t.Fatalf("解析实例详情: %v", err)
		}
	}
	return status, data
}

func (re *runtimeEnv) instanceStatus(t *testing.T, token string, instanceID int64) int8 {
	t.Helper()
	_, data := re.instanceDetail(t, token, instanceID)
	inst, _ := data["instance"].(map[string]any)
	if inst == nil {
		t.Fatalf("实例详情缺 instance 字段: %v", data)
	}
	return int8(inst["status"].(float64))
}

// notifications 拉取本人指定类型的通知（readAt 是否为空随行返回）。
func (re *runtimeEnv) notifications(t *testing.T, token, typ string) []map[string]any {
	t.Helper()
	status, api := re.env.Request(t, http.MethodGet, "/notify/message/page?type="+typ+"&pageNo=1&pageSize=50", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询通知 %s：HTTP %d code %d msg %q", typ, status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析通知分页: %v", err)
	}
	return page.List
}

func (re *runtimeEnv) unreadCount(t *testing.T, rows []map[string]any) int {
	t.Helper()
	n := 0
	for _, row := range rows {
		if row["readAt"] == nil {
			n++
		}
	}
	return n
}

// startMarker 把 fake handler 的「业务写」锚定到定义行（remark 列）。
func (re *runtimeEnv) startMarker(t *testing.T, defID int64) {
	t.Helper()
	re.handler.markerDefID = defID
}

func (re *runtimeEnv) definitionRemark(t *testing.T, defID int64) string {
	t.Helper()
	status, api := re.env.Request(t, http.MethodGet, fmt.Sprintf("/bpm/definition/%d", defID), re.adminTok, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("读取定义 %d：HTTP %d code %d", defID, status, api.Code)
	}
	var data struct {
		Remark string `json:"remark"`
	}
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析定义详情: %v", err)
	}
	return data.Remark
}

// ---------- 用例 ----------

// TestIntegrationRuntimeStartCandidatePoolAndNotify 覆盖发起链路（spec §4.2/§4.13）：
// 已发布定义可发起、停用不可发起、候选池物化（角色规则每人一条）、无审批人 400、
// 权限码规则候选池（super_admin 视为持有人）、bpm_todo 通知扇出。
func TestIntegrationRuntimeStartCandidatePoolAndNotify(t *testing.T) {
	re := newRuntimeEnv(t)
	roleID := re.roleID
	_, aTok := re.createUser(t, "rt_user_a", []int64{roleID})
	_, bTok := re.createUser(t, "rt_user_b", []int64{roleID})
	rID, _ := re.createUser(t, "rt_requester", []int64{roleID})

	// 角色规则候选池：A、B 各一条待办 + 各一条 bpm_todo 通知。
	defID := re.createDef(t, "rt_pool_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(roleID, 10), 0),
	})
	inst := re.start(t, rID, defID, 9001, "报销单 #9001", "差旅 300 元")
	if inst.Status != model.InstanceStatusRunning || inst.DefinitionID != defID || inst.InitiatorID != rID {
		t.Fatalf("实例快照不符: %+v", inst)
	}
	if inst.Title != "报销单 #9001" || inst.Summary != "差旅 300 元" || inst.BusinessID != 9001 {
		t.Fatalf("标题/摘要/业务 ID 快照不符: %+v", inst)
	}
	aRows := re.listTasks(t, aTok, "todo")
	bRows := re.listTasks(t, bTok, "todo")
	if len(aRows) != 1 || len(bRows) != 1 {
		t.Fatalf("候选池应各有一条待办：A=%d B=%d", len(aRows), len(bRows))
	}
	aTodo := re.notifications(t, aTok, "bpm_todo")
	bTodo := re.notifications(t, bTok, "bpm_todo")
	if re.unreadCount(t, aTodo) != 1 || re.unreadCount(t, bTodo) != 1 {
		t.Fatalf("A/B 应各有 1 条未读 bpm_todo：A=%d B=%d", re.unreadCount(t, aTodo), re.unreadCount(t, bTodo))
	}
	if aTodo[0]["bizType"] != "bpm_task" || aTodo[0]["route"] != "/bpm/task" {
		t.Fatalf("bpm_todo 通知定位字段不符: %v", aTodo[0])
	}
	if int64(aTodo[0]["bizId"].(float64)) != int64(aRows[0]["id"].(float64)) {
		t.Fatalf("通知 bizId 应为任务 ID")
	}

	// 权限码规则候选池：持码用户 + super_admin（视为持全部）。B 授合同审批码。
	status, api := re.env.Request(t, http.MethodPut, "/system/role", re.adminTok, map[string]any{
		"id": roleID, "name": "bpm 运行测试角色", "code": "bpm_runtime_role", "dataScope": 5, "status": 0,
		"menuIds": append(runtimeRoleMenus, 4105),
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("给角色补合同审批码：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	permDefID := re.createDef(t, "rt_perm_flow", []map[string]any{
		node("合同审批", model.AssigneePermission, "crm:contract:approve", 0),
	})
	re.start(t, rID, permDefID, 9002, "合同 #9002", "")
	// 持有者 = B（绑码角色）+ admin（super_admin）→ 各一条待办。
	bPermRows := re.listTasks(t, bTok, "todo")
	adminTodo := re.listTasks(t, re.adminTok, "todo")
	if len(bPermRows) != 2 || len(adminTodo) != 1 { // B：池实例 1 + 前例 1；admin：池实例 1
		t.Fatalf("权限码候选池应含持码用户与超管：B=%d admin=%d", len(bPermRows), len(adminTodo))
	}

	// 停用定义不可发起（spec §4.1）。
	status, api = re.env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/definition/%d/disable", defID), re.adminTok, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("停用定义：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	re.startWantStatus(t, rID, defID, 9003, "不应发起", http.StatusBadRequest)

	// 无审批人：角色存在但无启用持有人 → 400（不跳过节点、不自动通过）。
	// 发布期只校验角色存在（spec §4.2），持有人为空在发起时拒绝。
	emptyRoleID := re.createRole(t, "bpm_empty_role", "空角色")
	emptyDefID := re.createDef(t, "rt_empty_flow", []map[string]any{
		node("空节点", model.AssigneeRole, strconv.FormatInt(emptyRoleID, 10), 0),
	})
	re.startWantStatus(t, rID, emptyDefID, 9004, "不应发起", http.StatusBadRequest)

	// 停用用户不进候选池：唯一持有人被停用 → 解析为空 → 400。
	disabledID, _ := re.createUser(t, "rt_user_off", []int64{emptyRoleID})
	status, api = re.env.Request(t, http.MethodPut, "/system/user", re.adminTok, map[string]any{
		"id": disabledID, "nickname": "rt_user_off-昵称", "deptId": 101, "status": 1, "roleIds": []int64{},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("停用用户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	offDefID := re.createDef(t, "rt_off_flow", []map[string]any{
		node("停用节点", model.AssigneeRole, strconv.FormatInt(emptyRoleID, 10), 0),
	})
	re.startWantStatus(t, rID, offDefID, 9005, "不应发起", http.StatusBadRequest)
}

// TestIntegrationRuntimeApproveAdvanceFinish 覆盖推进与终态（spec §4.3/§4.4）：
// 通过推进下一节点、最后一节点通过回调 OnApproved、先办者胜出、409、403、
// 时间线完整、通知结果与已读联动。
func TestIntegrationRuntimeApproveAdvanceFinish(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID
	pool := re.createRole(t, "rt_adv_pool", "审批组A/B")
	_, aTok := re.createUser(t, "rt_adv_a", []int64{menus, pool})
	_, bTok := re.createUser(t, "rt_adv_b", []int64{menus, pool})
	cID, cTok := re.createUser(t, "rt_adv_c", []int64{menus})
	rID, rTok := re.createUser(t, "rt_adv_r", []int64{menus})

	defID := re.createDef(t, "rt_adv_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(pool, 10), 0),
		node("总监审批", model.AssigneeUser, strconv.FormatInt(cID, 10), 0),
	})
	re.startMarker(t, defID)
	inst := re.start(t, rID, defID, 7001, "请假 #7001", "11 月年假")

	// 非办理人不可通过（403）。
	aTask := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst.ID)
	if status, api := re.approve(t, rTok, aTask, ""); status != http.StatusForbidden {
		t.Fatalf("非办理人通过应 403，实际 HTTP %d msg %q", status, api.Msg)
	}

	// B 的兄弟任务 ID（取消后办理自己的任务应 409）。
	bTask := re.taskIDOf(t, re.listTasks(t, bTok, "todo"), inst.ID)

	// A 通过 → 推进到节点 1（C 出待办），兄弟 B 取消（先办者胜出）。
	if status, api := re.approve(t, aTok, aTask, "同意"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("A 通过：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusRunning {
		t.Fatalf("还有下一节点，实例应保持运行中，实际 %d", got)
	}
	// 中间节点通过只推进任务：提交后钩子不得触发（PR #66 审查 P2-1）。
	if n := re.handler.afterApprovedCount(); n != 0 {
		t.Fatalf("中间节点通过不应调用 AfterApproved，实际 %d 次", n)
	}
	if rows := re.listTasks(t, bTok, "todo"); len(rows) != 0 {
		t.Fatalf("兄弟任务应被取消，B 仍有 %d 条待办", len(rows))
	}
	if done := re.listTasks(t, bTok, "done"); len(done) != 0 {
		t.Fatalf("取消不是 B 办理的，不应出现在已办，实际 %d", len(done))
	}
	cTask := re.taskIDOf(t, re.listTasks(t, cTok, "todo"), inst.ID)

	// 对已取消任务再办理 → 409（B 办理自己被取消的兄弟任务）。
	if status, api := re.approve(t, bTok, bTask, ""); status != http.StatusConflict {
		t.Fatalf("对已取消任务办理应 409，实际 HTTP %d msg %q", status, api.Msg)
	}

	// C 通过（最后一节点）→ 实例通过 + OnApproved 同事务回调 + 结果通知。
	if status, api := re.approve(t, cTok, cTask, "通过"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("C 通过：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusApproved {
		t.Fatalf("实例应为通过，实际 %d", got)
	}
	if n := re.handler.callCount("approved:"); n != 1 {
		t.Fatalf("OnApproved 应回调恰 1 次，实际 %d", n)
	}
	// 最终通过：提交后钩子恰触发一次（PR #66 审查 P2-1）。
	if n := re.handler.afterApprovedCount(); n != 1 {
		t.Fatalf("最终通过应调用 AfterApproved 恰 1 次，实际 %d", n)
	}
	if re.definitionRemark(t, defID) != "biz-approved" {
		t.Fatalf("handler 的业务写应随事务提交")
	}

	// 已办列表：A/C 各一条（通过），B 空。
	if rows := re.listTasks(t, aTok, "done"); len(rows) != 1 {
		t.Fatalf("A 已办应 1 条，实际 %d", len(rows))
	}
	if rows := re.listTasks(t, cTok, "done"); len(rows) != 1 {
		t.Fatalf("C 已办应 1 条，实际 %d", len(rows))
	}

	// 时间线：两条通过记录升序、操作人昵称解析、意见落库。
	_, detail := re.instanceDetail(t, rTok, inst.ID)
	records, _ := detail["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("时间线应 2 条记录，实际 %d", len(records))
	}
	first, _ := records[0].(map[string]any)
	if first["action"].(float64) != float64(model.RecordActionApprove) || first["nodeName"] != "组长审批" || first["comment"] != "同意" {
		t.Fatalf("第一条记录不符: %v", first)
	}
	if first["operatorName"] == "" || first["operatorName"] == nil {
		t.Fatalf("操作人昵称应解析: %v", first)
	}
	tasks, _ := detail["tasks"].([]any)
	if len(tasks) != 3 { // A、B、C 三条任务（B 取消）
		t.Fatalf("节点任务视图应 3 条，实际 %d", len(tasks))
	}

	// 通知联动：A/B/C 的 bpm_todo 已读；发起人 1 条未读 bpm_result。
	for _, tok := range []string{aTok, bTok, cTok} {
		if n := re.unreadCount(t, re.notifications(t, tok, "bpm_todo")); n != 0 {
			t.Fatalf("办理相关人的 bpm_todo 应全部已读，仍有 %d 条未读", n)
		}
	}
	result := re.notifications(t, rTok, "bpm_result")
	if re.unreadCount(t, result) != 1 {
		t.Fatalf("发起人应收到 1 条未读 bpm_result，实际 %d", re.unreadCount(t, result))
	}
	if result[0]["bizType"] != "bpm_instance" || int64(result[0]["bizId"].(float64)) != inst.ID {
		t.Fatalf("bpm_result 定位字段不符: %v", result[0])
	}

	// 实例结束后再办理 → 409。
	if status, api := re.approve(t, cTok, cTask, ""); status != http.StatusConflict {
		t.Fatalf("实例结束后办理应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
}

// TestIntegrationRuntimeReject 覆盖驳回（spec §4.3）：原因必填、实例驳回、
// 同实例全部待办取消、OnRejected 回调带原因、结果通知。
func TestIntegrationRuntimeReject(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID
	pool := re.createRole(t, "rt_rej_pool", "审批组A/B")
	_, aTok := re.createUser(t, "rt_rej_a", []int64{menus, pool})
	_, bTok := re.createUser(t, "rt_rej_b", []int64{menus, pool})
	rID, rTok := re.createUser(t, "rt_rej_r", []int64{menus})

	defID := re.createDef(t, "rt_rej_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(pool, 10), 0),
	})
	re.startMarker(t, defID)
	inst := re.start(t, rID, defID, 7002, "请假 #7002", "")
	aTask := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst.ID)

	// 驳回原因必填（1–500 字）。
	if status, api := re.reject(t, aTok, aTask, "  "); status != http.StatusBadRequest {
		t.Fatalf("空原因应 400，实际 HTTP %d msg %q", status, api.Msg)
	}
	long := strings.Repeat("驳", 501)
	if status, api := re.reject(t, aTok, aTask, long); status != http.StatusBadRequest {
		t.Fatalf("超长原因应 400，实际 HTTP %d msg %q", status, api.Msg)
	}

	if status, api := re.reject(t, aTok, aTask, "金额不符合制度"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("驳回：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusRejected {
		t.Fatalf("实例应为驳回，实际 %d", got)
	}
	if re.handler.callCount("rejected:") != 1 {
		t.Fatalf("OnRejected 应回调 1 次")
	}
	// 兄弟任务随实例结束取消。
	if rows := re.listTasks(t, bTok, "todo"); len(rows) != 0 {
		t.Fatalf("驳回后兄弟待办应取消，B 仍有 %d 条", len(rows))
	}
	// 对已驳回实例的任务再办理 → 409。
	if status, api := re.approve(t, bTok, aTask, ""); status != http.StatusConflict {
		t.Fatalf("驳回后办理应 409，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 时间线：驳回记录带原因；结果通知已发。
	_, detail := re.instanceDetail(t, rTok, inst.ID)
	records, _ := detail["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("时间线应 1 条驳回记录，实际 %d", len(records))
	}
	rec, _ := records[0].(map[string]any)
	if rec["action"].(float64) != float64(model.RecordActionReject) || rec["comment"] != "金额不符合制度" {
		t.Fatalf("驳回记录不符: %v", rec)
	}
	result := re.notifications(t, rTok, "bpm_result")
	if re.unreadCount(t, result) != 1 {
		t.Fatalf("发起人应收到驳回结果通知，实际未读 %d", re.unreadCount(t, result))
	}
}

// TestIntegrationRuntimeWithdrawTiming 覆盖撤回时机（spec §4.3）：
// 仅发起人、仅运行中、仅首个（通过/驳回）审批动作前；转办记录不阻断撤回。
func TestIntegrationRuntimeWithdrawTiming(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID
	pool := re.createRole(t, "rt_wd_pool", "审批组A/B")
	_, aTok := re.createUser(t, "rt_wd_a", []int64{menus, pool})
	bID, _ := re.createUser(t, "rt_wd_b", []int64{menus, pool})
	cID, _ := re.createUser(t, "rt_wd_c", []int64{menus})
	rID, rTok := re.createUser(t, "rt_wd_r", []int64{menus})

	defID := re.createDef(t, "rt_wd_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(pool, 10), 0),
		node("总监审批", model.AssigneeUser, strconv.FormatInt(cID, 10), 0),
	})
	re.startMarker(t, defID)

	// 场景 1：非发起人撤回 → 403。
	inst1 := re.start(t, rID, defID, 7101, "请假 #7101", "")
	if status, api := re.withdraw(t, aTok, inst1.ID, ""); status != http.StatusForbidden {
		t.Fatalf("非发起人撤回应 403，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 场景 2：首个审批前撤回成功 → 实例撤回、待办取消、回调、结果通知。
	if status, api := re.withdraw(t, rTok, inst1.ID, "信息填错了"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("撤回：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst1.ID); got != model.InstanceStatusWithdrawn {
		t.Fatalf("实例应为撤回，实际 %d", got)
	}
	if re.handler.callCount("withdrawn:") != 1 {
		t.Fatalf("OnWithdrawn 应回调 1 次")
	}
	if rows := re.listTasks(t, aTok, "todo"); len(rows) != 0 {
		t.Fatalf("撤回后待办应取消，A 仍有 %d 条", len(rows))
	}
	if n := re.unreadCount(t, re.notifications(t, aTok, "bpm_todo")); n != 0 {
		t.Fatalf("撤回后 A 的 bpm_todo 应已读，仍有 %d 条未读", n)
	}
	if n := re.unreadCount(t, re.notifications(t, rTok, "bpm_result")); n != 1 {
		t.Fatalf("发起人应收到撤回结果通知，实际 %d", n)
	}

	// 场景 3：首个通过动作之后撤回 → 409。
	inst2 := re.start(t, rID, defID, 7102, "请假 #7102", "")
	task2 := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst2.ID)
	if status, api := re.approve(t, aTok, task2, "ok"); status != http.StatusOK {
		t.Fatalf("A 通过：HTTP %d msg %q", status, api.Msg)
	}
	if status, api := re.withdraw(t, rTok, inst2.ID, ""); status != http.StatusConflict {
		t.Fatalf("首个审批后撤回应 409，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 场景 4：转办记录不阻断撤回（只有通过/驳回记录算审批动作）。
	inst3 := re.start(t, rID, defID, 7103, "请假 #7103", "")
	task3 := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst3.ID)
	if status, api := re.transfer(t, aTok, task3, bID, ""); status != http.StatusOK {
		t.Fatalf("转办：HTTP %d msg %q", status, api.Msg)
	}
	if status, api := re.withdraw(t, rTok, inst3.ID, ""); status != http.StatusOK {
		t.Fatalf("仅转办记录后撤回应成功，实际 HTTP %d msg %q", status, api.Msg)
	}
}

// TestIntegrationRuntimeTransfer 覆盖转办（spec §4.3）：同节点新任务、原任务
// 关闭、目标须在职、转办记录、实例继续运行。
func TestIntegrationRuntimeTransfer(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID
	pool := re.createRole(t, "rt_tr_pool", "审批组A/B")
	_, aTok := re.createUser(t, "rt_tr_a", []int64{menus, pool})
	bID, bTok := re.createUser(t, "rt_tr_b", []int64{menus, pool})
	rID, rTok := re.createUser(t, "rt_tr_r", []int64{menus})

	defID := re.createDef(t, "rt_tr_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(pool, 10), 0),
	})
	re.startMarker(t, defID)
	inst := re.start(t, rID, defID, 7201, "报销 #7201", "")
	aTask := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst.ID)

	// 非办理人转办 → 403。
	if status, api := re.transfer(t, rTok, aTask, bID, ""); status != http.StatusForbidden {
		t.Fatalf("非办理人转办应 403，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 目标停用用户 → 400。
	disabledID, _ := re.createUser(t, "rt_tr_off", []int64{})
	status, api := re.env.Request(t, http.MethodPut, "/system/user", re.adminTok, map[string]any{
		"id": disabledID, "nickname": "rt_tr_off-昵称", "deptId": 101, "status": 1, "roleIds": []int64{},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("停用用户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api := re.transfer(t, aTok, aTask, disabledID, ""); status != http.StatusBadRequest {
		t.Fatalf("转办给停用用户应 400，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 正常转办：原任务关闭（已办可见转办态），B 出同节点新任务，实例运行中。
	if status, api := re.transfer(t, aTok, aTask, bID, "出差不便"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("转办：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusRunning {
		t.Fatalf("转办后实例应继续运行，实际 %d", got)
	}
	doneRows := re.listTasks(t, aTok, "done")
	if len(doneRows) != 1 || doneRows[0]["status"].(float64) != float64(model.TaskStatusTransferred) {
		t.Fatalf("A 已办应有转办任务，实际 %v", doneRows)
	}
	bTask := re.taskIDOf(t, re.listTasks(t, bTok, "todo"), inst.ID)
	if bTask == aTask {
		t.Fatal("转办应生成新任务")
	}
	if status, api := re.approve(t, bTok, bTask, "接手"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("B 通过转办任务：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusApproved {
		t.Fatalf("单节点通过后实例应为通过，实际 %d", got)
	}
	// 时间线含转办记录（含转办目标）。
	_, detail := re.instanceDetail(t, rTok, inst.ID)
	records, _ := detail["records"].([]any)
	var hasTransfer bool
	for _, raw := range records {
		rec, _ := raw.(map[string]any)
		if rec["action"].(float64) == float64(model.RecordActionTransfer) {
			hasTransfer = true
			if rec["transferToUserName"] == nil || rec["transferToUserName"] == "" {
				t.Fatalf("转办记录应带目标昵称: %v", rec)
			}
		}
	}
	if !hasTransfer {
		t.Fatalf("时间线缺少转办记录: %v", records)
	}
}

// TestIntegrationRuntimeConcurrent409 覆盖并发办理（spec §4.3）：
// 同任务并行办理恰好一个成功其余 409；候选池兄弟并行先办者胜出。
func TestIntegrationRuntimeConcurrent409(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID
	pool := re.createRole(t, "rt_cc_pool", "审批组A/B")
	_, aTok := re.createUser(t, "rt_cc_a", []int64{menus, pool})
	_, bTok := re.createUser(t, "rt_cc_b", []int64{menus, pool})
	rID, rTok := re.createUser(t, "rt_cc_r", []int64{menus})

	defID := re.createDef(t, "rt_cc_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(pool, 10), 0),
	})
	re.startMarker(t, defID)

	// 场景 1：同一任务 8 路并行办理（同一办理人）。
	inst1 := re.start(t, rID, defID, 7301, "报销 #7301", "")
	task1 := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst1.ID)
	const goroutines = 8
	type result struct {
		status int
		msg    string
	}
	results := make([]result, goroutines)
	var wg sync.WaitGroup
	var startGate = make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-startGate // 同一瞬间放行，保证真正并发
			status, api := re.approve(t, aTok, task1, "并发通过")
			results[i] = result{status: status, msg: api.Msg}
		}(i)
	}
	close(startGate)
	wg.Wait()
	okCount, conflict := 0, 0
	for _, r := range results {
		switch r.status {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("并发办理出现意外状态 %d：msg %q", r.status, r.msg)
		}
	}
	if okCount != 1 || conflict != goroutines-1 {
		t.Fatalf("同任务并发应恰好 1 成功 + %d 个 409，实际 ok=%d conflict=%d", goroutines-1, okCount, conflict)
	}
	if got := re.instanceStatus(t, rTok, inst1.ID); got != model.InstanceStatusApproved {
		t.Fatalf("实例应为通过，实际 %d", got)
	}
	if n := re.handler.callCount("approved:"); n != 1 {
		t.Fatalf("并发下 OnApproved 应恰 1 次，实际 %d", n)
	}

	// 场景 2：候选池兄弟并行（A、B 各自办理自己的任务）。
	inst2 := re.start(t, rID, defID, 7302, "报销 #7302", "")
	aTask := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst2.ID)
	bTask := re.taskIDOf(t, re.listTasks(t, bTok, "todo"), inst2.ID)
	sibling := make([]result, 2)
	var wg2 sync.WaitGroup
	gate2 := make(chan struct{})
	for i, tc := range []struct {
		tok  string
		task int64
	}{{aTok, aTask}, {bTok, bTask}} {
		wg2.Add(1)
		go func(i int, tok string, taskID int64) {
			defer wg2.Done()
			<-gate2
			status, api := re.approve(t, tok, taskID, "")
			sibling[i] = result{status: status, msg: api.Msg}
		}(i, tc.tok, tc.task)
	}
	close(gate2)
	wg2.Wait()
	okCount = 0
	for _, r := range sibling {
		switch r.status {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
		default:
			t.Fatalf("兄弟并行出现意外状态 %d：msg %q", r.status, r.msg)
		}
	}
	if okCount != 1 {
		t.Fatalf("先办者胜出应恰好 1 个成功，实际 %d（%v）", okCount, sibling)
	}
	if got := re.instanceStatus(t, rTok, inst2.ID); got != model.InstanceStatusApproved {
		t.Fatalf("实例应为通过，实际 %d", got)
	}
	if n := re.handler.callCount("approved:"); n != 2 { // 场景 1 一次 + 场景 2 一次
		t.Fatalf("OnApproved 累计应 2 次，实际 %d", n)
	}
	_, detail := re.instanceDetail(t, re.adminTok, inst2.ID)
	records, _ := detail["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("胜出者只有一条通过记录，实际 %d", len(records))
	}
}

// TestIntegrationRuntimeHandlerRollback 覆盖回调事务语义（spec §4.4）：
// handler 失败整笔回滚——任务保持待办、实例保持运行、无记录、handler 自己的
// 业务写一并蒸发；恢复后重试成功。
func TestIntegrationRuntimeHandlerRollback(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID
	pool := re.createRole(t, "rt_rb_pool", "审批组A")
	_, aTok := re.createUser(t, "rt_rb_a", []int64{menus, pool})
	rID, rTok := re.createUser(t, "rt_rb_r", []int64{menus})

	defID := re.createDef(t, "rt_rb_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(pool, 10), 0),
	})
	re.startMarker(t, defID)
	inst := re.start(t, rID, defID, 7401, "报销 #7401", "")
	aTask := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst.ID)

	// 通过 → OnApproved 注入失败 → HTTP 500，整笔回滚。
	re.handler.failApprove = true
	if status, api := re.approve(t, aTok, aTask, "同意"); status != http.StatusInternalServerError {
		t.Fatalf("handler 失败应 500，实际 HTTP %d msg %q", status, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusRunning {
		t.Fatalf("回滚后实例应保持运行中，实际 %d", got)
	}
	if rows := re.listTasks(t, aTok, "todo"); len(rows) != 1 {
		t.Fatalf("回滚后任务应保持待办，实际 %d 条", len(rows))
	}
	if remark := re.definitionRemark(t, defID); remark != "" {
		t.Fatalf("handler 的业务写应随事务回滚，remark=%q", remark)
	}
	_, detail := re.instanceDetail(t, rTok, inst.ID)
	if records, _ := detail["records"].([]any); len(records) != 0 {
		t.Fatalf("回滚后不应留下审批记录，实际 %d 条", len(records))
	}
	// 通知不残留：发起人无结果通知，A 的 bpm_todo 仍未读（任务复活但通知未消化）。
	if n := re.unreadCount(t, re.notifications(t, rTok, "bpm_result")); n != 0 {
		t.Fatalf("回滚后不应有结果通知，实际 %d 条", n)
	}

	// 恢复 handler → 重试成功。
	re.handler.failApprove = false
	if status, api := re.approve(t, aTok, aTask, "同意"); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("恢复后通过：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusApproved {
		t.Fatalf("实例应为通过，实际 %d", got)
	}
	if remark := re.definitionRemark(t, defID); remark != "biz-approved" {
		t.Fatalf("handler 的业务写应提交，remark=%q", remark)
	}

	// 驳回回调失败同样整笔回滚：新实例，注入失败后实例保持运行中。
	re.handler.failReject = true
	inst2 := re.start(t, rID, defID, 7402, "报销 #7402", "")
	task2 := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst2.ID)
	if status, api := re.reject(t, aTok, task2, "不合格"); status != http.StatusInternalServerError {
		t.Fatalf("驳回回调失败应 500，实际 HTTP %d msg %q", status, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst2.ID); got != model.InstanceStatusRunning {
		t.Fatalf("驳回回滚后实例应保持运行中，实际 %d", got)
	}
	re.handler.failReject = false
	if status, api := re.reject(t, aTok, task2, "不合格"); status != http.StatusOK {
		t.Fatalf("恢复后驳回：HTTP %d msg %q", status, api.Msg)
	}
	if got := re.instanceStatus(t, rTok, inst2.ID); got != model.InstanceStatusRejected {
		t.Fatalf("实例应为驳回，实际 %d", got)
	}
}

// TestIntegrationRuntimeTenantIsolation 覆盖租户隔离（spec §4.6）：
// 跨租户任务/实例一律 404，列表互不可见。
func TestIntegrationRuntimeTenantIsolation(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID
	pool := re.createRole(t, "rt_ti_pool", "审批组A")
	_, aTok := re.createUser(t, "rt_ti_a", []int64{menus, pool})
	rID, _ := re.createUser(t, "rt_ti_r", []int64{menus})

	defID := re.createDef(t, "rt_ti_flow", []map[string]any{
		node("组长审批", model.AssigneeRole, strconv.FormatInt(pool, 10), 0),
	})
	inst := re.start(t, rID, defID, 7501, "报销 #7501", "")
	aTask := re.taskIDOf(t, re.listTasks(t, aTok, "todo"), inst.ID)

	// 租户 2 admin（同名 admin 账号，独立租户上下文）。
	status, api := re.env.Request(t, http.MethodPost, "/system/tenant", re.adminTok, map[string]any{
		"name": "bpm_runtime_tenant_two", "contactName": "联系人", "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建租户：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = re.env.Request(t, http.MethodGet, "/system/tenant/page?name=bpm_runtime_tenant_two&pageNo=1&pageSize=10", re.adminTok, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("查询租户: %v", err)
	}
	tenant2 := page.List[0].ID
	status, api = re.env.Request(t, http.MethodPost, "/system/auth/login", "", map[string]any{
		"username": "admin", "password": "123456", "tenantId": tenant2,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 2 登录：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(api.Data, &login); err != nil {
		t.Fatalf("解析租户 2 登录: %v", err)
	}
	t2Tok := login.AccessToken

	// 跨租户任务办理 → 404（不泄露存在性）。
	if status, api := re.approve(t, t2Tok, aTask, ""); status != http.StatusNotFound {
		t.Fatalf("跨租户办理应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 跨租户实例详情/撤回 → 404。
	if status, _ := re.instanceDetail(t, t2Tok, inst.ID); status != http.StatusNotFound {
		t.Fatalf("跨租户详情应 404，实际 HTTP %d", status)
	}
	if status, api := re.withdraw(t, t2Tok, inst.ID, ""); status != http.StatusNotFound {
		t.Fatalf("跨租户撤回应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	// 列表互不可见。
	if _, total := pageDefinitions(t, re.env, t2Tok, "&key=rt_ti_flow"); total != 0 {
		t.Fatalf("租户 2 不应看到租户 1 的定义")
	}
	if rows := re.listTasks(t, t2Tok, "todo"); len(rows) != 0 {
		t.Fatalf("租户 2 不应看到租户 1 的待办，实际 %d 条", len(rows))
	}
}

// TestIntegrationRuntimeDeptLeaderRulesHTTP 覆盖部门负责人规则的真实数据路径
// （spec §4.2）：initiator_dept_leader 空缺沿部门树向上、dept_leader 指定部门、
// 到根仍空缺 400（负责人停用视为空缺）。解析语义已被纯函数表驱动用例锁定
// （assignee_test.go），本用例证明 system 网关的部门树查询（UserDeptID/
// DeptLeaderUserID/DeptParentID）在真实 MySQL 上与语义一致。
func TestIntegrationRuntimeDeptLeaderRulesHTTP(t *testing.T) {
	re := newRuntimeEnv(t)
	menus := re.roleID

	// 部门树：300 BPM 事业部（负责人 leaderA）→ 301 交付组（空缺）→ 302 专项组（空缺）。
	leaderID, _ := re.createUser(t, "rt_dl_leader", []int64{menus})
	status, api := re.env.Request(t, http.MethodPost, "/system/dept", re.adminTok, map[string]any{
		"name": "BPM 事业部", "parentId": 0, "leaderUserId": leaderID, "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建部门 300：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	dept300 := re.findDeptByName(t, "BPM 事业部")
	status, api = re.env.Request(t, http.MethodPost, "/system/dept", re.adminTok, map[string]any{
		"name": "交付组", "parentId": dept300, "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建部门 301：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	dept301 := re.findDeptByName(t, "交付组")
	status, api = re.env.Request(t, http.MethodPost, "/system/dept", re.adminTok, map[string]any{
		"name": "专项组", "parentId": dept301, "status": 0,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建部门 302：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	dept302 := re.findDeptByName(t, "专项组")

	// 成员挂在最深的 302（两级空缺）；发起人挂 300。部门在创建时指定。
	memberID, _ := re.createUserWithDept(t, "rt_dl_member", []int64{menus}, dept302)
	rID, rTok := re.createUserWithDept(t, "rt_dl_r", []int64{menus}, dept300)

	// initiator_dept_leader：302 → 301 空缺 → 300 负责人 leaderA。
	defID := re.createDef(t, "rt_dl_flow", []map[string]any{
		node("发起人部门负责人", model.AssigneeInitiatorDeptLeader, "", 0),
	})
	inst := re.start(t, rID, defID, 7601, "请假 #7601", "")
	if got := re.instanceStatus(t, rTok, inst.ID); got != model.InstanceStatusRunning {
		t.Fatalf("实例应运行中，实际 %d", got)
	}
	leaderTok := re.env.Login(t, "rt_dl_leader", "123456")
	if rows := re.listTasks(t, leaderTok, "todo"); len(rows) != 1 {
		t.Fatalf("发起人部门负责人（向上查找）应物化 1 条待办，实际 %d", len(rows))
	}

	// dept_leader 指定部门：成员在 302 发起，向上命中 300 负责人。
	def2 := re.createDef(t, "rt_dl_flow2", []map[string]any{
		node("指定部门负责人", model.AssigneeDeptLeader, "", dept302),
	})
	inst2 := re.start(t, memberID, def2, 7602, "报销 #7602", "")
	if rows := re.listTasks(t, leaderTok, "todo"); len(rows) != 2 {
		t.Fatalf("指定部门负责人（向上查找）应再物化 1 条待办，实际 %d", len(rows))
	}
	if got := re.instanceStatus(t, rTok, inst2.ID); got != model.InstanceStatusRunning {
		t.Fatalf("实例应运行中，实际 %d", got)
	}

	// 到根仍空缺：负责人停用视为空缺 → 400（保留角色绑定，仅停用）。
	status, api = re.env.Request(t, http.MethodPut, "/system/user", re.adminTok, map[string]any{
		"id": leaderID, "nickname": "rt_dl_leader-昵称", "deptId": dept300, "status": 1, "roleIds": []int64{menus},
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("停用负责人：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	re.startWantStatus(t, memberID, defID, 7603, "不应发起", http.StatusBadRequest)
}

// TestIntegrationRuntimeCommonRoleTodoAccess 普通角色（内置 role 2）经种子
// 授权 512/5121 后可直接打开待办页（PR #66 审查 P1-2）：三个 tab 的查询
// 接口都不再 403，通知深链的落点可用。
func TestIntegrationRuntimeCommonRoleTodoAccess(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	demo := env.Login(t, "demo", "123456")
	for _, tab := range []string{"todo", "done", "initiated"} {
		status, api := env.Request(t, http.MethodGet,
			"/bpm/task/page?tab="+tab+"&pageNo=1&pageSize=10", demo, nil)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("普通角色查询 %s 待办：HTTP %d code %d msg %q，期望 HTTP 200 code 0",
				tab, status, api.Code, api.Msg)
		}
	}
}

// createUserWithDept 建指定部门的启用用户，返回 ID（不登录）。
func (re *runtimeEnv) createUserWithDept(t *testing.T, username string, roleIDs []int64, deptID int64) (int64, string) {
	t.Helper()
	status, api := re.env.Request(t, http.MethodPost, "/system/user", re.adminTok, map[string]any{
		"username": username, "nickname": username + "-昵称", "password": "123456",
		"deptId": deptID, "roleIds": roleIDs,
	})
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建用户 %s：HTTP %d code %d msg %q", username, status, api.Code, api.Msg)
	}
	status, api = re.env.Request(t, http.MethodGet, "/system/user/page?username="+username+"&pageNo=1&pageSize=10", re.adminTok, nil)
	var page struct {
		List []struct {
			ID int64 `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || len(page.List) != 1 {
		t.Fatalf("查询用户 %s: %v（data=%s）", username, err, string(api.Data))
	}
	return page.List[0].ID, re.env.Login(t, username, "123456")
}

// findDeptByName 从部门树按名称查部门 ID（树接口返回嵌套 children）。
func (re *runtimeEnv) findDeptByName(t *testing.T, name string) int64 {
	t.Helper()
	status, api := re.env.Request(t, http.MethodGet, "/system/dept/tree", re.adminTok, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询部门树：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var tree []map[string]any
	if err := json.Unmarshal(api.Data, &tree); err != nil {
		t.Fatalf("解析部门树: %v（data=%s）", err, string(api.Data))
	}
	var result int64
	var walk func(rows []map[string]any)
	walk = func(rows []map[string]any) {
		for _, row := range rows {
			if row["name"] == name {
				result = int64(row["id"].(float64))
			}
			if children, ok := row["children"].([]any); ok {
				list := make([]map[string]any, 0, len(children))
				for _, c := range children {
					if m, ok := c.(map[string]any); ok {
						list = append(list, m)
					}
				}
				walk(list)
			}
		}
	}
	walk(tree)
	if result == 0 {
		t.Fatalf("部门树中未找到 %s", name)
	}
	return result
}
