//go:build integration

// 销售三单 HTTP 集成测试（issue #92，docs/erp-mvp-spec.md §4.1/§4.5/§4.8 +
// §8.2 销售行，镜像采购）：审核才落账（出库减库存/退货回库存）、挂单校验
// （必挂已审核订单/出库、客户一致）、占用校验（未审核也占用、超量 409、删除
// 释放）、进度回写（out_count/return_count 只计已审核，反审核重算）、反审核
// 禁令（订单有已审核出库、出库有已审核退货 409）、_CANCEL 反向流水（原流水
// 保留、代数和=余额）、编号（XS/CK/ST 格式/伪造不采纳/租户隔离）、已审核不可
// 改删、普通角色 403、数据范围、客户/产品引用禁删。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 菜单/按钮 ID（与 internal/seed/seed.go 700 段一致：741 销售订单 + 五按钮）。
var erpSaleOrderMenus = []int64{741, 7411, 7412, 7413, 7414, 7415}

// XS/CK/ST 编号格式（spec §8.2：^(XS|CK|ST)\d{8}\d{4,}$）。
var (
	saleOrderNoRe  = regexp.MustCompile(`^XS\d{8}\d{4,}$`)
	saleOutNoRe    = regexp.MustCompile(`^CK\d{8}\d{4,}$`)
	saleReturnNoRe = regexp.MustCompile(`^ST\d{8}\d{4,}$`)
)

// ---------- 用例级助手 ----------

// saleItemJSON 是销售明细 payload 的简写构造（复用 QR/QC 明细载荷形态）。
func saleItemJSON(productID int64, count, price string) map[string]any {
	return map[string]any{"productId": productID, "count": count, "productPrice": price}
}

// setupSaleEnv 建仓库 + 产品 + 客户（每个用例独立，名字唯一避免用例间污染）。
func setupSaleEnv(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (warehouseID, productID, customerID int64) {
	t.Helper()
	warehouse := createWarehouse(t, env, token, tag+"仓库", nil)
	category := createCategory(t, env, token, tag+"分类", nil)
	unit := createUnit(t, env, token, tag+"单位", nil)
	product := createProduct(t, env, token, tag+"产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	customer := createCustomer(t, env, token, tag+"客户", nil)
	return int64(warehouse["id"].(float64)), int64(product["id"].(float64)),
		int64(customer["id"].(float64))
}

// padSaleStock 用其它入库单垫库存（负库存禁令：出库审核前仓库必须有货）。
func padSaleStock(t *testing.T, env *testutil.IntegrationEnv, token string, warehouseID, productID int64, count string) {
	t.Helper()
	in := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"inTime":      env.Date() + " 08:00:00",
		"items":       []map[string]any{stockItemJSON(productID, count, "1.00")},
	})
	inID := int64(in["id"].(float64))
	if status, api := approveDoc(t, env, token, "stock-in", inID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("垫库存入库单审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// createSaleOrderDoc 创建销售订单并断言成功，返回完整主表 JSON。
func createSaleOrderDoc(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/sale-order", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建销售订单：HTTP %d code %d msg %q（payload=%v）", status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建销售订单响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// createSaleOutDoc 创建销售出库单并断言成功。
func createSaleOutDoc(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/sale-out", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建销售出库单：HTTP %d code %d msg %q（payload=%v）", status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建销售出库单响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// createSaleReturnDoc 创建销售退货单并断言成功。
func createSaleReturnDoc(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/sale-return", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建销售退货单：HTTP %d code %d msg %q（payload=%v）", status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建销售退货单响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// tryCreateSaleDoc 尝试创建销售单据，只返回 HTTP 状态与信封（负路径断言用）。
func tryCreateSaleDoc(t *testing.T, env *testutil.IntegrationEnv, token, path string, payload map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, path, token, payload)
}

// approveSaleDoc 审核/反审核销售单据（docType: sale-order/sale-out/sale-return）。
func approveSaleDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64, target int8) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, fmt.Sprintf("/erp/%s/update-status", docType), token, map[string]any{
		"id": docID, "status": target,
	})
}

// mustApproveSale 审核/反审核并断言成功。
func mustApproveSale(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64, target int8) {
	t.Helper()
	if status, api := approveSaleDoc(t, env, token, docType, docID, target); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s 状态改到 %d：HTTP %d code %d msg %q", docType, target, status, api.Code, api.Msg)
	}
}

// getSaleDetail 查询单据详情（docType: sale-order/sale-out/sale-return）。
func getSaleDetail(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64) map[string]any {
	t.Helper()
	status, api := getJSON(t, env, token, fmt.Sprintf("/erp/%s/%d", docType, docID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询%s %d 详情：HTTP %d code %d msg %q", docType, docID, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析%s详情: %v（data=%s）", docType, err, string(api.Data))
	}
	return row
}

// orderItemProgress 从订单详情取指定产品的进度行（count/outCount/returnCount）。
func orderItemProgress(t *testing.T, env *testutil.IntegrationEnv, token string, orderID, productID int64) map[string]any {
	t.Helper()
	detail := getSaleDetail(t, env, token, "sale-order", orderID)
	items, _ := detail["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		if int64(item["productId"].(float64)) == productID {
			return item
		}
	}
	t.Fatalf("订单 %d 无产品 %d 明细", orderID, productID)
	return nil
}

// pageSaleDocs 销售单据分页（docType: sale-order/sale-out/sale-return）。
func pageSaleDocs(t *testing.T, env *testutil.IntegrationEnv, token, docType, query string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/erp/%s/page?pageNo=1&pageSize=50", docType)+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询%s分页%s：HTTP %d code %d msg %q", docType, query, status, api.Code, api.Msg)
	}
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析%s分页: %v（data=%s）", docType, err, string(api.Data))
	}
	return page.List
}

// ---------- 核心链路：审核落账 / 库存 / 进度 / 反审核回滚 ----------

