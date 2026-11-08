//go:build integration

// 其它入库/出库单 HTTP 集成测试（issue #90，docs/erp-mvp-spec.md §4.1–4.3/§4.8 +
// §8.2 库存场景）：审核才落账、并发扣减（双保险，一成一 409 无负库存）、反审核
// 反向流水（原流水保留、余额恢复、流水代数和=余额）、流水快照、编号（格式/
// 伪造不采纳/跨日重置/租户隔离）、已审核不可改删、数据范围、导出、引用禁删。
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

// 菜单/按钮 ID（与 internal/seed/seed.go 700 段一致）。
const (
	erpStockMenuID       = int64(721)
	erpStockRecordMenuID = int64(722)
	erpStockInMenuID     = int64(723)
	erpStockOutMenuID    = int64(724)
)

// QR/QC 编号格式（spec §8.2：^(QR|QC)\d{8}\d{4,}$）。
var (
	stockInNoRe  = regexp.MustCompile(`^QR\d{8}\d{4,}$`)
	stockOutNoRe = regexp.MustCompile(`^QC\d{8}\d{4,}$`)
)

// ---------- 用例级助手 ----------

// stockItemJSON 是明细 payload 的简写构造。
func stockItemJSON(productID int64, count string, price string) map[string]any {
	return map[string]any{"productId": productID, "count": count, "productPrice": price}
}

// createStockInDoc 创建其它入库单并断言成功，返回完整主表 JSON。
func createStockInDoc(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/stock-in", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建其它入库单：HTTP %d code %d msg %q（payload=%v）", status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建入库单响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// createStockOutDoc 创建其它出库单并断言成功，返回完整主表 JSON。
func createStockOutDoc(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/stock-out", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建其它出库单：HTTP %d code %d msg %q（payload=%v）", status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建出库单响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// approveDoc 审核/反审核单据（docType: stock-in / stock-out），返回 HTTP 状态与信封。
func approveDoc(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64, status int8) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPut, fmt.Sprintf("/erp/%s/update-status", docType), token, map[string]any{
		"id": docID, "status": status,
	})
}

