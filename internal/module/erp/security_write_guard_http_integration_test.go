//go:build integration

// 安全 F1 回归（安全审查 run-4，issue #98 修复轮 2）：已审核单据不可修改/删除的
// 状态不变量必须绑定到写入语句，不能只在前置读取处校验。本文件用审计产物的
// 确定性锁编排复现两种竞态：
//
//  1. 并发 approve 先条件认领 10→20（持有文档行 X 锁）并卡在库存入账；
//     此时 update 请求若仍按事务外旧快照全字段 Save，会把状态覆盖回 10；
//  2. 同一窗口的 delete 请求若按主键软删，会删掉已审核行而留下库存流水。
//
// 修复后两种交错都收敛为：approve 提交成功（200），update/delete 在事务内
// 重读/条件写时看到 status=20 → 409，文档保持已审核、明细与流水不变。
package erp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// securityRaceResult 是竞态用例里单次 HTTP 调用的结果（不 Fatal，交主 goroutine 断言）。
type securityRaceResult struct {
	status int
	code   int
	msg    string
	err    error
}

// securityDoJSON 在 goroutine 中发起请求（env.Request 依赖 testing.T，不能跨 goroutine 使用）。
func securityDoJSON(env *testutil.IntegrationEnv, method, path, token string, body any) securityRaceResult {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, env.APIPrefixURL(path), bytes.NewReader(payload))
	if err != nil {
		return securityRaceResult{err: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		return securityRaceResult{err: err}
	}
	defer resp.Body.Close()
	var api testutil.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return securityRaceResult{status: resp.StatusCode, err: err}
	}
	return securityRaceResult{status: resp.StatusCode, code: api.Code, msg: api.Msg}
}

// lockStockRow 在独立事务里持有仓库×产品库存行的 X 锁：让 approve 在认领文档
// 状态之后卡在唯一入账入口的库存更新上，从而稳定暴露「已认领未提交」窗口。
func lockStockRow(t *testing.T, env *testutil.IntegrationEnv, productID, warehouseID int64) *gormTx {
	t.Helper()
	tx := tenant.Skip(env.DB).Begin()
	if tx.Error != nil {
		t.Fatalf("begin lock tx: %v", tx.Error)
	}
	var locked string
	if err := tx.Raw("SELECT `count` FROM `erp_stock` WHERE `tenant_id` = 1 AND `product_id` = ? AND `warehouse_id` = ? FOR UPDATE", productID, warehouseID).Scan(&locked).Error; err != nil {
		t.Fatalf("lock stock row: %v", err)
	}
	return &gormTx{tx: tx}
}

// gormTx 包一层便于在用例里明确表达「释放锁」。
type gormTx struct {
	tx *gorm.DB
}

func (g *gormTx) release(t *testing.T) {
	t.Helper()
	if err := g.tx.Rollback().Error; err != nil {
		t.Fatalf("release stock lock: %v", err)
	}
}

