//go:build integration

// 合同审批迁移到 bpm 的 HTTP 集成用例（issue #47，docs/oa-mvp-spec.md §4.5）：
//   - 提交 → 待办可见 → 通过/驳回/撤回三种结果正确（含驳回原因落库）；
//   - 合同详情时间线与待办一致，状态端点不再提供直通审批；
//   - 待审合同作废时审批实例自动终止；
//   - 存量待审合同 backfill 幂等（第二次执行 0 条）；
//   - 流程未发布/审批人解析为空时提交返回可读 400。
//
// 主 seam = 真实 router + 生产中间件 + 真实 MySQL（测试库 _t47）。
package crm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	crmmodel "github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// submitContract 提交合同审批并返回 HTTP 状态与业务响应（原始信封）。
func submitContract(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) (int, testutil.APIResponse) {
	t.Helper()
	return changeContractStatusDirect(t, env, token, id, crmmodel.ContractStatusPending, "")
}

// mustContractDetail 读取合同详情并断言成功。
func mustContractDetail(t *testing.T, env *testutil.IntegrationEnv, token string, id int64) map[string]any {
	t.Helper()
	status, api := getContractRaw(t, env, token, id)
	mustOK(t, "读取合同详情", status, api)
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析合同详情: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// approvalOf 取详情里的 approval 段（无实例返回 nil）。
func approvalOf(t *testing.T, detail map[string]any) map[string]any {
	t.Helper()
	raw, ok := detail["approval"].(map[string]any)
	if !ok || raw == nil {
		return nil
	}
	return raw
}

// rejectTask 在待办中心驳回任务。
func rejectTask(t *testing.T, env *testutil.IntegrationEnv, token string, taskID int64, reason string) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/task/%d/reject", taskID), token, map[string]any{
		"comment": reason,
	})
}

// TestIntegrationContractApprovalBPMFlow 提交 → 待办 → 通过：合同置已签，
// 详情时间线与待办一致；状态端点不再允许直通审批（issue #47）。
func TestIntegrationContractApprovalBPMFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "合同审批宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "审批流合同", map[string]any{
		"customerId": customerID, "amount": "128000.00",
	})
	contractID := int64(contract["id"].(float64))
	contractNo := contract["contractNo"].(string)

	// 提交：同一事务启动实例，响应自带时间线与当前待办。
	status, api := submitContract(t, env, admin, contractID)
	mustOK(t, "提交合同审批", status, api)
	var submitted map[string]any
	if err := json.Unmarshal(api.Data, &submitted); err != nil {
		t.Fatalf("解析提交响应: %v（data=%s）", err, string(api.Data))
	}
	if submitted["status"] != float64(crmmodel.ContractStatusPending) {
		t.Fatalf("提交后状态应待审，实际 %v", submitted["status"])
	}
	approval := approvalOf(t, submitted)
	if approval == nil {
		t.Fatal("提交响应应包含审批时间线")
	}
	inst := approval["instance"].(map[string]any)
	if inst["businessType"] != "crm_contract_approval" || int64(inst["businessId"].(float64)) != contractID {
		t.Fatalf("实例业务类型/ID 错误：%v", inst)
	}
	if !strings.Contains(inst["title"].(string), contractNo) {
		t.Fatalf("实例标题应含合同号 %s，实际 %q", contractNo, inst["title"])
	}
	summary := inst["summary"].(string)
	if !strings.Contains(summary, "合同审批宿主客户") || !strings.Contains(summary, "128000.00") {
		t.Fatalf("实例摘要应含客户名与金额，实际 %q", summary)
	}

	// 待办可见：管理员持有 crm:contract:approve（super_admin），是候选池办理人。
	todo := myContractApprovalTodo(t, env, admin, contractID)
	if todo == nil {
		t.Fatal("提交后应在待办中心看到合同审批任务")
	}
	if todo["instanceTitle"] != inst["title"] {
		t.Fatalf("待办标题应与实例快照一致：%v vs %v", todo["instanceTitle"], inst["title"])
	}

	// 状态端点不再直通审批（即使持有审批权限码）。
	if st, direct := changeContractStatusDirect(t, env, admin, contractID, crmmodel.ContractStatusSigned, ""); st != http.StatusBadRequest {
		t.Fatalf("直通审批应 400，实际 HTTP %d msg %q", st, direct.Msg)
	}

	// 通过：合同置已签，时间线出现通过记录，待办被消化。
	taskID := int64(todo["id"].(float64))
	status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/task/%d/approve", taskID), admin, map[string]any{
		"comment": "同意签约",
	})
	mustOK(t, "审批通过", status, api)

	detail := mustContractDetail(t, env, admin, contractID)
	if detail["status"] != float64(crmmodel.ContractStatusSigned) {
		t.Fatalf("通过后合同应已签，实际 %v", detail["status"])
	}
	approval = approvalOf(t, detail)
	if approval == nil {
		t.Fatal("通过后详情应保留审批时间线")
	}
	inst = approval["instance"].(map[string]any)
	if inst["status"] != float64(bpmmodel.InstanceStatusApproved) {
		t.Fatalf("实例状态应通过，实际 %v", inst["status"])
	}
	records := approval["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("时间线应有 1 条通过记录，实际 %d", len(records))
	}
	rec := records[0].(map[string]any)
	if rec["action"] != float64(bpmmodel.RecordActionApprove) || rec["comment"] != "同意签约" {
		t.Fatalf("通过记录内容错误：%v", rec)
	}
	if pending := todoTasksOf(t, approval); len(pending) != 0 {
		t.Fatalf("通过后不应有当前待办，实际 %v", pending)
	}
	if todo := myContractApprovalTodo(t, env, admin, contractID); todo != nil {
		t.Fatalf("通过后待办中心不应再有该合同任务：%v", todo)
	}
}