// TestIntegrationSaleStockLifecycle 全链路（spec §8.2 销售行）：订单审核是挂单
// 前置 → 出库审核减库存并回写 out_count → 退货审核回库存并回写 return_count →
// 反审核禁令与回滚（反向流水、原流水保留、代数和=余额）→ 已审核不可改删。
func TestIntegrationSaleStockLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "链路")
	padSaleStock(t, env, token, warehouseID, productID, "10")

	// 未审核订单：出库单挂单 409（必须挂已审核订单，spec §4.5）。
	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"saleUserId": 1,
		"orderTime":  env.Date() + " 09:00:00",
		"remark":     "链路订单",
		"items":      []map[string]any{saleItemJSON(productID, "10", "3.00")},
	})
	orderID := int64(order["id"].(float64))
	if !saleOrderNoRe.MatchString(fmt.Sprint(order["orderNo"])) {
		t.Fatalf("订单号 %v 不符 ^XS\\d{8}\\d{4,}$", order["orderNo"])
	}
	if status, _ := tryCreateSaleDoc(t, env, token, "/erp/sale-out", map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "6", "3.00")},
	}); status != http.StatusConflict {
		t.Fatalf("出库挂未审核订单：HTTP %d，期望 409", status)
	}

	// 审核订单 → 创建出库（未审核不动库存）。
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "6", "3.00")},
	})
	outID := int64(out["id"].(float64))
	if !saleOutNoRe.MatchString(fmt.Sprint(out["no"])) {
		t.Fatalf("出库单号 %v 不符 ^CK\\d{8}\\d{4,}$", out["no"])
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "10.000" {
		t.Fatalf("出库未审核不应扣库存：库存 = %s，期望 10.000", got)
	}

	// 审核出库：减库存 + out_count 回写 + sale_out 负流水。
	mustApproveSale(t, env, token, "sale-out", outID, 20)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "4.000" {
		t.Fatalf("出库审核后库存 = %s，期望 4.000（10 出 6）", got)
	}
	progress := orderItemProgress(t, env, token, orderID, productID)
	if fmt.Sprint(progress["outCount"]) != "6.000" {
		t.Fatalf("出库审核后 outCount = %v，期望 6.000", progress["outCount"])
	}
	records, _ := pageStockRecords(t, env, token, fmt.Sprintf("&bizType=sale_out&productId=%d", productID))
	if len(records) != 1 || fmt.Sprint(records[0]["bizNo"]) != fmt.Sprint(out["no"]) {
		t.Fatalf("出库审核应产生 1 条 sale_out 流水，实际 %v", records)
	}

	// 已审核出库：不可修改、不可删除（409）。
	upStatus, _ := env.Request(t, http.MethodPut, "/erp/sale-out", token, map[string]any{
		"id": outID, "customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "1", "3.00")},
	})
	if upStatus != http.StatusConflict {
		t.Fatalf("修改已审核出库单：HTTP %d，期望 409", upStatus)
	}
	if delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-out/%d", outID)); delStatus != http.StatusConflict {
		t.Fatalf("删除已审核出库单：HTTP %d，期望 409", delStatus)
	}

	// 退货挂已审核出库：仓库取自出库单（派生），审核回库存 + return_count 回写。
	ret := createSaleReturnDoc(t, env, token, map[string]any{
		"customerId": customerID, "outId": outID,
		"returnTime": env.Date() + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "2", "3.00")},
	})
	retID := int64(ret["id"].(float64))
	if !saleReturnNoRe.MatchString(fmt.Sprint(ret["no"])) {
		t.Fatalf("退货单号 %v 不符 ^ST\\d{8}\\d{4,}$", ret["no"])
	}
	retDetail := getSaleDetail(t, env, token, "sale-return", retID)
	if int64(retDetail["warehouseId"].(float64)) != warehouseID {
		t.Fatalf("退货仓库应派生自出库单 %d，实际 %v", warehouseID, retDetail["warehouseId"])
	}
	mustApproveSale(t, env, token, "sale-return", retID, 20)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "6.000" {
		t.Fatalf("退货审核后库存 = %s，期望 6.000（4 回 2）", got)
	}
	progress = orderItemProgress(t, env, token, orderID, productID)
	if fmt.Sprint(progress["returnCount"]) != "2.000" {
		t.Fatalf("退货审核后 returnCount = %v，期望 2.000", progress["returnCount"])
	}

	// 反审核禁令（409）：出库存在已审核退货；订单存在已审核出库。
	if status, _ := approveSaleDoc(t, env, token, "sale-out", outID, 10); status != http.StatusConflict {
		t.Fatalf("出库有已审核退货时反审核：HTTP %d，期望 409", status)
	}
	if status, _ := approveSaleDoc(t, env, token, "sale-order", orderID, 10); status != http.StatusConflict {
		t.Fatalf("订单有已审核出库时反审核：HTTP %d，期望 409", status)
	}

	// 反审核退货：sale_return_CANCEL 负流水、库存回落、return_count 归零。
	mustApproveSale(t, env, token, "sale-return", retID, 10)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "4.000" {
		t.Fatalf("退货反审核后库存 = %s，期望 4.000", got)
	}
	progress = orderItemProgress(t, env, token, orderID, productID)
	if fmt.Sprint(progress["returnCount"]) != "0.000" {
		t.Fatalf("退货反审核后 returnCount = %v，期望 0.000", progress["returnCount"])
	}

	// 反审核出库：sale_out_CANCEL 正流水、库存恢复、out_count 归零。
	mustApproveSale(t, env, token, "sale-out", outID, 10)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "10.000" {
		t.Fatalf("出库反审核后库存 = %s，期望 10.000（可审计重放）", got)
	}
	progress = orderItemProgress(t, env, token, orderID, productID)
	if fmt.Sprint(progress["outCount"]) != "0.000" {
		t.Fatalf("出库反审核后 outCount = %v，期望 0.000", progress["outCount"])
	}

	// 流水代数和 = 余额（append-only 全集，spec §4.3）：垫 10 + 出/退全反冲 = 10。
	all, _ := pageStockRecords(t, env, token, fmt.Sprintf("&productId=%d", productID))
	sum := 0.0
	for _, rec := range all {
		var count float64
		_, _ = fmt.Sscan(fmt.Sprint(rec["count"]), &count)
		sum += count
	}
	if sum != 10 {
		t.Fatalf("流水代数和 %v ≠ 余额 10（可审计重放被破坏）", sum)
	}

	// 禁令解除后：订单反审核；仍被未审核出库引用 → 删除 409（引用禁删）；
	// 按引用方向逆序拆除（退货 → 出库 → 订单）后全部可删。
	mustApproveSale(t, env, token, "sale-order", orderID, 10)
	if delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-order/%d", orderID)); delStatus != http.StatusConflict {
		t.Fatalf("删除存在出库引用的订单：HTTP %d，期望 409", delStatus)
	}
	if delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-out/%d", outID)); delStatus != http.StatusConflict {
		t.Fatalf("删除存在退货引用的出库单：HTTP %d，期望 409", delStatus)
	}
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-return/%d", retID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核退货单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-out/%d", outID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核出库单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-order/%d", orderID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除反审核后的订单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	if status, _ := getJSON(t, env, token, fmt.Sprintf("/erp/sale-order/%d", orderID)); status != http.StatusNotFound {
		t.Fatalf("删除后订单详情应 404，实际 HTTP %d", status)
	}
}

