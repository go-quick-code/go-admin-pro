//go:build integration

// 仓库 HTTP 集成测试（issue #89，docs/erp-mvp-spec.md §3.1/§4.10/§5）：
// 仓库 CRUD（address/principal 展示、status、sort）、分页筛选、非法输入 400。
// 删除引用约束骨架（erp_warehouse 引用类型）见 party_refs 单测与 #90+ 单据票接入。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createWarehouse 创建仓库并断言成功，返回落库后的完整仓库 JSON。
func createWarehouse(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/erp/warehouse", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建仓库 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建仓库响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageWarehouses 查询仓库分页并返回 list 与 total。
func pageWarehouses(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/warehouse/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询仓库分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析仓库分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// TestIntegrationWarehouseCRUD 仓库 CRUD 全链路：地址/负责人/排序/状态全字段落库
// 回读、分页筛选、更新、软删除。
func TestIntegrationWarehouseCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	first := createWarehouse(t, env, token, "原料仓", map[string]any{
		"address":   "上海市浦东新区仓库一路 1 号",
		"principal": "张仓管",
		"sort":      2,
		"status":    0,
	})
	warehouseID := int64(first["id"].(float64))
	if first["address"] != "上海市浦东新区仓库一路 1 号" || first["principal"] != "张仓管" {
		t.Fatalf("创建回读 address/principal 不符：%v/%v", first["address"], first["principal"])
	}
	if first["sort"] != float64(2) || first["status"] != float64(0) {
		t.Fatalf("创建回读 sort/status 不符：%v/%v", first["sort"], first["status"])
	}
	// owner = 创建人（spec §4.10）。
	if first["ownerUserId"] == float64(0) {
		t.Fatalf("仓库 ownerUserId 恒有值，实际 %v", first["ownerUserId"])
	}

	// 分页与筛选：名称/状态。
	_, total := pageWarehouses(t, env, token, "")
	if total != 1 {
		t.Fatalf("仓库分页 total=%d，期望 1", total)
	}
	_, total = pageWarehouses(t, env, token, "&name=原料")
	if total != 1 {
		t.Fatalf("按名称筛选 total=%d，期望 1", total)
	}
	_, total = pageWarehouses(t, env, token, "&status=1")
	if total != 0 {
		t.Fatalf("按状态 1 筛选 total=%d，期望 0", total)
	}

	// 更新：名称/地址/负责人/排序/状态全字段覆盖生效。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/warehouse", token, map[string]any{
		"id": warehouseID, "name": "成品仓", "address": "杭州市仓储路 8 号",
		"principal": "李仓管", "sort": 5, "status": 1,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新仓库：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	list, _ := pageWarehouses(t, env, token, "&name=成品仓")
	if len(list) != 1 || list[0]["address"] != "杭州市仓储路 8 号" ||
		list[0]["principal"] != "李仓管" || list[0]["status"] != float64(1) {
		t.Fatalf("更新未生效：list=%v", list)
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/warehouse/%d", warehouseID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除仓库：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ := getJSON(t, env, token, fmt.Sprintf("/erp/warehouse/%d", warehouseID))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	_, total = pageWarehouses(t, env, token, "")
	if total != 0 {
		t.Fatalf("删除后列表 total=%d，期望 0（软删除不可见）", total)
	}
}

// TestIntegrationWarehouseValidation 非法输入 400：名称必填、状态枚举
// （0 启用 / 1 停用）、列表筛选非法状态拒绝。
func TestIntegrationWarehouseValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, _ := env.Request(t, http.MethodPost, "/erp/warehouse", token, map[string]any{"address": "无名仓库"})
	if status != http.StatusBadRequest {
		t.Fatalf("缺名称创建：HTTP %d，期望 400", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/erp/warehouse", token, map[string]any{"name": "非法状态仓", "status": 9})
	if status != http.StatusBadRequest {
		t.Fatalf("status=9 创建：HTTP %d，期望 400", status)
	}
	// sort 列是 MySQL int（int32）：超出列域入口 400，不能落到 MySQL 1264 → 500。
	status, _ = env.Request(t, http.MethodPost, "/erp/warehouse", token, map[string]any{"name": "超域排序仓", "sort": 2147483648})
	if status != http.StatusBadRequest {
		t.Fatalf("sort=2147483648 创建：HTTP %d，期望 400", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/erp/warehouse", token, map[string]any{"name": "负超域排序仓", "sort": -2147483649})
	if status != http.StatusBadRequest {
		t.Fatalf("sort=-2147483649 创建：HTTP %d，期望 400", status)
	}
	// int32 边界值可用。
	boundary := createWarehouse(t, env, token, "边界排序仓", map[string]any{"sort": 2147483647})
	if boundary["sort"] != float64(2147483647) {
		t.Fatalf("边界 sort 回读 = %v，期望 2147483647", boundary["sort"])
	}
	// 非法输入不落库（边界仓合法，故只校验列表中有且仅有 1 条）。
	_, total := pageWarehouses(t, env, token, "")
	if total != 1 {
		t.Fatalf("非法排序被落库或边界值缺失：total=%d，期望 1", total)
	}
	// 列表筛选非法状态同样拒绝（绝不静默当「无此筛选」）。
	status, _ = env.Request(t, http.MethodGet, "/erp/warehouse/page?pageNo=1&pageSize=10&status=9", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d，期望 400", status)
	}
}

// TestIntegrationWarehouseDocRefs 仓库被单据主表引用禁删（spec §4.9，#98 审查
// MINOR）：未审核其它入库单挂 W → 删除 W 409；调拨计调出/调入两仓；删除单据
// 释放引用后仓库恢复可删。
func TestIntegrationWarehouseDocRefs(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouse := createWarehouse(t, env, token, "单据引用仓", nil)
	warehouseID := int64(warehouse["id"].(float64))
	warehouseB := createWarehouse(t, env, token, "调拨调出仓", nil)
	warehouseBID := int64(warehouseB["id"].(float64))
	warehouseC := createWarehouse(t, env, token, "调拨调入仓", nil)
	warehouseCID := int64(warehouseC["id"].(float64))
	category := createCategory(t, env, token, "单据引用分类", nil)
	unit := createUnit(t, env, token, "单据引用单位", nil)
	product := createProduct(t, env, token, "单据引用产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})

	// 未审核其它入库单引用 W：未落账也必须禁删（防「建单→删仓→审核」）。
	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(int64(product["id"].(float64)), "1", "1.00")},
	})
	docID := int64(doc["id"].(float64))
	status, api := deleteByID(t, env, token, fmt.Sprintf("/erp/warehouse/%d", warehouseID))
	if status != http.StatusConflict || !strings.Contains(api.Msg, "单据引用") {
		t.Fatalf("删除被未审核入库单引用的仓库：HTTP %d msg %q，期望 409 且提示单据引用", status, api.Msg)
	}

	// 调拨单引用调出/调入两仓：两侧都禁删。
	moveStatus, moveAPI := env.Request(t, http.MethodPost, "/erp/stock-move", token, map[string]any{
		"fromWarehouseId": warehouseBID, "toWarehouseId": warehouseCID,
		"moveTime": env.Date() + " 10:00:00",
		"items":    []map[string]any{{"productId": int64(product["id"].(float64)), "count": "1"}},
	})
	if moveStatus != http.StatusOK || moveAPI.Code != 0 {
		t.Fatalf("创建调拨单：HTTP %d code %d msg %q", moveStatus, moveAPI.Code, moveAPI.Msg)
	}
	for _, id := range []int64{warehouseBID, warehouseCID} {
		status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/warehouse/%d", id))
		if status != http.StatusConflict || !strings.Contains(api.Msg, "单据引用") {
			t.Fatalf("删除被调拨单引用的仓库 %d：HTTP %d msg %q，期望 409 且提示单据引用", id, status, api.Msg)
		}
	}

	// 删除未审核单据释放引用后，仓库恢复可删。
	if status, api := deleteByID(t, env, token, fmt.Sprintf("/erp/stock-in/%d", docID)); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("删除未审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = deleteByID(t, env, token, fmt.Sprintf("/erp/warehouse/%d", warehouseID))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("引用释放后删除仓库：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// TestIntegrationWarehouseSoftDeletedApprove 仓库被软删后审核落账复核（spec
// §4.1/§4.9，#98 审查 MINOR 双保险）：直接改库软删仓库后审核单据必须 409，
// 库存与流水不产生（绝不向已删除仓库落库存）。
func TestIntegrationWarehouseSoftDeletedApprove(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "软删仓审核")
	doc := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "3", "1.00")},
	})
	docID := int64(doc["id"].(float64))

	// 绕过删除引用守卫直接软删仓库（模拟历史脏数据/并发删除）。
	if err := tenant.Skip(env.DB).Exec(
		"UPDATE `erp_warehouse` SET `deleted_at` = NOW(3) WHERE `id` = ?", warehouseID,
	).Error; err != nil {
		t.Fatalf("软删仓库: %v", err)
	}
	status, api := approveDoc(t, env, token, "stock-in", docID, 20)
	if status != http.StatusConflict || !strings.Contains(api.Msg, "仓库不存在或已删除") {
		t.Fatalf("软删仓库后审核入库单：HTTP %d msg %q，期望 409 且提示仓库已删除", status, api.Msg)
	}
	if got := getStockCount(t, env, token, productID, warehouseID); got != "0.000" {
		t.Fatalf("软删仓库审核失败不应落库存：%s，期望 0.000", got)
	}
	if records, total := pageStockRecords(t, env, token, ""); total != 0 || len(records) != 0 {
		t.Fatalf("软删仓库审核失败不应产生流水：total=%d list=%d，期望 0", total, len(records))
	}
	if detail := getStockInDetail(t, env, token, docID); detail["status"] != float64(10) {
		t.Fatalf("审核失败后单据应保持未审核，实际 status=%v", detail["status"])
	}
}