// TestIntegrationContractApprovalRejectAndResubmit 驳回回草稿并落原因，
// 重新提交清空原因并生成新的运行中实例（issue #47）。
func TestIntegrationContractApprovalRejectAndResubmit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "驳回原因宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "驳回合同", map[string]any{
		"customerId": customerID, "amount": "100.00",
	})
	contractID := int64(contract["id"].(float64))

	if status, api := submitContract(t, env, admin, contractID); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	todo := myContractApprovalTodo(t, env, admin, contractID)
	if todo == nil {
		t.Fatal("提交后应有待办")
	}
	status, api := rejectTask(t, env, admin, int64(todo["id"].(float64)), "金额需要重新核算")
	mustOK(t, "审批驳回", status, api)

	detail := mustContractDetail(t, env, admin, contractID)
	if detail["status"] != float64(crmmodel.ContractStatusDraft) {
		t.Fatalf("驳回后应回草稿，实际 %v", detail["status"])
	}
	if detail["rejectReason"] != "金额需要重新核算" {
		t.Fatalf("驳回原因未落库：%v", detail["rejectReason"])
	}
	approval := approvalOf(t, detail)
	if approval == nil {
		t.Fatal("驳回后仍应保留审批时间线")
	}
	if approval["instance"].(map[string]any)["status"] != float64(bpmmodel.InstanceStatusRejected) {
		t.Fatalf("实例状态应驳回：%v", approval["instance"])
	}
	records := approval["records"].([]any)
	rec := records[len(records)-1].(map[string]any)
	if rec["action"] != float64(bpmmodel.RecordActionReject) || rec["comment"] != "金额需要重新核算" {
		t.Fatalf("驳回记录内容错误：%v", rec)
	}

	// 重新提交：驳回原因清空，生成新的运行中实例（旧实例保留为历史）。
	if status, api := submitContract(t, env, admin, contractID); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("重新提交：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	detail = mustContractDetail(t, env, admin, contractID)
	if detail["status"] != float64(crmmodel.ContractStatusPending) {
		t.Fatalf("重新提交后应待审，实际 %v", detail["status"])
	}
	if detail["rejectReason"] != "" && detail["rejectReason"] != nil {
		t.Fatalf("重新提交应清空驳回原因，实际 %v", detail["rejectReason"])
	}
	approval = approvalOf(t, detail)
	if approval["instance"].(map[string]any)["status"] != float64(bpmmodel.InstanceStatusRunning) {
		t.Fatalf("重新提交后最新实例应运行中：%v", approval["instance"])
	}
	if todo := myContractApprovalTodo(t, env, admin, contractID); todo == nil {
		t.Fatal("重新提交后应重新出现待办")
	}
}