// TestIntegrationSecurityUpdateApprovedRace 复现 F1 的 update 变体，并断言语义：
// 先提交的 approve 赢；并发 update 必须 409，最终状态 20、流水 1 条、明细未被篡改。
func TestIntegrationSecurityUpdateApprovedRace(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "竞态更新")

	// 垫一笔库存，物化 erp_stock 行以便加锁。
	padSaleStock(t, env, token, warehouseID, productID, "5")

	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"inTime":      env.Date() + " 10:00:00",
		"remark":      "race-doc",
		"items":       []map[string]any{stockItemJSON(productID, "1", "1.00")},
	})
	docID := int64(doc["id"].(float64))

	lock := lockStockRow(t, env, productID, warehouseID)
	approveCh := make(chan securityRaceResult, 1)
	go func() {
		approveCh <- securityDoJSON(env, http.MethodPut, "/erp/stock-in/update-status", token, map[string]any{"id": docID, "status": 20})
	}()
	time.Sleep(500 * time.Millisecond)

	updateCh := make(chan securityRaceResult, 1)
	go func() {
		updateCh <- securityDoJSON(env, http.MethodPut, "/erp/stock-in", token, map[string]any{
			"id":          docID,
			"warehouseId": warehouseID,
			"inTime":      env.Date() + " 10:30:00",
			"remark":      "mutated-after-claim",
			"items":       []map[string]any{stockItemJSON(productID, "2", "1.00")},
		})
	}()
	time.Sleep(500 * time.Millisecond)

	lock.release(t)
	approve := <-approveCh
	update := <-updateCh
	t.Logf("approve=%+v update=%+v", approve, update)

	if approve.err != nil || approve.status != http.StatusOK || approve.code != 0 {
		t.Fatalf("并发审核未成功：%+v", approve)
	}
	if update.err != nil || update.status != http.StatusConflict {
		t.Fatalf("并发修改已审核单据：%+v，期望 HTTP 409", update)
	}

	var status int
	if err := tenant.Skip(env.DB).Raw("SELECT `status` FROM `erp_stock_in` WHERE `id` = ?", docID).Scan(&status).Error; err != nil {
		t.Fatalf("read doc status: %v", err)
	}
	var records int64
	if err := tenant.Skip(env.DB).Raw("SELECT COUNT(*) FROM `erp_stock_record` WHERE `tenant_id` = 1 AND `biz_type` = 'stock_in' AND `biz_id` = ?", docID).Scan(&records).Error; err != nil {
		t.Fatalf("count stock records: %v", err)
	}
	var items int64
	if err := tenant.Skip(env.DB).Raw("SELECT COUNT(*) FROM `erp_stock_in_item` WHERE `stock_in_id` = ? AND `deleted_at` IS NULL", docID).Scan(&items).Error; err != nil {
		t.Fatalf("count live items: %v", err)
	}
	var liveCount, remark, stock string
	if err := tenant.Skip(env.DB).Raw("SELECT `count` FROM `erp_stock_in_item` WHERE stock_in_id = ? AND `deleted_at` IS NULL", docID).Scan(&liveCount).Error; err != nil {
		t.Fatalf("read live item count: %v", err)
	}
	if err := tenant.Skip(env.DB).Raw("SELECT `remark` FROM `erp_stock_in` WHERE `id` = ?", docID).Scan(&remark).Error; err != nil {
		t.Fatalf("read doc remark: %v", err)
	}
	if err := tenant.Skip(env.DB).Raw("SELECT `count` FROM `erp_stock` WHERE `tenant_id` = 1 AND `product_id` = ? AND `warehouse_id` = ?", productID, warehouseID).Scan(&stock).Error; err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Logf("doc_status=%d stock_records=%d live_items=%d live_count=%s remark=%q stock=%s", status, records, items, liveCount, remark, stock)

	if status != 20 {
		t.Fatalf("并发修改把已审核状态覆盖了：status=%d，期望 20", status)
	}
	if records != 1 {
		t.Fatalf("库存流水 = %d 条，期望 1（审核只落一次账）", records)
	}
	if items != 1 || liveCount != "1.000" {
		t.Fatalf("明细被篡改：items=%d live_count=%s，期望 1 行 1.000", items, liveCount)
	}
	if remark != "race-doc" {
		t.Fatalf("单据字段被篡改：remark=%q，期望 race-doc", remark)
	}
	if stock != "6.000" {
		t.Fatalf("库存余额 = %s，期望 6.000（垫 5 + 审核 1）", stock)
	}
}