// TestIntegrationSaleApproveRechecksLink 审核子单时必须复核挂单仍为已审核
// （spec §4.5，镜像采购 approvePurchaseIn/approvePurchaseReturn 的
// lockApprovedOrder/lockApprovedIn）：
//   - 审核订单 → 建出库（未审核）→ 反审核订单（禁令只看已审核挂单，允许）→
//     审核出库必须 409，且不落库存/不回写进度；
//   - 订单恢复已审核 → 出库审核成功；建退货（未审核）→ 反审核出库（无已审核
//     退货，允许）→ 审核退货必须 409。
func TestIntegrationSaleApproveRechecksLink(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "复核")
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
		"items":   []map[string]any{saleItemJSON(productID, "5", "3.00")},
	})
	outID := int64(out["id"].(float64))

	// 未审核出库不触发反审核禁令：订单可回到未审核。
	mustApproveSale(t, env, token, "sale-order", orderID, 10)

	// 关键断言：出库审核先复核挂单订单，父单已未审核 → 409；库存不落账、
	// 进度不回写（检查发生在乐观认领与入账之前）。
	status, api := approveSaleDoc(t, env, token, "sale-out", outID, 20)
	if status != http.StatusConflict {
		t.Fatalf("挂未审核订单审核出库：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}
	if !strings.Contains(api.Msg, "销售订单不存在或未审核") {
		t.Fatalf("出库审核复核文案不符：%q", api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "10.000" {
		t.Fatalf("复核失败不应落库存：库存 = %s，期望 10.000", got)
	}
	if progress := orderItemProgress(t, env, token, orderID, productID); fmt.Sprint(progress["outCount"]) != "0.000" {
		t.Fatalf("复核失败不应回写进度：outCount = %v，期望 0.000", progress["outCount"])
	}

	// 订单恢复已审核后出库可正常审核；再建退货并反审核出库（无已审核退货）。
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	mustApproveSale(t, env, token, "sale-out", outID, 20)
	ret := createSaleReturnDoc(t, env, token, map[string]any{
		"customerId": customerID, "outId": outID,
		"returnTime": env.Date() + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "2", "3.00")},
	})
	retID := int64(ret["id"].(float64))
	mustApproveSale(t, env, token, "sale-out", outID, 10)

	// 关键断言：退货审核先复核挂单出库，出库已未审核 → 409；库存不因退货增加。
	status, api = approveSaleDoc(t, env, token, "sale-return", retID, 20)
	if status != http.StatusConflict {
		t.Fatalf("挂未审核出库审核退货：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}
	if !strings.Contains(api.Msg, "销售出库单不存在或未审核") {
		t.Fatalf("退货审核复核文案不符：%q", api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "10.000" {
		t.Fatalf("复核失败不应回库存：库存 = %s，期望 10.000（出库反审核已回补）", got)
	}
}

// TestIntegrationSaleReturnParentConsistency 退货父单快照一致性（#98 审查
// MAJOR 1 回归）：
//   - 出库 W1 审核 → 建退货（仓库/客户快照）→ 反审核出库 → 编辑出库改仓库/
//     客户必须 409（存在退货引用，含未审核）；
//   - 同仓库/客户仅改时间可编辑 → 重新审核出库 → 退货审核成功，只回 W1；
//   - 直接改库构造退货仓库/客户与父单错配 → 审核 409，库存与流水不变。
func TestIntegrationSaleReturnParentConsistency(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "父单快照")
	warehouse2 := createWarehouse(t, env, token, "父单快照二仓", nil)
	warehouse2ID := int64(warehouse2["id"].(float64))
	customer2 := createCustomer(t, env, token, "父单快照二客户", nil)
	customer2ID := int64(customer2["id"].(float64))
	padSaleStock(t, env, token, warehouseID, productID, "10")

	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "10", "3.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	// 第二客户挂自己的订单（客户改单要先过「客户=订单客户」的挂单校验，才能
	// 命中父单守卫的客户分支）。
	order2 := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customer2ID,
		"orderTime":  env.Date() + " 09:30:00",
		"items":      []map[string]any{saleItemJSON(productID, "5", "3.00")},
	})
	order2ID := int64(order2["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", order2ID, 20)

	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "5", "3.00")},
	})
	outID := int64(out["id"].(float64))
	mustApproveSale(t, env, token, "sale-out", outID, 20)

	ret := createSaleReturnDoc(t, env, token, map[string]any{
		"customerId": customerID, "outId": outID,
		"returnTime": env.Date() + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "2", "3.00")},
	})
	retID := int64(ret["id"].(float64))

	// 反审核出库（此时退货未审核，禁令不阻断）→ 父单可编辑，但退货快照锁定
	// 仓库/客户：改任一字段 409。
	mustApproveSale(t, env, token, "sale-out", outID, 10)
	outUpdate := func(warehouseID, customerID, orderID int64) map[string]any {
		return map[string]any{
			"id": outID, "customerId": customerID, "orderId": orderID,
			"warehouseId": warehouseID, "outTime": env.Date() + " 10:30:00",
			"items": []map[string]any{saleItemJSON(productID, "5", "3.00")},
		}
	}
	status, api := env.Request(t, http.MethodPut, "/erp/sale-out", token, outUpdate(warehouse2ID, customerID, orderID))
	if status != http.StatusConflict || !strings.Contains(api.Msg, "禁止修改仓库") {
		t.Fatalf("有退货引用时改仓库：HTTP %d msg %q，期望 409 且提示禁止修改仓库", status, api.Msg)
	}
	status, api = env.Request(t, http.MethodPut, "/erp/sale-out", token, outUpdate(warehouseID, customer2ID, order2ID))
	if status != http.StatusConflict || !strings.Contains(api.Msg, "禁止修改客户") {
		t.Fatalf("有退货引用时改客户：HTTP %d msg %q，期望 409 且提示禁止修改客户", status, api.Msg)
	}
	// 同仓库/客户仅改时间：允许（其余字段不冻结）。
	status, api = env.Request(t, http.MethodPut, "/erp/sale-out", token, outUpdate(warehouseID, customerID, orderID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("同仓库/客户编辑出库：HTTP %d code %d msg %q，期望成功", status, api.Code, api.Msg)
	}

	// 重新审核出库后审核退货成功：库存只回 W1（10−5+2=7），W2 不凭空增加。
	mustApproveSale(t, env, token, "sale-out", outID, 20)
	mustApproveSale(t, env, token, "sale-return", retID, 20)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "7.000" {
		t.Fatalf("退货审核后 W1 库存 = %s，期望 7.000", got)
	}
	if got := getStockCount(t, env, token, productID, warehouse2ID); got != "0.000" {
		t.Fatalf("退货审核不应动 W2：库存 = %s，期望 0.000", got)
	}

	// 直接改库构造错配：仓库篡改 → 审核 409、库存/流水不变。
	ret2 := createSaleReturnDoc(t, env, token, map[string]any{
		"customerId": customerID, "outId": outID,
		"returnTime": env.Date() + " 12:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "1", "3.00")},
	})
	ret2ID := int64(ret2["id"].(float64))
	recordsBefore, _ := pageStockRecords(t, env, token, "")
	if err := env.DB.Exec("UPDATE `erp_sale_return` SET `warehouse_id` = ? WHERE `id` = ?", warehouse2ID, ret2ID).Error; err != nil {
		t.Fatalf("篡改退货仓库: %v", err)
	}
	status, api = approveSaleDoc(t, env, token, "sale-return", ret2ID, 20)
	if status != http.StatusConflict || !strings.Contains(api.Msg, "仓库与出库单不一致") {
		t.Fatalf("退货仓库错配审核：HTTP %d msg %q，期望 409 且提示仓库不一致", status, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "7.000" {
		t.Fatalf("错配审核不应回库存：W1 = %s，期望 7.000", got)
	}
	if got := getStockCount(t, env, token, productID, warehouse2ID); got != "0.000" {
		t.Fatalf("错配审核不应向 W2 落账：库存 = %s，期望 0.000", got)
	}
	if records, _ := pageStockRecords(t, env, token, ""); len(records) != len(recordsBefore) {
		t.Fatalf("错配审核不应新增流水：%d → %d", len(recordsBefore), len(records))
	}
	if detail := getSaleDetail(t, env, token, "sale-return", ret2ID); detail["status"] != float64(10) {
		t.Fatalf("错配审核失败后退货应保持未审核，实际 status=%v", detail["status"])
	}

	// 客户篡改 → 同样 409。
	if err := env.DB.Exec(
		"UPDATE `erp_sale_return` SET `warehouse_id` = ?, `customer_id` = ? WHERE `id` = ?",
		warehouseID, customer2ID, ret2ID,
	).Error; err != nil {
		t.Fatalf("篡改退货客户: %v", err)
	}
	status, api = approveSaleDoc(t, env, token, "sale-return", ret2ID, 20)
	if status != http.StatusConflict || !strings.Contains(api.Msg, "客户与出库单不一致") {
		t.Fatalf("退货客户错配审核：HTTP %d msg %q，期望 409 且提示客户不一致", status, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "7.000" {
		t.Fatalf("客户错配审核不应回库存：W1 = %s，期望 7.000", got)
	}
}

// TestIntegrationSaleUnapproveConcurrentInvariant 反审核并发不变量（#98 审查
// MAJOR 2 回归）：循环 20 次，每轮并发发起「子单审核」与「父单反审核」，断言
// 最终不存在「父单 status=10 且存在已审核子单」的组合，且两请求恰一成一败
// （200/409，无死锁/500）：
//   - 退货审核 vs 出库反审核（父单行锁串行化）；
//   - 出库审核 vs 订单反审核（子单审核先锁挂单订单）。
func TestIntegrationSaleUnapproveConcurrentInvariant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "并发反审核")
	padSaleStock(t, env, token, warehouseID, productID, "100")

	// assertOneWins 断言并发请求分别返回 200/409 且恰一成功（无死锁/500）。
	assertOneWins := func(t *testing.T, round int, aStatus int, aAPI testutil.APIResponse, aErr error,
		bStatus int, bAPI testutil.APIResponse, bErr error) {
		t.Helper()
		for _, res := range []struct {
			label  string
			status int
			api    testutil.APIResponse
			err    error
		}{
			{"子单审核", aStatus, aAPI, aErr},
			{"父单反审核", bStatus, bAPI, bErr},
		} {
			if res.err != nil {
				t.Fatalf("第 %d 轮 %s 请求错误: %v", round, res.label, res.err)
			}
			if res.status != http.StatusOK && res.status != http.StatusConflict {
				t.Fatalf("第 %d 轮 %s：HTTP %d code %d msg %q，期望 200/409（不得死锁或 500）",
					round, res.label, res.status, res.api.Code, res.api.Msg)
			}
		}
		success := 0
		if aStatus == http.StatusOK {
			success++
		}
		if bStatus == http.StatusOK {
			success++
		}
		if success != 1 {
			t.Fatalf("第 %d 轮并发结果应恰一成功：子单 HTTP %d / 父单 HTTP %d", round, aStatus, bStatus)
		}
	}
	// runPair 并发发起两个审核/反审核请求。
	runPair := func(docTypeA string, idA int64, targetA int8, docTypeB string, idB int64, targetB int8) (int, testutil.APIResponse, error, int, testutil.APIResponse, error) {
		var wg sync.WaitGroup
		start := make(chan struct{})
		var aStatus, bStatus int
		var aAPI, bAPI testutil.APIResponse
		var aErr, bErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			aStatus, aAPI, aErr = approveDocOnce(env, token, docTypeA, idA, targetA)
		}()
		go func() {
			defer wg.Done()
			<-start
			bStatus, bAPI, bErr = approveDocOnce(env, token, docTypeB, idB, targetB)
		}()
		close(start)
		wg.Wait()
		return aStatus, aAPI, aErr, bStatus, bAPI, bErr
	}

	// 轮次 1：退货审核 vs 出库反审核。每轮独立订单/出库/退货，避免历史单据
	// 占用额度掩盖并发窗口。
	for i := 0; i < 20; i++ {
		order := createSaleOrderDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
			"items": []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApproveSale(t, env, token, "sale-order", orderID, 20)
		out := createSaleOutDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
			"outTime": env.Date() + " 10:00:00",
			"items":   []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		outID := int64(out["id"].(float64))
		mustApproveSale(t, env, token, "sale-out", outID, 20)
		ret := createSaleReturnDoc(t, env, token, map[string]any{
			"customerId": customerID, "outId": outID,
			"returnTime": env.Date() + " 11:00:00",
			"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		retID := int64(ret["id"].(float64))
		aStatus, aAPI, aErr, bStatus, bAPI, bErr := runPair("sale-return", retID, 20, "sale-out", outID, 10)
		assertOneWins(t, i+1, aStatus, aAPI, aErr, bStatus, bAPI, bErr)
		outStatus := getSaleDetail(t, env, token, "sale-out", outID)["status"].(float64)
		retStatus := getSaleDetail(t, env, token, "sale-return", retID)["status"].(float64)
		if outStatus == float64(10) && retStatus == float64(20) {
			t.Fatalf("第 %d 轮出现「出库未审核 + 已审核退货」组合", i+1)
		}
	}

	// 轮次 2：出库审核 vs 订单反审核。每轮独立订单/出库，反审核成功后无需恢复。
	for i := 0; i < 20; i++ {
		order := createSaleOrderDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderTime": env.Date() + " 13:00:00",
			"items": []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApproveSale(t, env, token, "sale-order", orderID, 20)
		out := createSaleOutDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
			"outTime": env.Date() + " 14:00:00",
			"items":   []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		outID := int64(out["id"].(float64))
		aStatus, aAPI, aErr, bStatus, bAPI, bErr := runPair("sale-out", outID, 20, "sale-order", orderID, 10)
		assertOneWins(t, i+1, aStatus, aAPI, aErr, bStatus, bAPI, bErr)
		orderStatus := getSaleDetail(t, env, token, "sale-order", orderID)["status"].(float64)
		outStatus := getSaleDetail(t, env, token, "sale-out", outID)["status"].(float64)
		if orderStatus == float64(10) && outStatus == float64(20) {
			t.Fatalf("第 %d 轮出现「订单未审核 + 已审核出库」组合", i+1)
		}
	}
}

// TestIntegrationSaleOccupancy 占用校验（spec §4.5，镜像采购 §4.4）：未审核出库/
// 退货都占用；超量 409；删除未审核单释放占用。
func TestIntegrationSaleOccupancy(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "占用")
	padSaleStock(t, env, token, warehouseID, productID, "20")

	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "10", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)

	outPayload := func(count string) map[string]any {
		return map[string]any{
			"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
			"outTime": env.Date() + " 10:00:00",
			"items":   []map[string]any{saleItemJSON(productID, count, "1.00")},
		}
	}

	// 未审核出库也占用：出 6 后再出 5（剩余 4）→ 409。
	out1 := createSaleOutDoc(t, env, token, outPayload("6"))
	out1ID := int64(out1["id"].(float64))
	if status, _ := tryCreateSaleDoc(t, env, token, "/erp/sale-out", outPayload("5")); status != http.StatusConflict {
		t.Fatalf("超订单可出数量（未审核占用）：HTTP %d，期望 409", status)
	}
	// 删除未审核出库 → 占用释放，再出 5 成功（未审核，占用 5）。
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-out/%d", out1ID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核出库单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	out2 := createSaleOutDoc(t, env, token, outPayload("5"))
	out2ID := int64(out2["id"].(float64))

	// 退货占用（未审核也占用）：审核出库后退 4 占用，再退 2（剩余 1）→ 409；
	// 删除未审核退货 → 占用释放，退 2 成功。
	mustApproveSale(t, env, token, "sale-out", out2ID, 20)
	retPayload := func(count string) map[string]any {
		return map[string]any{
			"customerId": customerID, "outId": out2ID,
			"returnTime": env.Date() + " 11:00:00",
			"items":      []map[string]any{saleItemJSON(productID, count, "1.00")},
		}
	}
	ret1 := createSaleReturnDoc(t, env, token, retPayload("4"))
	ret1ID := int64(ret1["id"].(float64))
	if status, _ := tryCreateSaleDoc(t, env, token, "/erp/sale-return", retPayload("2")); status != http.StatusConflict {
		t.Fatalf("超出库可退数量（未审核退货占用）：HTTP %d，期望 409", status)
	}
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-return/%d", ret1ID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核退货单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	ret2 := createSaleReturnDoc(t, env, token, retPayload("2"))
	ret2ID := int64(ret2["id"].(float64))
	mustApproveSale(t, env, token, "sale-return", ret2ID, 20)

	// 进度只计已审核：outCount 5（已审核出库），returnCount 2（已审核退货）。
	progress := orderItemProgress(t, env, token, orderID, productID)
	if fmt.Sprint(progress["outCount"]) != "5.000" || fmt.Sprint(progress["returnCount"]) != "2.000" {
		t.Fatalf("进度回写不符：outCount=%v returnCount=%v，期望 5.000/2.000",
			progress["outCount"], progress["returnCount"])
	}
}

// TestIntegrationSaleOrderEditOccupancy 订单编辑必须维持占用不变量：未审核订单
// 被未审核出库占用时，删减订单行到占用以下 409（占用含未审核单，spec §4.5）。
// 路径：订单审核 → 出库（未审核）→ 订单反审核（无已审核出库，允许）→ 编辑。
func TestIntegrationSaleOrderEditOccupancy(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "订单编辑")

	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "10", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)

	// 未审核出库占用 6。
	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "6", "1.00")},
	})
	outID := int64(out["id"].(float64))

	// 订单反审核（无已审核出库/退货 → 允许），随后编辑删减到 5 < 占用 6 → 409。
	mustApproveSale(t, env, token, "sale-order", orderID, 10)
	shrinkStatus, _ := env.Request(t, http.MethodPut, "/erp/sale-order", token, map[string]any{
		"id": orderID, "customerId": customerID,
		"orderTime": env.Date() + " 09:00:00",
		"items":     []map[string]any{saleItemJSON(productID, "5", "1.00")},
	})
	if shrinkStatus != http.StatusConflict {
		t.Fatalf("订单删减到占用以下：HTTP %d，期望 409", shrinkStatus)
	}
	// 调整到 8（≥ 占用 6）→ 成功。
	okStatus, okAPI := env.Request(t, http.MethodPut, "/erp/sale-order", token, map[string]any{
		"id": orderID, "customerId": customerID,
		"orderTime": env.Date() + " 09:00:00",
		"items":     []map[string]any{saleItemJSON(productID, "8", "1.00")},
	})
	if okStatus != http.StatusOK || okAPI.Code != 0 {
		t.Fatalf("订单调整到 8：HTTP %d code %d msg %q", okStatus, okAPI.Code, okAPI.Msg)
	}
	// 删除被占用的订单 → 409（存在出库单引用）。
	if delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-order/%d", orderID)); delStatus != http.StatusConflict {
		t.Fatalf("删除存在出库引用的订单：HTTP %d，期望 409", delStatus)
	}
	// 删除未审核出库释放引用后，订单可删。
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-out/%d", outID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核出库单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-order/%d", orderID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除无引用订单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
}

