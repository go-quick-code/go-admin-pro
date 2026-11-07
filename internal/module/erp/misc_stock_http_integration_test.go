//go:build integration

// 库存调拨/盘点 HTTP 集成测试（issue #93，docs/erp-mvp-spec.md §4.6 + §8.2
// 调拨/盘点行）：调拨一增一减同额成对、同仓 409、源仓不足 409、反审核成对
// 反向流水；盘点盘盈/盘亏调整、审核以当时账面为准（base_count 仅记录）、
// 审核后账面=实际、反审核按净额反向；编号 DB/PD、租户隔离、数据范围、普通
// 角色 403、引用禁删、导出。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// 菜单/按钮 ID（与 internal/seed/seed.go 700 段一致：725 调拨、726 盘点）。
const (
	erpStockMoveMenuID  = int64(725)
	erpStockCheckMenuID = int64(726)
)

// DB/PD 编号格式（spec §8.2：^(DB|PD)\d{8}\d{4,}$）。
var (
	stockMoveNoRe  = regexp.MustCompile(`^DB\d{8}\d{4,}$`)
	stockCheckNoRe = regexp.MustCompile(`^PD\d{8}\d{4,}$`)
)

// ---------- 用例级助手 ----------

// moveItemJSON 是调拨明细 payload 的简写构造（产品 + 数量）。
func moveItemJSON(productID int64, count string) map[string]any {
	return map[string]any{"productId": productID, "count": count}
}

// checkItemJSON 是盘点明细 payload 的简写构造（产品 + 实际清点数）。
func checkItemJSON(productID int64, actualCount string) map[string]any {
	return map[string]any{"productId": productID, "actualCount": actualCount}
}

