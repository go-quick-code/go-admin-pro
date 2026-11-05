//go:build integration

// 回款计划 + 回款记录 HTTP 集成测试（issue #18）：真实 router + 生产中间件
// （JWT/权限/租户插件）+ 真实 MySQL，在 HTTP 契约上验证计划/记录 CRUD、
// 金额不变量、派生状态与逾期筛选、HK 编号、删除约束、数据权限与租户隔离、
// 以及合同作废/删除被回款拦截的真实路径。
package crm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// crmReceivableMenuID 是种子中的回款管理菜单 ID（含 crm:receivable:query）。
const crmReceivableMenuID = int64(4120)

// receivableRecordNoRe 校验编号形状：HK + yyyyMMdd + 至少 4 位序号（超 9999 自然扩位）。
var receivableRecordNoRe = regexp.MustCompile(`^HK\d{8}\d{4,}$`)

// createReceivablePlan 创建回款计划并断言成功，返回落库后的完整计划 JSON。
func createReceivablePlan(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/receivable/plan", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建回款计划：HTTP %d code %d msg %q，期望 HTTP 200 code 0", status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建回款计划响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageReceivablePlans 查询回款计划分页并返回 list 与 total。
func pageReceivablePlans(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/receivable/plan/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询回款计划分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析回款计划分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// createReceivableRecord 创建回款记录并断言成功，返回落库后的完整记录 JSON。
func createReceivableRecord(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/crm/receivable/record", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建回款记录：HTTP %d code %d msg %q，期望 HTTP 200 code 0", status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建回款记录响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageReceivableRecords 查询回款记录分页并返回 list 与 total。
func pageReceivableRecords(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/crm/receivable/record/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询回款记录分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析回款记录分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// TestIntegrationReceivablePlanCRUD 计划 CRUD 全链路：
// 创建（期次/金额/到期日、默认派生状态）、列表（合同/状态/逾期筛选、合同金额与
// 已收金额、派生状态文案）、更新（排除自身校验）、删除（无记录可删）、
// 金额不变量 SUM(计划) ≤ 合同金额 与非法输入 400。
func TestIntegrationReceivablePlanCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "回款计划宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "回款计划合同", map[string]any{
		"customerId": customerID, "amount": "10000.00",
	})
	contractID := int64(contract["id"].(float64))

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	nextMonth := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	// 第一期：已到期未收款 → 逾期（4），overdue=true。
	first := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "3000.00",
		"dueDate": yesterday, "remark": "首期",
	})
	firstID := int64(first["id"].(float64))
	if first["periodNo"] != float64(1) || first["amount"] != "3000.00" {
		t.Fatalf("计划字段不符：periodNo=%v amount=%v", first["periodNo"], first["amount"])
	}
	if first["receivedAmount"] != "0.00" {
		t.Fatalf("新计划已收金额应为 0.00，实际 %v", first["receivedAmount"])
	}
	if first["status"] != float64(4) || first["statusName"] != "逾期" || first["overdue"] != true {
		t.Fatalf("已到期未收款应派生逾期：status=%v statusName=%v overdue=%v",
			first["status"], first["statusName"], first["overdue"])
	}
	if first["contractAmount"] != "10000.00" || first["contractName"] != "回款计划合同" {
		t.Fatalf("计划应带合同金额/名称：contractAmount=%v contractName=%v",
			first["contractAmount"], first["contractName"])
	}

	// 第二期：未到期未收款 → 待回款（1）。
	second := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 2, "amount": "7000.00", "dueDate": nextMonth,
	})
	secondID := int64(second["id"].(float64))
	if second["status"] != float64(1) || second["statusName"] != "待回款" || second["overdue"] != false {
		t.Fatalf("未到期未收款应派生待回款：status=%v statusName=%v overdue=%v",
			second["status"], second["statusName"], second["overdue"])
	}

	// 列表：按合同筛选返回两期，带合同信息与派生状态。
	list, total := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d", contractID))
	if total != 2 || len(list) != 2 {
		t.Fatalf("按合同筛选计划 total=%d list=%d，期望 2", total, len(list))
	}
	for _, row := range list {
		if row["contractNo"] != contract["contractNo"] || row["contractAmount"] != "10000.00" {
			t.Fatalf("列表行应带合同编号/金额：contractNo=%v contractAmount=%v",
				row["contractNo"], row["contractAmount"])
		}
		if row["statusName"] == "" {
			t.Fatalf("列表行应带派生状态文案：%+v", row)
		}
	}

	// 派生状态筛选：逾期只命中第一期；待回款只命中第二期。
	overdue, total := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d&status=4", contractID))
	if total != 1 || len(overdue) != 1 || int64(overdue[0]["id"].(float64)) != firstID {
		t.Fatalf("status=4 应只命中第一期，实际 total=%d list=%+v", total, overdue)
	}
	pending, total := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d&status=1", contractID))
	if total != 1 || len(pending) != 1 || int64(pending[0]["id"].(float64)) != secondID {
		t.Fatalf("status=1 应只命中第二期，实际 total=%d list=%+v", total, pending)
	}
	// 逾期布尔筛选。
	overdueOnly, total := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d&overdue=true", contractID))
	if total != 1 || len(overdueOnly) != 1 || overdueOnly[0]["overdue"] != true {
		t.Fatalf("overdue=true 应只命中逾期期次，实际 total=%d list=%+v", total, overdueOnly)
	}
	if _, total := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d&overdue=false", contractID)); total != 1 {
		t.Fatalf("overdue=false 应只命中未逾期期次，实际 total=%d", total)
	}

	// 非法状态筛选 400（绝不静默当「无此筛选」）。
	status, api := env.Request(t, http.MethodGet, "/crm/receivable/plan/page?pageNo=1&pageSize=10&status=9", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 金额不变量：两期合计已满 10000，再建 0.01 拒绝并点名合同金额。
	status, api = env.Request(t, http.MethodPost, "/crm/receivable/plan", admin, map[string]any{
		"contractId": contractID, "periodNo": 3, "amount": "0.01", "dueDate": nextMonth,
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "合同金额") {
		t.Fatalf("计划合计超合同金额：HTTP %d code %d msg %q，期望 400 且含「合同金额」", status, api.Code, api.Msg)
	}

	// 更新金额时排除自身：第一期改回 3000 仍合法（3000 + 7000 = 10000）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/crm/receivable/plan", admin, map[string]any{
		"id": firstID, "periodNo": 1, "amount": "3000.00", "dueDate": yesterday, "remark": "首期改",
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新计划（排除自身）：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	// 改大到 3000.01 超出合同金额 → 400。
	status, api = env.Request(t, http.MethodPut, "/crm/receivable/plan", admin, map[string]any{
		"id": firstID, "periodNo": 1, "amount": "3000.01", "dueDate": yesterday,
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "合同金额") {
		t.Fatalf("更新计划超合同金额：HTTP %d code %d msg %q，期望 400 且含「合同金额」", status, api.Code, api.Msg)
	}
	// 负数计划金额 400。
	status, api = env.Request(t, http.MethodPost, "/crm/receivable/plan", admin, map[string]any{
		"contractId": contractID, "periodNo": 4, "amount": "-1.00", "dueDate": nextMonth,
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "不能为负") {
		t.Fatalf("负数计划金额：HTTP %d code %d msg %q，期望 400 且含「不能为负」", status, api.Code, api.Msg)
	}
	// 期次非正、到期日缺失/非法 400。
	for name, payload := range map[string]map[string]any{
		"期次为 0": {"contractId": contractID, "periodNo": 0, "amount": "1.00", "dueDate": nextMonth},
		"到期日缺失": {"contractId": contractID, "periodNo": 5, "amount": "1.00"},
		"到期日非法": {"contractId": contractID, "periodNo": 5, "amount": "1.00", "dueDate": "2026/11/05"},
	} {
		status, api := env.Request(t, http.MethodPost, "/crm/receivable/plan", admin, payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", name, status, api.Code, api.Msg)
		}
	}

	// 无记录的计划可删；删除后列表只剩一期。
	delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/receivable/plan/%d", secondID), admin, nil)
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除无记录计划：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	deleteStatus, _ := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/receivable/plan/%d", secondID), admin, nil)
	if deleteStatus != http.StatusBadRequest && deleteStatus != http.StatusNotFound {
		t.Fatalf("重复删除计划应 404/400，实际 HTTP %d", deleteStatus)
	}
	if _, total := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d", contractID)); total != 1 {
		t.Fatalf("删除后计划 total=%d，期望 1", total)
	}
}

// TestIntegrationReceivableRecordAndDerivedStatus 回款记录全链路：
// HK 编号、记录列表按合同/计划筛选、已收金额与派生状态联动（部分回款/已回款）、
// 记录合计超计划金额 400、计划有记录禁删、删除记录后计划恢复可删与状态回退。
func TestIntegrationReceivableRecordAndDerivedStatus(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "回款记录宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "回款记录合同", map[string]any{
		"customerId": customerID, "amount": "5000.00",
	})
	contractID := int64(contract["id"].(float64))
	nextMonth := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "1000.00", "dueDate": nextMonth,
	})
	planID := int64(plan["id"].(float64))

	today := time.Now().Format("2006-01-02")
	first := createReceivableRecord(t, env, admin, map[string]any{
		"planId": planID, "amount": "400.00", "receiptDate": today,
		"method": "银行转账", "remark": "第一笔",
	})
	no, _ := first["recordNo"].(string)
	if !receivableRecordNoRe.MatchString(no) {
		t.Fatalf("回款记录编号 %q 不符合 HK+yyyyMMdd+4位序号", no)
	}
	if want := "HK" + time.Now().Format("20060102") + "0001"; no != want {
		t.Fatalf("租户当日首个回款记录编号应为 %s，实际 %s", want, no)
	}
	if first["contractId"] != float64(contractID) || first["planId"] != float64(planID) {
		t.Fatalf("记录应带出计划与合同：contractId=%v planId=%v", first["contractId"], first["planId"])
	}
	if first["planPeriodNo"] != float64(1) || first["contractNo"] != contract["contractNo"] {
		t.Fatalf("记录应带出期次与合同编号：planPeriodNo=%v contractNo=%v",
			first["planPeriodNo"], first["contractNo"])
	}

	// 计划派生为部分回款（2），已收 400.00。
	list, total := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d", contractID))
	if total != 1 || list[0]["status"] != float64(2) || list[0]["statusName"] != "部分回款" ||
		list[0]["receivedAmount"] != "400.00" {
		t.Fatalf("部分回款派生不符：%+v", list)
	}

	// 记录合计超计划金额：再收 600.01 拒绝并点名计划金额。
	status, api := env.Request(t, http.MethodPost, "/crm/receivable/record", admin, map[string]any{
		"planId": planID, "amount": "600.01", "receiptDate": today,
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "计划金额") {
		t.Fatalf("记录合计超计划金额：HTTP %d code %d msg %q，期望 400 且含「计划金额」", status, api.Code, api.Msg)
	}
	// 负数回款 400。
	status, api = env.Request(t, http.MethodPost, "/crm/receivable/record", admin, map[string]any{
		"planId": planID, "amount": "-0.01", "receiptDate": today,
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "不能为负") {
		t.Fatalf("负数回款：HTTP %d code %d msg %q，期望 400 且含「不能为负」", status, api.Code, api.Msg)
	}
	// 到账日期缺失 400。
	status, api = env.Request(t, http.MethodPost, "/crm/receivable/record", admin, map[string]any{
		"planId": planID, "amount": "1.00",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("到账日期缺失：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 计划有记录禁止删除（400）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/receivable/plan/%d", planID), admin, nil)
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "回款记录") {
		t.Fatalf("删除有记录的期次：HTTP %d code %d msg %q，期望 400 且含「回款记录」", status, api.Code, api.Msg)
	}

	// 收齐剩余 600 → 已回款（3），overdue=false。
	second := createReceivableRecord(t, env, admin, map[string]any{
		"planId": planID, "amount": "600.00", "receiptDate": today, "method": "现金",
	})
	if second["amount"] != "600.00" {
		t.Fatalf("第二笔记账金额不符：%v", second["amount"])
	}
	list, _ = pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d", contractID))
	if list[0]["status"] != float64(3) || list[0]["statusName"] != "已回款" ||
		list[0]["receivedAmount"] != "1000.00" || list[0]["overdue"] != false {
		t.Fatalf("收齐后应派生已回款：%+v", list[0])
	}

	// 记录列表：按合同与按计划筛选各 2 条，新→旧。
	records, total := pageReceivableRecords(t, env, admin, fmt.Sprintf("&contractId=%d", contractID))
	if total != 2 || len(records) != 2 {
		t.Fatalf("按合同筛选记录 total=%d list=%d，期望 2", total, len(records))
	}
	if _, total := pageReceivableRecords(t, env, admin, fmt.Sprintf("&planId=%d", planID)); total != 2 {
		t.Fatalf("按计划筛选记录 total=%d，期望 2", total)
	}

	// 删除一条记录：计划回到部分回款；再删一条：计划回到待回款，且可删除。
	for i := 0; i < 2; i++ {
		delStatus, delAPI := env.Request(t, http.MethodDelete,
			fmt.Sprintf("/crm/receivable/record/%d", int64(records[i]["id"].(float64))), admin, nil)
		if delStatus != http.StatusOK || delAPI.Code != 0 {
			t.Fatalf("删除回款记录 #%d：HTTP %d code %d msg %q", i, delStatus, delAPI.Code, delAPI.Msg)
		}
		list, _ = pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d", contractID))
		wantStatus := float64(2)
		if i == 1 {
			wantStatus = 1
		}
		if list[0]["status"] != wantStatus {
			t.Fatalf("删除 %d 条记录后计划状态应为 %v，实际 %v", i+1, wantStatus, list[0]["status"])
		}
	}
	if _, total := pageReceivableRecords(t, env, admin, fmt.Sprintf("&planId=%d", planID)); total != 0 {
		t.Fatalf("记录软删除后应不可见，实际 total=%d", total)
	}
	if delStatus, delAPI := env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/receivable/plan/%d", planID), admin, nil); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("记录清空后计划应可删：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
}

// createReceivableRecordOnce 在独立 goroutine 中发一次创建请求：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），错误带回主 goroutine 断言。
func createReceivableRecordOnce(env *testutil.IntegrationEnv, token string, planID int64, amount string) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"planId": planID, "amount": amount, "receiptDate": time.Now().Format("2006-01-02"),
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/crm/receivable/record"), bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK || api.Code != 0 {
		return "", fmt.Errorf("HTTP %d code %d msg %q", resp.StatusCode, api.Code, api.Msg)
	}
	var row struct {
		RecordNo string `json:"recordNo"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil {
		return "", err
	}
	return row.RecordNo, nil
}

// TestIntegrationReceivableRecordNumberUniqueUnderConcurrency 并发登记回款不重号：
// 同租户同日 N 个并发记录请求，编号两两不同（crm_sequence 原子发放，照 KH/SJ/HT）。
func TestIntegrationReceivableRecordNumberUniqueUnderConcurrency(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "并发回款宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "并发回款合同", map[string]any{
		"customerId": customerID, "amount": "100000.00",
	})
	contractID := int64(contract["id"].(float64))
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "100000.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	planID := int64(plan["id"].(float64))

	const n = 20
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			no, err := createReceivableRecordOnce(env, admin, planID, "1.00")
			results[i], errs[i] = no, err
		}(i)
	}
	wg.Wait()
	seen := make(map[string]bool, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发登记回款 #%d 失败: %v", i, err)
		}
		if results[i] == "" {
			t.Fatalf("并发登记回款 #%d 未返回编号", i)
		}
		if seen[results[i]] {
			t.Fatalf("回款记录编号 %s 重复发放", results[i])
		}
		seen[results[i]] = true
	}
	if _, total := pageReceivableRecords(t, env, admin, fmt.Sprintf("&planId=%d", planID)); total != n {
		t.Fatalf("并发登记后记录 total=%d，期望 %d", total, n)
	}
}

// TestIntegrationContractVoidBlockedByReceivable 合同作废/删除被真实回款拦截：
// 守卫在 app 装配处注册后，存在回款记录的合同作废 400、删除 400；
// 删除回款记录后守卫放行，作废与删除恢复可用。
func TestIntegrationContractVoidBlockedByReceivable(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "作废拦截宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	// 草稿合同可删；建计划与记录后作废/删除都被拦截。
	contract := createContract(t, env, admin, "有回款的草稿合同", map[string]any{
		"customerId": customerID, "amount": "1000.00",
	})
	contractID := int64(contract["id"].(float64))
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "1000.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	planID := int64(plan["id"].(float64))
	record := createReceivableRecord(t, env, admin, map[string]any{
		"planId": planID, "amount": "500.00", "receiptDate": time.Now().Format("2006-01-02"),
	})

	status, api := changeContractStatus(t, env, admin, contractID, 4, "")
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "回款记录") {
		t.Fatalf("有回款的合同作废：HTTP %d code %d msg %q，期望 400 且含「回款记录」", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", contractID), admin, nil)
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "回款记录") {
		t.Fatalf("有回款的合同删除：HTTP %d code %d msg %q，期望 400 且含「回款记录」", status, api.Code, api.Msg)
	}

	// 删除回款记录后作废与删除恢复可用。
	if delStatus, delAPI := env.Request(t, http.MethodDelete,
		fmt.Sprintf("/crm/receivable/record/%d", int64(record["id"].(float64))), admin, nil); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除回款记录：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	if status, api = changeContractStatus(t, env, admin, contractID, 4, ""); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("记录清空后作废合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/contract/%d", contractID), admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("作废后删除合同：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// setupReceivableScopeEnv 搭建回款数据范围观察场景（合同矩阵同款部门布局）：
// 各销售在部门 101/102/市场二组名下各持一单合同与一期计划，每期各有 admin
// 登记的回款记录；观察者在 102 持本人合同/计划/记录。
func setupReceivableScopeEnv(t *testing.T, env *testutil.IntegrationEnv, admin, roleCode string, dataScope int, customDeptIDs []int64, menuIDs ...int64) (observer string) {
	t.Helper()
	rndDept := int64(101)
	mktDept := int64(102)

	customer := createCustomer(t, env, admin, "回款范围矩阵宿主客户", nil)
	customerID := int64(customer["id"].(float64))

	createUserByAPI(t, env, admin, "receivable_owner_rnd", rndDept, nil)
	createUserByAPI(t, env, admin, "receivable_owner_mkt", mktDept, nil)
	createUserByAPI(t, env, admin, "receivable_owner_mkt2", mktDept, nil)
	subDept := createDeptByAPI(t, env, admin, "回款二组", mktDept)
	createUserByAPI(t, env, admin, "receivable_owner_sub", subDept, nil)

	dueDate := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	for _, u := range []string{"receivable_owner_rnd", "receivable_owner_mkt", "receivable_owner_mkt2", "receivable_owner_sub"} {
		id := findUserIDByUsername(t, env, admin, u)
		contract := createContract(t, env, admin, "回款范围合同_of_"+u, map[string]any{
			"customerId": customerID, "ownerUserId": id, "amount": "1000.00",
		})
		plan := createReceivablePlan(t, env, admin, map[string]any{
			"contractId": int64(contract["id"].(float64)), "periodNo": 1,
			"amount": "1000.00", "dueDate": dueDate,
		})
		createReceivableRecord(t, env, admin, map[string]any{
			"planId": int64(plan["id"].(float64)), "amount": "10.00", "receiptDate": today,
		})
	}

	if len(menuIDs) == 0 {
		menuIDs = []int64{crmReceivableMenuID, 4121, 4122, 4123, 4124}
	}
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "回款观察者角色", "code": roleCode, "dataScope": dataScope,
		"deptIds": customDeptIDs, "menuIds": menuIDs,
	})
	observer = "receivable_observer_" + roleCode
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerID := findUserIDByUsername(t, env, admin, observer)
	contract := createContract(t, env, admin, "回款范围合同_of_"+observer, map[string]any{
		"customerId": customerID, "ownerUserId": observerID, "amount": "1000.00",
	})
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": int64(contract["id"].(float64)), "periodNo": 1,
		"amount": "1000.00", "dueDate": dueDate,
	})
	createReceivableRecord(t, env, admin, map[string]any{
		"planId": int64(plan["id"].(float64)), "amount": "10.00", "receiptDate": today,
	})
	return observer
}

// visibleReceivableContractNames 按观察者视角查计划与记录分页，返回合同名称集合。
func visibleReceivableContractNames(t *testing.T, env *testutil.IntegrationEnv, token string) (plans, records map[string]bool) {
	t.Helper()
	planList, _ := pageReceivablePlans(t, env, token, "")
	recordList, _ := pageReceivableRecords(t, env, token, "")
	plans = make(map[string]bool, len(planList))
	records = make(map[string]bool, len(recordList))
	for _, row := range planList {
		plans[row["contractName"].(string)] = true
	}
	for _, row := range recordList {
		records[row["contractName"].(string)] = true
	}
	return plans, records
}

// assertNameSet 断言集合与期望完全一致（want 全可见，notWant 全不可见）。
func assertNameSet(t *testing.T, phase string, got map[string]bool, want, notWant []string) {
	t.Helper()
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s：%q 应可见，实际不可见（可见集合 %v）", phase, name, got)
		}
	}
	for _, name := range notWant {
		if got[name] {
			t.Fatalf("%s：%q 不应可见，实际可见", phase, name)
		}
	}
}

// TestIntegrationReceivableDataScopeMatrix 计划/记录列表按 5 种数据范围过滤：
// 可见性 join 合同后套用合同 owner 谓词（spec §4.1），计划与记录口径一致。
func TestIntegrationReceivableDataScopeMatrix(t *testing.T) {
	cases := []struct {
		name      string
		roleCode  string
		dataScope int
		customIDs []int64
		want      []string
		notWant   []string
	}{
		{
			name: "全部数据", roleCode: "rv_all", dataScope: 1,
			want: []string{
				"回款范围合同_of_receivable_owner_rnd", "回款范围合同_of_receivable_owner_mkt",
				"回款范围合同_of_receivable_owner_mkt2", "回款范围合同_of_receivable_owner_sub",
			},
		},
		{
			name: "自定义部门_精确集合不含子部门", roleCode: "rv_custom", dataScope: 2, customIDs: []int64{101, 103},
			want: []string{"回款范围合同_of_receivable_owner_rnd"},
			notWant: []string{
				"回款范围合同_of_receivable_owner_mkt", "回款范围合同_of_receivable_owner_mkt2",
				"回款范围合同_of_receivable_owner_sub",
			},
		},
		{
			name: "本部门", roleCode: "rv_dept", dataScope: 3,
			want: []string{"回款范围合同_of_receivable_owner_mkt", "回款范围合同_of_receivable_owner_mkt2"},
			notWant: []string{
				"回款范围合同_of_receivable_owner_rnd", "回款范围合同_of_receivable_owner_sub",
			},
		},
		{
			name: "本部门及以下", roleCode: "rv_dept_child", dataScope: 4,
			want: []string{
				"回款范围合同_of_receivable_owner_mkt", "回款范围合同_of_receivable_owner_mkt2",
				"回款范围合同_of_receivable_owner_sub",
			},
			notWant: []string{"回款范围合同_of_receivable_owner_rnd"},
		},
		{
			name: "仅本人", roleCode: "rv_self", dataScope: 5,
			notWant: []string{
				"回款范围合同_of_receivable_owner_rnd", "回款范围合同_of_receivable_owner_mkt",
				"回款范围合同_of_receivable_owner_mkt2", "回款范围合同_of_receivable_owner_sub",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewIntegrationEnv(t)
			admin := env.Login(t, "admin", "123456")
			observer := setupReceivableScopeEnv(t, env, admin, tc.roleCode, tc.dataScope, tc.customIDs)
			token := env.Login(t, observer, "123456")

			want := tc.want
			if tc.dataScope == 5 {
				want = []string{"回款范围合同_of_" + observer}
			}
			plans, records := visibleReceivableContractNames(t, env, token)
			assertNameSet(t, tc.name+"（计划）", plans, want, tc.notWant)
			assertNameSet(t, tc.name+"（记录）", records, want, tc.notWant)
		})
	}
}

// TestIntegrationReceivableOutsideScope404 范围外计划/记录的修改、删除与登记一律 404
// （与列表可见性一致，不泄露存在性）。
func TestIntegrationReceivableOutsideScope404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := setupReceivableScopeEnv(t, env, admin, "rv_404", 5, nil)
	token := env.Login(t, observer, "123456")

	// 取一条观察者范围外的计划与记录。
	list, _ := pageReceivablePlans(t, env, admin, "&contractId=0")
	var otherPlanID int64
	for _, row := range list {
		if row["contractName"] == "回款范围合同_of_receivable_owner_rnd" {
			otherPlanID = int64(row["id"].(float64))
			break
		}
	}
	if otherPlanID == 0 {
		t.Fatal("应能找到他人计划")
	}
	records, _ := pageReceivableRecords(t, env, admin, fmt.Sprintf("&planId=%d", otherPlanID))
	if len(records) != 1 {
		t.Fatalf("他人计划应有 1 条记录，实际 %d", len(records))
	}
	otherRecordID := int64(records[0]["id"].(float64))

	status, _ := env.Request(t, http.MethodPut, "/crm/receivable/plan", token, map[string]any{
		"id": otherPlanID, "periodNo": 1, "amount": "1.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外修改计划：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/receivable/plan/%d", otherPlanID), token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除计划：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/crm/receivable/record", token, map[string]any{
		"planId": otherPlanID, "amount": "1.00", "receiptDate": time.Now().Format("2006-01-02"),
	})
	if status != http.StatusNotFound {
		t.Fatalf("范围外登记回款：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/receivable/record/%d", otherRecordID), token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("范围外删除回款记录：HTTP %d，期望 404", status)
	}
	if _, total := pageReceivableRecords(t, env, token, fmt.Sprintf("&planId=%d", otherPlanID)); total != 0 {
		t.Fatalf("范围外记录列表 total=%d，期望 0", total)
	}
}

// TestIntegrationReceivableTenantIsolation 租户隔离：租户 2 看不到租户 1 的计划/记录，
// 跨租户修改/删除/登记 404，跨租户合同下建计划 404。
func TestIntegrationReceivableTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "回款隔离宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "回款隔离合同", map[string]any{
		"customerId": customerID, "amount": "1000.00",
	})
	contractID := int64(contract["id"].(float64))
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "1000.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	planID := int64(plan["id"].(float64))
	record := createReceivableRecord(t, env, admin, map[string]any{
		"planId": planID, "amount": "100.00", "receiptDate": time.Now().Format("2006-01-02"),
	})
	recordID := int64(record["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "回款隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	if _, total := pageReceivablePlans(t, env, token2, ""); total != 0 {
		t.Fatalf("租户 2 计划 total=%d，期望 0（租户隔离）", total)
	}
	if _, total := pageReceivableRecords(t, env, token2, ""); total != 0 {
		t.Fatalf("租户 2 记录 total=%d，期望 0（租户隔离）", total)
	}
	status, _ := env.Request(t, http.MethodPut, "/crm/receivable/plan", token2, map[string]any{
		"id": planID, "periodNo": 1, "amount": "1.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 计划：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/crm/receivable/plan", token2, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "1.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 在租户 1 合同下建计划：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/crm/receivable/record", token2, map[string]any{
		"planId": planID, "amount": "1.00", "receiptDate": time.Now().Format("2006-01-02"),
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 在租户 1 计划下登记回款：HTTP %d，期望 404", status)
	}
	status, _ = env.Request(t, http.MethodDelete, fmt.Sprintf("/crm/receivable/record/%d", recordID), token2, nil)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 回款记录：HTTP %d，期望 404", status)
	}

	if _, total := pageReceivablePlans(t, env, admin, ""); total != 1 {
		t.Fatalf("租户 1 计划 total=%d，期望 1（不受影响）", total)
	}
}

// TestIntegrationReceivablePermissionDenied 无 crm:receivable:* 权限码一律 403：
// 观察者只绑客户页面菜单（401），未绑回款按钮（4120–4124）。
func TestIntegrationReceivablePermissionDenied(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "回款权限宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "回款权限合同", map[string]any{
		"customerId": customerID, "amount": "1000.00",
	})
	contractID := int64(contract["id"].(float64))
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "1000.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	planID := int64(plan["id"].(float64))

	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅客户查看角色四", "code": "rv_no_perm", "dataScope": 1, "menuIds": []int64{401},
	})
	createUserByAPI(t, env, admin, "rv_no_perm_user", 102, []int64{roleID})
	noPerm := env.Login(t, "rv_no_perm_user", "123456")

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "计划分页", method: http.MethodGet, path: "/crm/receivable/plan/page?pageNo=1&pageSize=10"},
		{name: "计划新建", method: http.MethodPost, path: "/crm/receivable/plan", body: map[string]any{
			"contractId": contractID, "periodNo": 1, "amount": "1.00",
			"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
		}},
		{name: "计划修改", method: http.MethodPut, path: "/crm/receivable/plan", body: map[string]any{
			"id": planID, "periodNo": 1, "amount": "1.00",
			"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
		}},
		{name: "计划删除", method: http.MethodDelete, path: fmt.Sprintf("/crm/receivable/plan/%d", planID)},
		{name: "记录分页", method: http.MethodGet, path: "/crm/receivable/record/page?pageNo=1&pageSize=10"},
		{name: "记录登记", method: http.MethodPost, path: "/crm/receivable/record", body: map[string]any{
			"planId": planID, "amount": "1.00", "receiptDate": time.Now().Format("2006-01-02"),
		}},
		{name: "记录删除", method: http.MethodDelete, path: "/crm/receivable/record/1"},
	}
	for _, tc := range requests {
		status, api := env.Request(t, tc.method, tc.path, noPerm, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("%s 无权限：HTTP %d code %d msg %q，期望 403", tc.name, status, api.Code, api.Msg)
		}
	}

	if status, api := env.Request(t, http.MethodGet, "/crm/receivable/plan/page?pageNo=1&pageSize=10", admin, nil); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("admin 查询回款计划分页：HTTP %d code %d", status, api.Code)
	}
}

// TestIntegrationReceivablePlanAmountNotBelowReceived 金额不变量在更新路径同样成立：
// 计划金额不能改到低于该计划已回款金额（否则 SUM(记录) ≤ 计划金额 被破坏）；
// 改到恰好等于已回款金额放行，派生状态自然变为已回款。
func TestIntegrationReceivablePlanAmountNotBelowReceived(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	customer := createCustomer(t, env, admin, "计划改额宿主客户", nil)
	customerID := int64(customer["id"].(float64))
	contract := createContract(t, env, admin, "计划改额合同", map[string]any{
		"customerId": customerID, "amount": "1000.00",
	})
	contractID := int64(contract["id"].(float64))
	plan := createReceivablePlan(t, env, admin, map[string]any{
		"contractId": contractID, "periodNo": 1, "amount": "1000.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	planID := int64(plan["id"].(float64))
	createReceivableRecord(t, env, admin, map[string]any{
		"planId": planID, "amount": "400.00", "receiptDate": time.Now().Format("2006-01-02"),
	})

	status, api := env.Request(t, http.MethodPut, "/crm/receivable/plan", admin, map[string]any{
		"id": planID, "periodNo": 1, "amount": "399.99",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	})
	if status != http.StatusBadRequest || !strings.Contains(api.Msg, "已回款金额") {
		t.Fatalf("计划金额低于已回款：HTTP %d code %d msg %q，期望 400 且含「已回款金额」", status, api.Code, api.Msg)
	}

	if status, api = env.Request(t, http.MethodPut, "/crm/receivable/plan", admin, map[string]any{
		"id": planID, "periodNo": 1, "amount": "400.00",
		"dueDate": time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	}); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("计划金额等于已回款应放行：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	list, _ := pageReceivablePlans(t, env, admin, fmt.Sprintf("&contractId=%d", contractID))
	if list[0]["amount"] != "400.00" || list[0]["status"] != float64(3) {
		t.Fatalf("改额后计划应为 400.00/已回款，实际 %v/%v", list[0]["amount"], list[0]["status"])
	}
}