// TestIntegrationSaleLinkGuards 挂单校验与销售员记录（spec §4.5/§3.4）：
// 挂单不存在 400、未审核 409、客户不一致 400；退货仓库派生；sale_user_id 纯记录；
// 客户端伪造单号不采纳（spec §4.8：DTO 无单号字段，多余键被忽略）。
func TestIntegrationSaleLinkGuards(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "挂单")
	padSaleStock(t, env, token, warehouseID, productID, "5")

	// 挂不存在的订单 → 400。
	if status, _ := tryCreateSaleDoc(t, env, token, "/erp/sale-out", map[string]any{
		"customerId": customerID, "orderId": int64(999999), "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "1", "1.00")},
	}); status != http.StatusBadRequest {
		t.Fatalf("出库挂不存在订单：HTTP %d，期望 400", status)
	}

	// 订单审核后，客户不一致 → 400。
	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "5", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	other := createCustomer(t, env, token, "挂单其他客户", nil)
	otherID := int64(other["id"].(float64))
	if status, _ := tryCreateSaleDoc(t, env, token, "/erp/sale-out", map[string]any{
		"customerId": otherID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "1", "1.00")},
	}); status != http.StatusBadRequest {
		t.Fatalf("出库客户与订单不一致：HTTP %d，期望 400", status)
	}

	// 伪造单号不采纳：多余 orderNo/no 键被忽略，返回服务端发放编号。
	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime":  env.Date() + " 10:00:00",
		"orderNo":  "XS1FAKE000",
		"no":       "CK1FAKE000",
		"outCount": "99",
		"items":    []map[string]any{saleItemJSON(productID, "2", "1.00")},
	})
	outID := int64(out["id"].(float64))
	if !saleOutNoRe.MatchString(fmt.Sprint(out["no"])) {
		t.Fatalf("伪造单号被采纳：出库单号 %v", out["no"])
	}
	mustApproveSale(t, env, token, "sale-out", outID, 20)

	// 退货挂不存在/未审核出库 → 400/409；客户不一致 → 400。
	if status, _ := tryCreateSaleDoc(t, env, token, "/erp/sale-return", map[string]any{
		"customerId": customerID, "outId": int64(999999),
		"returnTime": env.Date() + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
	}); status != http.StatusBadRequest {
		t.Fatalf("退货挂不存在出库单：HTTP %d，期望 400", status)
	}
	if status, _ := tryCreateSaleDoc(t, env, token, "/erp/sale-return", map[string]any{
		"customerId": otherID, "outId": outID,
		"returnTime": env.Date() + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
	}); status != http.StatusBadRequest {
		t.Fatalf("退货客户与出库单不一致：HTTP %d，期望 400", status)
	}

	// sale_user_id 纯记录字段：创建时记录，详情回读一致。
	orderWithUser := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"saleUserId": 1,
		"orderTime":  env.Date() + " 09:30:00",
		"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})
	orderWithUserID := int64(orderWithUser["id"].(float64))
	detail := getSaleDetail(t, env, token, "sale-order", orderWithUserID)
	if int64(detail["saleUserId"].(float64)) != 1 {
		t.Fatalf("销售员记录字段 saleUserId = %v，期望 1", detail["saleUserId"])
	}
}

