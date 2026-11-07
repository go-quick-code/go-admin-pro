//go:build integration

// 产品单位 HTTP 集成测试（issue #88，docs/erp-mvp-spec.md §3.1/§5）：
// 单位是仅名称字典（无换算率），分页 CRUD + 状态校验；被产品引用禁删 409
// 的用例在产品域测试文件（引用约束随产品落地）。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createUnit 创建产品单位并断言成功，返回落库后的完整单位 JSON。
func createUnit(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/erp/product-unit", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建单位 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建单位响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageUnits 查询单位分页并返回 list 与 total。
func pageUnits(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/product-unit/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询单位分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析单位分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// TestIntegrationProductUnitCRUD 单位 CRUD 全链路：分页筛选、详情、更新、软删除。
func TestIntegrationProductUnitCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	first := createUnit(t, env, token, "千克", map[string]any{"status": 0})
	second := createUnit(t, env, token, "台", nil)

	// 分页与名称筛选。
	list, total := pageUnits(t, env, token, "")
	if total != 2 || len(list) != 2 {
		t.Fatalf("单位分页 total=%d len=%d，期望 2", total, len(list))
	}
	_, total = pageUnits(t, env, token, "&name=千")
	if total != 1 {
		t.Fatalf("按名称筛选 total=%d，期望 1", total)
	}

	// 详情回读。
	status, api := getJSON(t, env, token, fmt.Sprintf("/erp/product-unit/%d", int64(first["id"].(float64))))
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询单位详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail map[string]any
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析详情: %v", err)
	}
	if detail["name"] != "千克" || detail["status"] != float64(0) {
		t.Fatalf("详情字段不符：name=%v status=%v", detail["name"], detail["status"])
	}

	// 更新：名称与状态生效。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/product-unit", token, map[string]any{
		"id": first["id"], "name": "公斤", "status": 1,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新单位：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	list, _ = pageUnits(t, env, token, "&name=公斤")
	if len(list) != 1 || list[0]["status"] != float64(1) {
		t.Fatalf("更新未生效：list=%v", list)
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/product-unit/%d", int64(second["id"].(float64))))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除单位：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ = getJSON(t, env, token, fmt.Sprintf("/erp/product-unit/%d", int64(second["id"].(float64))))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	_, total = pageUnits(t, env, token, "")
	if total != 1 {
		t.Fatalf("删除后列表 total=%d，期望 1（软删除不可见）", total)
	}

	// 不存在 404。
	status, _ = getJSON(t, env, token, "/erp/product-unit/999999")
	if status != http.StatusNotFound {
		t.Fatalf("查询单位 999999：HTTP %d，期望 404", status)
	}
}

// TestIntegrationProductUnitValidation 非法输入 400：名称必填、状态枚举。
func TestIntegrationProductUnitValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	status, api := env.Request(t, http.MethodPost, "/erp/product-unit", token, map[string]any{"name": ""})
	if status != http.StatusBadRequest {
		t.Fatalf("空名称：HTTP %d code %d，期望 400", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, "/erp/product-unit", token, map[string]any{"name": "非法状态", "status": 9})
	if status != http.StatusBadRequest {
		t.Fatalf("状态 9：HTTP %d code %d，期望 400", status, api.Code)
	}
	// 列表筛选非法枚举同样拒绝（绝不静默当「无此筛选」）。
	status, api = env.Request(t, http.MethodGet, "/erp/product-unit/page?pageNo=1&pageSize=10&status=9", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d code %d，期望 400", status, api.Code)
	}

	// 非法状态不落库。
	_, total := pageUnits(t, env, token, "&name=非法状态")
	if total != 0 {
		t.Fatalf("非法状态单位被落库：total=%d，期望 0", total)
	}
}