// createStockMoveDoc 创建库存调拨单并断言成功，返回完整主表 JSON。
func createStockMoveDoc(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/stock-move", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建库存调拨单：HTTP %d code %d msg %q（payload=%v）", status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建调拨单响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// createStockCheckDoc 创建库存盘点单并断言成功，返回完整主表 JSON。
func createStockCheckDoc(t *testing.T, env *testutil.IntegrationEnv, token string, payload map[string]any) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/erp/stock-check", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建库存盘点单：HTTP %d code %d msg %q（payload=%v）", status, api.Code, api.Msg, payload)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建盘点单响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// getMiscDocDetail 查询调拨/盘点单详情（docType: stock-move / stock-check）。
func getMiscDocDetail(t *testing.T, env *testutil.IntegrationEnv, token, docType string, docID int64) map[string]any {
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

// seedWarehouseStock 通过其它入库单（审核）给指定仓库垫库存（非本票被测路径）。
func seedWarehouseStock(t *testing.T, env *testutil.IntegrationEnv, token string, warehouseID, productID int64, count string) {
	t.Helper()
	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"inTime":      env.Date() + " 08:00:00",
		"type":        50, // 赠品
		"items":       []map[string]any{stockItemJSON(productID, count, "0.00")},
	})
	if status, api := approveDoc(t, env, token, "stock-in", int64(doc["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("垫库存审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// recordsByBizNo 从流水列表筛出指定单号的记录（按 id ASC 返回，便于断言行序）。
func recordsByBizNo(records []map[string]any, bizNo string) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		if fmt.Sprint(rec["bizNo"]) == bizNo {
			out = append(out, rec)
		}
	}
	// pageStockRecords 固定 id DESC；这里翻转为 id ASC（正向在前，反向在后）。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// recordCount 返回流水的 count 数值（毫单位字符串直接参与断言）。
func recordCount(rec map[string]any) string {
	return fmt.Sprint(rec["count"])
}

// ---------- 调拨 ----------

// TestIntegrationStockMoveLifecycle 调拨生命周期（spec §4.6/§4.3、§8.2 调拨行）：
// 未审核不动库存 → 审核同事务一增一减、两笔流水同额成对 → 已审核不可改删 →
// 重复审核幂等 → 反审核成对反向流水（原流水保留）→ 余额恢复 → 可改可删。
func TestIntegrationStockMoveLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	fromWarehouseID, productID := setupStockEnv(t, env, token, "调拨")
	toWarehouse := createWarehouse(t, env, token, "调拨目标仓库", nil)
	toWarehouseID := int64(toWarehouse["id"].(float64))
	seedWarehouseStock(t, env, token, fromWarehouseID, productID, "10")

	doc := createStockMoveDoc(t, env, token, map[string]any{
		"fromWarehouseId": fromWarehouseID,
		"toWarehouseId":   toWarehouseID,
		"moveTime":        env.Date() + " 10:00:00",
		"items":           []map[string]any{moveItemJSON(productID, "4.5")},
	})
	docID := int64(doc["id"].(float64))
	if !stockMoveNoRe.MatchString(fmt.Sprint(doc["no"])) {
		t.Fatalf("调拨单号 %v 不符 ^DB\\d{8}\\d{4,}$", doc["no"])
	}
	if doc["status"] != float64(10) {
		t.Fatalf("新建调拨单应未审核(10)，实际 %v", doc["status"])
	}
	if got := getStockCount(t, env, token, productID, fromWarehouseID); got != "10.000" {
		t.Fatalf("未审核不应动库存：调出仓 = %s，期望 10.000", got)
	}
	if got := getStockCount(t, env, token, productID, toWarehouseID); got != "0.000" {
		t.Fatalf("未审核不应动库存：调入仓 = %s，期望 0.000", got)
	}

	// 详情：两仓名称与明细数量服务端解析。
	detail := getMiscDocDetail(t, env, token, "stock-move", docID)
	if detail["fromWarehouseName"] != "调拨仓库" || detail["toWarehouseName"] != "调拨目标仓库" {
		t.Fatalf("详情仓库名 = %v → %v，期望 调拨仓库 → 调拨目标仓库", detail["fromWarehouseName"], detail["toWarehouseName"])
	}
	items := detail["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["count"] != "4.500" {
		t.Fatalf("详情明细 = %v，期望 1 行 count=4.500", items)
	}

	// 审核：一增一减同额成对（两笔流水，count 互为相反数，同一单号）。
	if status, api := approveDoc(t, env, token, "stock-move", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核调拨单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, fromWarehouseID); got != "5.500" {
		t.Fatalf("审核后调出仓 = %s，期望 5.500（10 − 4.5）", got)
	}
	if got := getStockCount(t, env, token, productID, toWarehouseID); got != "4.500" {
		t.Fatalf("审核后调入仓 = %s，期望 4.500", got)
	}
	records, _ := pageStockRecords(t, env, token, "&bizType=stock_move")
	moveRecords := recordsByBizNo(records, fmt.Sprint(doc["no"]))
	if len(moveRecords) != 2 {
		t.Fatalf("调拨审核应产生 2 条流水（出仓负 + 入仓正），实际 %d：%v", len(moveRecords), moveRecords)
	}
	negFound, posFound := false, false
	for _, rec := range moveRecords {
		if rec["bizType"] != "stock_move" {
			t.Fatalf("流水 bizType = %v，期望 stock_move", rec["bizType"])
		}
		switch recordCount(rec) {
		case "-4.500":
			negFound = true
			if int64(rec["warehouseId"].(float64)) != fromWarehouseID {
				t.Fatalf("出仓流水仓库 = %v，期望 %d", rec["warehouseId"], fromWarehouseID)
			}
		case "4.500":
			posFound = true
			if int64(rec["warehouseId"].(float64)) != toWarehouseID {
				t.Fatalf("入仓流水仓库 = %v，期望 %d", rec["warehouseId"], toWarehouseID)
			}
		default:
			t.Fatalf("意外流水 count = %v", rec["count"])
		}
	}
	if !negFound || !posFound {
		t.Fatalf("流水未同额成对：neg=%v pos=%v records=%v", negFound, posFound, moveRecords)
	}

	// 已审核：不可改、不可删（409）；重复审核幂等（不新增流水）。
	upStatus, _ := env.Request(t, http.MethodPut, "/erp/stock-move", token, map[string]any{
		"id": docID, "fromWarehouseId": fromWarehouseID, "toWarehouseId": toWarehouseID,
		"moveTime": env.Date() + " 10:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})
	if upStatus != http.StatusConflict {
		t.Fatalf("修改已审核调拨单：HTTP %d，期望 409", upStatus)
	}
	delStatus, _ := deleteByID(t, env, token, fmt.Sprintf("/erp/stock-move/%d", docID))
	if delStatus != http.StatusConflict {
		t.Fatalf("删除已审核调拨单：HTTP %d，期望 409", delStatus)
	}
	if status, api := approveDoc(t, env, token, "stock-move", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("重复审核应幂等成功：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	records, _ = pageStockRecords(t, env, token, "&bizType=stock_move")
	if got := len(recordsByBizNo(records, fmt.Sprint(doc["no"]))); got != 2 {
		t.Fatalf("重复审核不应新增流水：实际 %d 条，期望 2", got)
	}

	// 反审核：成对反向流水（_CANCEL，原流水保留）、两仓余额恢复。
	if status, api := approveDoc(t, env, token, "stock-move", docID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("反审核调拨单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, fromWarehouseID); got != "10.000" {
		t.Fatalf("反审核后调出仓 = %s，期望 10.000", got)
	}
	if got := getStockCount(t, env, token, productID, toWarehouseID); got != "0.000" {
		t.Fatalf("反审核后调入仓 = %s，期望 0.000", got)
	}
	records, _ = pageStockRecords(t, env, token, "&bizType=stock_move")
	moveRecords = recordsByBizNo(records, fmt.Sprint(doc["no"]))
	if len(moveRecords) != 2 {
		t.Fatalf("按 bizType=stock_move 过滤应只剩 2 条正向流水，实际 %d", len(moveRecords))
	}
	cancelRecords, _ := pageStockRecords(t, env, token, "&bizType=stock_move_CANCEL")
	cancels := recordsByBizNo(cancelRecords, fmt.Sprint(doc["no"]))
	if len(cancels) != 2 {
		t.Fatalf("反审核应产生 2 条 _CANCEL 流水，实际 %d", len(cancels))
	}
	cancelCounts := map[string]bool{}
	for _, rec := range cancels {
		cancelCounts[recordCount(rec)] = true
	}
	if !cancelCounts["4.500"] || !cancelCounts["-4.500"] {
		t.Fatalf("反向流水未成对：%v", cancelCounts)
	}

	// 反审核后回到未审核：可修改、可删除。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/stock-move", token, map[string]any{
		"id": docID, "fromWarehouseId": fromWarehouseID, "toWarehouseId": toWarehouseID,
		"moveTime": env.Date() + " 11:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "2")},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("反审核后修改调拨单：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/stock-move/%d", docID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除未审核调拨单：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
}

// TestIntegrationStockMoveValidation 调拨校验（spec §4.6/§4.2）：同仓 409、
// 源仓不足 409（整单事务回滚：状态仍 10、余额不变）、明细/仓库/产品/状态 400。
func TestIntegrationStockMoveValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	fromWarehouseID, productID := setupStockEnv(t, env, token, "调拨校验")
	toWarehouse := createWarehouse(t, env, token, "调拨校验目标", nil)
	toWarehouseID := int64(toWarehouse["id"].(float64))

	base := func() map[string]any {
		return map[string]any{
			"fromWarehouseId": fromWarehouseID,
			"toWarehouseId":   toWarehouseID,
			"moveTime":        env.Date() + " 09:00:00",
			"items":           []map[string]any{moveItemJSON(productID, "1")},
		}
	}

	// 同仓 409（创建与编辑）。
	payload := base()
	payload["toWarehouseId"] = fromWarehouseID
	status, api := env.Request(t, http.MethodPost, "/erp/stock-move", token, payload)
	if status != http.StatusConflict {
		t.Fatalf("同仓调拨创建：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	valid := createStockMoveDoc(t, env, token, base())
	payload = base()
	payload["id"] = int64(valid["id"].(float64))
	payload["toWarehouseId"] = fromWarehouseID
	status, _ = env.Request(t, http.MethodPut, "/erp/stock-move", token, payload)
	if status != http.StatusConflict {
		t.Fatalf("同仓调拨编辑：HTTP %d，期望 409", status)
	}

	// 明细/引用/状态校验 400。
	for _, bad := range []string{"0", "-1", "0.000"} {
		p := base()
		p["items"] = []map[string]any{moveItemJSON(productID, bad)}
		status, _ = env.Request(t, http.MethodPost, "/erp/stock-move", token, p)
		if status != http.StatusBadRequest {
			t.Fatalf("调拨数量 %s 应 400，实际 HTTP %d", bad, status)
		}
	}
	p := base()
	p["items"] = []map[string]any{}
	if status, _ = env.Request(t, http.MethodPost, "/erp/stock-move", token, p); status != http.StatusBadRequest {
		t.Fatalf("空调拨明细应 400，实际 HTTP %d", status)
	}
	p = base()
	p["fromWarehouseId"] = 999999
	if status, _ = env.Request(t, http.MethodPost, "/erp/stock-move", token, p); status != http.StatusBadRequest {
		t.Fatalf("调出仓库不存在应 400，实际 HTTP %d", status)
	}
	p = base()
	p["toWarehouseId"] = 999999
	if status, _ = env.Request(t, http.MethodPost, "/erp/stock-move", token, p); status != http.StatusBadRequest {
		t.Fatalf("调入仓库不存在应 400，实际 HTTP %d", status)
	}
	p = base()
	p["items"] = []map[string]any{moveItemJSON(999999, "1")}
	if status, _ = env.Request(t, http.MethodPost, "/erp/stock-move", token, p); status != http.StatusBadRequest {
		t.Fatalf("产品不存在应 400，实际 HTTP %d", status)
	}

	// 非法目标态 400。
	moveID := int64(valid["id"].(float64))
	if status, _ = approveDoc(t, env, token, "stock-move", moveID, 30); status != http.StatusBadRequest {
		t.Fatalf("非法审核目标态应 400，实际 HTTP %d", status)
	}

	// 源仓不足 409：整单事务回滚（状态仍 10、两仓余额不变）。
	seedWarehouseStock(t, env, token, fromWarehouseID, productID, "2")
	insufficient := createStockMoveDoc(t, env, token, map[string]any{
		"fromWarehouseId": fromWarehouseID,
		"toWarehouseId":   toWarehouseID,
		"moveTime":        env.Date() + " 10:00:00",
		"items":           []map[string]any{moveItemJSON(productID, "5")},
	})
	insufficientID := int64(insufficient["id"].(float64))
	status, api = approveDoc(t, env, token, "stock-move", insufficientID, 20)
	if status != http.StatusConflict {
		t.Fatalf("源仓不足审核：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	for _, kw := range []string{"调拨校验产品", "调拨校验仓库", "2.000", "5.000"} {
		if !regexp.MustCompile(regexp.QuoteMeta(kw)).MatchString(api.Msg) {
			t.Fatalf("源仓不足 409 文案缺少 %q：%q", kw, api.Msg)
		}
	}
	detail := getMiscDocDetail(t, env, token, "stock-move", insufficientID)
	if detail["status"] != float64(10) {
		t.Fatalf("审核失败后单据应回滚为未审核(10)，实际 %v", detail["status"])
	}
	if got := getStockCount(t, env, token, productID, fromWarehouseID); got != "2.000" {
		t.Fatalf("审核失败后调出仓 = %s，期望 2.000（事务整体回滚）", got)
	}
	if got := getStockCount(t, env, token, productID, toWarehouseID); got != "0.000" {
		t.Fatalf("审核失败后调入仓 = %s，期望 0.000", got)
	}

	// 改小数量后可正常审核（余额 2）。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/stock-move", token, map[string]any{
		"id": insufficientID, "fromWarehouseId": fromWarehouseID, "toWarehouseId": toWarehouseID,
		"moveTime": env.Date() + " 10:30:00",
		"items":    []map[string]any{moveItemJSON(productID, "2")},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("修改不足单据：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	if status, api := approveDoc(t, env, token, "stock-move", insufficientID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("改小数量后审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, toWarehouseID); got != "2.000" {
		t.Fatalf("审核后调入仓 = %s，期望 2.000", got)
	}
}

// ---------- 盘点 ----------

// TestIntegrationStockCheckLifecycle 盘点生命周期（spec §4.6、§8.2 盘点行）：
// 盘盈入/盘亏出按「实际 − 审核时账面」调整、base_count 仅创建快照且审核不
// 参与、审核后账面=实际、反审核按净额反向、账实一致不产生流水。
func TestIntegrationStockCheckLifecycle(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "盘点")
	seedWarehouseStock(t, env, token, warehouseID, productID, "10")

	// 创建时账面 10，实际清点 12（盘盈 +2 的快照基准）。
	doc := createStockCheckDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"checkTime":   env.Date() + " 10:00:00",
		"remark":      "月末盘点",
		"items":       []map[string]any{checkItemJSON(productID, "12")},
	})
	docID := int64(doc["id"].(float64))
	if !stockCheckNoRe.MatchString(fmt.Sprint(doc["no"])) {
		t.Fatalf("盘点单号 %v 不符 ^PD\\d{8}\\d{4,}$", doc["no"])
	}
	detail := getMiscDocDetail(t, env, token, "stock-check", docID)
	items := detail["items"].([]any)
	first := items[0].(map[string]any)
	if first["baseCount"] != "10.000" {
		t.Fatalf("创建时 baseCount = %v，期望 10.000（创建时账面快照）", first["baseCount"])
	}
	if first["actualCount"] != "12.000" {
		t.Fatalf("actualCount = %v，期望 12.000", first["actualCount"])
	}

	// 创建后账面变化：再入库 +5 → 审核时账面 15。审核以当时账面为准
	//（差异 = 12 − 15 = −3 盘亏），base_count 不参与计算。
	seedWarehouseStock(t, env, token, warehouseID, productID, "5")
	if got := getStockCount(t, env, token, productID, warehouseID); got != "15.000" {
		t.Fatalf("审核前账面 = %s，期望 15.000", got)
	}
	if status, api := approveDoc(t, env, token, "stock-check", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核盘点单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "12.000" {
		t.Fatalf("审核后账面 = %s，期望 12.000（= 实际清点数）", got)
	}
	records, _ := pageStockRecords(t, env, token, "&bizType=stock_check")
	docRecords := recordsByBizNo(records, fmt.Sprint(doc["no"]))
	if len(docRecords) != 1 || recordCount(docRecords[0]) != "-3.000" {
		t.Fatalf("盘亏调整流水 = %v，期望 1 条 count=-3.000（12 − 15，以当时账面为准）", docRecords)
	}

	// 反审核：按净额反向（−3 的反向 = +3），账面回到 15；原流水保留。
	if status, api := approveDoc(t, env, token, "stock-check", docID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("反审核盘点单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "15.000" {
		t.Fatalf("反审核后账面 = %s，期望 15.000", got)
	}
	cancelRecords, _ := pageStockRecords(t, env, token, "&bizType=stock_check_CANCEL")
	cancels := recordsByBizNo(cancelRecords, fmt.Sprint(doc["no"]))
	if len(cancels) != 1 || recordCount(cancels[0]) != "3.000" {
		t.Fatalf("盘点反审核流水 = %v，期望 1 条 count=3.000", cancels)
	}

	// 再审核：账面仍 15，实际 12 → 再调整 −3；多轮后净额仍准确。
	if status, api := approveDoc(t, env, token, "stock-check", docID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("再次审核盘点单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "12.000" {
		t.Fatalf("再审核后账面 = %s，期望 12.000", got)
	}
	if status, api := approveDoc(t, env, token, "stock-check", docID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("再次反审核盘点单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "15.000" {
		t.Fatalf("再次反审核后账面 = %s，期望 15.000（净额反向多轮正确）", got)
	}

	// 账实一致：实际 = 当前账面 15 → 审核成功但不产生调整流水，账面不变。
	equalDoc := createStockCheckDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"checkTime":   env.Date() + " 11:00:00",
		"items":       []map[string]any{checkItemJSON(productID, "15")},
	})
	equalID := int64(equalDoc["id"].(float64))
	if status, api := approveDoc(t, env, token, "stock-check", equalID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("账实一致审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	allCheckRecords, _ := pageStockRecords(t, env, token, "&bizType=stock_check")
	if got := len(recordsByBizNo(allCheckRecords, fmt.Sprint(equalDoc["no"]))); got != 0 {
		t.Fatalf("账实一致不应产生调整流水，实际 %d 条", got)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "15.000" {
		t.Fatalf("账实一致后账面 = %s，期望 15.000", got)
	}
	if status, api := approveDoc(t, env, token, "stock-check", equalID, 10); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("账实一致反审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "15.000" {
		t.Fatalf("账实一致反审核后账面 = %s，期望 15.000", got)
	}
}

// TestIntegrationStockCheckValidation 盘点校验：实际数 ≥ 0（0 合法、负数 400）、
// 空明细/引用/状态 400；盘盈被后续消耗后反审核 409（整单回滚，状态仍 20）。
func TestIntegrationStockCheckValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "盘点校验")

	base := func() map[string]any {
		return map[string]any{
			"warehouseId": warehouseID,
			"checkTime":   env.Date() + " 09:00:00",
			"items":       []map[string]any{checkItemJSON(productID, "1")},
		}
	}

	// 实际清点数为负 400；0 合法。
	payload := base()
	payload["items"] = []map[string]any{checkItemJSON(productID, "-0.001")}
	status, _ := env.Request(t, http.MethodPost, "/erp/stock-check", token, payload)
	if status != http.StatusBadRequest {
		t.Fatalf("实际清点数为负应 400，实际 HTTP %d", status)
	}
	zeroDoc := createStockCheckDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"checkTime":   env.Date() + " 09:00:00",
		"items":       []map[string]any{checkItemJSON(productID, "0")},
	})
	if zeroDoc["status"] != float64(10) {
		t.Fatalf("实际清点数 0 应合法且未审核，实际 %v", zeroDoc["status"])
	}

	// 空明细 / 仓库 / 产品 / 状态。
	p := base()
	p["items"] = []map[string]any{}
	if status, _ = env.Request(t, http.MethodPost, "/erp/stock-check", token, p); status != http.StatusBadRequest {
		t.Fatalf("空盘点明细应 400，实际 HTTP %d", status)
	}
	p = base()
	p["warehouseId"] = 999999
	if status, _ = env.Request(t, http.MethodPost, "/erp/stock-check", token, p); status != http.StatusBadRequest {
		t.Fatalf("仓库不存在应 400，实际 HTTP %d", status)
	}
	p = base()
	p["items"] = []map[string]any{checkItemJSON(999999, "1")}
	if status, _ = env.Request(t, http.MethodPost, "/erp/stock-check", token, p); status != http.StatusBadRequest {
		t.Fatalf("产品不存在应 400，实际 HTTP %d", status)
	}
	valid := createStockCheckDoc(t, env, token, base())
	validID := int64(valid["id"].(float64))
	if status, _ = approveDoc(t, env, token, "stock-check", validID, 30); status != http.StatusBadRequest {
		t.Fatalf("非法审核目标态应 400，实际 HTTP %d", status)
	}

	// 盘盈后新货被消耗：反审核需反向出账 2 而余额 0 → 409，状态仍 20、余额不变。
	seedWarehouseStock(t, env, token, warehouseID, productID, "10")
	surplus := createStockCheckDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"checkTime":   env.Date() + " 10:00:00",
		"items":       []map[string]any{checkItemJSON(productID, "12")},
	})
	surplusID := int64(surplus["id"].(float64))
	if status, api := approveDoc(t, env, token, "stock-check", surplusID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("盘盈审核：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "12.000" {
		t.Fatalf("盘盈审核后账面 = %s，期望 12.000", got)
	}
	out := createStockOutDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID,
		"outTime":     env.Date() + " 10:30:00",
		"type":        10,
		"items":       []map[string]any{stockItemJSON(productID, "12", "0.00")},
	})
	if status, api := approveDoc(t, env, token, "stock-out", int64(out["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("消耗盘盈库存：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api := approveDoc(t, env, token, "stock-check", surplusID, 10)
	if status != http.StatusConflict {
		t.Fatalf("盘盈已被消耗后反审核：HTTP %d code %d msg %q，期望 409", status, api.Code, api.Msg)
	}
	detail := getMiscDocDetail(t, env, token, "stock-check", surplusID)
	if detail["status"] != float64(20) {
		t.Fatalf("反审核失败后状态应保持 20，实际 %v", detail["status"])
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "0.000" {
		t.Fatalf("反审核失败后账面 = %s，期望 0.000（整单回滚）", got)
	}
}

// ---------- 编号 / 租户 / 权限 ----------

// TestIntegrationMiscStockNumbering 编号（spec §4.8/§8.2 编号行）：DB/PD 格式、
// 同类型连续、按 biz_type 独立序列、客户端伪造单号不采纳、跨日重置。
func TestIntegrationMiscStockNumbering(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "编号")
	second := createWarehouse(t, env, token, "编号目标", nil)
	secondID := int64(second["id"].(float64))

	forged := "DB199901010001"
	move := createStockMoveDoc(t, env, token, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 09:00:00", "no": forged,
		"items": []map[string]any{moveItemJSON(productID, "1")},
	})
	if got := fmt.Sprint(move["no"]); got != "DB"+env.DateKey()+"0001" {
		t.Fatalf("调拨首单号 = %s，期望 DB%s0001（伪造单号不采纳）", got, env.DateKey())
	}
	move2 := createStockMoveDoc(t, env, token, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 09:30:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})
	if got := fmt.Sprint(move2["no"]); got != "DB"+env.DateKey()+"0002" {
		t.Fatalf("调拨第二单号 = %s，期望 DB%s0002", got, env.DateKey())
	}

	check := createStockCheckDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "checkTime": env.Date() + " 09:00:00", "no": forged,
		"items": []map[string]any{checkItemJSON(productID, "0")},
	})
	if got := fmt.Sprint(check["no"]); got != "PD"+env.DateKey()+"0001" {
		t.Fatalf("盘点首单号 = %s，期望 PD%s0001（biz_type 独立序列）", got, env.DateKey())
	}

	// 编辑不采纳伪造单号、保持原号。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/stock-check", token, map[string]any{
		"id": int64(check["id"].(float64)), "warehouseId": warehouseID,
		"checkTime": env.Date() + " 09:30:00", "no": forged,
		"items": []map[string]any{checkItemJSON(productID, "0")},
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("编辑盘点单：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	detail := getMiscDocDetail(t, env, token, "stock-check", int64(check["id"].(float64)))
	if fmt.Sprint(detail["no"]) != fmt.Sprint(check["no"]) {
		t.Fatalf("编辑不应改动单号：期望 %v 实际 %v", check["no"], detail["no"])
	}

	// 跨日重置：昨日序列行不影响今天发放。
	yesterday := "20000101"
	for _, biz := range []string{"stock_move", "stock_check"} {
		if err := env.DB.Exec(
			"INSERT INTO `erp_sequence` (`tenant_id`, `biz_type`, `date_key`, `last_value`) VALUES (1, ?, ?, 999999) "+
				"ON DUPLICATE KEY UPDATE `last_value` = `last_value`", biz, yesterday,
		).Error; err != nil {
			t.Fatalf("预置昨日序列 %s: %v", biz, err)
		}
	}
	move3 := createStockMoveDoc(t, env, token, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 10:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})
	if got := fmt.Sprint(move3["no"]); got != "DB"+env.DateKey()+"0003" {
		t.Fatalf("跨日重置失效：调拨单号 = %s，期望 DB%s0003", got, env.DateKey())
	}
}

// TestIntegrationMiscStockTenantIsolation 租户隔离（spec §4.10/§8.2 编号行）：
// 租户 2 看不到租户 1 的调拨/盘点单据，跨租户详情/审核/删除一律 404；
// DB/PD 序列按租户独立发放（租户 2 首单也是 0001）。
func TestIntegrationMiscStockTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, admin, "调拨隔离")
	second := createWarehouse(t, env, admin, "调拨隔离目标", nil)
	secondID := int64(second["id"].(float64))

	move := createStockMoveDoc(t, env, admin, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 09:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})
	check := createStockCheckDoc(t, env, admin, map[string]any{
		"warehouseId": warehouseID, "checkTime": env.Date() + " 09:00:00",
		"items": []map[string]any{checkItemJSON(productID, "0")},
	})
	moveID := int64(move["id"].(float64))
	checkID := int64(check["id"].(float64))

	tenant2 := createTenantByAPI(t, env, admin, "调拨盘点隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	for _, docType := range []string{"stock-move", "stock-check"} {
		_, total := pageStockDocs(t, env, token2, docType, "")
		if total != 0 {
			t.Fatalf("租户 2 的 %s total=%d，期望 0", docType, total)
		}
	}
	for _, c := range []struct {
		docType string
		id      int64
	}{
		{"stock-move", moveID},
		{"stock-check", checkID},
	} {
		status, _ := getJSON(t, env, token2, fmt.Sprintf("/erp/%s/%d", c.docType, c.id))
		if status != http.StatusNotFound {
			t.Fatalf("租户 2 读租户 1 %s：HTTP %d，期望 404", c.docType, status)
		}
		status, _ = approveDoc(t, env, token2, c.docType, c.id, 20)
		if status != http.StatusNotFound {
			t.Fatalf("租户 2 审核租户 1 %s：HTTP %d，期望 404", c.docType, status)
		}
		status, _ = deleteByID(t, env, token2, fmt.Sprintf("/erp/%s/%d", c.docType, c.id))
		if status != http.StatusNotFound {
			t.Fatalf("租户 2 删除租户 1 %s：HTTP %d，期望 404", c.docType, status)
		}
	}

	// 序列按租户独立：租户 2 首单也是 0001。
	wh2, prod2 := setupStockEnv(t, env, token2, "调拨隔离二")
	second2 := createWarehouse(t, env, token2, "调拨隔离二目标", nil)
	move2 := createStockMoveDoc(t, env, token2, map[string]any{
		"fromWarehouseId": wh2, "toWarehouseId": int64(second2["id"].(float64)),
		"moveTime": env.Date() + " 09:00:00",
		"items":    []map[string]any{moveItemJSON(prod2, "1")},
	})
	if got := fmt.Sprint(move2["no"]); got != "DB"+env.DateKey()+"0001" {
		t.Fatalf("租户 2 调拨首单号 = %s，期望 DB%s0001（序列按租户隔离）", got, env.DateKey())
	}
	check2 := createStockCheckDoc(t, env, token2, map[string]any{
		"warehouseId": wh2, "checkTime": env.Date() + " 09:00:00",
		"items": []map[string]any{checkItemJSON(prod2, "0")},
	})
	if got := fmt.Sprint(check2["no"]); got != "PD"+env.DateKey()+"0001" {
		t.Fatalf("租户 2 盘点首单号 = %s，期望 PD%s0001", got, env.DateKey())
	}
}

