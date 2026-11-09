//go:build integration

// 单据内核 adapter conformance（ADR 0018）：同一组 10 步生命周期断言跑
// 12 类单据（stock-in/out、purchase-order/in/return、sale-order/sale-out/sale-return、
// stock-move/check、finance-receipt/payment），锁住「内核重构前后 HTTP 行为等价」。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

type docConformanceCase struct {
	name      string
	path      string // /erp/stock-in
	docType   string // stock-in
	noPattern *regexp.Regexp
	// noKey 是创建响应里的单号 JSON 字段，空默认 "no"（采购订单用 orderNo）。
	noKey string
	// setup 可选：返回 (warehouseID, productID, extras)；nil 时用 setupStockEnv。
	setup  func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any)
	create func(env *testutil.IntegrationEnv, warehouseID, productID int64, extras map[string]any) map[string]any
	update func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64, extras map[string]any) map[string]any
}

func docConformanceCases() []docConformanceCase {
	return []docConformanceCase{
		{
			name:      "stock-in",
			path:      "/erp/stock-in",
			docType:   "stock-in",
			noPattern: stockInNoRe,
			create: func(env *testutil.IntegrationEnv, warehouseID, productID int64, _ map[string]any) map[string]any {
				return map[string]any{
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 10:00:00",
					"type":        50,
					"remark":      "conformance",
					"items":       []map[string]any{stockItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64, _ map[string]any) map[string]any {
				return map[string]any{
					"id":          docID,
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 11:00:00",
					"type":        99,
					"remark":      "conformance-updated",
					"items":       []map[string]any{stockItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "stock-out",
			path:      "/erp/stock-out",
			docType:   "stock-out",
			noPattern: stockOutNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID := setupStockEnv(t, env, token, tag)
				in := createStockInDoc(t, env, token, map[string]any{
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 09:00:00",
					"type":        99,
					"items":       []map[string]any{stockItemJSON(productID, "10", "1.00")},
				})
				if status, api := approveDoc(t, env, token, "stock-in", int64(in["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
					t.Fatalf("seed 入库审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
				}
				return warehouseID, productID, nil
			},
			create: func(env *testutil.IntegrationEnv, warehouseID, productID int64, _ map[string]any) map[string]any {
				return map[string]any{
					"warehouseId": warehouseID,
					"outTime":     env.Date() + " 10:00:00",
					"type":        99,
					"remark":      "conformance",
					"items":       []map[string]any{stockItemJSON(productID, "2", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64, _ map[string]any) map[string]any {
				return map[string]any{
					"id":          docID,
					"warehouseId": warehouseID,
					"outTime":     env.Date() + " 11:00:00",
					"type":        99,
					"remark":      "conformance-updated",
					"items":       []map[string]any{stockItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "purchase-in",
			path:      "/erp/purchase-in",
			docType:   "purchase-in",
			noPattern: purchaseInNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, tag)
				order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
					"supplierId": supplierID,
					"orderTime":  env.Date() + " 09:00:00",
					"items":      []map[string]any{purchaseItemJSON(productID, "100", "2.50")},
				})
				orderID := int64(order["id"].(float64))
				mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
				return warehouseID, productID, map[string]any{"supplierId": supplierID, "orderId": orderID}
			},
			create: func(env *testutil.IntegrationEnv, warehouseID, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"supplierId":  extras["supplierId"],
					"orderId":     extras["orderId"],
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 10:00:00",
					"remark":      "conformance",
					"items":       []map[string]any{purchaseItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":          docID,
					"supplierId":  extras["supplierId"],
					"orderId":     extras["orderId"],
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 11:00:00",
					"remark":      "conformance-updated",
					"items":       []map[string]any{purchaseItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "purchase-order",
			path:      "/erp/purchase-order",
			docType:   "purchase-order",
			noPattern: purchaseOrderNoRe,
			noKey:     "orderNo",
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, tag)
				return warehouseID, productID, map[string]any{"supplierId": supplierID}
			},
			create: func(env *testutil.IntegrationEnv, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"supplierId": extras["supplierId"],
					"orderTime":  env.Date() + " 10:00:00",
					"remark":     "conformance",
					"items":      []map[string]any{purchaseItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":         docID,
					"supplierId": extras["supplierId"],
					"orderTime":  env.Date() + " 11:00:00",
					"remark":     "conformance-updated",
					"items":      []map[string]any{purchaseItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "purchase-return",
			path:      "/erp/purchase-return",
			docType:   "purchase-return",
			noPattern: purchaseReturnNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, tag)
				order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
					"supplierId": supplierID,
					"orderTime":  env.Date() + " 09:00:00",
					"items":      []map[string]any{purchaseItemJSON(productID, "100", "2.50")},
				})
				orderID := int64(order["id"].(float64))
				mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
				in := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
					"supplierId":  supplierID,
					"orderId":     orderID,
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 10:00:00",
					"items":       []map[string]any{purchaseItemJSON(productID, "10", "2.50")},
				})
				inID := int64(in["id"].(float64))
				mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)
				return warehouseID, productID, map[string]any{"supplierId": supplierID, "inId": inID}
			},
			create: func(env *testutil.IntegrationEnv, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"supplierId": extras["supplierId"],
					"inId":       extras["inId"],
					"returnTime": env.Date() + " 12:00:00",
					"remark":     "conformance",
					"items":      []map[string]any{purchaseItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":         docID,
					"supplierId": extras["supplierId"],
					"inId":       extras["inId"],
					"returnTime": env.Date() + " 12:30:00",
					"remark":     "conformance-updated",
					"items":      []map[string]any{purchaseItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "sale-order",
			path:      "/erp/sale-order",
			docType:   "sale-order",
			noPattern: saleOrderNoRe,
			noKey:     "orderNo",
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, customerID := setupSaleEnv(t, env, token, tag)
				return warehouseID, productID, map[string]any{"customerId": customerID}
			},
			create: func(env *testutil.IntegrationEnv, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"customerId": extras["customerId"],
					"saleUserId": 0,
					"orderTime":  env.Date() + " 10:00:00",
					"remark":     "conformance",
					"items":      []map[string]any{saleItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":         docID,
					"customerId": extras["customerId"],
					"saleUserId": 0,
					"orderTime":  env.Date() + " 11:00:00",
					"remark":     "conformance-updated",
					"items":      []map[string]any{saleItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "sale-out",
			path:      "/erp/sale-out",
			docType:   "sale-out",
			noPattern: saleOutNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, customerID := setupSaleEnv(t, env, token, tag)
				padSaleStock(t, env, token, warehouseID, productID, "10")
				order := createSaleOrderDoc(t, env, token, map[string]any{
					"customerId": customerID,
					"orderTime":  env.Date() + " 09:00:00",
					"items":      []map[string]any{saleItemJSON(productID, "10", "3.00")},
				})
				orderID := int64(order["id"].(float64))
				mustApproveSale(t, env, token, "sale-order", orderID, 20)
				// 第二张已审核订单：编辑时把出库单从 order1 换挂到 order2，
				// 覆盖 beforeLock 存在的理由（编辑可换父单）。
				order2 := createSaleOrderDoc(t, env, token, map[string]any{
					"customerId": customerID,
					"orderTime":  env.Date() + " 09:30:00",
					"items":      []map[string]any{saleItemJSON(productID, "10", "3.00")},
				})
				order2ID := int64(order2["id"].(float64))
				mustApproveSale(t, env, token, "sale-order", order2ID, 20)
				return warehouseID, productID, map[string]any{
					"customerId": customerID,
					"orderId":    orderID,
					"orderId2":   order2ID,
				}
			},
			create: func(env *testutil.IntegrationEnv, warehouseID, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"customerId":  extras["customerId"],
					"orderId":     extras["orderId"],
					"warehouseId": warehouseID,
					"outTime":     env.Date() + " 10:00:00",
					"items":       []map[string]any{saleItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":          docID,
					"customerId":  extras["customerId"],
					"orderId":     extras["orderId2"],
					"warehouseId": warehouseID,
					"outTime":     env.Date() + " 11:00:00",
					"items":       []map[string]any{saleItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "sale-return",
			path:      "/erp/sale-return",
			docType:   "sale-return",
			noPattern: saleReturnNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, customerID := setupSaleEnv(t, env, token, tag)
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
				mustApproveSale(t, env, token, "sale-out", outID, 20)
				return warehouseID, productID, map[string]any{"customerId": customerID, "outId": outID}
			},
			create: func(env *testutil.IntegrationEnv, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"customerId": extras["customerId"],
					"outId":      extras["outId"],
					"returnTime": env.Date() + " 12:00:00",
					"items":      []map[string]any{saleItemJSON(productID, "5", "2.50")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":         docID,
					"customerId": extras["customerId"],
					"outId":      extras["outId"],
					"returnTime": env.Date() + " 12:30:00",
					"items":      []map[string]any{saleItemJSON(productID, "2", "3.00")},
				}
			},
		},
		{
			name:      "stock-move",
			path:      "/erp/stock-move",
			docType:   "stock-move",
			noPattern: stockMoveNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				fromID, productID := setupStockEnv(t, env, token, tag)
				to := createWarehouse(t, env, token, tag+"目标仓", nil)
				toID := int64(to["id"].(float64))
				seedWarehouseStock(t, env, token, fromID, productID, "10")
				return fromID, productID, map[string]any{"fromWarehouseId": fromID, "toWarehouseId": toID}
			},
			create: func(env *testutil.IntegrationEnv, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"fromWarehouseId": extras["fromWarehouseId"],
					"toWarehouseId":   extras["toWarehouseId"],
					"moveTime":        env.Date() + " 10:00:00",
					"items":           []map[string]any{moveItemJSON(productID, "5")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, _, productID int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":              docID,
					"fromWarehouseId": extras["fromWarehouseId"],
					"toWarehouseId":   extras["toWarehouseId"],
					"moveTime":        env.Date() + " 11:00:00",
					"items":           []map[string]any{moveItemJSON(productID, "2")},
				}
			},
		},
		{
			name:      "stock-check",
			path:      "/erp/stock-check",
			docType:   "stock-check",
			noPattern: stockCheckNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID := setupStockEnv(t, env, token, tag)
				seedWarehouseStock(t, env, token, warehouseID, productID, "10")
				return warehouseID, productID, nil
			},
			create: func(env *testutil.IntegrationEnv, warehouseID, productID int64, _ map[string]any) map[string]any {
				return map[string]any{
					"warehouseId": warehouseID,
					"checkTime":   env.Date() + " 10:00:00",
					"remark":      "conformance",
					"items":       []map[string]any{checkItemJSON(productID, "7")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, warehouseID, productID int64, _ map[string]any) map[string]any {
				return map[string]any{
					"id":          docID,
					"warehouseId": warehouseID,
					"checkTime":   env.Date() + " 11:00:00",
					"remark":      "conformance-updated",
					"items":       []map[string]any{checkItemJSON(productID, "8")},
				}
			},
		},
		{
			name:      "finance-receipt",
			path:      "/erp/finance-receipt",
			docType:   "finance-receipt",
			noPattern: financeReceiptNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, customerID := setupSaleEnv(t, env, token, tag)
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
				mustApproveSale(t, env, token, "sale-out", outID, 20)
				account := createFinanceAccountDoc(t, env, token, tag+"账户", nil)
				return warehouseID, productID, map[string]any{
					"customerId":    customerID,
					"accountId":     int64(account["id"].(float64)),
					"saleOutId":     outID,
					"receiptUserId": 1,
				}
			},
			create: func(env *testutil.IntegrationEnv, _, _ int64, extras map[string]any) map[string]any {
				return map[string]any{
					"customerId":    extras["customerId"],
					"accountId":     extras["accountId"],
					"receiptUserId": extras["receiptUserId"],
					"receiptTime":   env.Date() + " 12:00:00",
					"items":         []map[string]any{financeItemJSON("sale_out", extras["saleOutId"].(int64), "5.00")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, _, _ int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":            docID,
					"customerId":    extras["customerId"],
					"accountId":     extras["accountId"],
					"receiptUserId": extras["receiptUserId"],
					"receiptTime":   env.Date() + " 12:30:00",
					"items":         []map[string]any{financeItemJSON("sale_out", extras["saleOutId"].(int64), "2.00")},
				}
			},
		},
		{
			name:      "finance-payment",
			path:      "/erp/finance-payment",
			docType:   "finance-payment",
			noPattern: financePaymentNoRe,
			setup: func(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (int64, int64, map[string]any) {
				t.Helper()
				warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, tag)
				order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
					"supplierId": supplierID,
					"orderTime":  env.Date() + " 09:00:00",
					"items":      []map[string]any{purchaseItemJSON(productID, "100", "2.50")},
				})
				orderID := int64(order["id"].(float64))
				mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
				in := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
					"supplierId":  supplierID,
					"orderId":     orderID,
					"warehouseId": warehouseID,
					"inTime":      env.Date() + " 10:00:00",
					"items":       []map[string]any{purchaseItemJSON(productID, "10", "2.50")},
				})
				inID := int64(in["id"].(float64))
				mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)
				account := createFinanceAccountDoc(t, env, token, tag+"账户", nil)
				return warehouseID, productID, map[string]any{
					"supplierId":    supplierID,
					"accountId":     int64(account["id"].(float64)),
					"purchaseInId":  inID,
					"paymentUserId": 1,
				}
			},
			create: func(env *testutil.IntegrationEnv, _, _ int64, extras map[string]any) map[string]any {
				return map[string]any{
					"supplierId":    extras["supplierId"],
					"accountId":     extras["accountId"],
					"paymentUserId": extras["paymentUserId"],
					"paymentTime":   env.Date() + " 12:00:00",
					"items":         []map[string]any{financeItemJSON("purchase_in", extras["purchaseInId"].(int64), "5.00")},
				}
			},
			update: func(env *testutil.IntegrationEnv, docID, _, _ int64, extras map[string]any) map[string]any {
				return map[string]any{
					"id":            docID,
					"supplierId":    extras["supplierId"],
					"accountId":     extras["accountId"],
					"paymentUserId": extras["paymentUserId"],
					"paymentTime":   env.Date() + " 12:30:00",
					"items":         []map[string]any{financeItemJSON("purchase_in", extras["purchaseInId"].(int64), "2.00")},
				}
			},
		},
	}
}

func createConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, warehouseID, productID int64, extras map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, tc.path, token, tc.create(env, warehouseID, productID, extras))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

func updateConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, docID, warehouseID, productID int64, extras map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, tc.path, token, tc.update(env, docID, warehouseID, productID, extras))
}

func deleteConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, docID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodDelete, fmt.Sprintf("%s/%d", tc.path, docID), token, nil)
}

func getConformanceDoc(t *testing.T, env *testutil.IntegrationEnv, token string, tc docConformanceCase, docID int64) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodGet, fmt.Sprintf("%s/%d", tc.path, docID), token, nil)
}

