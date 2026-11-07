//go:build integration

// 仓库 HTTP 集成测试（issue #89，docs/erp-mvp-spec.md §3.1/§4.10/§5）：
// 仓库 CRUD（address/principal 展示、status、sort）、分页筛选、非法输入 400。
// 删除引用约束骨架（erp_warehouse 引用类型）见 party_refs 单测与 #90+ 单据票接入。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

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
	// 非法输入不落库。
	_, total := pageWarehouses(t, env, token, "")
	if total != 0 {
		t.Fatalf("非法输入仓库被落库：total=%d，期望 0", total)
	}
	// 列表筛选非法状态同样拒绝（绝不静默当「无此筛选」）。
	status, _ = env.Request(t, http.MethodGet, "/erp/warehouse/page?pageNo=1&pageSize=10&status=9", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d，期望 400", status)
	}
}