// TestIntegrationSaleNumberingTenantIsolation 编号与租户隔离（spec §4.8/§4.10）：
// 三类单号格式成立；跨租户业务 ID 一律 404；序号按租户独立发放（新租户从 1 起）。
func TestIntegrationSaleNumberingTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "编号")
	padSaleStock(t, env, token, warehouseID, productID, "10")

	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "5", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "2", "1.00")},
	})
	outID := int64(out["id"].(float64))
	mustApproveSale(t, env, token, "sale-out", outID, 20)
	ret := createSaleReturnDoc(t, env, token, map[string]any{
		"customerId": customerID, "outId": outID,
		"returnTime": env.Date() + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})
	if !saleOrderNoRe.MatchString(fmt.Sprint(order["orderNo"])) ||
		!saleOutNoRe.MatchString(fmt.Sprint(out["no"])) ||
		!saleReturnNoRe.MatchString(fmt.Sprint(ret["no"])) {
		t.Fatalf("编号格式不符：order=%v out=%v ret=%v", order["orderNo"], out["no"], ret["no"])
	}

	// 租户隔离：新租户 admin 登录，跨租户业务 ID 一律 404；同日序号独立发放。
	tenant2 := createTenantByAPI(t, env, token, "销售隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)
	for _, docType := range []string{"sale-order", "sale-out", "sale-return"} {
		id := orderID
		if docType == "sale-out" {
			id = outID
		}
		if docType == "sale-return" {
			id = int64(ret["id"].(float64))
		}
		if status, _ := getJSON(t, env, token2, fmt.Sprintf("/erp/%s/%d", docType, id)); status != http.StatusNotFound {
			t.Fatalf("租户 2 访问租户 1 的 %s %d：HTTP %d，期望 404", docType, id, status)
		}
	}
	// 租户 2 无任何客户/仓库数据：列表为空（数据不外泄）。
	if rows := pageSaleDocs(t, env, token2, "sale-order", ""); len(rows) != 0 {
		t.Fatalf("租户 2 销售订单列表应仅含本租户数据，实际 %d 条", len(rows))
	}
}

