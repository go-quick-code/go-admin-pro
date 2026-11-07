//go:build integration

// 财务域 HTTP 集成测试（issue #94，docs/erp-mvp-spec.md §4.7 + §8.2 财务行）：
// 收款/付款明细只挂已审核单据（未审核 409、不存在 400）、核销上限（审核 409）、
// 审核回写与反审核重算、反审核禁令（出库/退货/入库/退货单存在已审核收付款
// 409）、账户默认唯一互斥、余额列=已审核收付款净额、账户被引用禁删、编号
// SK/FK、租户隔离、数据范围、普通角色 403、已审核不可改删、导出列。
package erp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// SK/FK 编号格式（spec §8.2：^(SK|FK)\d{8}\d{4,}$）。
var (
	financeReceiptNoRe = regexp.MustCompile(`^SK\d{8}\d{4,}$`)
	financePaymentNoRe = regexp.MustCompile(`^FK\d{8}\d{4,}$`)
)

// ---------- 用例级助手 ----------

// financeItemJSON 是核销明细 payload 的简写构造。
func financeItemJSON(bizType string, bizID int64, price string) map[string]any {
	return map[string]any{"bizType": bizType, "bizId": bizID, "price": price}
}

// createFinanceAccountDoc 创建结算账户并断言成功。
func createFinanceAccountDoc(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/erp/finance-account", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建结算账户 %s：HTTP %d code %d msg %q", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建结算账户响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageFinanceAccountRows 结算账户分页（返回 list）。
func pageFinanceAccountRows(t *testing.T, env *testutil.IntegrationEnv, token, query string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/finance-account/page?pageNo=1&pageSize=200"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询结算账户分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析结算账户分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List
}

// financeAccountBalance 查询账户详情并返回余额（十进制字符串）。
func financeAccountBalance(t *testing.T, env *testutil.IntegrationEnv, token string, accountID int64) string {
	t.Helper()
	status, api := getJSON(t, env, token, fmt.Sprintf("/erp/finance-account/%d", accountID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询账户 %d 详情：HTTP %d code %d msg %q", accountID, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析账户详情: %v（data=%s）", err, string(api.Data))
	}
	return fmt.Sprint(row["balance"])
}

// createFinanceDoc 创建收款/付款单并断言成功（docType: finance-receipt /
// finance-payment），返回主表 JSON。
func createFinanceDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := createFinanceDocRaw(t, env, token, docType, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建 %s：HTTP %d code %d msg %q（payload=%v）", docType, status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建 %s 响应: %v（data=%s）", docType, err, string(api.Data))
	}
	return row
}

// createFinanceDocRaw 创建收款/付款单但不 Fatal（负路径断言用）。
func createFinanceDocRaw(t *testing.T, env *testutil.IntegrationEnv, token, docType string, payload map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/erp/"+docType, token, payload)
}

// getFinanceDetail 查询收款/付款单详情。
func getFinanceDetail(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64) map[string]any {
	t.Helper()
	status, api := getJSON(t, env, token, fmt.Sprintf("/erp/%s/%d", docType, docID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 %s %d 详情：HTTP %d code %d msg %q", docType, docID, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析 %s 详情: %v（data=%s）", docType, err, string(api.Data))
	}
	return row
}

// approveFinanceDoc 审核/反审核收款/付款单。
func approveFinanceDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64, status int8) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, fmt.Sprintf("/erp/%s/update-status", docType), token, map[string]any{
		"id": docID, "status": status,
	})
}

// mustApproveFinance 审核/反审核并断言成功。
func mustApproveFinance(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64, status int8) {
	t.Helper()
	if httpStatus, api := approveFinanceDoc(t, env, token, docType, docID, status); httpStatus != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s %d 状态改到 %d：HTTP %d code %d msg %q", docType, docID, status, httpStatus, api.Code, api.Msg)
	}
}

// pageFinanceDocs 收款/付款单分页（返回 list）。
func pageFinanceDocs(t *testing.T, env *testutil.IntegrationEnv, token, docType, query string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/erp/%s/page?pageNo=1&pageSize=50", docType)+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 %s 分页%s：HTTP %d code %d msg %q", docType, query, status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析 %s 分页: %v（data=%s）", docType, err, string(api.Data))
	}
	return page.List
}

// defaultAccountIDs 返回列表中 defaultStatus=1 的账户名（默认互斥断言）。
func defaultAccountIDs(rows []map[string]any) []string {
	var defaults []string
	for _, row := range rows {
		if fmt.Sprint(row["defaultStatus"]) == "1" {
			defaults = append(defaults, fmt.Sprint(row["name"]))
		}
	}
	return defaults
}