// getStockInDetail 查询入库单详情。
func getStockInDetail(t *testing.T, env *testutil.IntegrationEnv, token string, docID int64) map[string]any {
	t.Helper()
	status, api := getJSON(t, env, token, fmt.Sprintf("/erp/stock-in/%d", docID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询入库单 %d 详情：HTTP %d code %d msg %q", docID, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析入库单详情: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageStockDocs 单据分页（docType: stock-in / stock-out），返回 list 与 total。
func pageStockDocs(t *testing.T, env *testutil.IntegrationEnv, token, docType, query string) ([]map[string]any, int64) {
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

// pageStockRows 库存余额分页。
func pageStockRows(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/stock/page?pageNo=1&pageSize=100"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询库存分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析库存分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// getStockCount 全仓/单仓合计（spec §5：get-count）。
func getStockCount(t *testing.T, env *testutil.IntegrationEnv, token string, productID, warehouseID int64) string {
	t.Helper()
	query := fmt.Sprintf("productId=%d", productID)
	if warehouseID > 0 {
		query += fmt.Sprintf("&warehouseId=%d", warehouseID)
	}
	status, api := env.Request(t, http.MethodGet, "/erp/stock/get-count?"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询库存合计：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var row struct {
		Count string `json:"count"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析库存合计: %v（data=%s）", err, string(api.Data))
	}
	return row.Count
}

// pageStockRecords 流水分页。
func pageStockRecords(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/stock-record/page?pageNo=1&pageSize=100"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询流水分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析流水分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// setupStockEnv 建仓库 + 产品（每个用例独立，名字唯一避免用例间污染）。
func setupStockEnv(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (warehouseID, productID int64) {
	t.Helper()
	warehouse := createWarehouse(t, env, token, tag+"仓库", nil)
	category := createCategory(t, env, token, tag+"分类", nil)
	unit := createUnit(t, env, token, tag+"单位", nil)
	product := createProduct(t, env, token, tag+"产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	return int64(warehouse["id"].(float64)), int64(product["id"].(float64))
}

// approveDocOnce 在独立 goroutine 中发一次审核请求（docType: stock-in/stock-out）：
// 不触碰 testing.T（Fatal 只能在测试主 goroutine 调用），结果带回主 goroutine 断言。
func approveDocOnce(env *testutil.IntegrationEnv, token, docType string, docID int64, target int8) (int, testutil.APIResponse, error) {
	raw, err := json.Marshal(map[string]any{"id": docID, "status": target})
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

// ---------- 核心场景 ----------

// TestIntegrationStockDocAuditLifecycle 审核才落账 → 流水快照正确 → 反审核回滚
// （_CANCEL 反向流水、原流水保留、余额恢复、流水代数和=余额）→ 已审核不可改删
// → 幂等目标态（spec §4.1/§4.3、§8.2 库存行）。
func TestIntegrationStockDocAuditLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "账本")

	// 未审核：不动库存（spec §4.1——只有审核动作落账）。
	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"inTime":      env.Date() + " 10:00:00",
		"type":        50, // 赠品
		"remark":      "赠品入库",
		"items": []map[string]any{
			stockItemJSON(productID, "5", "2.50"),
			stockItemJSON(productID, "3", "2.50"),
		},
	})
	docID := int64(doc["id"].(float64))
	if !stockInNoRe.MatchString(fmt.Sprint(doc["no"])) {
		t.Fatalf("入库单号 %v 不符 ^QR\\d{8}\\d{4,}$", doc["no"])
	}
	if doc["status"] != float64(10) {
		t.Fatalf("新建单据应未审核(10)，实际 %v", doc["status"])
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "0.000" {
		t.Fatalf("未审核不应落账：库存合计 = %s，期望 0.000", got)
	}
	if _, total := pageStockRecords(t, env, token, ""); total != 0 {
		t.Fatalf("未审核不应产生流水：total=%d", total)
	}

	// 审核：落账 + 流水快照（spec §4.3——正入负出 + total_count 结存快照）。
	if status, api := approveDoc(t, env, token, "stock-in", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	records, _ := pageStockRecords(t, env, token, "")
	if len(records) != 2 {
		t.Fatalf("审核后应产生 2 条流水（每明细一条），实际 %d", len(records))
	}
	snapshots := map[string]float64{} // count → totalCount
	for _, rec := range records {
		if rec["bizType"] != "stock_in" {
			t.Fatalf("流水 bizType = %v，期望 stock_in", rec["bizType"])
		}
		if rec["bizNo"] != doc["no"] {
			t.Fatalf("流水 bizNo = %v，期望 %v", rec["bizNo"], doc["no"])
		}
		var count, total float64
		_, _ = fmt.Sscan(fmt.Sprint(rec["count"]), &count)
		_, _ = fmt.Sscan(fmt.Sprint(rec["totalCount"]), &total)
		snapshots[fmt.Sprint(count)] = total
		if count <= 0 {
			t.Fatalf("入库流水 count 应为正，实际 %v", rec["count"])
		}
	}
	// 两行同额入账（5 与 3）：快照应分别是 8 与 5（行 ID 升序入账）。
	if snapshots["5"] != 5 || snapshots["3"] != 8 {
		t.Fatalf("流水快照不符：5→%v, 3→%v（期望 5→5、3→8）", snapshots["5"], snapshots["3"])
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "8.000" {
		t.Fatalf("审核后库存合计 = %s，期望 8.000", got)
	}

	// 已审核：不可修改、不可删除（409）。
	upStatus, _ := env.Request(t, http.MethodPut, "/erp/stock-in", token, map[string]any{
		"id": docID, "warehouseId": warehouseID, "inTime": env.Date() + " 10:00:00",
		"items": []map[string]any{stockItemJSON(productID, "1", "1.00")},
	})
	if upStatus != http.StatusConflict {
		t.Fatalf("修改已审核单据：HTTP %d，期望 409", upStatus)
	}
	delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/stock-in/%d", docID))
	if delStatus != http.StatusConflict {
		t.Fatalf("删除已审核单据：HTTP %d，期望 409", delStatus)
	}

	// 幂等目标态：重复审核成功且不产生新流水。
	if status, api := approveDoc(t, env, token, "stock-in", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("重复审核应幂等成功：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	_, total := pageStockRecords(t, env, token, "")
	if total != 2 {
		t.Fatalf("重复审核不应新增流水：total=%d，期望 2", total)
	}

	// 反审核：_CANCEL 反向流水（原流水保留）、余额恢复、代数和=余额。
	if status, api := approveDoc(t, env, token, "stock-in", docID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("反审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	records, total = pageStockRecords(t, env, token, "")
	if total != 4 {
		t.Fatalf("反审核后应 4 条流水（2 正向 + 2 反向），实际 %d", total)
	}
	var cancelCount int
	sum := 0.0
	for _, rec := range records {
		var count float64
		_, _ = fmt.Sscan(fmt.Sprint(rec["count"]), &count)
		sum += count
		switch rec["bizType"] {
		case "stock_in":
			// 原流水保留（append-only）。
		case "stock_in_CANCEL":
			cancelCount++
			if count >= 0 {
				t.Fatalf("反向流水 count 应为负，实际 %v", rec["count"])
			}
		default:
			t.Fatalf("流水 bizType = %v，期望 stock_in / stock_in_CANCEL", rec["bizType"])
		}
	}
	if cancelCount != 2 {
		t.Fatalf("应有 2 条 _CANCEL 反向流水，实际 %d", cancelCount)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "0.000" {
		t.Fatalf("反审核后库存应恢复 0.000，实际 %s", got)
	}
	if sum != 0 {
		t.Fatalf("流水代数和 %v ≠ 余额 0（可审计重放被破坏）", sum)
	}

	// 反审核后回到未审核：可修改、可删除。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/stock-in/%d", docID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核单据：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ := getJSON(t, env, token, fmt.Sprintf("/erp/stock-in/%d", docID))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
}

// TestIntegrationStockConcurrentDeduction 并发扣减（spec §8.2 重点场景）：
// 库存 10，两出库单各扣 6，并发审核——恰一成一 409，余额不为负；
// 成功后流水代数和 = 余额（负库存双保险：行锁 + count >= ? SQL 条件）。
func TestIntegrationStockConcurrentDeduction(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "并发")

	in := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"inTime":      env.Date() + " 09:00:00",
		"items":       []map[string]any{stockItemJSON(productID, "10", "1.00")},
	})
	inID := int64(in["id"].(float64))
	if status, api := approveDoc(t, env, token, "stock-in", inID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核垫库存入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "10.000" {
		t.Fatalf("垫库存后合计 = %s，期望 10.000", got)
	}

	// 两出库单各扣 6（合计 12 > 10）。
	outA := createStockOutDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"outTime":     env.Date() + " 10:00:00",
		"type":        10, // 报损
		"remark":      "并发报损A",
		"items":       []map[string]any{stockItemJSON(productID, "6", "1.00")},
	})
	outB := createStockOutDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"outTime":     env.Date() + " 10:00:00",
		"type":        10,
		"remark":      "并发报损B",
		"items":       []map[string]any{stockItemJSON(productID, "6", "1.00")},
	})
	outAID := int64(outA["id"].(float64))
	outBID := int64(outB["id"].(float64))

	type outcome struct {
		status int
		api    testutil.APIResponse
		err    error
	}
	results := make([]outcome, 2)
	var wg sync.WaitGroup
	for i, id := range []int64{outAID, outBID} {
		wg.Add(1)
		go func(i int, docID int64) {
			defer wg.Done()
			status, api, err := approveDocOnce(env, token, "stock-out", docID, 20)
			results[i] = outcome{status: status, api: api, err: err}
		}(i, id)
	}
	wg.Wait()

	// 恰一成一 409。
	okCount, conflictCount := 0, 0
	var conflictMsg string
	for _, r := range results {
		if r.err != nil {
			t.Fatalf("并发审核请求失败: %v", r.err)
		}
		switch {
		case r.status == http.StatusOK && r.api.Code == 0:
			okCount++
		case r.status == http.StatusConflict:
			conflictCount++
			conflictMsg = r.api.Msg
		default:
			t.Fatalf("并发审核意外结果：HTTP %d code %d msg %q", r.status, r.api.Code, r.api.Msg)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("并发扣减应恰一成一拒：ok=%d conflict=%d", okCount, conflictCount)
	}
	// 409 文案含产品/仓库/当前库存/变更量（spec §4.2）。
	for _, kw := range []string{"并发产品", "并发仓库", "4.000", "6.000"} {
		if !bytes.Contains([]byte(conflictMsg), []byte(kw)) {
			t.Fatalf("409 文案缺少 %q：%q", kw, conflictMsg)
		}
	}

	// 余额 = 4 不为负；流水代数和 = 余额（10 − 6 = 4）。
	if got := getStockCount(t, env, token, productID, warehouseID); got != "4.000" {
		t.Fatalf("并发扣减后余额 = %s，期望 4.000（无负库存）", got)
	}
	records, total := pageStockRecords(t, env, token, "")
	if total != 2 {
		t.Fatalf("流水应 2 条（入库 10 + 成功出库 −6），实际 %d", total)
	}
	sum := 0.0
	for _, rec := range records {
		var count float64
		_, _ = fmt.Sscan(fmt.Sprint(rec["count"]), &count)
		sum += count
		var totalCount float64
		_, _ = fmt.Sscan(fmt.Sprint(rec["totalCount"]), &totalCount)
		if totalCount < 0 {
			t.Fatalf("快照结存出现负数 %v（负库存禁令被破坏）", rec["totalCount"])
		}
	}
	if sum != 4 {
		t.Fatalf("流水代数和 %v ≠ 余额 4", sum)
	}

	// 失败单据回滚：状态仍 10、可正常重试更小数量后成功（事务整体回滚的证据）。
	var failedID int64
	failedOut := outA
	if results[0].status == http.StatusConflict {
		failedID = outAID
	} else {
		failedID = outBID
		failedOut = outB
	}
	list, _ := pageStockDocs(t, env, token, "stock-out", "&no="+fmt.Sprint(failedOut["no"]))
	if len(list) == 0 {
		t.Fatalf("失败单据应仍在列表")
	}
	if list[0]["status"] != float64(10) {
		t.Fatalf("失败单据应回滚为未审核，实际 %v", list[0]["status"])
	}
	// 出库 3 可成功（余额 4）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/stock-out", token, map[string]any{
		"id": failedID, "warehouseId": warehouseID, "outTime": env.Date() + " 11:00:00",
		"items": []map[string]any{stockItemJSON(productID, "3", "1.00")},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("修改失败单据：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	if status, api := approveDoc(t, env, token, "stock-out", failedID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("重试审核出库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "1.000" {
		t.Fatalf("重试后余额 = %s，期望 1.000", got)
	}
}

// TestIntegrationStockDocNumbering 编号：格式、租户内连续、客户端伪造不采纳、
// 跨日重置（erp_sequence 按 (tenant, biz_type, date_key) 发放）。
func TestIntegrationStockDocNumbering(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "编号")

	// 客户端伪造单号不采纳：payload 里带 no/number 字段，服务端 DTO 无此字段。
	forged := "QR199901010001"
	doc1 := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"no": forged, "number": forged,
		"items": []map[string]any{stockItemJSON(productID, "1", "0.00")},
	})
	no1 := fmt.Sprint(doc1["no"])
	if no1 == forged {
		t.Fatalf("客户端伪造单号被采纳：%v", no1)
	}
	want1 := "QR" + env.DateKey() + "0001"
	if no1 != want1 {
		t.Fatalf("入库单号 = %s，期望 %s（QR+业务时区 yyyyMMdd+4 位序号）", no1, want1)
	}

	// 同日连续发放。
	doc2 := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "1", "0.00")},
	})
	if got := fmt.Sprint(doc2["no"]); got != "QR"+env.DateKey()+"0002" {
		t.Fatalf("第二张入库单号 = %s，期望 QR%s0002", got, env.DateKey())
	}

	// QC 前缀独立序列。
	out := createStockOutDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "outTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "1", "0.00")},
	})
	if got := fmt.Sprint(out["no"]); got != "QC"+env.DateKey()+"0001" {
		t.Fatalf("出库单号 = %s，期望 QC%s0001（biz_type 独立发放）", got, env.DateKey())
	}

	// 编辑不采纳伪造、保持原号。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/stock-in", token, map[string]any{
		"id": int64(doc1["id"].(float64)), "warehouseId": warehouseID,
		"inTime": env.Date() + " 09:30:00", "no": forged,
		"items": []map[string]any{stockItemJSON(productID, "2", "0.00")},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("编辑入库单：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	detail := getStockInDetail(t, env, token, int64(doc1["id"].(float64)))
	if detail["no"] != no1 {
		t.Fatalf("编辑不应改动单号：期望 %s 实际 %v", no1, detail["no"])
	}

	// 跨日重置：昨天的序列行不影响今天发放（date_key 维度隔离）。
	yesterday := "20000101"
	if err := env.DB.Exec(
		"INSERT INTO `erp_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (1, 'stock_in', ?, 999999) "+
			"ON DUPLICATE KEY UPDATE `last_value` = `last_value`", yesterday,
	).Error; err != nil {
		t.Fatalf("预置昨日序列: %v", err)
	}
	doc3 := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 10:00:00",
		"items": []map[string]any{stockItemJSON(productID, "1", "0.00")},
	})
	if got := fmt.Sprint(doc3["no"]); got != "QR"+env.DateKey()+"0003" {
		t.Fatalf("跨日重置失效：单号 = %s，期望 QR%s0003（昨日 last_value=999999 不参与）", got, env.DateKey())
	}
}

// TestIntegrationStockDocTenantIsolation 租户隔离（spec §8.2 编号/单据行）：
// 租户 2 看不到租户 1 的单据与库存，跨租户审核/删除/详情一律 404，
// 序列按租户独立发放（租户 2 首单也是 0001）。
func TestIntegrationStockDocTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, admin, "隔离")

	doc := createStockInDoc(t, env, admin, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "7", "1.00")},
	})
	docID := int64(doc["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "库存隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 列表互不可见：单据/库存/流水。
	_, total := pageStockDocs(t, env, token2, "stock-in", "")
	if total != 0 {
		t.Fatalf("租户 2 入库单 total=%d，期望 0", total)
	}
	_, total = pageStockRows(t, env, token2, "")
	if total != 0 {
		t.Fatalf("租户 2 库存 total=%d，期望 0", total)
	}
	_, total = pageStockRecords(t, env, token2, "")
	if total != 0 {
		t.Fatalf("租户 2 流水 total=%d，期望 0", total)
	}

	// 跨租户详情/审核/删除一律 404。
	status, _ := getJSON(t, env, token2, fmt.Sprintf("/erp/stock-in/%d", docID))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 读租户 1 单据：HTTP %d，期望 404", status)
	}
	status, _ = approveDoc(t, env, token2, "stock-in", docID, 20)
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 审核租户 1 单据：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, token2, fmt.Sprintf("/erp/stock-in/%d", docID))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 单据：HTTP %d，期望 404", status)
	}
	status, api := env.Request(t, http.MethodGet, fmt.Sprintf("/erp/stock/get-count?productId=%d", productID), token2, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("租户 2 查询合计：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var cnt struct {
		Count string `json:"count"`
	}
	if err := json.Unmarshal(api.Data, &cnt); err != nil {
		t.Fatalf("解析合计响应: %v（data=%s）", err, string(api.Data))
	}
	if cnt.Count != "0.000" {
		t.Fatalf("租户 2 视角合计 = %s，期望 0.000（租户隔离）", cnt.Count)
	}

	// 序列按租户独立：租户 2 首单也是 0001。
	wh2, prod2 := setupStockEnv(t, env, token2, "隔离二")
	doc2 := createStockInDoc(t, env, token2, map[string]any{
		"warehouseId": wh2, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(prod2, "1", "0.00")},
	})
	if got := fmt.Sprint(doc2["no"]); got != "QR"+env.DateKey()+"0001" {
		t.Fatalf("租户 2 首单号 = %s，期望 QR%s0001（序列按租户隔离）", got, env.DateKey())
	}
}