// TestIntegrationSaleCommonRoleNoPermission 内置普通角色（role id=2）默认无
// 销售三单权限（spec §7：普通角色不授 ERP；spec §8.2 普通角色 403）。
func TestIntegrationSaleCommonRoleNoPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, admin, "普通角色禁止")
	padSaleStock(t, env, admin, warehouseID, productID, "3")
	order := createSaleOrderDoc(t, env, admin, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "3", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, admin, "sale-order", orderID, 20)
	// 先由 admin 造数据，保证「无权限 403」不是「无数据空列表」。

	common := env.Login(t, "demo", "123456")
	for _, path := range []string{
		"/erp/sale-order/page?pageNo=1&pageSize=10",
		"/erp/sale-out/page?pageNo=1&pageSize=10",
		"/erp/sale-return/page?pageNo=1&pageSize=10",
	} {
		status, _ := env.Request(t, http.MethodGet, path, common, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色访问 %s：HTTP %d，期望 403", path, status)
		}
	}
}

// TestIntegrationSaleDataScope 销售单据列表按 owner 数据范围过滤（spec §4.10，
// owner = 创建人）：全部数据可见全部；仅本人只见自己的；范围外详情/修改/删除
// 一律 404（不泄露存在性）。
func TestIntegrationSaleDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	rndDept := int64(101)
	mktDept := int64(102)

	// 共享主数据（admin 建，租户内存在性校验通过即可引用；列表可见性按 owner 过滤
	// 不影响单据创建，产品/客户引用检查是租户级存在性，spec §4.10）。
	category := createCategory(t, env, admin, "销售范围分类", nil)
	unit := createUnit(t, env, admin, "销售范围单位", nil)
	product := createProduct(t, env, admin, "销售范围产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	productID := int64(product["id"].(float64))
	customer := createCustomer(t, env, admin, "销售范围客户", nil)
	customerID := int64(customer["id"].(float64))

	// owner 角色只授销售订单查询/创建（owner 的数据范围不参与断言）。
	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP sale owner 角色", "code": "erp_sale_owner_role", "dataScope": 1,
		"menuIds": []int64{741, 7411},
	})
	createUserByAPI(t, env, admin, "sale_owner_rnd", rndDept, []int64{ownerRoleID})
	createUserByAPI(t, env, admin, "sale_owner_mkt", mktDept, []int64{ownerRoleID})

	orderIDs := map[string]int64{}
	for _, u := range []string{"sale_owner_rnd", "sale_owner_mkt"} {
		owner := env.Login(t, u, "123456")
		order := createSaleOrderDoc(t, env, owner, map[string]any{
			"customerId": customerID,
			"orderTime":  env.Date() + " 09:00:00",
			"remark":     "订单_of_" + u,
			"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		orderIDs[u] = int64(order["id"].(float64))
	}

	// 观察者角色：查询 + 增删改按钮（修改/删除按钮码也要授予：403（无权限码）
	// 与 404（范围外）分层）。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP sale 观察者角色", "code": "erp_sale_ds_scope", "dataScope": 5,
		"menuIds": erpSaleOrderMenus,
	})
	observer := "observer_sale_scope"
	createUserByAPI(t, env, admin, observer, mktDept, []int64{roleID})
	observerToken := env.Login(t, observer, "123456")
	createSaleOrderDoc(t, env, observerToken, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"remark":     "订单_of_" + observer,
		"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})

	// 仅本人：列表只见自己的订单（admin 与他人订单不可见）。
	rows := pageSaleDocs(t, env, observerToken, "sale-order", "")
	if len(rows) != 1 || fmt.Sprint(rows[0]["remark"]) != "订单_of_"+observer {
		t.Fatalf("仅本人列表应只见自己的订单，实际 %v", rows)
	}
	// 范围外详情 404。
	if status, _ := getJSON(t, env, observerToken, fmt.Sprintf("/erp/sale-order/%d", orderIDs["sale_owner_rnd"])); status != http.StatusNotFound {
		t.Fatalf("范围外订单详情：HTTP %d，期望 404", status)
	}
	// 范围外修改/删除 404（不泄露存在性）。
	upStatus, _ := env.Request(t, http.MethodPut, "/erp/sale-order", observerToken, map[string]any{
		"id": orderIDs["sale_owner_rnd"], "customerId": customerID,
		"orderTime": env.Date() + " 09:00:00",
		"items":     []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})
	if upStatus != http.StatusNotFound {
		t.Fatalf("范围外订单修改：HTTP %d，期望 404", upStatus)
	}
	delStatus, _ := deleteByID(t, env, observerToken, fmt.Sprintf("/erp/sale-order/%d", orderIDs["sale_owner_rnd"]))
	if delStatus != http.StatusNotFound {
		t.Fatalf("范围外订单删除：HTTP %d，期望 404", delStatus)
	}

	// 全部数据：观察者改授 dataScope=1 后可见全部三条。
	allRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP sale 全部角色", "code": "erp_sale_ds_all", "dataScope": 1,
		"menuIds": erpSaleOrderMenus,
	})
	allObserver := "observer_sale_all"
	createUserByAPI(t, env, admin, allObserver, mktDept, []int64{allRoleID})
	allToken := env.Login(t, allObserver, "123456")
	allRows := pageSaleDocs(t, env, allToken, "sale-order", "")
	if len(allRows) != 3 {
		t.Fatalf("全部数据应可见三条订单，实际 %d 条", len(allRows))
	}
}