// TestIntegrationMiscStockDataScope 数据范围（spec §4.10 + ADR 0004）：单据
// owner = 创建人；「仅本人」观察者只看到自己的单据，范围外详情/审核/删除 404。
func TestIntegrationMiscStockDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, admin, "调拨范围")
	second := createWarehouse(t, env, admin, "调拨范围目标", nil)
	secondID := int64(second["id"].(float64))

	ownerRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "调拨 owner 角色", "code": "erp_stock_move_owner_role", "dataScope": 1,
		"menuIds": []int64{erpStockMoveMenuID, 7251, 7252},
	})
	createUserByAPI(t, env, admin, "stock_move_owner", 101, []int64{ownerRoleID})
	ownerToken := env.Login(t, "stock_move_owner", "123456")
	createStockMoveDoc(t, env, ownerToken, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 09:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})

	// 观察者：仅本人（dataScope=5），有全部调拨按钮（含 approve），范围外仍 404。
	roleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "调拨观察者角色", "code": "erp_stock_move_observer_role", "dataScope": 5,
		"menuIds": []int64{erpStockMoveMenuID, 7251, 7252, 7253, 7254, 7255},
	})
	createUserByAPI(t, env, admin, "stock_move_observer", 102, []int64{roleID})
	observerToken := env.Login(t, "stock_move_observer", "123456")
	createStockMoveDoc(t, env, observerToken, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 09:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})

	list, total := pageStockDocs(t, env, observerToken, "stock-move", "")
	if total != 1 || len(list) != 1 {
		t.Fatalf("仅本人观察者应只见自己的调拨单：total=%d list=%v", total, list)
	}
	_, total = pageStockDocs(t, env, ownerToken, "stock-move", "")
	if total != 2 {
		t.Fatalf("owner（全部范围）应见 2 张调拨单：total=%d", total)
	}

	ownerDocs, _ := pageStockDocs(t, env, ownerToken, "stock-move", "")
	ownerDocID := int64(ownerDocs[len(ownerDocs)-1]["id"].(float64))
	status, _ := getJSON(t, env, observerToken, fmt.Sprintf("/erp/stock-move/%d", ownerDocID))
	if status != http.StatusNotFound {
		t.Fatalf("观察者读范围外调拨单：HTTP %d，期望 404", status)
	}
	status, _ = approveDoc(t, env, observerToken, "stock-move", ownerDocID, 20)
	if status != http.StatusNotFound {
		t.Fatalf("观察者审核范围外调拨单：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, observerToken, fmt.Sprintf("/erp/stock-move/%d", ownerDocID))
	if status != http.StatusNotFound {
		t.Fatalf("观察者删除范围外调拨单：HTTP %d，期望 404", status)
	}
}

// TestIntegrationMiscStockPermission 普通角色 403（spec §7）：无 ERP 菜单的
// 角色访问调拨/盘接口 403；仅 query 角色审核 403（approve 是独立权限码）。
func TestIntegrationMiscStockPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, admin, "调拨权限")
	second := createWarehouse(t, env, admin, "调拨权限目标", nil)
	secondID := int64(second["id"].(float64))
	move := createStockMoveDoc(t, env, admin, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 09:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})

	roleA := createRoleByAPI(t, env, admin, map[string]any{
		"name": "无 ERP 角色（调拨盘点）", "code": "erp_misc_stock_no_menu_role", "dataScope": 1,
		"menuIds": []int64{},
	})
	createUserByAPI(t, env, admin, "misc_stock_no_perm", 101, []int64{roleA})
	tokenA := env.Login(t, "misc_stock_no_perm", "123456")

	for _, path := range []string{"/erp/stock-move/page?pageNo=1&pageSize=10", "/erp/stock-check/page?pageNo=1&pageSize=10"} {
		status, _ := env.Request(t, http.MethodGet, path, tokenA, nil)
		if status != http.StatusForbidden {
			t.Fatalf("无 ERP 菜单角色访问 %s：HTTP %d，期望 403", path, status)
		}
	}
	status, _ := env.Request(t, http.MethodPost, "/erp/stock-check", tokenA, map[string]any{
		"warehouseId": warehouseID, "checkTime": env.Date() + " 09:00:00",
		"items": []map[string]any{checkItemJSON(productID, "0")},
	})
	if status != http.StatusForbidden {
		t.Fatalf("无 ERP 菜单角色创建盘点单：HTTP %d，期望 403", status)
	}

	// 角色 B：仅调拨 query（菜单 725，无按钮）→ 读得到，审核 403。
	roleB := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅查询调拨角色", "code": "erp_misc_stock_query_only_role", "dataScope": 1,
		"menuIds": []int64{erpStockMoveMenuID},
	})
	createUserByAPI(t, env, admin, "misc_stock_query_only", 101, []int64{roleB})
	tokenB := env.Login(t, "misc_stock_query_only", "123456")
	status, _ = env.Request(t, http.MethodGet, "/erp/stock-move/page?pageNo=1&pageSize=10", tokenB, nil)
	if status != http.StatusOK {
		t.Fatalf("仅 query 角色读调拨列表：HTTP %d，期望 200", status)
	}
	status, _ = approveDoc(t, env, tokenB, "stock-move", int64(move["id"].(float64)), 20)
	if status != http.StatusForbidden {
		t.Fatalf("仅 query 角色审核调拨单：HTTP %d，期望 403（approve 独立权限码）", status)
	}
}