// TestIntegrationSecurityDeleteApprovedRace 复现 F1 的 delete 变体：approve 先
// 认领后，并发 delete 必须 409；最终行未软删、状态 20、流水 1 条。
func TestIntegrationSecurityDeleteApprovedRace(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "竞态删除")

	padSaleStock(t, env, token, warehouseID, productID, "5")

	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"inTime":      env.Date() + " 12:00:00",
		"remark":      "race-delete-doc",
		"items":       []map[string]any{stockItemJSON(productID, "1", "1.00")},
	})
	docID := int64(doc["id"].(float64))

	lock := lockStockRow(t, env, productID, warehouseID)
	approveCh := make(chan securityRaceResult, 1)
	go func() {
		approveCh <- securityDoJSON(env, http.MethodPut, "/erp/stock-in/update-status", token, map[string]any{"id": docID, "status": 20})
	}()
	time.Sleep(500 * time.Millisecond)

	deleteCh := make(chan securityRaceResult, 1)
	go func() {
		deleteCh <- securityDoJSON(env, http.MethodDelete, "/erp/stock-in/"+strconv.FormatInt(docID, 10), token, nil)
	}()
	time.Sleep(500 * time.Millisecond)

	lock.release(t)
	approve := <-approveCh
	del := <-deleteCh
	t.Logf("approve=%+v delete=%+v", approve, del)

	if approve.err != nil || approve.status != http.StatusOK || approve.code != 0 {
		t.Fatalf("并发审核未成功：%+v", approve)
	}
	if del.err != nil || del.status != http.StatusConflict {
		t.Fatalf("并发删除已审核单据：%+v，期望 HTTP 409", del)
	}

	var status int
	var deleted any
	if err := tenant.Skip(env.DB).Raw("SELECT `status`, `deleted_at` FROM `erp_stock_in` WHERE `id` = ?", docID).Row().Scan(&status, &deleted); err != nil {
		t.Fatalf("read doc: %v", err)
	}
	var records int64
	if err := tenant.Skip(env.DB).Raw("SELECT COUNT(*) FROM `erp_stock_record` WHERE `tenant_id` = 1 AND `biz_type` = 'stock_in' AND `biz_id` = ?", docID).Scan(&records).Error; err != nil {
		t.Fatalf("count stock records: %v", err)
	}
	t.Logf("doc_status=%d deleted_at=%v stock_records=%d", status, deleted, records)

	if status != 20 {
		t.Fatalf("已审核单据状态 = %d，期望 20", status)
	}
	if deleted != nil {
		t.Fatalf("已审核单据被并发软删：deleted_at=%v", deleted)
	}
	if records != 1 {
		t.Fatalf("库存流水 = %d 条，期望 1", records)
	}
}

// mustApproveStockDoc 审核杂项库存单据（stock-in/stock-out/stock-move/stock-check）。
func mustApproveStockDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64) {
	t.Helper()
	if status, api := approveDoc(t, env, token, docType, docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("%s %d 审核：HTTP %d code %d msg %q", docType, docID, status, api.Code, api.Msg)
	}
}

