//go:build integration

// 采购三单 HTTP 集成测试（issue #91，docs/erp-mvp-spec.md §4.4 + §8.2 采购行）：
// 进度回写（只计已审核）、未审核占用、超量 409（订单余量与入库余量）、退货挂
// 已审核入库、反审核禁令、入库反审核反向流水回滚库存与进度、编号（CG/RK/RT
// 格式/伪造不采纳/按业务类型独立/租户隔离）、占用并发（恰一成一 409）、已审核
// 不可改删、删除释放占用、数据范围、租户隔离、普通角色 403、引用禁删、导出。
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

// 菜单/按钮 ID（与 internal/seed/seed.go 700 段一致：731–733 采购三单）。
const (
	erpPurchaseOrderMenuID  = int64(731)
	erpPurchaseInMenuID     = int64(732)
	erpPurchaseReturnMenuID = int64(733)
)

// CG/RK/RT 编号格式（spec §8.2：^(CG|RK|RT)\d{8}\d{4,}$）。
var (
	purchaseOrderNoRe  = regexp.MustCompile(`^CG\d{8}\d{4,}$`)
	purchaseInNoRe     = regexp.MustCompile(`^RK\d{8}\d{4,}$`)
	purchaseReturnNoRe = regexp.MustCompile(`^RT\d{8}\d{4,}$`)
)

// ---------- 用例级助手 ----------

// purchaseItemJSON 是采购明细 payload 的简写构造。
func purchaseItemJSON(productID int64, count string, price string) map[string]any {
	return map[string]any{"productId": productID, "count": count, "productPrice": price}
}

// setupPurchaseEnv 建仓库 + 产品 + 供应商（每个用例独立，名字唯一避免污染）。
func setupPurchaseEnv(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (warehouseID, productID, supplierID int64) {
	t.Helper()
	warehouse := createWarehouse(t, env, token, tag+"仓库", nil)
	category := createCategory(t, env, token, tag+"分类", nil)
	unit := createUnit(t, env, token, tag+"单位", nil)
	product := createProduct(t, env, token, tag+"产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	supplier := createSupplier(t, env, token, tag+"供应商", nil)
	return int64(warehouse["id"].(float64)), int64(product["id"].(float64)), int64(supplier["id"].(float64))
}

// createPurchaseDoc 创建采购单据并断言成功（docType: purchase-order / purchase-in /
// purchase-return），返回完整主表 JSON。
func createPurchaseDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/"+docType, token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建采购单据 %s：HTTP %d code %d msg %q（payload=%v）", docType, status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建%s响应: %v（data=%s）", docType, err, string(api.Data))
	}
	return row
}

// createPurchaseDocRaw 创建采购单据但不 Fatal（校验类用例断言 400/409 用）。
func createPurchaseDocRaw(t *testing.T, env *testutil.IntegrationEnv, token, docType string, payload map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/erp/"+docType, token, payload)
}

// approvePurchaseDoc 审核/反审核采购单据，返回 HTTP 状态与业务信封。
func approvePurchaseDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64, status int8) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, fmt.Sprintf("/erp/%s/update-status", docType), token, map[string]any{
		"id": docID, "status": status,
	})
}

// mustApprovePurchaseDoc 审核并断言成功。
func mustApprovePurchaseDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64) {
	t.Helper()
	if status, api := approvePurchaseDoc(t, env, token, docType, docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核%s %d：HTTP %d code %d msg %q", docType, docID, status, api.Code, api.Msg)
	}
}

// getPurchaseDetail 查询采购单据详情。
func getPurchaseDetail(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64) map[string]any {
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

// pagePurchaseDocs 采购单据分页，返回 list 与 total。
func pagePurchaseDocs(t *testing.T, env *testutil.IntegrationEnv, token, docType, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/erp/%s/page?pageNo=1&pageSize=50", docType)+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询%s分页%s：HTTP %d code %d msg %q", docType, query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析%s分页: %v（data=%s）", docType, err, string(api.Data))
	}
	return page.List, page.Total
}

// orderItemProgress 从订单详情明细中按产品取进度（inCount/returnCount 字符串）。
func orderItemProgress(t *testing.T, detail map[string]any, productID int64) (inCount, returnCount string) {
	t.Helper()
	items, ok := detail["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("订单详情缺明细：%v", detail)
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if int64(item["productId"].(float64)) == productID {
			return fmt.Sprint(item["inCount"]), fmt.Sprint(item["returnCount"])
		}
	}
	t.Fatalf("订单明细未找到产品 %d", productID)
	return "", ""
}