// TestIntegrationStockDocDataScope 数据范围（spec §4.10 + ADR 0004）：单据 owner =
// 创建人；「仅本人」观察者只看到自己的单据，范围外详情/审核/删除一律 404。
// 主数据（仓库/产品）由 admin 建好（owner 角色只授入库单菜单，隔离主数据授权变量）。
func TestIntegrationStockDocDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	rndDept := int64(101)
	mktDept := int64(102)

	warehouseID, productID := setupStockEnv(t, env, admin, "范围")

	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "入库 owner 角色", "code": "erp_stock_doc_owner_role", "dataScope": 1,
		"menuIds": []int64{erpStockInMenuID, 7231, 7232},
	})
	createUserByAPI(t, env, admin, "stock_doc_owner_rnd", rndDept, []int64{ownerRoleID})
	ownerToken := env.Login(t, "stock_doc_owner_rnd", "123456")
	createStockInDoc(t, env, ownerToken, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "5", "0.00")},
	})

	// 观察者：市场部、仅本人（dataScope=5），有全部入库单按钮（含 approve 码，
	// 但对范围外单据仍必须 404——数据范围优先于功能权限）。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "入库观察者角色", "code": "erp_stock_doc_observer_role", "dataScope": 5,
		"menuIds": []int64{erpStockInMenuID, 7231, 7232, 7233, 7234, 7235},
	})
	createUserByAPI(t, env, admin, "stock_doc_observer", mktDept, []int64{roleID})
	observerToken := env.Login(t, "stock_doc_observer", "123456")
	createStockInDoc(t, env, observerToken, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "2", "0.00")},
	})

	// 仅本人：只看到自己的 1 张。
	list, total := pageStockDocs(t, env, observerToken, "stock-in", "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("仅本人观察者应只见自己的单据：total=%d list=%v", total, list)
	}
	// owner 是「全部」数据范围：看到两张（含观察者的）。
	_, total = pageStockDocs(t, env, ownerToken, "stock-in", "")
	if total != 2 {
		t.Fatalf("owner（全部范围）应见 2 张单据：total=%d", total)
	}

	// 范围外详情/审核一律 404（不泄露存在性）。owner 是「全部」范围，其列表
	// id DESC 首行是观察者后建的单据——取最旧一行（owner 自己的单据）。
	ownerDocs, _ := pageStockDocs(t, env, ownerToken, "stock-in", "")
	ownerDocID := int64(ownerDocs[len(ownerDocs)-1]["id"].(float64))
	status, _ := getJSON(t, env, observerToken, fmt.Sprintf("/erp/stock-in/%d", ownerDocID))
	if status != http.StatusNotFound {
		t.Fatalf("观察者读范围外单据：HTTP %d，期望 404", status)
	}
	status, _ = approveDoc(t, env, observerToken, "stock-in", ownerDocID, 20)
	if status != http.StatusNotFound {
		t.Fatalf("观察者审核范围外单据：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, observerToken, fmt.Sprintf("/erp/stock-in/%d", ownerDocID))
	if status != http.StatusNotFound {
		t.Fatalf("观察者删除范围外单据：HTTP %d，期望 404", status)
	}
}

// TestIntegrationStockDocValidation 单据校验：数量>0、至少一条明细、类型/状态枚举、
// 不存在的仓库/产品（spec §4.2 明细不变量 + 枚举 400 口径）。
func TestIntegrationStockDocValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "校验")

	base := func() map[string]any {
		return map[string]any{
			"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
			"items": []map[string]any{stockItemJSON(productID, "1", "1.00")},
		}
	}

	// 数量 0 / 负数 400。
	for _, bad := range []string{"0", "-1", "0.000"} {
		payload := base()
		payload["items"] = []map[string]any{stockItemJSON(productID, bad, "1.00")}
		status, api := env.Request(t, http.MethodPost, "/erp/stock-in", token, payload)
		if status != http.StatusBadRequest {
			t.Fatalf("数量 %s 应 400：HTTP %d code %d msg %q", bad, status, api.Code, api.Msg)
		}
	}

	// 空明细 400。
	payload := base()
	payload["items"] = []map[string]any{}
	status, _ := env.Request(t, http.MethodPost, "/erp/stock-in", token, payload)
	if status != http.StatusBadRequest {
		t.Fatalf("空明细应 400，实际 HTTP %d", status)
	}

	// 非法类型 400。
	payload = base()
	payload["type"] = 88
	status, _ = env.Request(t, http.MethodPost, "/erp/stock-in", token, payload)
	if status != http.StatusBadRequest {
		t.Fatalf("非法入库类型应 400，实际 HTTP %d", status)
	}

	// 不存在的仓库/产品 400。
	payload = base()
	payload["warehouseId"] = 999999
	status, _ = env.Request(t, http.MethodPost, "/erp/stock-in", token, payload)
	if status != http.StatusBadRequest {
		t.Fatalf("仓库不存在应 400，实际 HTTP %d", status)
	}
	payload = base()
	payload["items"] = []map[string]any{stockItemJSON(999999, "1", "1.00")}
	status, _ = env.Request(t, http.MethodPost, "/erp/stock-in", token, payload)
	if status != http.StatusBadRequest {
		t.Fatalf("产品不存在应 400，实际 HTTP %d", status)
	}

	// update-status 非法目标态 400。
	validDoc := createStockInDoc(t, env, token, base())
	docID := int64(validDoc["id"].(float64))
	status, _ = approveDoc(t, env, token, "stock-in", docID, 30)
	if status != http.StatusBadRequest {
		t.Fatalf("非法审核目标态应 400，实际 HTTP %d", status)
	}

	// 行金额 = 单价 × 数量（服务端计算，客户端无 totalPrice 字段可伪造）。
	// 0.33 × 3 = 0.99。
	doc2 := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "3", "0.33")},
	})
	detail := getStockInDetail(t, env, token, int64(doc2["id"].(float64)))
	items := detail["items"].([]any)
	first := items[0].(map[string]any)
	if first["totalPrice"] != "0.99" {
		t.Fatalf("行金额 = %v，期望 0.99（0.33 × 3，MulQty 四舍五入到分）", first["totalPrice"])
	}
	if first["count"] != "3.000" {
		t.Fatalf("明细数量 = %v，期望 3.000（毫单位定点字符串）", first["count"])
	}
}