// approveFinanceOnce 在独立 goroutine 中发一次审核请求（并发核销用例）：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），结果带回主 goroutine 断言。
func approveFinanceOnce(env *testutil.IntegrationEnv, token, docType string, id int64, status int8) (int, testutil.APIResponse, error) {
	raw, err := json.Marshal(map[string]any{"id": id, "status": status})
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPut, env.APIPrefixURL("/erp/"+docType+"/update-status"), bytes.NewReader(raw))
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
		return resp.StatusCode, testutil.APIResponse{}, err
	}
	return resp.StatusCode, api, nil
}

// TestIntegrationFinanceConcurrentLimit 并发核销上限：两张各 6.00 的收款单同时
// 审核对总额 10.00 的出库，恰一成一 409（目标单据行锁串行化 + READ COMMITTED
// 当前读），回写金额等于成功那笔（spec §4.7 上限不变量）。
func TestIntegrationFinanceConcurrentLimit(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "并发核销")
	padSaleStock(t, env, token, warehouseID, productID, "1")

	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{saleItemJSON(productID, "1", "10.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "1", "10.00")},
	})
	outID := int64(out["id"].(float64))
	mustApproveSale(t, env, token, "sale-out", outID, 20)
	account := createFinanceAccountDoc(t, env, token, "并发核销账户", map[string]any{"defaultStatus": 1})
	accountID := int64(account["id"].(float64))

	receiptIDs := make([]int64, 0, 2)
	for i := 0; i < 2; i++ {
		receipt := createFinanceDoc(t, env, token, "finance-receipt", map[string]any{
			"customerId": customerID, "accountId": accountID,
			"receiptTime": env.Date() + " 12:00:00",
			"items":       []map[string]any{financeItemJSON("sale_out", outID, "6.00")},
		})
		receiptIDs = append(receiptIDs, int64(receipt["id"].(float64)))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([][2]int, 0, 2) // [status, code]
	for _, id := range receiptIDs {
		wg.Add(1)
		go func(receiptID int64) {
			defer wg.Done()
			status, api, err := approveFinanceOnce(env, token, "finance-receipt", receiptID, 20)
			if err != nil {
				return
			}
			mu.Lock()
			results = append(results, [2]int{status, api.Code})
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	if len(results) != 2 {
		t.Fatalf("并发审核应有两个结果，实际 %d", len(results))
	}
	okCount, conflictCount := 0, 0
	for _, r := range results {
		if r[0] == http.StatusOK && r[1] == 0 {
			okCount++
		} else if r[0] == http.StatusConflict {
			conflictCount++
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("并发核销应恰一成一 409，实际 成功 %d / 冲突 %d（results=%v）", okCount, conflictCount, results)
	}
	// 回写守恒：已审核核销合计最多 6.00（另一笔被上限拒绝）。
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-out", outID)["receiptPrice"]); got != "6.00" {
		t.Fatalf("并发核销后 receiptPrice = %s，期望 6.00", got)
	}
}

// ---------- 收款单主链路（spec §8.2 财务行） ----------

// TestIntegrationFinanceReceiptLifecycle 收款单：挂未审核出库 409/挂不存在 400/
// 客户不一致 400/审核上限 409/审核回写 receiptPrice/反审核重算释放/出库有已审核
// 收款时反审核 409/销售退货退款回写/已审核不可改删/账户引用禁删/导出列。
func TestIntegrationFinanceReceiptLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "收款链路")
	padSaleStock(t, env, token, warehouseID, productID, "10")

	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "10", "3.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "10", "3.00")},
	})
	outID := int64(out["id"].(float64))
	account := createFinanceAccountDoc(t, env, token, "收款账户", map[string]any{"defaultStatus": 1})
	accountID := int64(account["id"].(float64))
	otherCustomer := createCustomer(t, env, token, "收款链路他客户", nil)

	baseReceipt := func(customerID int64, items []map[string]any) map[string]any {
		return map[string]any{
			"customerId": customerID, "accountId": accountID, "receiptUserId": 1,
			"receiptTime": env.Date() + " 12:00:00", "items": items,
		}
	}
	// 挂未审核出库：409（明细只能挂已审核单据，spec §4.7）。
	if status, _ := createFinanceDocRaw(t, env, token, "finance-receipt", baseReceipt(customerID,
		[]map[string]any{financeItemJSON("sale_out", outID, "1.00")})); status != http.StatusConflict {
		t.Fatalf("收款挂未审核出库：HTTP %d，期望 409", status)
	}
	// 挂不存在：400。
	if status, _ := createFinanceDocRaw(t, env, token, "finance-receipt", baseReceipt(customerID,
		[]map[string]any{financeItemJSON("sale_out", 99999999, "1.00")})); status != http.StatusBadRequest {
		t.Fatalf("收款挂不存在出库：HTTP %d，期望 400", status)
	}
	// 业务类型非法（付款方向的 purchase_in）：400。
	if status, _ := createFinanceDocRaw(t, env, token, "finance-receipt", baseReceipt(customerID,
		[]map[string]any{financeItemJSON("purchase_in", outID, "1.00")})); status != http.StatusBadRequest {
		t.Fatalf("收款挂采购入库：HTTP %d，期望 400", status)
	}
	mustApproveSale(t, env, token, "sale-out", outID, 20)
	// 客户不一致：400（收款单客户必须与被核销出库客户一致）。
	if status, _ := createFinanceDocRaw(t, env, token, "finance-receipt", baseReceipt(
		int64(otherCustomer["id"].(float64)),
		[]map[string]any{financeItemJSON("sale_out", outID, "1.00")})); status != http.StatusBadRequest {
		t.Fatalf("收款客户与出库客户不一致：HTTP %d，期望 400", status)
	}

	// 收款单 #1（12.00）：编号 SK、未审核不回写。
	receipt1 := createFinanceDoc(t, env, token, "finance-receipt", baseReceipt(customerID,
		[]map[string]any{financeItemJSON("sale_out", outID, "12.00")}))
	receipt1ID := int64(receipt1["id"].(float64))
	if !financeReceiptNoRe.MatchString(fmt.Sprint(receipt1["no"])) {
		t.Fatalf("收款单号 %v 不符 ^SK\\d{8}\\d{4,}$", receipt1["no"])
	}
	if detail := getFinanceDetail(t, env, token, "finance-receipt", receipt1ID); fmt.Sprint(detail["totalPrice"]) != "12.00" {
		t.Fatalf("收款单核销合计 = %v，期望 12.00", detail["totalPrice"])
	}
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-out", outID)["receiptPrice"]); got != "0.00" {
		t.Fatalf("未审核收款不应回写 receiptPrice：实际 %s，期望 0.00", got)
	}
	if got := financeAccountBalance(t, env, token, accountID); got != "0.00" {
		t.Fatalf("未审核收款不应计入余额：实际 %s，期望 0.00", got)
	}

	// 审核 #1：回写 receiptPrice=12.00、余额=12.00。
	mustApproveFinance(t, env, token, "finance-receipt", receipt1ID, 20)
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-out", outID)["receiptPrice"]); got != "12.00" {
		t.Fatalf("审核收款后 receiptPrice = %s，期望 12.00", got)
	}
	if got := financeAccountBalance(t, env, token, accountID); got != "12.00" {
		t.Fatalf("余额列 = %s，期望 12.00（已审核收款 12 − 付款 0）", got)
	}
	// 已审核：不可改、不可删（409）。
	updatePayload := baseReceipt(customerID, []map[string]any{financeItemJSON("sale_out", outID, "12.00")})
	updatePayload["id"] = receipt1ID
	if status, _ := env.Request(t, http.MethodPut, "/erp/finance-receipt", token, updatePayload); status != http.StatusConflict {
		t.Fatalf("修改已审核收款单：HTTP %d，期望 409", status)
	}
	if status, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/finance-receipt/%d", receipt1ID)); status != http.StatusConflict {
		t.Fatalf("删除已审核收款单：HTTP %d，期望 409", status)
	}
	// 账户被收款单引用：禁删 409。
	if status, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/finance-account/%d", accountID)); status != http.StatusConflict {
		t.Fatalf("删除被收款单引用的账户：HTTP %d，期望 409", status)
	}

	// #2（18.00）审核 → 合计 30.00（等于上限放行）。
	receipt2 := createFinanceDoc(t, env, token, "finance-receipt", baseReceipt(customerID,
		[]map[string]any{financeItemJSON("sale_out", outID, "18.00")}))
	receipt2ID := int64(receipt2["id"].(float64))
	mustApproveFinance(t, env, token, "finance-receipt", receipt2ID, 20)
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-out", outID)["receiptPrice"]); got != "30.00" {
		t.Fatalf("两笔收款后 receiptPrice = %s，期望 30.00", got)
	}
	// #3（0.01）审核超总额：409（30.00 + 0.01 > 30.00）。
	receipt3 := createFinanceDoc(t, env, token, "finance-receipt", baseReceipt(customerID,
		[]map[string]any{financeItemJSON("sale_out", outID, "0.01")}))
	receipt3ID := int64(receipt3["id"].(float64))
	if status, _ := approveFinanceDoc(t, env, token, "finance-receipt", receipt3ID, 20); status != http.StatusConflict {
		t.Fatalf("核销超总额：HTTP %d，期望 409", status)
	}

	// 反审核 #2：重算 receiptPrice=12.00，额度释放后 #3 可审核（12.01 ≤ 30）。
	mustApproveFinance(t, env, token, "finance-receipt", receipt2ID, 10)
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-out", outID)["receiptPrice"]); got != "12.00" {
		t.Fatalf("反审核收款后 receiptPrice = %s，期望 12.00（重算）", got)
	}
	if got := financeAccountBalance(t, env, token, accountID); got != "12.00" {
		t.Fatalf("反审核后余额 = %s，期望 12.00", got)
	}
	mustApproveFinance(t, env, token, "finance-receipt", receipt3ID, 20)
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-out", outID)["receiptPrice"]); got != "12.01" {
		t.Fatalf("额度释放后 receiptPrice = %s，期望 12.01", got)
	}
	mustApproveFinance(t, env, token, "finance-receipt", receipt3ID, 10)

	// 出库存在已审核收款（#1）：反审核 409（spec §4.1/§4.7）。
	if status, _ := approveSaleDoc(t, env, token, "sale-out", outID, 10); status != http.StatusConflict {
		t.Fatalf("出库存在已审核收款时反审核：HTTP %d，期望 409", status)
	}

	// 销售退货退款核销：审核退货 → 收款 6.00 → refundPrice=6.00 → 退货反审核 409。
	returnDoc := createSaleReturnDoc(t, env, token, map[string]any{
		"customerId": customerID, "outId": outID,
		"returnTime": env.Date() + " 13:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "2", "3.00")},
	})
	returnID := int64(returnDoc["id"].(float64))
	mustApproveSale(t, env, token, "sale-return", returnID, 20)
	refundReceipt := createFinanceDoc(t, env, token, "finance-receipt", baseReceipt(customerID,
		[]map[string]any{financeItemJSON("sale_return", returnID, "6.00")}))
	refundReceiptID := int64(refundReceipt["id"].(float64))
	mustApproveFinance(t, env, token, "finance-receipt", refundReceiptID, 20)
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-return", returnID)["refundPrice"]); got != "6.00" {
		t.Fatalf("销售退货 refundPrice = %s，期望 6.00", got)
	}
	if status, _ := approveSaleDoc(t, env, token, "sale-return", returnID, 10); status != http.StatusConflict {
		t.Fatalf("退货存在已审核收款时反审核：HTTP %d，期望 409", status)
	}
	// 反审核退款收款 → refundPrice 归零 → 退货可反审核。
	mustApproveFinance(t, env, token, "finance-receipt", refundReceiptID, 10)
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-return", returnID)["refundPrice"]); got != "0.00" {
		t.Fatalf("反审核退款收款后 refundPrice = %s，期望 0.00", got)
	}
	mustApproveSale(t, env, token, "sale-return", returnID, 10)

	// 反审核 #1 → receiptPrice 归零 → 出库可反审核（额度释放）。
	mustApproveFinance(t, env, token, "finance-receipt", receipt1ID, 10)
	if got := fmt.Sprint(getSaleDetail(t, env, token, "sale-out", outID)["receiptPrice"]); got != "0.00" {
		t.Fatalf("反审核全部收款后 receiptPrice = %s，期望 0.00", got)
	}
	mustApproveSale(t, env, token, "sale-out", outID, 10)

	// 导出：列标签与内容（核销金额合计）。
	header, rows := getExportRows(t, env, token, "/erp/finance-receipt/export-excel")
	for _, col := range []string{"收款单号", "客户", "结算账户", "收款时间", "状态", "核销金额合计", "创建时间"} {
		expectColumn(t, "收款单导出", header, col)
	}
	if len(rows) < 3 {
		t.Fatalf("收款单导出应至少 3 行，实际 %d", len(rows))
	}
}