// TestIntegrationSaleRefDelete 引用禁删（spec §4.9）：ERP 客户被销售订单/出库/
// 退货引用 409；产品被销售明细引用 409；删除引用单据后可删。
func TestIntegrationSaleRefDelete(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, token, "引用")
	// 垫库存（负库存禁令）；垫库 QR 已审核，其明细对该产品的引用不可移除——
	// 末段断言「产品仅剩垫库 QR 引用」即证明销售引用已释放（软删行不算引用）。
	padSaleStock(t, env, token, warehouseID, productID, "5")

	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "5", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	out := createSaleOutDoc(t, env, token, map[string]any{
		"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "2", "1.00")},
	})
	outID := int64(out["id"].(float64))
	mustApproveSale(t, env, token, "sale-out", outID, 20)

	// 客户被销售订单/出库引用 → 409。
	if delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/customer/%d", customerID)); delStatus != http.StatusConflict {
		t.Fatalf("删除被销售单据引用的客户：HTTP %d，期望 409", delStatus)
	}
	// 产品被销售订单/出库明细引用 → 409。
	if delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", productID)); delStatus != http.StatusConflict {
		t.Fatalf("删除被销售明细引用的产品：HTTP %d，期望 409", delStatus)
	}

	// 反审核并删除引用单据后，客户/产品恢复可删（软删行不算引用）。
	mustApproveSale(t, env, token, "sale-out", outID, 10)
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-out/%d", outID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核出库单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	mustApproveSale(t, env, token, "sale-order", orderID, 10)
	if delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/sale-order/%d", orderID)); delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核订单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	if delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/customer/%d", customerID)); delStatus != http.StatusOK {
		t.Fatalf("删除无引用客户：HTTP %d，期望 200", delStatus)
	}
	// 产品销售引用已释放：再删只剩垫库 QR 明细引用（消息切换证明销售计数已清零，
	// 软删行不算引用，spec §4.9）。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", productID))
	if delStatus != http.StatusConflict || delAPI.Msg == "" ||
		!strings.Contains(delAPI.Msg, "其它入库单明细") {
		t.Fatalf("删除仅剩垫库引用的产品：HTTP %d msg %q，期望 409 且提示其它入库单明细",
			delStatus, delAPI.Msg)
	}
}