// createPurchaseOnce 在独立 goroutine 中发一次创建请求（并发占用用例）：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），结果带回主 goroutine 断言。
func createPurchaseOnce(env *testutil.IntegrationEnv, token, docType string, payload map[string]any) (int, testutil.APIResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, testutil.APIResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, env.APIPrefixURL("/erp/"+docType), bytes.NewReader(raw))
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

// ---------- 核心场景 ----------

// TestIntegrationPurchaseOrderLifecycle 订单生命周期：编号 CG（格式/伪造不采纳/
// 序列独立起始）→ 审核状态机（幂等目标态）→ 已审核不可改删 → 进度回写列默认
// 0 → 供应商/产品引用禁删（spec §4.4/§4.8/§4.9）。
func TestIntegrationPurchaseOrderLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "订单")

	// 创建：客户端伪造单号不采纳（payload 带 no 字段，DTO 上没有该字段）。
	doc := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"no":         "CG209901010001",
		"supplierId": supplierID,
		"orderTime":  env.Date() + " 09:00:00",
		"remark":     "首笔采购",
		"items": []map[string]any{
			purchaseItemJSON(productID, "10", "2.50"),
		},
	})
	docID := int64(doc["id"].(float64))
	if !purchaseOrderNoRe.MatchString(fmt.Sprint(doc["no"])) {
		t.Fatalf("订单号 %v 不符 ^CG\\d{8}\\d{4,}$", doc["no"])
	}
	if doc["no"] == "CG209901010001" {
		t.Fatalf("客户端伪造单号被采纳：%v", doc["no"])
	}
	if got := fmt.Sprint(doc["no"]); got != "CG"+env.DateKey()+"0001" {
		t.Fatalf("订单号 = %s，期望 CG%s0001（序列按业务类型独立发放）", got, env.DateKey())
	}
	if doc["status"] != float64(10) {
		t.Fatalf("新建订单应未审核(10)，实际 %v", doc["status"])
	}

	// 详情：进度回写列默认 0、行金额服务端计算。
	detail := getPurchaseDetail(t, env, token, "purchase-order", docID)
	if inCount, returnCount := orderItemProgress(t, detail, productID); inCount != "0.000" || returnCount != "0.000" {
		t.Fatalf("进度回写默认值 = %s/%s，期望 0.000/0.000", inCount, returnCount)
	}
	items := detail["items"].([]any)
	item0 := items[0].(map[string]any)
	if item0["totalPrice"] != "25.00" {
		t.Fatalf("行金额 = %v，期望 25.00（单价 2.50 × 数量 10，服务端权威）", item0["totalPrice"])
	}

	// 审核（幂等目标态）：重复审核/同态放行。
	mustApprovePurchaseDoc(t, env, token, "purchase-order", docID)
	if status, api := approvePurchaseDoc(t, env, token, "purchase-order", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("重复审核应幂等放行：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 已审核不可改删（spec §4.1）。
	status, api := env.Request(t, http.MethodPut, "/erp/purchase-order", token, map[string]any{
		"id": docID, "supplierId": supplierID, "orderTime": env.Date() + " 10:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "5", "2.50")},
	})
	if status != http.StatusConflict {
		t.Fatalf("修改已审核订单：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}
	status, _ = deleteByID(t, env, token, fmt.Sprintf("/erp/purchase-order/%d", docID))
	if status != http.StatusConflict {
		t.Fatalf("删除已审核订单：HTTP %d，期望 409", status)
	}

	// 未审核订单不可挂入库（409，spec §4.4：入库必挂已审核订单）。
	draft := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "2.50")},
	})
	draftID := int64(draft["id"].(float64))
	status, api = createPurchaseDocRaw(t, env, token, "purchase-in", map[string]any{
		"supplierId":  supplierID,
		"orderId":     draftID,
		"warehouseId": warehouseID,
		"inTime":      env.Date() + " 10:00:00",
		"items":       []map[string]any{purchaseItemJSON(productID, "1", "2.50")},
	})
	if status != http.StatusConflict {
		t.Fatalf("挂未审核订单创建入库：HTTP %d code %d，期望 409（msg=%q）", status, api.Code, api.Msg)
	}

	// 引用禁删：供应商被订单引用 409、产品被订单明细引用 409（spec §4.9）。
	status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/supplier/%d", supplierID))
	if status != http.StatusConflict {
		t.Fatalf("删除被订单引用的供应商：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}
	status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", productID))
	if status != http.StatusConflict {
		t.Fatalf("删除被订单明细引用的产品：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}

	// 删除未审核订单（无挂单）自然可行。
	status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/purchase-order/%d", draftID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除未审核订单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationPurchaseInOccupancyProgress 占用校验与进度回写（spec §4.4 +
// §8.2 采购行）：未审核入库占量、超量 409、删除释放占用、审核回写 in_count
// （只计已审核）、反审核禁令在并发两单同抢余量上的串行化另见并发用例。
func TestIntegrationPurchaseInOccupancyProgress(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "占用")

	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "10", "2.50")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)

	inTime := env.Date() + " 10:00:00"
	inPayload := func(count string) map[string]any {
		return map[string]any{
			"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
			"inTime": inTime, "remark": "分批入库",
			"items": []map[string]any{purchaseItemJSON(productID, count, "2.50")},
		}
	}

	// 未审核入库单 6：不动库存、不回写进度（spec §4.1/§4.4）。
	doc1 := createPurchaseDoc(t, env, token, "purchase-in", inPayload("6"))
	if !purchaseInNoRe.MatchString(fmt.Sprint(doc1["no"])) {
		t.Fatalf("入库单号 %v 不符 ^RK\\d{8}\\d{4,}$", doc1["no"])
	}
	if got := fmt.Sprint(doc1["no"]); got != "RK"+env.DateKey()+"0001" {
		t.Fatalf("入库单号 = %s，期望 RK%s0001（CG/RK 序列相互独立）", got, env.DateKey())
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "0.000" {
		t.Fatalf("未审核入库不应落账：库存 = %s，期望 0.000", got)
	}
	if inCount, _ := orderItemProgress(t, getPurchaseDetail(t, env, token, "purchase-order", orderID), productID); inCount != "0.000" {
		t.Fatalf("未审核入库不应回写进度：in_count = %s", inCount)
	}

	// 未审核单也占量：再入 5（6+5 > 10）→ 409。
	status, api := createPurchaseDocRaw(t, env, token, "purchase-in", inPayload("5"))
	if status != http.StatusConflict {
		t.Fatalf("未审核占用下再入 5：HTTP %d code %d，期望 409（msg=%q）", status, api.Code, api.Msg)
	}

	// 恰好贴满（6+4=10）放行。
	doc2 := createPurchaseDoc(t, env, token, "purchase-in", inPayload("4"))
	doc2ID := int64(doc2["id"].(float64))

	// 删除未审核入库单自然释放占用（spec §4.4）：删 doc2 后可再入 5。
	if status, api := deleteByID(t, env, token, fmt.Sprintf("/erp/purchase-in/%d", doc2ID)); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除未审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 入库必挂「供应商一致」：换供应商 400。
	status, api = createPurchaseDocRaw(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID + 999999, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": inTime, "items": []map[string]any{purchaseItemJSON(productID, "1", "2.50")},
	})
	if status == http.StatusOK && api.Code == 0 {
		t.Fatal("不存在的供应商应被拒绝")
	}

	// 入库余量 4 全量（doc1 未审核 6 仍占量）：审核落账 → 库存 4、in_count = 4
	//（只计已审核；未审核 doc1 不回写但占量）。
	doc3 := createPurchaseDoc(t, env, token, "purchase-in", inPayload("4"))
	doc3ID := int64(doc3["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-in", doc3ID)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "4.000" {
		t.Fatalf("审核入库后库存 = %s，期望 4.000", got)
	}
	if inCount, _ := orderItemProgress(t, getPurchaseDetail(t, env, token, "purchase-order", orderID), productID); inCount != "4.000" {
		t.Fatalf("审核入库后 in_count = %s，期望 4.000", inCount)
	}

	// 入库单已审核后不可改删（spec §4.1）。
	status, _ = deleteByID(t, env, token, fmt.Sprintf("/erp/purchase-in/%d", doc3ID))
	if status != http.StatusConflict {
		t.Fatalf("删除已审核入库单：HTTP %d，期望 409", status)
	}

	// 超订单余量（6 在途 + 4 已入 = 10 满额）：再入 0.001 → 409（超量）。
	status, api = createPurchaseDocRaw(t, env, token, "purchase-in", inPayload("0.001"))
	if status != http.StatusConflict {
		t.Fatalf("超订单余量入库：HTTP %d code %d，期望 409（msg=%q）", status, api.Code, api.Msg)
	}
}

// TestIntegrationPurchaseReturnFlow 退货链路（spec §4.4 + §8.2）：退货必挂已审核
// 入库（409）、仓库取自入库单、超入库量 409、未审核退货占量、审核减库存并回写
// return_count（只计已审核）。
func TestIntegrationPurchaseReturnFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "退货")
	category := createCategory(t, env, token, "退货二分类", nil)
	unit := createUnit(t, env, token, "退货二单位", nil)
	product2 := createProduct(t, env, token, "退货二产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	product2ID := int64(product2["id"].(float64))

	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "10", "3.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)

	inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "6", "3.00")},
	})
	inID := int64(inDoc["id"].(float64))
	// 未审核入库不可挂退货（409）。
	status, api := createPurchaseDocRaw(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 11:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "3.00")},
	})
	if status != http.StatusConflict {
		t.Fatalf("挂未审核入库创建退货：HTTP %d code %d，期望 409（msg=%q）", status, api.Code, api.Msg)
	}
	mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "6.000" {
		t.Fatalf("审核入库后库存 = %s，期望 6.000", got)
	}

	// 退货：仓库取自入库单（payload 塞仓库字段也不采纳——契约上无该字段）。
	retPayload := map[string]any{
		"supplierId": supplierID, "inId": inID, "warehouseId": warehouseID + 888888,
		"returnTime": env.Date() + " 11:00:00", "remark": "质量退回",
		"items": []map[string]any{purchaseItemJSON(productID, "2", "3.00")},
	}
	ret := createPurchaseDoc(t, env, token, "purchase-return", retPayload)
	retID := int64(ret["id"].(float64))
	if !purchaseReturnNoRe.MatchString(fmt.Sprint(ret["no"])) {
		t.Fatalf("退货单号 %v 不符 ^RT\\d{8}\\d{4,}$", ret["no"])
	}
	if got := fmt.Sprint(ret["no"]); got != "RT"+env.DateKey()+"0001" {
		t.Fatalf("退货单号 = %s，期望 RT%s0001（RT 序列独立）", got, env.DateKey())
	}
	retDetail := getPurchaseDetail(t, env, token, "purchase-return", retID)
	if int64(retDetail["warehouseId"].(float64)) != warehouseID {
		t.Fatalf("退货仓库 = %v，期望取自入库单 %d", retDetail["warehouseId"], warehouseID)
	}
	// 未审核退货不动库存、不回写进度。
	if got := getStockCount(t, env, token, productID, warehouseID); got != "6.000" {
		t.Fatalf("未审核退货不应落账：库存 = %s", got)
	}
	if _, returnCount := orderItemProgress(t, getPurchaseDetail(t, env, token, "purchase-order", orderID), productID); returnCount != "0.000" {
		t.Fatalf("未审核退货不应回写 return_count：%s", returnCount)
	}

	// 未审核退货占入库余量：再退 5（2+5 > 6）→ 409。
	status, api = createPurchaseDocRaw(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 11:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "5", "3.00")},
	})
	if status != http.StatusConflict {
		t.Fatalf("未审核退货占用下再退 5：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}

	// 退货产品不在入库单中 → 入库量为 0，409（超入库量）。
	status, api = createPurchaseDocRaw(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 11:00:00",
		"items": []map[string]any{purchaseItemJSON(product2ID, "1", "3.00")},
	})
	if status != http.StatusConflict {
		t.Fatalf("退货产品不在入库单：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}

	// 审核退货：减库存 6→4、return_count = 2（只计已审核）、in_count 保持 6。
	mustApprovePurchaseDoc(t, env, token, "purchase-return", retID)
	if got := getStockCount(t, env, token, productID, warehouseID); got != "4.000" {
		t.Fatalf("审核退货后库存 = %s，期望 4.000", got)
	}
	inCount, returnCount := orderItemProgress(t, getPurchaseDetail(t, env, token, "purchase-order", orderID), productID)
	if inCount != "6.000" || returnCount != "2.000" {
		t.Fatalf("审核退货后进度 = %s/%s，期望 6.000/2.000", inCount, returnCount)
	}

	// 已审核退货后：再退 3（6 − 2 = 4 余量，3 ≤ 4 放行）→ 验证余量公式。
	// 超入库量：已退 2 + 未审核 3 = 5 ≤ 6 放行（第二张退货单已建立占用）；
	// 再来一笔 2（5+2 > 6）→ 409。
	ret2 := createPurchaseDoc(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 12:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "3", "3.00")},
	})
	_ = ret2
	status, api = createPurchaseDocRaw(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 12:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "2", "3.00")},
	})
	if status != http.StatusConflict {
		t.Fatalf("超入库量退货：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}
}

// TestIntegrationPurchaseUnapproveBans 反审核禁令与回滚（spec §4.4 + §8.2）：
// 订单存在已审核入库 → 409；入库存在已审核退货 → 409；退货反审核恢复库存与
// 进度；入库反审核经反向流水回滚库存与进度（原流水保留、代数和=余额）。
func TestIntegrationPurchaseUnapproveBans(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "禁令")

	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "8", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)

	inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "8", "1.00")},
	})
	inID := int64(inDoc["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)

	// 订单反审核禁令：存在已审核入库 → 409。
	if status, api := approvePurchaseDoc(t, env, token, "purchase-order", orderID, 10); status != http.StatusConflict {
		t.Fatalf("有已审核入库时订单反审核：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}

	ret := createPurchaseDoc(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 11:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "3", "1.00")},
	})
	retID := int64(ret["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-return", retID)

	// 入库反审核禁令：存在已审核退货 → 409。
	if status, api := approvePurchaseDoc(t, env, token, "purchase-in", inID, 10); status != http.StatusConflict {
		t.Fatalf("有已审核退货时入库反审核：HTTP %d，期望 409（msg=%q）", status, api.Msg)
	}

	// 退货反审核：库存 8−3=5 → 8；return_count 3 → 0（重算）。
	if status, api := approvePurchaseDoc(t, env, token, "purchase-return", retID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("退货反审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "8.000" {
		t.Fatalf("退货反审核后库存 = %s，期望 8.000", got)
	}
	if _, returnCount := orderItemProgress(t, getPurchaseDetail(t, env, token, "purchase-order", orderID), productID); returnCount != "0.000" {
		t.Fatalf("退货反审核后 return_count = %s，期望 0.000（重算）", returnCount)
	}

	// 入库反审核：库存 8 → 0；in_count 8 → 0；purchase_in_CANCEL 反向流水、
	// 原流水保留（spec §4.3：余额 = 初始 + 流水代数和）。
	if status, api := approvePurchaseDoc(t, env, token, "purchase-in", inID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("入库反审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "0.000" {
		t.Fatalf("入库反审核后库存 = %s，期望 0.000", got)
	}
	if inCount, _ := orderItemProgress(t, getPurchaseDetail(t, env, token, "purchase-order", orderID), productID); inCount != "0.000" {
		t.Fatalf("入库反审核后 in_count = %s，期望 0.000（重算）", inCount)
	}
	records, _ := pageStockRecords(t, env, token, "")
	forward, cancel := 0, 0
	for _, rec := range records {
		switch rec["bizType"] {
		case "purchase_in":
			forward++
		case "purchase_in_CANCEL":
			cancel++
		}
	}
	if forward != 1 || cancel != 1 {
		t.Fatalf("入库流水应为正向 1 条 + 反向 1 条（原流水保留），实际 正%d 反%d", forward, cancel)
	}
	// 反审核后订单可以反审核（已审核挂单已解除）。
	if status, api := approvePurchaseDoc(t, env, token, "purchase-order", orderID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("解除挂单后订单反审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationPurchaseConcurrentOccupancy 占用并发（spec §8.2 采购行加分项）：
// 两张入库单同抢订单余量 5，恰一成一 409，无双重占用。
func TestIntegrationPurchaseConcurrentOccupancy(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "并发")

	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)

	payload := map[string]any{
		"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
	}
	// 两请求同时创建（各占 5，余量 5）：占用校验在订单行锁内执行，恰一成一 409。
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([][2]int, 0, 2) // [status, code]
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, api, err := createPurchaseOnce(env, token, "purchase-in", payload)
			if err != nil {
				return
			}
			mu.Lock()
			results = append(results, [2]int{status, api.Code})
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(results) != 2 {
		t.Fatalf("并发创建应有两个结果，实际 %d", len(results))
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
		t.Fatalf("并发抢余量应恰一成一 409，实际 成功 %d / 冲突 %d（results=%v）", okCount, conflictCount, results)
	}
	// 余额守恒：两张单最多占用 5。
	list, total := pagePurchaseDocs(t, env, token, "purchase-in", "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("并发后入库单应只有 1 张，实际 total=%d", total)
	}
}

// TestIntegrationPurchaseNumbering 编号：CG/RK/RT 前缀 + 业务时区日期键 + 序号
// 按 biz_type 独立自增（spec §4.8/§8.2 编号行）。
func TestIntegrationPurchaseNumbering(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "编号")

	dateKey := env.DateKey()
	order1 := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	if got := fmt.Sprint(order1["no"]); got != "CG"+dateKey+"0001" {
		t.Fatalf("首张订单号 = %s，期望 CG%s0001", got, dateKey)
	}
	order2 := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	if got := fmt.Sprint(order2["no"]); got != "CG"+dateKey+"0002" {
		t.Fatalf("第二张订单号 = %s，期望 CG%s0002（同日自增）", got, dateKey)
	}
	// 入库必挂已审核订单：先审核 order1 再创建入库单。
	mustApprovePurchaseDoc(t, env, token, "purchase-order", int64(order1["id"].(float64)))
	inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": int64(order1["id"].(float64)), "warehouseId": warehouseID,
		"inTime": env.Date() + " 09:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	if got := fmt.Sprint(inDoc["no"]); got != "RK"+dateKey+"0001" {
		t.Fatalf("入库单号 = %s，期望 RK%s0001（RK 序列独立于 CG）", got, dateKey)
	}
	mustApprovePurchaseDoc(t, env, token, "purchase-in", int64(inDoc["id"].(float64)))
	ret := createPurchaseDoc(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": int64(inDoc["id"].(float64)), "returnTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	if got := fmt.Sprint(ret["no"]); got != "RT"+dateKey+"0001" {
		t.Fatalf("退货单号 = %s，期望 RT%s0001（RT 序列独立）", got, dateKey)
	}
}

// TestIntegrationPurchaseTenantIsolation 租户隔离（spec §4.6/§8.2）：租户 2 列表
// 为空、跨租户详情/审核/删除 404、跨租户挂单 409（不泄露存在性）、序列按租户
// 独立发放。
func TestIntegrationPurchaseTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	_, productID, supplierID := setupPurchaseEnv(t, env, admin, "隔离")

	order := createPurchaseDoc(t, env, admin, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, admin, "purchase-order", orderID)

	tenant2 := createTenantByAPI(t, env, admin, "采购隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 列表互不可见。
	for _, docType := range []string{"purchase-order", "purchase-in", "purchase-return"} {
		_, total := pagePurchaseDocs(t, env, token2, docType, "")
		if total != 0 {
			t.Fatalf("租户 2 %s total=%d，期望 0", docType, total)
		}
	}
	// 跨租户详情/审核/删除一律 404。
	status, _ := getJSON(t, env, token2, fmt.Sprintf("/erp/purchase-order/%d", orderID))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 读租户 1 订单：HTTP %d，期望 404", status)
	}
	status, _ = approvePurchaseDoc(t, env, token2, "purchase-order", orderID, 20)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 审核租户 1 订单：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, token2, fmt.Sprintf("/erp/purchase-order/%d", orderID))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 订单：HTTP %d，期望 404", status)
	}
	// 跨租户挂单：入库挂租户 1 订单 → 409（租户内不存在）。
	wh2, prod2, sup2 := setupPurchaseEnv(t, env, token2, "隔离二")
	status, api := createPurchaseDocRaw(t, env, token2, "purchase-in", map[string]any{
		"supplierId": sup2, "orderId": orderID, "warehouseId": wh2,
		"inTime": env.Date() + " 09:00:00",
		"items":  []map[string]any{purchaseItemJSON(prod2, "1", "1.00")},
	})
	if status != http.StatusConflict {
		t.Fatalf("租户 2 挂租户 1 订单：HTTP %d code %d，期望 409（msg=%q）", status, api.Code, api.Msg)
	}
	// 序列按租户独立：租户 2 首张订单也是 0001。
	order2 := createPurchaseDoc(t, env, token2, "purchase-order", map[string]any{
		"supplierId": sup2, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(prod2, "1", "1.00")},
	})
	if got := fmt.Sprint(order2["no"]); got != "CG"+env.DateKey()+"0001" {
		t.Fatalf("租户 2 首单号 = %s，期望 CG%s0001（序列按租户隔离）", got, env.DateKey())
	}
}