// ---------- 付款单主链路（收款单镜像） ----------

// TestIntegrationFinancePaymentLifecycle 付款单：挂未审核入库 409/审核上限 409/
// 审核回写 paymentPrice/账户余额为负净额/入库有已审核付款时反审核 409/采购退货
// 退款回写/反审核释放额度后可反审核/业务类型非法 400。
func TestIntegrationFinancePaymentLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "付款链路")

	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "10", "2.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
	inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "10", "2.00")},
	})
	inID := int64(inDoc["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)
	account := createFinanceAccountDoc(t, env, token, "付款账户", map[string]any{"defaultStatus": 1})
	accountID := int64(account["id"].(float64))

	basePayment := func(items []map[string]any) map[string]any {
		return map[string]any{
			"supplierId": supplierID, "accountId": accountID, "paymentUserId": 1,
			"paymentTime": env.Date() + " 12:00:00", "items": items,
		}
	}

	// 未审核入库：另建订单 + 未审核入库，付款挂它 409。
	order2 := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:10:00",
		"items": []map[string]any{purchaseItemJSON(productID, "5", "2.00")},
	})
	order2ID := int64(order2["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", order2ID)
	in2 := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": order2ID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:10:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "5", "2.00")},
	})
	in2ID := int64(in2["id"].(float64))
	if status, _ := createFinanceDocRaw(t, env, token, "finance-payment", basePayment(
		[]map[string]any{financeItemJSON("purchase_in", in2ID, "1.00")})); status != http.StatusConflict {
		t.Fatalf("付款挂未审核入库：HTTP %d，期望 409", status)
	}
	// 业务类型非法（销售出库）：400。
	if status, _ := createFinanceDocRaw(t, env, token, "finance-payment", basePayment(
		[]map[string]any{financeItemJSON("sale_out", inID, "1.00")})); status != http.StatusBadRequest {
		t.Fatalf("付款挂销售出库：HTTP %d，期望 400", status)
	}
	mustApprovePurchaseDoc(t, env, token, "purchase-in", in2ID)

	// 付款 #1（5.00）审核 → paymentPrice=5.00，余额 = −5.00（收款 0 − 付款 5）。
	payment1 := createFinanceDoc(t, env, token, "finance-payment", basePayment(
		[]map[string]any{financeItemJSON("purchase_in", inID, "5.00")}))
	payment1ID := int64(payment1["id"].(float64))
	if !financePaymentNoRe.MatchString(fmt.Sprint(payment1["no"])) {
		t.Fatalf("付款单号 %v 不符 ^FK\\d{8}\\d{4,}$", payment1["no"])
	}
	mustApproveFinance(t, env, token, "finance-payment", payment1ID, 20)
	if got := fmt.Sprint(getPurchaseDetail(t, env, token, "purchase-in", inID)["paymentPrice"]); got != "5.00" {
		t.Fatalf("审核付款后 paymentPrice = %s，期望 5.00", got)
	}
	if got := financeAccountBalance(t, env, token, accountID); got != "-5.00" {
		t.Fatalf("余额列 = %s，期望 -5.00（已审核收款 0 − 付款 5）", got)
	}
	// #2 超总额（5.00 + 15.01 > 20.00）：409。
	payment2 := createFinanceDoc(t, env, token, "finance-payment", basePayment(
		[]map[string]any{financeItemJSON("purchase_in", inID, "15.01")}))
	payment2ID := int64(payment2["id"].(float64))
	if status, _ := approveFinanceDoc(t, env, token, "finance-payment", payment2ID, 20); status != http.StatusConflict {
		t.Fatalf("付款核销超总额：HTTP %d，期望 409", status)
	}
	// 入库存在已审核付款：反审核 409。
	if status, _ := approvePurchaseDoc(t, env, token, "purchase-in", inID, 10); status != http.StatusConflict {
		t.Fatalf("入库存在已审核付款时反审核：HTTP %d，期望 409", status)
	}

	// 采购退货退款：退货 2 @ 2.00 审核 → 付款 4.00 → refundPrice=4.00 →
	// 退货反审核 409 → 反审核付款 → refundPrice=0 → 退货可反审核。
	returnDoc := createPurchaseDoc(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 13:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "2", "2.00")},
	})
	returnID := int64(returnDoc["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-return", returnID)
	refundPayment := createFinanceDoc(t, env, token, "finance-payment", basePayment(
		[]map[string]any{financeItemJSON("purchase_return", returnID, "4.00")}))
	refundPaymentID := int64(refundPayment["id"].(float64))
	mustApproveFinance(t, env, token, "finance-payment", refundPaymentID, 20)
	if got := fmt.Sprint(getPurchaseDetail(t, env, token, "purchase-return", returnID)["refundPrice"]); got != "4.00" {
		t.Fatalf("采购退货 refundPrice = %s，期望 4.00", got)
	}
	if status, _ := approvePurchaseDoc(t, env, token, "purchase-return", returnID, 10); status != http.StatusConflict {
		t.Fatalf("采购退货存在已审核付款时反审核：HTTP %d，期望 409", status)
	}
	mustApproveFinance(t, env, token, "finance-payment", refundPaymentID, 10)
	if got := fmt.Sprint(getPurchaseDetail(t, env, token, "purchase-return", returnID)["refundPrice"]); got != "0.00" {
		t.Fatalf("反审核退款付款后 refundPrice = %s，期望 0.00", got)
	}
	if status, _ := approvePurchaseDoc(t, env, token, "purchase-return", returnID, 10); status != http.StatusOK {
		t.Fatalf("释放额度后反审核采购退货：HTTP %d，期望 200", status)
	}

	// 反审核付款 #1 → paymentPrice 归零 → 入库可反审核。
	mustApproveFinance(t, env, token, "finance-payment", payment1ID, 10)
	if got := fmt.Sprint(getPurchaseDetail(t, env, token, "purchase-in", inID)["paymentPrice"]); got != "0.00" {
		t.Fatalf("反审核付款后 paymentPrice = %s，期望 0.00", got)
	}
	if status, _ := approvePurchaseDoc(t, env, token, "purchase-in", inID, 10); status != http.StatusOK {
		t.Fatalf("释放额度后反审核入库：HTTP %d，期望 200", status)
	}
}