// TestIntegrationMiscStockRefsAndExport 引用禁删（spec §4.9：产品被调拨/盘点
// 明细引用 409）+ 两类单据导出列/文件名（spec §4.11）。
func TestIntegrationMiscStockRefsAndExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, moveProductID := setupStockEnv(t, env, token, "调拨引用")
	second := createWarehouse(t, env, token, "调拨引用目标", nil)
	secondID := int64(second["id"].(float64))
	category := createCategory(t, env, token, "盘点引用分类", nil)
	unit := createUnit(t, env, token, "盘点引用单位", nil)
	checkProduct := createProduct(t, env, token, "盘点引用产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	checkProductID := int64(checkProduct["id"].(float64))

	// 未审核调拨/盘点明细即构成引用：产品禁删 409。
	createStockMoveDoc(t, env, token, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 09:00:00",
		"items":    []map[string]any{moveItemJSON(moveProductID, "1")},
	})
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", moveProductID))
	if delStatus != http.StatusConflict {
		t.Fatalf("删除被调拨明细引用的产品：HTTP %d code %d msg %q，期望 409", delStatus, delAPI.Code, delAPI.Msg)
	}
	createStockCheckDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "checkTime": env.Date() + " 09:00:00",
		"items": []map[string]any{checkItemJSON(checkProductID, "0")},
	})
	delStatus, delAPI = deleteByID(t, env, token, fmt.Sprintf("/erp/product/%d", checkProductID))
	if delStatus != http.StatusConflict {
		t.Fatalf("删除被盘点明细引用的产品：HTTP %d code %d msg %q，期望 409", delStatus, delAPI.Code, delAPI.Msg)
	}

	// 导出：调拨（调出/调入仓库列）与盘点（仓库/盘点时间列）。
	seedWarehouseStock(t, env, token, warehouseID, moveProductID, "5")
	move := createStockMoveDoc(t, env, token, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": secondID,
		"moveTime": env.Date() + " 10:00:00",
		"items":    []map[string]any{moveItemJSON(moveProductID, "1")},
	})
	if status, api := approveDoc(t, env, token, "stock-move", int64(move["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核导出用调拨单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	header, rows := getExportRows(t, env, token, "/erp/stock-move/export-excel")
	for _, col := range []string{"单号", "调出仓库", "调入仓库", "调拨时间", "状态", "创建时间"} {
		expectColumn(t, "调拨单导出", header, col)
	}
	if len(rows) == 0 || rows[0][1] != "调拨引用仓库" || rows[0][2] != "调拨引用目标" {
		t.Fatalf("调拨单导出仓库列 = %v，期望 调拨引用仓库 → 调拨引用目标", rows)
	}

	check := createStockCheckDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "checkTime": env.Date() + " 10:00:00", "remark": "导出盘点",
		"items": []map[string]any{checkItemJSON(checkProductID, "0")},
	})
	if status, api := approveDoc(t, env, token, "stock-check", int64(check["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核导出用盘点单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	header, rows = getExportRows(t, env, token, "/erp/stock-check/export-excel")
	for _, col := range []string{"单号", "仓库", "盘点时间", "状态", "备注", "创建时间"} {
		expectColumn(t, "盘点单导出", header, col)
	}
	if len(rows) == 0 || rows[0][1] != "调拨引用仓库" || rows[0][4] != "导出盘点" {
		t.Fatalf("盘点单导出列 = %v，期望 仓库=调拨引用仓库、备注=导出盘点", rows)
	}
}