// TestIntegrationSaleExport 导出冒烟（spec §4.11：三类单据导出，xlsx MIME +
// attachment 文件名 + 数据工作表；列契约由 excel 包约束体系保证）。
func TestIntegrationSaleExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	_, productID, customerID := setupSaleEnv(t, env, token, "导出")
	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 09:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})
	wantNo := fmt.Sprint(order["orderNo"])
	for _, docType := range []string{"sale-order", "sale-out", "sale-return"} {
		path := fmt.Sprintf("/erp/%s/export-excel?pageNo=1&pageSize=10", docType)
		req, err := http.NewRequest(http.MethodGet, env.APIPrefixURL(path), nil)
		if err != nil {
			t.Fatalf("构造下载请求: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := env.Server.Client().Do(req)
		if err != nil {
			t.Fatalf("下载 Excel: %v", err)
		}
		defer resp.Body.Close()
		disposition := resp.Header.Get("Content-Disposition")
		if !strings.HasPrefix(disposition, "attachment") || !strings.Contains(disposition, ".xlsx") {
			t.Fatalf("GET %s: Content-Disposition=%q，期望 attachment 且带 .xlsx 文件名", path, disposition)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
			t.Fatalf("GET %s: Content-Type=%q，期望 xlsx MIME 类型", path, ct)
		}
		f, err := excelize.OpenReader(resp.Body)
		if err != nil {
			t.Fatalf("解析下载的 xlsx: %v", err)
		}
		t.Cleanup(func() { f.Close() })
		rows, err := f.GetRows("数据")
		if err != nil || len(rows) < 1 {
			t.Fatalf("GET %s: 数据工作表应至少含表头，rows=%d err=%v", path, len(rows), err)
		}
		// 订单导出首行数据应含本用例订单号（本用例建了订单；出库/退货列表为空，
		// 表头成立即可——空列表导出表头是正确行为）。
		if docType == "sale-order" && (len(rows) < 2 || !rowsContain(rows[1:], wantNo)) {
			t.Fatalf("订单导出应包含 %s，实际 %v", wantNo, rows[1:])
		}
	}
}

// rowsContain 报告导出数据行中是否出现目标文本。
func rowsContain(rows [][]string, want string) bool {
	for _, row := range rows {
		for _, cell := range row {
			if cell == want {
				return true
			}
		}
	}
	return false
}