// ---------- 结算账户规则 ----------

// TestIntegrationFinanceAccountRules 默认账户唯一互斥（置默认取消原默认）、
// 状态/默认枚举校验、删除默认后新默认可设、被引用禁删（见收款链路）、余额实时列。
func TestIntegrationFinanceAccountRules(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	a := createFinanceAccountDoc(t, env, token, "账户A", map[string]any{"defaultStatus": 1, "status": 0, "no": "NO-A"})
	aID := int64(a["id"].(float64))
	b := createFinanceAccountDoc(t, env, token, "账户B", nil)
	bID := int64(b["id"].(float64))
	c := createFinanceAccountDoc(t, env, token, "账户C", map[string]any{"defaultStatus": 1})
	cID := int64(c["id"].(float64))

	// 创建 C（默认）时取消 A：默认唯一。
	rows := pageFinanceAccountRows(t, env, token, "")
	if defaults := defaultAccountIDs(rows); len(defaults) != 1 || defaults[0] != "账户C" {
		t.Fatalf("默认账户应唯一为 账户C，实际 %v", defaults)
	}
	// 置 A 为默认：C 被取消。
	if status, api := env.Request(t, http.MethodPut, "/erp/finance-account", token, map[string]any{
		"id": aID, "name": "账户A", "no": "NO-A", "status": 0, "sort": 2, "defaultStatus": 1,
	}); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("置默认账户 A：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	rows = pageFinanceAccountRows(t, env, token, "")
	if defaults := defaultAccountIDs(rows); len(defaults) != 1 || defaults[0] != "账户A" {
		t.Fatalf("置默认后应唯一为 账户A，实际 %v", defaults)
	}
	// 余额：无核销 = 0.00。
	if got := financeAccountBalance(t, env, token, aID); got != "0.00" {
		t.Fatalf("账户无核销余额 = %s，期望 0.00", got)
	}
	// 枚举校验：status=9 / defaultStatus=2 均 400。
	if status, _ := env.Request(t, http.MethodPut, "/erp/finance-account", token, map[string]any{
		"id": aID, "name": "账户A", "status": 9,
	}); status != http.StatusBadRequest {
		t.Fatalf("非法账户状态：HTTP %d，期望 400", status)
	}
	if status, _ := env.Request(t, http.MethodPut, "/erp/finance-account", token, map[string]any{
		"id": aID, "name": "账户A", "defaultStatus": 2,
	}); status != http.StatusBadRequest {
		t.Fatalf("非法默认标记：HTTP %d，期望 400", status)
	}
	// 删除普通账户 B：200；再查 404。
	if status, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/finance-account/%d", bID)); status != http.StatusOK {
		t.Fatalf("删除未引用账户 B：HTTP %d，期望 200", status)
	}
	if status, _ := getJSON(t, env, token, fmt.Sprintf("/erp/finance-account/%d", bID)); status != http.StatusNotFound {
		t.Fatalf("删除后查询账户 B：HTTP %d，期望 404", status)
	}
	// 删除默认账户 C 后新默认可设（生成列唯一键不残留）。
	if status, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/finance-account/%d", cID)); status != http.StatusOK {
		t.Fatalf("删除默认账户 C：HTTP %d，期望 200", status)
	}
	d := createFinanceAccountDoc(t, env, token, "账户D", map[string]any{"defaultStatus": 1})
	_ = d
	rows = pageFinanceAccountRows(t, env, token, "")
	defaults := defaultAccountIDs(rows)
	if len(defaults) != 1 || defaults[0] != "账户D" {
		t.Fatalf("删除默认账户后新默认应为 账户D，实际 %v", defaults)
	}
}