// TestIntegrationContractApprovalWithdrawAndVoid 撤回回草稿；待审作废时实例
// 自动终止且待办消失（issue #47）。
func TestIntegrationContractApprovalWithdrawAndVoid(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "撤回作废宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 撤回：发起人经 bpm 实例撤回，合同回草稿。
	withdrawContract := createContract(t, env, admin, "撤回归档合同", map[string]any{
		"customerId": customerID, "amount": "10.00",
	})
	withdrawID := int64(withdrawContract["id"].(float64))
	if status, api := submitContract(t, env, admin, withdrawID); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	detail := mustContractDetail(t, env, admin, withdrawID)
	instanceID := int64(approvalOf(t, detail)["instance"].(map[string]any)["id"].(float64))
	status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/instance/%d/withdraw", instanceID), admin, map[string]any{
		"comment": "方案调整",
	})
	mustOK(t, "撤回审批", status, api)
	detail = mustContractDetail(t, env, admin, withdrawID)
	if detail["status"] != float64(crmmodel.ContractStatusDraft) {
		t.Fatalf("撤回后合同应回草稿，实际 %v", detail["status"])
	}
	approval := approvalOf(t, detail)
	if approval["instance"].(map[string]any)["status"] != float64(bpmmodel.InstanceStatusWithdrawn) {
		t.Fatalf("实例状态应撤回：%v", approval["instance"])
	}
	if pending := todoTasksOf(t, approval); len(pending) != 0 {
		t.Fatalf("撤回后不应有当前待办：%v", pending)
	}

	// 作废：待审合同作废时同事务终止运行中实例（时间线落撤回记录，待办消失）。
	voidContract := createContract(t, env, admin, "待审作废合同", map[string]any{
		"customerId": customerID, "amount": "20.00",
	})
	voidID := int64(voidContract["id"].(float64))
	if status, api := submitContract(t, env, admin, voidID); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if todo := myContractApprovalTodo(t, env, admin, voidID); todo == nil {
		t.Fatal("作废前应有待办")
	}
	status, api = changeContractStatusDirect(t, env, admin, voidID, crmmodel.ContractStatusVoid, "")
	mustOK(t, "待审合同作废", status, api)
	detail = mustContractDetail(t, env, admin, voidID)
	if detail["status"] != float64(crmmodel.ContractStatusVoid) {
		t.Fatalf("作废后状态应为作废，实际 %v", detail["status"])
	}
	approval = approvalOf(t, detail)
	if approval["instance"].(map[string]any)["status"] != float64(bpmmodel.InstanceStatusWithdrawn) {
		t.Fatalf("作废应终止运行中实例：%v", approval["instance"])
	}
	if pending := todoTasksOf(t, approval); len(pending) != 0 {
		t.Fatalf("作废后不应有当前待办：%v", pending)
	}
	if todo := myContractApprovalTodo(t, env, admin, voidID); todo != nil {
		t.Fatalf("作废后待办中心不应再有该合同任务：%v", todo)
	}
}

// todoTasksOf 取审批详情里的当前待办任务（status=0）。
func todoTasksOf(t *testing.T, approval map[string]any) []any {
	t.Helper()
	raw, ok := approval["tasks"].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, item := range raw {
		task := item.(map[string]any)
		if task["status"] == float64(bpmmodel.TaskStatusTodo) {
			out = append(out, task)
		}
	}
	return out
}