// TestIntegrationDocConformance 同一组生命周期断言跑所有已接入内核的单据类型。
func TestIntegrationDocConformance(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	for _, tc := range docConformanceCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var (
				warehouseID, productID int64
				extras                 map[string]any
			)
			if tc.setup != nil {
				warehouseID, productID, extras = tc.setup(t, env, token, "一致性"+tc.name)
			} else {
				warehouseID, productID = setupStockEnv(t, env, token, "一致性"+tc.name)
			}

			// 1. create：未审核、单号格式。
			created := createConformanceDoc(t, env, token, tc, warehouseID, productID, extras)
			docID := int64(created["id"].(float64))
			if created["status"] != float64(10) {
				t.Fatalf("新建状态 = %v，期望 10", created["status"])
			}
			noKey := tc.noKey
			if noKey == "" {
				noKey = "no"
			}
			if !tc.noPattern.MatchString(fmt.Sprint(created[noKey])) {
				t.Fatalf("单号 %v 不符 %s", created[noKey], tc.noPattern)
			}

			// 2. get：详情可读。
			if status, api := getConformanceDoc(t, env, token, tc, docID); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 3. update（未审核）：成功。
			if status, api := updateConformanceDoc(t, env, token, tc, docID, warehouseID, productID, extras); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("未审核编辑：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 4. approve：成功。
			if status, api := approveDoc(t, env, token, tc.docType, docID, 20); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 5. update（已审核）：409。
			if status, api := updateConformanceDoc(t, env, token, tc, docID, warehouseID, productID, extras); status != http.StatusConflict || !strings.Contains(api.Msg, "已审核单据不可修改") {
				t.Fatalf("已审核编辑：HTTP %d msg %q，期望 409/已审核单据不可修改", status, api.Msg)
			}

			// 6. delete（已审核）：409。
			if status, api := deleteConformanceDoc(t, env, token, tc, docID); status != http.StatusConflict || !strings.Contains(api.Msg, "已审核单据不可删除") {
				t.Fatalf("已审核删除：HTTP %d msg %q，期望 409/已审核单据不可删除", status, api.Msg)
			}

			// 7. unapprove：成功。
			if status, api := approveDoc(t, env, token, tc.docType, docID, 10); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("反审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 8. update（反审核后）：成功。
			if status, api := updateConformanceDoc(t, env, token, tc, docID, warehouseID, productID, extras); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("反审核后编辑：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}

			// 9. delete（未审核）：成功，随后详情 404。
			if status, api := deleteConformanceDoc(t, env, token, tc, docID); status != http.StatusOK || api.Code != 0 {
				t.Fatalf("未审核删除：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}
			if status, api := getConformanceDoc(t, env, token, tc, docID); status != http.StatusNotFound {
				t.Fatalf("删除后详情：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
			}

			// 10. 幂等目标态：重复审核 200 且状态保持 20。
			idem := createConformanceDoc(t, env, token, tc, warehouseID, productID, extras)
			idemID := int64(idem["id"].(float64))
			for i := 0; i < 2; i++ {
				if status, api := approveDoc(t, env, token, tc.docType, idemID, 20); status != http.StatusOK || api.Code != 0 {
					t.Fatalf("第 %d 次审核：HTTP %d code %d msg %q", i+1, status, api.Code, api.Msg)
				}
			}
			status, api := getConformanceDoc(t, env, token, tc, idemID)
			if status != http.StatusOK || api.Code != 0 {
				t.Fatalf("幂等审核后详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
			}
			var got map[string]any
			if err := json.Unmarshal(api.Data, &got); err != nil {
				t.Fatalf("解析幂等详情: %v（data=%s）", err, string(api.Data))
			}
			if got["status"] != float64(20) {
				t.Fatalf("幂等审核后状态 = %v，期望 20", got["status"])
			}
		})
	}
}