// TestIntegrationPurchaseDataScope 数据范围（spec §4.10）：单据 owner = 创建人；
// 「仅本人」观察者只看到自己的单据，范围外详情/审核一律 404。
func TestIntegrationPurchaseDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	rndDept := int64(101)
	mktDept := int64(102)

	_, productID, supplierID := setupPurchaseEnv(t, env, admin, "范围")

	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "采购 owner 角色", "code": "erp_purchase_owner_role", "dataScope": 1,
		"menuIds": []int64{erpPurchaseOrderMenuID, 7311, 7312},
	})
	createUserByAPI(t, env, admin, "purchase_owner_rnd", rndDept, []int64{ownerRoleID})
	ownerToken := env.Login(t, "purchase_owner_rnd", "123456")
	order := createPurchaseDoc(t, env, ownerToken, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "3", "1.00")},
	})
	_ = order

	// 观察者：市场部、仅本人（dataScope=5），有全部订单按钮（含 approve 码，
	// 但对范围外单据仍必须 404——数据范围优先于功能权限）。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "采购观察者角色", "code": "erp_purchase_observer_role", "dataScope": 5,
		"menuIds": []int64{erpPurchaseOrderMenuID, 7311, 7312, 7313, 7314, 7315},
	})
	createUserByAPI(t, env, admin, "purchase_observer", mktDept, []int64{roleID})
	observerToken := env.Login(t, "purchase_observer", "123456")
	createPurchaseDoc(t, env, observerToken, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})

	// 仅本人：只看到自己的 1 张。
	list, total := pagePurchaseDocs(t, env, observerToken, "purchase-order", "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("仅本人观察者应只见自己的订单：total=%d list=%v", total, list)
	}
	// owner 是「全部」数据范围：看到两张。
	_, total = pagePurchaseDocs(t, env, ownerToken, "purchase-order", "")
	if total != 2 {
		t.Fatalf("owner（全部范围）应见 2 张订单：total=%d", total)
	}

	// 范围外详情/审核一律 404（不泄露存在性）：取 owner 最旧一行（自己的单据）。
	ownerDocs, _ := pagePurchaseDocs(t, env, ownerToken, "purchase-order", "")
	ownerDocID := int64(ownerDocs[len(ownerDocs)-1]["id"].(float64))
	status, _ := getJSON(t, env, observerToken, fmt.Sprintf("/erp/purchase-order/%d", ownerDocID))
	if status != http.StatusNotFound {
		t.Fatalf("观察者读范围外订单：HTTP %d，期望 404", status)
	}
	status, _ = approvePurchaseDoc(t, env, observerToken, "purchase-order", ownerDocID, 20)
	if status != http.StatusNotFound {
		t.Fatalf("观察者审核范围外订单：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, observerToken, fmt.Sprintf("/erp/purchase-order/%d", ownerDocID))
	if status != http.StatusNotFound {
		t.Fatalf("观察者删除范围外订单：HTTP %d，期望 404", status)
	}
}

// TestIntegrationPurchaseCommonRoleNoPermission 内置普通角色（role id=2，demo 用户）
// 默认无采购三单权限（spec §7：普通角色不授 ERP）。
func TestIntegrationPurchaseCommonRoleNoPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	_, _, supplierID := setupPurchaseEnv(t, env, admin, "禁止")

	common := env.Login(t, "demo", "123456")
	for _, path := range []string{
		"/erp/purchase-order/page?pageNo=1&pageSize=10",
		"/erp/purchase-in/page?pageNo=1&pageSize=10",
		"/erp/purchase-return/page?pageNo=1&pageSize=10",
	} {
		status, _ := env.Request(t, http.MethodGet, path, common, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色访问 %s：HTTP %d，期望 403", path, status)
		}
	}
	// 写路径同样 403（create/approve 权限码缺失）。
	status, _ := env.Request(t, http.MethodPost, "/erp/purchase-order", common, map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{},
	})
	if status != http.StatusForbidden {
		t.Fatalf("普通角色创建订单：HTTP %d，期望 403", status)
	}
}