// TestIntegrationContractVoidApproveConcurrent 作废与审批并发（PR #66 审查
// P2-2）：作废改为「实例 → 合同」锁序后与审批动作同向，不再出现反向加锁；
// 断言两边都不返回 500（MySQL 1213 死锁会冒泡成 500），作废成功，最终状态
// 合法（合同作废，实例已终止或已通过后合同被作废）。
func TestIntegrationContractVoidApproveConcurrent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "并发作废宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	for round := 0; round < 5; round++ {
		contract := createContract(t, env, admin, fmt.Sprintf("并发作废合同-%d", round), map[string]any{
			"customerId": customerID, "amount": "66.00",
		})
		contractID := int64(contract["id"].(float64))
		if status, api := submitContract(t, env, admin, contractID); status != http.StatusOK || api.Code != 0 {
			t.Fatalf("第 %d 轮提交审批：HTTP %d code %d msg %q", round, status, api.Code, api.Msg)
		}
		todo := myContractApprovalTodo(t, env, admin, contractID)
		if todo == nil {
			t.Fatalf("第 %d 轮提交后应有待办", round)
		}
		taskID := int64(todo["id"].(float64))

		type result struct {
			status int
			msg    string
		}
		var wg sync.WaitGroup
		gate := make(chan struct{})
		approveRes := make([]result, 1)
		voidRes := make([]result, 1)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-gate
			status, api := env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/task/%d/approve", taskID), admin,
				map[string]any{"comment": "并发通过"})
			approveRes[0] = result{status: status, msg: api.Msg}
		}()
		go func() {
			defer wg.Done()
			<-gate
			status, api := changeContractStatusDirect(t, env, admin, contractID, crmmodel.ContractStatusVoid, "")
			voidRes[0] = result{status: status, msg: api.Msg}
		}()
		close(gate)
		wg.Wait()

		if approveRes[0].status >= http.StatusInternalServerError {
			t.Fatalf("第 %d 轮并发审批返回 5xx（疑似死锁）：HTTP %d msg %q",
				round, approveRes[0].status, approveRes[0].msg)
		}
		if voidRes[0].status >= http.StatusInternalServerError {
			t.Fatalf("第 %d 轮并发作废返回 5xx（疑似死锁）：HTTP %d msg %q",
				round, voidRes[0].status, voidRes[0].msg)
		}
		// 作废是目标动作：无论审批先到还是后到都必须成功。
		if voidRes[0].status != http.StatusOK {
			t.Fatalf("第 %d 轮作废：HTTP %d msg %q，期望 200", round, voidRes[0].status, voidRes[0].msg)
		}
		// 审批可能成功（先拿到实例锁）或因实例已终止而 409；其余状态不合法。
		if approveRes[0].status != http.StatusOK && approveRes[0].status != http.StatusConflict {
			t.Fatalf("第 %d 轮审批：HTTP %d msg %q，期望 200（先办）或 409（实例已终止）",
				round, approveRes[0].status, approveRes[0].msg)
		}
		detail := mustContractDetail(t, env, admin, contractID)
		if detail["status"] != float64(crmmodel.ContractStatusVoid) {
			t.Fatalf("第 %d 轮最终合同状态 = %v，期望作废", round, detail["status"])
		}
		approval := approvalOf(t, detail)
		if approval == nil {
			t.Fatalf("第 %d 轮详情缺少审批时间线", round)
		}
		instStatus := approval["instance"].(map[string]any)["status"]
		if instStatus != float64(bpmmodel.InstanceStatusWithdrawn) &&
			instStatus != float64(bpmmodel.InstanceStatusApproved) {
			t.Fatalf("第 %d 轮实例最终状态不合法：%v", round, instStatus)
		}
	}
}