// assertApprovedUpdateDeleteRejected 断言已审核单据的 update 与 delete 都是 409，
// 且两者均未生效（详情仍可查、status 仍为 20）。
func assertApprovedUpdateDeleteRejected(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64, updateBody map[string]any) {
	t.Helper()
	status, api := env.Request(t, http.MethodPut, "/erp/"+docType, token, updateBody)
	if status != http.StatusConflict {
		t.Fatalf("%s %d 已审核后修改：HTTP %d code %d msg %q，期望 409", docType, docID, status, api.Code, api.Msg)
	}
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/%s/%d", docType, docID))
	if delStatus != http.StatusConflict {
		t.Fatalf("%s %d 已审核后删除：HTTP %d code %d msg %q，期望 409", docType, docID, delStatus, delAPI.Code, delAPI.Msg)
	}
	detailStatus, detailAPI := getJSON(t, env, token, fmt.Sprintf("/erp/%s/%d", docType, docID))
	if detailStatus != http.StatusOK || detailAPI.Code != 0 {
		t.Fatalf("%s %d 在 409 后应仍存在：HTTP %d code %d msg %q", docType, docID, detailStatus, detailAPI.Code, detailAPI.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(detailAPI.Data, &detail); err != nil {
		t.Fatalf("解析 %s %d 详情: %v", docType, docID, err)
	}
	if fmt.Sprint(detail["status"]) != "20" {
		t.Fatalf("%s %d 状态 = %v，期望 20", docType, docID, detail["status"])
	}
}

// TestIntegrationApprovedDocWritesRejected 对 12 类单据逐类建单并审核，断言
// update 与 delete 均 409 且未生效（安全 F1 的行为面回归；并发写入窗口由
// TestIntegrationSecurity*ApprovedRace 覆盖）。
func TestIntegrationApprovedDocWritesRejected(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	t.Run("stock-in", func(t *testing.T) {
		warehouseID, productID := setupStockEnv(t, env, token, "扫入库")
		doc := createStockInDoc(t, env, token, map[string]any{
			"warehouseId": warehouseID,
			"inTime":      env.Date() + " 09:00:00",
			"remark":      "扫入库",
			"items":       []map[string]any{stockItemJSON(productID, "1", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApproveStockDoc(t, env, token, "stock-in", id)
		assertApprovedUpdateDeleteRejected(t, env, token, "stock-in", id, map[string]any{
			"id": id, "warehouseId": warehouseID, "inTime": env.Date() + " 09:30:00",
			"remark": "篡改", "items": []map[string]any{stockItemJSON(productID, "2", "1.00")},
		})
	})

	t.Run("stock-out", func(t *testing.T) {
		warehouseID, productID := setupStockEnv(t, env, token, "扫出库")
		padSaleStock(t, env, token, warehouseID, productID, "5")
		doc := createStockOutDoc(t, env, token, map[string]any{
			"warehouseId": warehouseID,
			"outTime":     env.Date() + " 09:00:00",
			"remark":      "扫出库",
			"items":       []map[string]any{stockItemJSON(productID, "1", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApproveStockDoc(t, env, token, "stock-out", id)
		assertApprovedUpdateDeleteRejected(t, env, token, "stock-out", id, map[string]any{
			"id": id, "warehouseId": warehouseID, "outTime": env.Date() + " 09:30:00",
			"remark": "篡改", "items": []map[string]any{stockItemJSON(productID, "2", "1.00")},
		})
	})

	t.Run("purchase-order", func(t *testing.T) {
		_, productID, supplierID := setupPurchaseEnv(t, env, token, "扫采购订单")
		doc := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
			"supplierId": supplierID,
			"orderTime":  env.Date() + " 09:00:00",
			"remark":     "扫采购订单",
			"items":      []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-order", id)
		assertApprovedUpdateDeleteRejected(t, env, token, "purchase-order", id, map[string]any{
			"id": id, "supplierId": supplierID, "orderTime": env.Date() + " 09:30:00",
			"remark": "篡改", "items": []map[string]any{purchaseItemJSON(productID, "6", "1.00")},
		})
	})

	t.Run("purchase-in", func(t *testing.T) {
		warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "扫采购入库")
		order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
			"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
			"items": []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
		doc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
			"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
			"inTime": env.Date() + " 10:00:00",
			"items":  []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-in", id)
		assertApprovedUpdateDeleteRejected(t, env, token, "purchase-in", id, map[string]any{
			"id": id, "supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
			"inTime": env.Date() + " 10:30:00",
			"items":  []map[string]any{purchaseItemJSON(productID, "2", "1.00")},
		})
	})

	t.Run("purchase-return", func(t *testing.T) {
		warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "扫采购退货")
		order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
			"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
			"items": []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
		inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
			"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
			"inTime": env.Date() + " 10:00:00",
			"items":  []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
		})
		inID := int64(inDoc["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)
		doc := createPurchaseDoc(t, env, token, "purchase-return", map[string]any{
			"supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 11:00:00",
			"items": []map[string]any{purchaseItemJSON(productID, "1", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-return", id)
		assertApprovedUpdateDeleteRejected(t, env, token, "purchase-return", id, map[string]any{
			"id": id, "supplierId": supplierID, "inId": inID, "returnTime": env.Date() + " 11:30:00",
			"items": []map[string]any{purchaseItemJSON(productID, "2", "1.00")},
		})
	})

	t.Run("sale-order", func(t *testing.T) {
		_, productID, customerID := setupSaleEnv(t, env, token, "扫销售订单")
		doc := createSaleOrderDoc(t, env, token, map[string]any{
			"customerId": customerID,
			"orderTime":  env.Date() + " 09:00:00",
			"remark":     "扫销售订单",
			"items":      []map[string]any{saleItemJSON(productID, "5", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApproveSale(t, env, token, "sale-order", id, 20)
		assertApprovedUpdateDeleteRejected(t, env, token, "sale-order", id, map[string]any{
			"id": id, "customerId": customerID, "orderTime": env.Date() + " 09:30:00",
			"remark": "篡改", "items": []map[string]any{saleItemJSON(productID, "6", "1.00")},
		})
	})

	t.Run("sale-out", func(t *testing.T) {
		warehouseID, productID, customerID := setupSaleEnv(t, env, token, "扫销售出库")
		padSaleStock(t, env, token, warehouseID, productID, "5")
		order := createSaleOrderDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
			"items": []map[string]any{saleItemJSON(productID, "5", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApproveSale(t, env, token, "sale-order", orderID, 20)
		doc := createSaleOutDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
			"outTime": env.Date() + " 10:00:00",
			"items":   []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApproveSale(t, env, token, "sale-out", id, 20)
		assertApprovedUpdateDeleteRejected(t, env, token, "sale-out", id, map[string]any{
			"id": id, "customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
			"outTime": env.Date() + " 10:30:00",
			"items":   []map[string]any{saleItemJSON(productID, "2", "1.00")},
		})
	})

	t.Run("sale-return", func(t *testing.T) {
		warehouseID, productID, customerID := setupSaleEnv(t, env, token, "扫销售退货")
		padSaleStock(t, env, token, warehouseID, productID, "5")
		order := createSaleOrderDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
			"items": []map[string]any{saleItemJSON(productID, "5", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApproveSale(t, env, token, "sale-order", orderID, 20)
		out := createSaleOutDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
			"outTime": env.Date() + " 10:00:00",
			"items":   []map[string]any{saleItemJSON(productID, "5", "1.00")},
		})
		outID := int64(out["id"].(float64))
		mustApproveSale(t, env, token, "sale-out", outID, 20)
		doc := createSaleReturnDoc(t, env, token, map[string]any{
			"customerId": customerID, "outId": outID, "returnTime": env.Date() + " 11:00:00",
			"items": []map[string]any{saleItemJSON(productID, "1", "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApproveSale(t, env, token, "sale-return", id, 20)
		assertApprovedUpdateDeleteRejected(t, env, token, "sale-return", id, map[string]any{
			"id": id, "customerId": customerID, "outId": outID, "returnTime": env.Date() + " 11:30:00",
			"items": []map[string]any{saleItemJSON(productID, "2", "1.00")},
		})
	})

	t.Run("stock-move", func(t *testing.T) {
		fromWarehouseID, productID := setupStockEnv(t, env, token, "扫调拨")
		toWarehouseID := int64(createWarehouse(t, env, token, "扫调拨目标仓库", nil)["id"].(float64))
		seedWarehouseStock(t, env, token, fromWarehouseID, productID, "5")
		doc := createStockMoveDoc(t, env, token, map[string]any{
			"fromWarehouseId": fromWarehouseID,
			"toWarehouseId":   toWarehouseID,
			"moveTime":        env.Date() + " 09:00:00",
			"items":           []map[string]any{moveItemJSON(productID, "1")},
		})
		id := int64(doc["id"].(float64))
		mustApproveStockDoc(t, env, token, "stock-move", id)
		assertApprovedUpdateDeleteRejected(t, env, token, "stock-move", id, map[string]any{
			"id": id, "fromWarehouseId": fromWarehouseID, "toWarehouseId": toWarehouseID,
			"moveTime": env.Date() + " 09:30:00",
			"items":    []map[string]any{moveItemJSON(productID, "2")},
		})
	})

	t.Run("stock-check", func(t *testing.T) {
		warehouseID, productID := setupStockEnv(t, env, token, "扫盘点")
		seedWarehouseStock(t, env, token, warehouseID, productID, "5")
		doc := createStockCheckDoc(t, env, token, map[string]any{
			"warehouseId": warehouseID,
			"checkTime":   env.Date() + " 09:00:00",
			"remark":      "扫盘点",
			"items":       []map[string]any{checkItemJSON(productID, "3")},
		})
		id := int64(doc["id"].(float64))
		mustApproveStockDoc(t, env, token, "stock-check", id)
		assertApprovedUpdateDeleteRejected(t, env, token, "stock-check", id, map[string]any{
			"id": id, "warehouseId": warehouseID, "checkTime": env.Date() + " 09:30:00",
			"remark": "篡改", "items": []map[string]any{checkItemJSON(productID, "4")},
		})
	})

	t.Run("finance-receipt", func(t *testing.T) {
		warehouseID, productID, customerID := setupSaleEnv(t, env, token, "扫收款")
		padSaleStock(t, env, token, warehouseID, productID, "5")
		order := createSaleOrderDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
			"items": []map[string]any{saleItemJSON(productID, "5", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApproveSale(t, env, token, "sale-order", orderID, 20)
		out := createSaleOutDoc(t, env, token, map[string]any{
			"customerId": customerID, "orderId": orderID, "warehouseId": warehouseID,
			"outTime": env.Date() + " 10:00:00",
			"items":   []map[string]any{saleItemJSON(productID, "5", "1.00")},
		})
		outID := int64(out["id"].(float64))
		mustApproveSale(t, env, token, "sale-out", outID, 20)
		accountID := int64(createFinanceAccountDoc(t, env, token, "扫收款账户", nil)["id"].(float64))
		doc := createFinanceDoc(t, env, token, "finance-receipt", map[string]any{
			"customerId": customerID, "accountId": accountID, "receiptUserId": 1,
			"receiptTime": env.Date() + " 12:00:00",
			"items":       []map[string]any{financeItemJSON("sale_out", outID, "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApproveFinance(t, env, token, "finance-receipt", id, 20)
		assertApprovedUpdateDeleteRejected(t, env, token, "finance-receipt", id, map[string]any{
			"id": id, "customerId": customerID, "accountId": accountID, "receiptUserId": 1,
			"receiptTime": env.Date() + " 12:30:00",
			"items":       []map[string]any{financeItemJSON("sale_out", outID, "2.00")},
		})
	})

	t.Run("finance-payment", func(t *testing.T) {
		warehouseID, productID, supplierID := setupPurchaseEnv(t, env, token, "扫付款")
		order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
			"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
			"items": []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
		})
		orderID := int64(order["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
		inDoc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
			"supplierId": supplierID, "orderId": orderID, "warehouseId": warehouseID,
			"inTime": env.Date() + " 10:00:00",
			"items":  []map[string]any{purchaseItemJSON(productID, "5", "1.00")},
		})
		inID := int64(inDoc["id"].(float64))
		mustApprovePurchaseDoc(t, env, token, "purchase-in", inID)
		accountID := int64(createFinanceAccountDoc(t, env, token, "扫付款账户", nil)["id"].(float64))
		doc := createFinanceDoc(t, env, token, "finance-payment", map[string]any{
			"supplierId": supplierID, "accountId": accountID, "paymentUserId": 1,
			"paymentTime": env.Date() + " 12:00:00",
			"items":       []map[string]any{financeItemJSON("purchase_in", inID, "1.00")},
		})
		id := int64(doc["id"].(float64))
		mustApproveFinance(t, env, token, "finance-payment", id, 20)
		assertApprovedUpdateDeleteRejected(t, env, token, "finance-payment", id, map[string]any{
			"id": id, "supplierId": supplierID, "accountId": accountID, "paymentUserId": 1,
			"paymentTime": env.Date() + " 12:30:00",
			"items":       []map[string]any{financeItemJSON("purchase_in", inID, "2.00")},
		})
	})
}