// TestIntegrationFinanceTenantIsolation 租户隔离：账户/收款单列表与详情跨租户
// 一律不可见（404 / 空列表），默认账户互斥按租户独立。
func TestIntegrationFinanceTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	accountA := createFinanceAccountDoc(t, env, admin, "租户1账户", map[string]any{"defaultStatus": 1})
	accountAID := int64(accountA["id"].(float64))
	// 租户 1 造一张已审核销售出库 + 收款单，验证跨租户详情/修改/删除 404。
	warehouseID, productID, customerID := setupSaleEnv(t, env, admin, "租户隔离")
	padSaleStock(t, env, admin, warehouseID, productID, "1")
	order := createSaleOrderDoc(t, env, admin, map[string]any{
		"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, admin, "sale-order", orderID, 20)
	out := createSaleOutDoc(t, env, admin, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})
	outID := int64(out["id"].(float64))
	mustApproveSale(t, env, admin, "sale-out", outID, 20)
	receiptA := createFinanceDoc(t, env, admin, "finance-receipt", map[string]any{
		"customerId": customerID, "accountId": accountAID,
		"receiptTime": env.Date() + " 12:00:00",
		"items":       []map[string]any{financeItemJSON("sale_out", outID, "1.00")},
	})
	receiptAID := int64(receiptA["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "财务隔离租户")
	admin2 := loginTenant(t, env, "admin", "123456", tenant2)
	accountB := createFinanceAccountDoc(t, env, admin2, "租户2账户", map[string]any{"defaultStatus": 1})
	if int64(accountB["id"].(float64)) == accountAID {
		t.Fatalf("跨租户账户 ID 巧合相等，断言不可靠（%d）", accountAID)
	}
	// 租户 2 列表只含自己的账户。
	rows := pageFinanceAccountRows(t, env, admin2, "")
	if len(rows) != 1 || fmt.Sprint(rows[0]["name"]) != "租户2账户" {
		t.Fatalf("租户 2 账户列表应仅含本租户账户，实际 %v", rows)
	}
	// 跨租户账户/收款单详情、修改、删除一律 404（不泄露存在性）。
	if status, _ := getJSON(t, env, admin2, fmt.Sprintf("/erp/finance-account/%d", accountAID)); status != http.StatusNotFound {
		t.Fatalf("租户 2 查询租户 1 账户：HTTP %d，期望 404", status)
	}
	if status, _ := deleteByID(t, env, admin2, fmt.Sprintf("/erp/finance-account/%d", accountAID)); status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 账户：HTTP %d，期望 404", status)
	}
	if status, _ := getJSON(t, env, admin2, fmt.Sprintf("/erp/finance-receipt/%d", receiptAID)); status != http.StatusNotFound {
		t.Fatalf("租户 2 查询租户 1 收款单：HTTP %d，期望 404", status)
	}
	crossUpdate := map[string]any{
		"id": receiptAID, "customerId": customerID, "accountId": accountAID,
		"receiptTime": env.Date() + " 12:00:00",
		"items":       []map[string]any{financeItemJSON("sale_out", outID, "1.00")},
	}
	if status, _ := env.Request(t, http.MethodPut, "/erp/finance-receipt", admin2, crossUpdate); status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 收款单：HTTP %d，期望 404", status)
	}
	if status, _ := deleteByID(t, env, admin2, fmt.Sprintf("/erp/finance-receipt/%d", receiptAID)); status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 收款单：HTTP %d，期望 404", status)
	}
	// 租户 2 收付款列表为空（无数据外泄）。
	if rows := pageFinanceDocs(t, env, admin2, "finance-receipt", ""); len(rows) != 0 {
		t.Fatalf("租户 2 收款单列表应为空，实际 %d 条", len(rows))
	}
	if rows := pageFinanceDocs(t, env, admin2, "finance-payment", ""); len(rows) != 0 {
		t.Fatalf("租户 2 付款单列表应为空，实际 %d 条", len(rows))
	}
}