// TestIntegrationContractApprovalBackfill 存量待审合同补建实例幂等：
// 只处理「待审且无运行中实例」的合同，第二次执行创建 0 条（issue #47）。
func TestIntegrationContractApprovalBackfill(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "backfill 宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 正常提交的待审合同：已有运行中实例，backfill 应跳过。
	withInstance := createContract(t, env, admin, "backfill 已提交合同", map[string]any{
		"customerId": customerID, "amount": "1.00",
	})
	withInstanceID := int64(withInstance["id"].(float64))
	if status, api := submitContract(t, env, admin, withInstanceID); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("提交审批：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 两条历史待审合同：直接改 DB 模拟迁移前遗留（无 bpm 实例）。
	legacyIDs := make([]int64, 0, 2)
	for i := 0; i < 2; i++ {
		c := createContract(t, env, admin, fmt.Sprintf("backfill 存量合同%d", i), map[string]any{
			"customerId": customerID, "amount": "2.00",
		})
		id := int64(c["id"].(float64))
		ctx := tenant.WithTenantID(context.Background(), 1)
		if err := env.DB.WithContext(ctx).Model(&crmmodel.Contract{}).
			Where("id = ?", id).Update("status", crmmodel.ContractStatusPending).Error; err != nil {
			t.Fatalf("构造存量待审合同: %v", err)
		}
		legacyIDs = append(legacyIDs, id)
	}

	first, err := env.App.CRM.BackfillContractApprovals(context.Background())
	if err != nil {
		t.Fatalf("首次 backfill: %v", err)
	}
	if first.Scanned != 3 || first.Created != 2 || first.Skipped != 1 || first.Failed != 0 {
		t.Fatalf("首次 backfill 计数错误：%+v", first)
	}
	// 每条存量合同都应有运行中实例与待办。
	for _, id := range legacyIDs {
		detail := mustContractDetail(t, env, admin, id)
		approval := approvalOf(t, detail)
		if approval == nil {
			t.Fatalf("合同 %d backfill 后应有审批实例", id)
		}
		if approval["instance"].(map[string]any)["status"] != float64(bpmmodel.InstanceStatusRunning) {
			t.Fatalf("合同 %d 实例应运行中：%v", id, approval["instance"])
		}
		if todo := myContractApprovalTodo(t, env, admin, id); todo == nil {
			t.Fatalf("合同 %d backfill 后应有待办", id)
		}
	}

	// 重复执行：不重复创建实例（第二次 created=0）。
	second, err := env.App.CRM.BackfillContractApprovals(context.Background())
	if err != nil {
		t.Fatalf("二次 backfill: %v", err)
	}
	if second.Created != 0 || second.Failed != 0 {
		t.Fatalf("二次 backfill 不应创建实例：%+v", second)
	}
	if second.Scanned != 3 || second.Skipped != 3 {
		t.Fatalf("二次 backfill 应全部跳过：%+v", second)
	}
}

// TestIntegrationContractApprovalSubmitErrors 流程不可用时的提交错误语义：
// 未发布（400 可读）与审批人解析为空（400 可读）都整笔回滚，合同保持草稿。
func TestIntegrationContractApprovalSubmitErrors(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "提交错误宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	t.Run("流程未发布", func(t *testing.T) {
		ctx := tenant.WithTenantID(context.Background(), 1)
		if err := env.DB.WithContext(ctx).Model(&bpmmodel.ProcessDefinition{}).
			Where("`key` = ? AND status = ?", "crm_contract_approval", bpmmodel.DefinitionStatusPublished).
			Update("status", bpmmodel.DefinitionStatusDisabled).Error; err != nil {
			t.Fatalf("停用合同审批流程: %v", err)
		}
		c := createContract(t, env, admin, "未发布流程合同", map[string]any{"customerId": customerID})
		id := int64(c["id"].(float64))
		status, api := submitContract(t, env, admin, id)
		if status != http.StatusBadRequest || !strings.Contains(api.Msg, "合同审批流程未发布") {
			t.Fatalf("未发布流程提交：HTTP %d msg %q，期望 400 含「合同审批流程未发布」", status, api.Msg)
		}
		detail := mustContractDetail(t, env, admin, id)
		if detail["status"] != float64(crmmodel.ContractStatusDraft) {
			t.Fatalf("提交失败后合同应保持草稿，实际 %v", detail["status"])
		}
		if approval := approvalOf(t, detail); approval != nil {
			t.Fatalf("提交失败不应创建实例：%v", approval)
		}
	})

	t.Run("审批人解析为空", func(t *testing.T) {
		// 指定用户规则：先发布流程，再停用该用户 → 运行期解析为空。
		createUserByAPI(t, env, admin, "contract_approver_off", 101, nil)
		approverID := findUserIDByUsername(t, env, admin, "contract_approver_off")

		status, api := env.Request(t, http.MethodPost, "/bpm/definition", admin, map[string]any{
			"key":          "crm_contract_approval",
			"name":         "合同审批（指定用户）",
			"businessType": "crm_contract_approval",
			"nodes": []map[string]any{{
				"name": "合同审批", "assigneeType": "user", "assigneeValue": fmt.Sprint(approverID),
			}},
		})
		mustOK(t, "创建指定用户流程定义", status, api)
		var created struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(api.Data, &created); err != nil {
			t.Fatalf("解析流程定义响应: %v（data=%s）", err, string(api.Data))
		}
		status, api = env.Request(t, http.MethodPost, fmt.Sprintf("/bpm/definition/%d/publish", created.ID), admin, nil)
		mustOK(t, "发布指定用户流程定义", status, api)

		ctx := tenant.WithTenantID(context.Background(), 1)
		if err := env.DB.WithContext(ctx).Model(&struct {
			ID int64 `gorm:"primaryKey"`
		}{}).Table("system_user").Where("id = ?", approverID).Update("status", 1).Error; err != nil {
			t.Fatalf("停用审批人: %v", err)
		}

		c := createContract(t, env, admin, "无审批人合同", map[string]any{"customerId": customerID})
		id := int64(c["id"].(float64))
		status, api = submitContract(t, env, admin, id)
		if status != http.StatusBadRequest || !strings.Contains(api.Msg, "解析不到审批人") {
			t.Fatalf("无审批人提交：HTTP %d msg %q，期望 400 含「解析不到审批人」", status, api.Msg)
		}
		detail := mustContractDetail(t, env, admin, id)
		if detail["status"] != float64(crmmodel.ContractStatusDraft) {
			t.Fatalf("提交失败后合同应保持草稿，实际 %v", detail["status"])
		}
	})
}