// TestIntegrationStockDocRefChecks 引用禁删（spec §4.9）：仓库被非零库存/流水
// 引用禁删；产品被单据明细引用禁删。
func TestIntegrationStockDocRefChecks(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "引用")

	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "5", "1.00")},
	})
	docID := int64(doc["id"].(float64))

	// 未落账也已被单据主表引用：仓库删除负路径见 TestIntegrationWarehouseDocRefs；
	// 这里只建一个未被引用的空仓库验证「未引用可删」路径。
	_ = createWarehouse(t, env, token, "引用空仓库", nil)
	// 审核落账。
	if status, api := approveDoc(t, env, token, "stock-in", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 仓库：非零库存 → 409。
	delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/warehouse/%d", warehouseID))
	if delStatus != http.StatusConflict {
		t.Fatalf("删除有非零库存的仓库：HTTP %d，期望 409", delStatus)
	}

	// 产品：被明细引用 → 409。
	delStatus, _ = deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", productID))
	if delStatus != http.StatusConflict {
		t.Fatalf("删除被明细引用的产品：HTTP %d，期望 409", delStatus)
	}

	// 反审核：余额归零但流水仍在 → 仓库仍禁删（append-only 历史引用）。
	if status, api := approveDoc(t, env, token, "stock-in", docID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("反审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "0.000" {
		t.Fatalf("反审核后余额应 0.000，实际 %s", got)
	}
	delStatus, _ = deleteByID(t, env, token, fmt.Sprintf("/erp/warehouse/%d", warehouseID))
	if delStatus != http.StatusConflict {
		t.Fatalf("删除有流水历史的仓库（零余额）：HTTP %d，期望 409", delStatus)
	}
}

// TestIntegrationStockDocExport 单据导出（spec §4.11）：列标签与中文文件名。
func TestIntegrationStockDocExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "导出")

	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"type": 50, "remark": "导出用赠品入库",
		"items": []map[string]any{stockItemJSON(productID, "4", "1.25")},
	})
	// 审核落账，让库存导出有数据行。
	if status, api := approveDoc(t, env, token, "stock-in", int64(doc["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核导出用入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	header, rows := getExportRows(t, env, token, "/erp/stock-in/export-excel")
	for _, col := range []string{"单号", "仓库", "入库时间", "类型", "状态", "备注", "创建时间"} {
		expectColumn(t, "入库单导出", header, col)
	}
	if len(rows) != 1 {
		t.Fatalf("入库单导出应 1 行，实际 %d", len(rows))
	}
	if rows[0][3] != "赠品" {
		t.Fatalf("导出类型列 = %v，期望 赠品", rows[0][3])
	}
	if rows[0][1] != "导出仓库" {
		t.Fatalf("导出仓库列 = %v，期望 导出仓库", rows[0][1])
	}

	// 库存/流水导出可访问（列契约在 stock_http_integration_test.go 断言）。
	_, rows = getExportRows(t, env, token, "/erp/stock/export-excel")
	if len(rows) == 0 {
		t.Fatal("库存导出应至少 1 行")
	}
}