// TestIntegrationFinanceCommonRoleNoPermission 内置普通角色（role id=2）默认无
// 财务页权限（spec §7：普通角色不授 ERP；spec §8.2 普通角色 403）。
func TestIntegrationFinanceCommonRoleNoPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	createFinanceAccountDoc(t, env, admin, "普通角色禁止账户", map[string]any{"defaultStatus": 1})

	common := env.Login(t, "demo", "123456")
	for _, path := range []string{
		"/erp/finance-account/page?pageNo=1&pageSize=10",
		"/erp/finance-receipt/page?pageNo=1&pageSize=10",
		"/erp/finance-payment/page?pageNo=1&pageSize=10",
	} {
		if status, _ := env.Request(t, http.MethodGet, path, common, nil); status != http.StatusForbidden {
			t.Fatalf("普通角色访问 %s：HTTP %d，期望 403", path, status)
		}
	}
}

// TestIntegrationFinanceDataScope 财务单据与账户列表按 owner 数据范围过滤
// （spec §4.10，owner = 创建人）：仅本人只见自己的；范围外详情/修改/删除 404；
// 全部数据可见全部。
func TestIntegrationFinanceDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	rndDept := int64(111)
	mktDept := int64(112)

	// 共享主数据与被核销单据（admin 建；引用校验是租户级存在性）。
	warehouseID, productID, customerID := setupSaleEnv(t, env, admin, "财务范围")
	padSaleStock(t, env, admin, warehouseID, productID, "5")
	order := createSaleOrderDoc(t, env, admin, map[string]any{
		"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{saleItemJSON(productID, "5", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, admin, "sale-order", orderID, 20)
	out := createSaleOutDoc(t, env, admin, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "5", "1.00")},
	})
	outID := int64(out["id"].(float64))
	mustApproveSale(t, env, admin, "sale-out", outID, 20)
	sharedAccount := createFinanceAccountDoc(t, env, admin, "财务范围共享账户", map[string]any{"defaultStatus": 1})
	sharedAccountID := int64(sharedAccount["id"].(float64))

	// owner 角色：财务账户查询/新增 + 收款查询/新增/修改/删除。
	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP finance owner 角色", "code": "erp_finance_owner_role", "dataScope": 1,
		"menuIds": []int64{750, 7501, 752, 7521, 7522, 7523},
	})
	createUserByAPI(t, env, admin, "finance_owner_rnd", rndDept, []int64{ownerRoleID})
	createUserByAPI(t, env, admin, "finance_owner_mkt", mktDept, []int64{ownerRoleID})

	receiptIDs := map[string]int64{}
	for _, u := range []string{"finance_owner_rnd", "finance_owner_mkt"} {
		owner := env.Login(t, u, "123456")
		account := createFinanceAccountDoc(t, env, owner, "账户_of_"+u, map[string]any{"defaultStatus": 1})
		receipt := createFinanceDoc(t, env, owner, "finance-receipt", map[string]any{
			"customerId": customerID, "accountId": sharedAccountID,
			"receiptTime": env.Date() + " 12:00:00",
			"remark":      "收款_of_" + u,
			"items":       []map[string]any{financeItemJSON("sale_out", outID, "1.00")},
		})
		_ = account
		receiptIDs[u] = int64(receipt["id"].(float64))
	}

	// 观察者：仅本人（dataScope=5）——列表只见自己的收款与账户。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP finance 观察者角色", "code": "erp_finance_ds_scope", "dataScope": 5,
		"menuIds": []int64{750, 7501, 752, 7521, 7522, 7523},
	})
	observer := "observer_finance_scope"
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerToken := env.Login(t, observer, "123456")
	createFinanceAccountDoc(t, env, observerToken, "账户_of_"+observer, map[string]any{"defaultStatus": 1})
	ownReceipt := createFinanceDoc(t, env, observerToken, "finance-receipt", map[string]any{
		"customerId": customerID, "accountId": sharedAccountID,
		"receiptTime": env.Date() + " 12:00:00",
		"remark":      "收款_of_" + observer,
		"items":       []map[string]any{financeItemJSON("sale_out", outID, "1.00")},
	})
	ownReceiptID := int64(ownReceipt["id"].(float64))

	rows := pageFinanceDocs(t, env, observerToken, "finance-receipt", "")
	if len(rows) != 1 || fmt.Sprint(rows[0]["remark"]) != "收款_of_"+observer {
		t.Fatalf("仅本人列表应只见自己的收款单，实际 %v", rows)
	}
	accountRows := pageFinanceAccountRows(t, env, observerToken, "")
	if len(accountRows) != 1 || fmt.Sprint(accountRows[0]["name"]) != "账户_of_"+observer {
		t.Fatalf("仅本人账户列表应只见自己的账户，实际 %v", accountRows)
	}
	// 范围外详情/修改/删除 404。
	if status, _ := getJSON(t, env, observerToken, fmt.Sprintf("/erp/finance-receipt/%d", receiptIDs["finance_owner_rnd"])); status != http.StatusNotFound {
		t.Fatalf("范围外收款单详情：HTTP %d，期望 404", status)
	}
	updatePayload := map[string]any{
		"id": receiptIDs["finance_owner_rnd"], "customerId": customerID,
		"accountId": sharedAccountID, "receiptTime": env.Date() + " 12:00:00",
		"items": []map[string]any{financeItemJSON("sale_out", outID, "1.00")},
	}
	if status, _ := env.Request(t, http.MethodPut, "/erp/finance-receipt", observerToken, updatePayload); status != http.StatusNotFound {
		t.Fatalf("范围外收款单修改：HTTP %d，期望 404", status)
	}
	if status, _ := deleteByID(t, env, observerToken, fmt.Sprintf("/erp/finance-receipt/%d", receiptIDs["finance_owner_rnd"])); status != http.StatusNotFound {
		t.Fatalf("范围外收款单删除：HTTP %d，期望 404", status)
	}
	// 自有单据详情可读（正路径对照）。
	if status, _ := getJSON(t, env, observerToken, fmt.Sprintf("/erp/finance-receipt/%d", ownReceiptID)); status != http.StatusOK {
		t.Fatalf("本人收款单详情：HTTP %d，期望 200", status)
	}

	// 全部数据：观察者改授 dataScope=1 后可见三条收款单。
	allRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP finance 全部角色", "code": "erp_finance_ds_all", "dataScope": 1,
		"menuIds": []int64{750, 752},
	})
	allObserver := "observer_finance_all"
	createUserByAPI(t, env, admin, allObserver, mktDept, []int64{allRoleID})
	allToken := env.Login(t, allObserver, "123456")
	allRows := pageFinanceDocs(t, env, allToken, "finance-receipt", "")
	if len(allRows) != 3 {
		t.Fatalf("全部数据应可见三条收款单，实际 %d 条", len(allRows))
	}
}
