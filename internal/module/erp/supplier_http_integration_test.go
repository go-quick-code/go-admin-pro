//go:build integration

// 供应商 HTTP 集成测试（issue #89，docs/erp-mvp-spec.md §3.1/§4.10/§5）：
// 供应商字段与 ERP 客户同构（contact/mobile/tax_no/tax_percent/开户信息等
// 全字段）、分页筛选、非法输入 400。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createSupplier 创建供应商并断言成功，返回落库后的完整供应商 JSON。
func createSupplier(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/erp/supplier", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建供应商 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建供应商响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageSuppliers 查询供应商分页并返回 list 与 total。
func pageSuppliers(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/supplier/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询供应商分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析供应商分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// TestIntegrationSupplierCRUD 供应商 CRUD 全链路：全字段落库回读、分页筛选、
// 更新、软删除（与 ERP 客户同构镜像，spec §3.1「字段同 erp_customer」）。
func TestIntegrationSupplierCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	first := createSupplier(t, env, token, "华南钢材供应商", map[string]any{
		"contact":     "钱采购",
		"mobile":      "13700003333",
		"telephone":   "020-88000000",
		"email":       "supply@hn-steel.example",
		"fax":         "020-88000001",
		"taxNo":       "91440000MA1FL08Y3B",
		"taxPercent":  "13.50",
		"bankName":    "招商银行广州分行",
		"bankAccount": "6222000000000333",
		"bankAddress": "广州市天河区支行",
		"sort":        1,
		"status":      0,
		"remark":      "月结 30 天",
	})
	supplierID := int64(first["id"].(float64))
	if first["contact"] != "钱采购" || first["mobile"] != "13700003333" || first["taxNo"] != "91440000MA1FL08Y3B" {
		t.Fatalf("创建回读 contact/mobile/taxNo 不符：%v/%v/%v", first["contact"], first["mobile"], first["taxNo"])
	}
	if first["taxPercent"] != "13.50" {
		t.Fatalf("taxPercent 应为十进制字符串 \"13.50\"，实际 %v", first["taxPercent"])
	}
	if first["bankName"] != "招商银行广州分行" || first["bankAccount"] != "6222000000000333" || first["bankAddress"] != "广州市天河区支行" {
		t.Fatalf("创建回读开户信息不符：%v/%v/%v", first["bankName"], first["bankAccount"], first["bankAddress"])
	}
	if first["ownerUserId"] == float64(0) {
		t.Fatalf("供应商 ownerUserId 恒有值，实际 %v", first["ownerUserId"])
	}

	// 分页与筛选：名称/手机/状态。
	_, total := pageSuppliers(t, env, token, "")
	if total != 1 {
		t.Fatalf("供应商分页 total=%d，期望 1", total)
	}
	_, total = pageSuppliers(t, env, token, "&name=华南")
	if total != 1 {
		t.Fatalf("按名称筛选 total=%d，期望 1", total)
	}
	_, total = pageSuppliers(t, env, token, "&mobile=13700003333")
	if total != 1 {
		t.Fatalf("按手机筛选 total=%d，期望 1", total)
	}
	_, total = pageSuppliers(t, env, token, "&status=1")
	if total != 0 {
		t.Fatalf("按状态 1 筛选 total=%d，期望 0", total)
	}

	// 更新：全字段覆盖生效。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/supplier", token, map[string]any{
		"id": supplierID, "name": "华南钢材改", "contact": "孙采购", "taxPercent": "6.00",
		"bankName": "中国银行广州分行", "status": 1, "sort": 2,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新供应商：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	list, _ := pageSuppliers(t, env, token, "&name=华南钢材改")
	if len(list) != 1 || list[0]["taxPercent"] != "6.00" || list[0]["bankName"] != "中国银行广州分行" ||
		list[0]["status"] != float64(1) {
		t.Fatalf("更新未生效：list=%v", list)
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/supplier/%d", supplierID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除供应商：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ := getJSON(t, env, token, fmt.Sprintf("/erp/supplier/%d", supplierID))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	_, total = pageSuppliers(t, env, token, "")
	if total != 0 {
		t.Fatalf("删除后列表 total=%d，期望 0（软删除不可见）", total)
	}
}

// TestIntegrationSupplierValidation 非法输入 400：名称必填、状态枚举、税率约束
// （数字字面量/超列域/负值）。
func TestIntegrationSupplierValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{name: "缺名称", payload: map[string]any{"mobile": "13700003333"}},
		{name: "状态非法", payload: map[string]any{"name": "非法状态供应商", "status": 9}},
		{name: "税率为数字字面量", payload: map[string]any{"name": "数字税率供应商", "taxPercent": 13.5}},
		{name: "税率超列域", payload: map[string]any{"name": "超列域税率供应商", "taxPercent": "1000.00"}},
		{name: "税率为负", payload: map[string]any{"name": "负税率供应商", "taxPercent": "-0.01"}},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/erp/supplier", token, tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
	}

	// 非法输入不落库。
	_, total := pageSuppliers(t, env, token, "")
	if total != 0 {
		t.Fatalf("非法输入供应商被落库：total=%d，期望 0", total)
	}
	// 列表筛选非法状态同样拒绝（绝不静默当「无此筛选」）。
	status, _ := env.Request(t, http.MethodGet, "/erp/supplier/page?pageNo=1&pageSize=10&status=9", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d，期望 400", status)
	}
}