// TestIntegrationPurchaseValidation 校验类：仓库必填（入库 400）、数量 > 0、
// 明细至少一条、状态枚举、供应商与挂单不一致 400（spec §4.2/§4.4）。
func TestIntegrationPurchaseValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "校验")

	// 订单：数量非正 / 明细为空 / 供应商缺失。
	status, _ := createPurchaseDocRaw(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "0", "1.00")},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("订单数量 0：HTTP %d，期望 400", status)
	}
	status, _ = createPurchaseDocRaw(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("订单空明细：HTTP %d，期望 400", status)
	}

	// 入库：仓库缺失 400（必填，spec §4.4）。
	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
	status, _ = createPurchaseDocRaw(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": orderID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("入库缺仓库：HTTP %d，期望 400", status)
	}

	// 入库：供应商与订单不一致 400。
	supplier2 := createSupplier(t, env, token, "校验二供应商", nil)
	supplier2ID := int64(supplier2["id"].(float64))
	status, _ = createPurchaseDocRaw(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplier2ID, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("入库供应商与订单不一致：HTTP %d，期望 400", status)
	}

	// 状态枚举非法 400。
	status, _ = env.Request(t, http.MethodPut, "/erp/purchase-order/update-status", token, map[string]any{
		"id": orderID, "status": 30,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("非法审核状态 30：HTTP %d，期望 400", status)
	}

	// 编辑不允许更换挂单订单（入库）。
	inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	inID := int64(inDoc["id"].(float64))
	order2 := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	order2ID := int64(order2["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", order2ID)
	status, _ = env.Request(t, http.MethodPut, "/erp/purchase-in", token, map[string]any{
		"id": inID, "supplierId": supplierID, "orderId": order2ID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("入库更换挂单订单：HTTP %d，期望 400", status)
	}
}

// TestIntegrationPurchaseExport 导出三单：列契约与行数据（spec §4.11）。
func TestIntegrationPurchaseExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "导出")

	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "4", "1.25")},
	})
	orderID := int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
	inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "4", "1.25")},
	})
	inID := int64(inDoc["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)
	createPurchaseDoc(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 11:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "1.25")},
	})

	header, rows := getExportRows(t, env, token, "/erp/purchase-order/export-excel")
	for _, col := range []string{"单号", "供应商", "下单时间", "状态", "备注", "创建时间"} {
		expectColumn(t, "采购订单导出", header, col)
	}
	if len(rows) != 1 {
		t.Fatalf("采购订单导出应 1 行，实际 %d", len(rows))
	}
	if rows[0][1] != "导出供应商" {
		t.Fatalf("导出供应商列 = %v，期望 导出供应商", rows[0][1])
	}

	header, rows = getExportRows(t, env, token, "/erp/purchase-in/export-excel")
	for _, col := range []string{"单号", "供应商", "采购订单号", "仓库", "入库时间", "单据金额", "已核销金额", "状态", "创建时间"} {
		expectColumn(t, "采购入库单导出", header, col)
	}
	if len(rows) != 1 {
		t.Fatalf("采购入库单导出应 1 行，实际 %d", len(rows))
	}
	if rows[0][5] != "5.00" {
		t.Fatalf("入库单据金额 = %v，期望 5.00（1.25 × 4）", rows[0][5])
	}
	if rows[0][6] != "0.00" {
		t.Fatalf("已核销金额默认 = %v，期望 0.00（payment_price 本票只落列）", rows[0][6])
	}

	header, rows = getExportRows(t, env, token, "/erp/purchase-return/export-excel")
	for _, col := range []string{"单号", "供应商", "采购入库单号", "仓库", "退货时间", "单据金额", "已核销退款", "状态", "创建时间"} {
		expectColumn(t, "采购退货单导出", header, col)
	}
	if len(rows) != 1 {
		t.Fatalf("采购退货单导出应 1 行，实际 %d", len(rows))
	}
	if rows[0][1] != "导出供应商" {
		t.Fatalf("导出供应商列 = %v，期望 导出供应商", rows[0][1])
	}
}
