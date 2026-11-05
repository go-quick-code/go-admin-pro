//go:build integration

// PR #39 独立审查修复的 HTTP 集成用例（P0/P1）：
//   - P0：客户编辑接口改负责人与 transfer 端点同一授权口径（无码 403）；
//   - P1：回款计划/记录金额不变量在并发下不超限（行锁串行化）。
//
// 与既有用例共用真实 router + 生产中间件 + 真实 MySQL（-p 1 串行跑包）。
package crm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/pkg/money"
	"github.com/lautung/go-admin-pro/internal/testutil"
)

// postJSONOnce 在独立 goroutine 中发一次 JSON 请求：不触碰 testing.T，
// 结果带回主 goroutine 断言（Fatal 只能在测试主 goroutine 调用）。
func postJSONOnce(env *testutil.IntegrationEnv, token, path string, payload map[string]any) (int, testutil.APIResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL(path), bytes.NewReader(raw))
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return resp.StatusCode, api, err
	}
	return resp.StatusCode, api, nil
}

// TestIntegrationCustomerUpdateTransferAuthorizes P0：编辑接口改负责人必须持
// crm:customer:transfer（或负责人本人），只有 crm:customer:update 时返回 403，
// 客户负责人不变；持码者成功并落 transfer 历史；负责人本人无转移码也可转移。
func TestIntegrationCustomerUpdateTransferAuthorizes(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 只有客户查询 + 修改按钮码，没有转移码。
	updateOnlyRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "PR39 仅更新角色", "code": "pr39_update_only", "dataScope": 1,
		"menuIds": []int64{crmCustomerMenuID, 4012},
	})
	// 修改 + 转移码。
	transferRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "PR39 转移角色", "code": "pr39_transfer", "dataScope": 1,
		"menuIds": []int64{crmCustomerMenuID, 4012, 4031},
	})
	createUserByAPI(t, env, admin, "pr39_owner_a", 101, []int64{updateOnlyRole})
	createUserByAPI(t, env, admin, "pr39_owner_b", 102, nil)
	createUserByAPI(t, env, admin, "pr39_updater", 103, []int64{updateOnlyRole})
	createUserByAPI(t, env, admin, "pr39_transferer", 103, []int64{transferRole})
	aID := findUserIDByUsername(t, env, admin, "pr39_owner_a")
	bID := findUserIDByUsername(t, env, admin, "pr39_owner_b")
	updater := env.Login(t, "pr39_updater", "123456")
	ownerA := env.Login(t, "pr39_owner_a", "123456")
	transferer := env.Login(t, "pr39_transferer", "123456")

	// 客户归属 owner_a。
	c := createCustomer(t, env, admin, "PR39 授权客户", map[string]any{"ownerUserId": aID})
	id := int64(c["id"].(float64))

	// 1) 非负责人、无转移码：改负责人 → 403，且客户完全未被修改。
	status, api := env.Request(t, http.MethodPut, "/crm/customer", updater, map[string]any{
		"id": id, "name": "PR39 越权改名", "ownerUserId": bID,
	})
	if status != http.StatusForbidden {
		t.Fatalf("无转移码经编辑改负责人：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	st, detailAPI := getCustomer(t, env, admin, id)
	mustOK(t, "越权后查详情", st, detailAPI)
	var detail map[string]any
	if err := json.Unmarshal(detailAPI.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerUserId"] != float64(aID) || detail["name"] != "PR39 授权客户" {
		t.Fatalf("越权请求不应产生任何修改：owner=%v name=%v", detail["ownerUserId"], detail["name"])
	}

	// 2) 负责人本人、无转移码：改负责人给 owner_b → 放行（本人路径），落 transfer 历史。
	status, api = env.Request(t, http.MethodPut, "/crm/customer", ownerA, map[string]any{
		"id": id, "name": "PR39 本人改名", "ownerUserId": bID,
	})
	mustOK(t, "负责人本人经编辑改负责人", status, api)
	st, detailAPI = getCustomer(t, env, admin, id)
	mustOK(t, "本人转移后查详情", st, detailAPI)
	if err := json.Unmarshal(detailAPI.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerUserId"] != float64(bID) || detail["name"] != "PR39 本人改名" {
		t.Fatalf("本人转移后 owner/name 不符：owner=%v name=%v", detail["ownerUserId"], detail["name"])
	}
	rows, _ := pageOwnerHistory(t, env, admin, id)
	if len(rows) == 0 || rows[0]["action"] != "transfer" ||
		rows[0]["fromOwnerUserId"] != float64(aID) || rows[0]["toOwnerUserId"] != float64(bID) {
		t.Fatalf("编辑转移应落 transfer 历史（a→b），实际 %v", rows)
	}

	// 3) 持转移码的非负责人：把客户从 owner_b 转回 owner_a → 成功。
	status, api = env.Request(t, http.MethodPut, "/crm/customer", transferer, map[string]any{
		"id": id, "name": "PR39 授权客户", "ownerUserId": aID,
	})
	mustOK(t, "持转移码经编辑改负责人", status, api)
	st, detailAPI = getCustomer(t, env, admin, id)
	mustOK(t, "持码转移后查详情", st, detailAPI)
	if err := json.Unmarshal(detailAPI.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["ownerUserId"] != float64(aID) {
		t.Fatalf("持码转移后 owner 应为 owner_a，实际 %v", detail["ownerUserId"])
	}
}

// TestIntegrationReceivableAmountInvariantUnderConcurrency P1：并发创建回款计划/
// 记录时，行锁保证 SUM 校验串行化——合同 100、每笔 30 的 8 个并发请求只允许
// 3 个成功，最终 SUM 恰为 90（≤ 100），不会各自按旧合计通过校验造成超限。
func TestIntegrationReceivableAmountInvariantUnderConcurrency(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "PR39 并发金额客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "PR39 并发金额合同", map[string]any{
		"customerId": customerID, "amount": "100.00",
	})
	contractID := int64(contract["id"].(float64))

	const n = 8
	runConcurrent := func(path string, payload func(i int) map[string]any) int {
		results := make([]struct {
			status int
			api    testutil.APIResponse
			err    error
		}, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				st, api, err := postJSONOnce(env, admin, path, payload(i))
				results[i].status, results[i].api, results[i].err = st, api, err
			}(i)
		}
		wg.Wait()
		okCount := 0
		for i, r := range results {
			if r.err != nil {
				t.Fatalf("并发请求 #%d 失败: %v", i, r.err)
			}
			switch {
			case r.status == http.StatusOK && r.api.Code == 0:
				okCount++
			case r.status == http.StatusBadRequest:
				// 超出金额上限：允许失败。
			default:
				t.Fatalf("并发请求 #%d 出现意外结果：HTTP %d code %d msg %q",
					i, r.status, r.api.Code, r.api.Msg)
			}
		}
		return okCount
	}

	// 计划：每笔 30，合同 100 → 最多 3 笔成功。
	okPlans := runConcurrent("/crm/receivable/plan", func(i int) map[string]any {
		return map[string]any{
			"contractId": contractID, "periodNo": i + 1,
			"amount": "30.00", "dueDate": "2030-01-01",
		}
	})
	if okPlans != 3 {
		t.Fatalf("并发创建计划成功数 = %d，期望 3（合同 100 / 每笔 30）", okPlans)
	}
	plans, _ := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d", contractID))
	total := money.Money{}
	for _, p := range plans {
		amount, err := money.ParseMoney(p["amount"].(string))
		if err != nil {
			t.Fatalf("解析计划金额 %v: %v", p["amount"], err)
		}
		next, err := total.Add(amount)
		if err != nil {
			t.Fatalf("累加计划金额: %v", err)
		}
		total = next
	}
	if total.Cents() > mustMoneyCents(t, "100.00") {
		t.Fatalf("计划合计 %s 超过合同金额 100.00", total.String())
	}
	if total.Cents() != mustMoneyCents(t, "90.00") {
		t.Fatalf("计划合计 = %s，期望 90.00（3 笔成功）", total.String())
	}

	// 记录：用一张独立的 100 合同/计划验证并发登记（每笔 30 → 最多 3 笔成功），
	// 避免受上面 90 的计划合计干扰。
	contract2 := createContract(t, env, admin, "PR39 并发记录合同", map[string]any{
		"customerId": customerID, "amount": "100.00",
	})
	contract2ID := int64(contract2["id"].(float64))
	plan2 := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contract2ID, "periodNo": 1, "amount": "100.00", "dueDate": "2030-01-01",
	})
	plan2ID := int64(plan2["id"].(float64))
	today := time.Now().Format("2006-01-02")
	okRecords := runConcurrent("/crm/receivable/record", func(i int) map[string]any {
		return map[string]any{
			"planId": plan2ID, "amount": "30.00", "receiptDate": today,
			"method": "bank",
		}
	})
	if okRecords != 3 {
		t.Fatalf("并发登记记录成功数 = %d，期望 3（计划 100 / 每笔 30）", okRecords)
	}
	records, _ := pageReceivableRecords(t, env, admin, fmt.Sprintf("&planId=%d", plan2ID))
	received := money.Money{}
	for _, r := range records {
		amount, err := money.ParseMoney(r["amount"].(string))
		if err != nil {
			t.Fatalf("解析记录金额 %v: %v", r["amount"], err)
		}
		next, err := received.Add(amount)
		if err != nil {
			t.Fatalf("累加记录金额: %v", err)
		}
		received = next
	}
	if received.Cents() != mustMoneyCents(t, "90.00") {
		t.Fatalf("记录合计 = %s，期望 90.00（3 笔成功且不超限）", received.String())
	}
}

// mustMoneyCents 解析十进制金额为分（测试断言用）。
func mustMoneyCents(t *testing.T, text string) int64 {
	t.Helper()
	m, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("ParseMoney(%q): %v", text, err)
	}
	return m.Cents()
}
