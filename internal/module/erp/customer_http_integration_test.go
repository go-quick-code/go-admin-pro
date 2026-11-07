//go:build integration

// ERP 客户 HTTP 集成测试（issue #89，docs/erp-mvp-spec.md §3.1/§4.10/§5）：
// 客户全字段 CRUD（contact/mobile/telephone/email/fax/tax_no/tax_percent/
// 开户行三件套）、分页筛选、非法输入 400。ERP 客户与 CRM 客户零交叉
// （ADR 0015）见 party_isolation 集成测试。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// createCustomer 创建 ERP 客户并断言成功，返回落库后的完整客户 JSON。
func createCustomer(t *testing.T, env *testutil.IntegrationEnv, token, name string, extra map[string]any) map[string]any {
	t.Helper()
	payload := map[string]any{"name": name}
	for k, v := range extra {
		payload[k] = v
	}
	status, api := env.Request(t, http.MethodPost, "/erp/customer", token, payload)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("创建 ERP 客户 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0", name, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析创建 ERP 客户响应: %v（data=%s）", err, string(api.Data))
	}
	return row
}

// pageCustomers 查询 ERP 客户分页并返回 list 与 total。
func pageCustomers(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64) {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/erp/customer/page?pageNo=1&pageSize=50"+query, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 ERP 客户分页%s：HTTP %d code %d msg %q", query, status, api.Code, api.Msg)
	}
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析 ERP 客户分页: %v（data=%s）", err, string(api.Data))
	}
	return page.List, page.Total
}

// TestIntegrationCustomerCRUD 客户 CRUD 全链路：全字段落库回读（联系人/手机/
// 电话/邮箱/传真/税号/税率/开户行三件套/排序/状态）、分页筛选、更新、软删除。
func TestIntegrationCustomerCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	first := createCustomer(t, env, token, "华东金属", map[string]any{
		"contact":     "王销售",
		"mobile":      "13800001111",
		"telephone":   "021-66000000",
		"email":       "sales@hd-metal.example",
		"fax":         "021-66000001",
		"taxNo":       "91310000MA1FL08X2A",
		"taxPercent":  "13.00",
		"bankName":    "工商银行上海分行",
		"bankAccount": "6222000000000111",
		"bankAddress": "上海市浦东新区支行",
		"sort":        3,
		"status":      0,
		"remark":      "年框客户",
	})
	customerID := int64(first["id"].(float64))
	if first["contact"] != "王销售" || first["mobile"] != "13800001111" || first["telephone"] != "021-66000000" {
		t.Fatalf("创建回读联系三件套不符：%v/%v/%v", first["contact"], first["mobile"], first["telephone"])
	}
	if first["email"] != "sales@hd-metal.example" || first["fax"] != "021-66000001" {
		t.Fatalf("创建回读 email/fax 不符：%v/%v", first["email"], first["fax"])
	}
	if first["taxNo"] != "91310000MA1FL08X2A" {
		t.Fatalf("创建回读税号不符：%v", first["taxNo"])
	}
	// 税率以十进制字符串回读（decimal(5,2)，无 float 往返）。
	if first["taxPercent"] != "13.00" {
		t.Fatalf("taxPercent 应为十进制字符串 \"13.00\"，实际 %v", first["taxPercent"])
	}
	if first["bankName"] != "工商银行上海分行" || first["bankAccount"] != "6222000000000111" || first["bankAddress"] != "上海市浦东新区支行" {
		t.Fatalf("创建回读开户信息不符：%v/%v/%v", first["bankName"], first["bankAccount"], first["bankAddress"])
	}
	if first["sort"] != float64(3) || first["status"] != float64(0) || first["remark"] != "年框客户" {
		t.Fatalf("创建回读 sort/status/remark 不符：%v/%v/%v", first["sort"], first["status"], first["remark"])
	}
	if first["ownerUserId"] == float64(0) {
		t.Fatalf("客户 ownerUserId 恒有值，实际 %v", first["ownerUserId"])
	}

	// 分页与筛选：名称/手机/状态。
	_, total := pageCustomers(t, env, token, "")
	if total != 1 {
		t.Fatalf("客户分页 total=%d，期望 1", total)
	}
	_, total = pageCustomers(t, env, token, "&name=华东")
	if total != 1 {
		t.Fatalf("按名称筛选 total=%d，期望 1", total)
	}
	_, total = pageCustomers(t, env, token, "&mobile=13800001111")
	if total != 1 {
		t.Fatalf("按手机筛选 total=%d，期望 1", total)
	}
	_, total = pageCustomers(t, env, token, "&status=1")
	if total != 0 {
		t.Fatalf("按状态 1 筛选 total=%d，期望 0", total)
	}

	// 更新：全字段覆盖（含税务与开户信息）生效。
	upStatus, upAPI := env.Request(t, http.MethodPut, "/erp/customer", token, map[string]any{
		"id": customerID, "name": "华东金属改", "contact": "赵销售", "mobile": "13900002222",
		"taxNo": "91310000MA1FL08X2B", "taxPercent": "9.00",
		"bankName": "建设银行上海分行", "bankAccount": "6222000000000222", "bankAddress": "上海市静安区支行",
		"status": 1, "sort": 1,
	})
	if upStatus != http.StatusOK || upAPI.Code != 0 {
		t.Fatalf("更新客户：HTTP %d code %d msg %q", upStatus, upAPI.Code, upAPI.Msg)
	}
	list, _ := pageCustomers(t, env, token, "&name=华东金属改")
	if len(list) != 1 || list[0]["taxPercent"] != "9.00" || list[0]["bankName"] != "建设银行上海分行" ||
		list[0]["status"] != float64(1) {
		t.Fatalf("更新未生效：list=%v", list)
	}

	// 软删除：详情 404、列表不可见。
	delStatus, delAPI := deleteByID(t, env, token, fmt.Sprintf("/erp/customer/%d", customerID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除客户：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	status, _ := getJSON(t, env, token, fmt.Sprintf("/erp/customer/%d", customerID))
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d", status)
	}
	_, total = pageCustomers(t, env, token, "")
	if total != 0 {
		t.Fatalf("删除后列表 total=%d，期望 0（软删除不可见）", total)
	}
}

// TestIntegrationCustomerValidation 非法输入 400：名称必填、状态枚举、税率
// 数字字面量拒绝 / 超两位小数拒绝 / 超列域（999.99）拒绝。
func TestIntegrationCustomerValidation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{name: "缺名称", payload: map[string]any{"mobile": "13800001111"}},
		{name: "状态非法", payload: map[string]any{"name": "非法状态客户", "status": 9}},
		{name: "税率为数字字面量", payload: map[string]any{"name": "数字税率客户", "taxPercent": 13.0}},
		{name: "税率超两位小数", payload: map[string]any{"name": "三位税率客户", "taxPercent": "13.005"}},
		{name: "税率超列域", payload: map[string]any{"name": "超列域税率客户", "taxPercent": "1000.00"}},
		{name: "税率为负", payload: map[string]any{"name": "负税率客户", "taxPercent": "-1.00"}},
	}
	for _, tc := range cases {
		status, api := env.Request(t, http.MethodPost, "/erp/customer", token, tc.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s：HTTP %d code %d msg %q，期望 400", tc.name, status, api.Code, api.Msg)
		}
	}

	// 非法输入不落库。
	_, total := pageCustomers(t, env, token, "")
	if total != 0 {
		t.Fatalf("非法输入客户被落库：total=%d，期望 0", total)
	}
	// 列表筛选非法状态同样拒绝（绝不静默当「无此筛选」）。
	status, _ := env.Request(t, http.MethodGet, "/erp/customer/page?pageNo=1&pageSize=10&status=9", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("筛选 status=9：HTTP %d，期望 400", status)
	}
}

// TestIntegrationCustomerNullableTaxFields 税务/开户字段未提交保持 NULL（未填写
// ≠ 0）：创建仅名称 → taxPercent/bank* 回读 null。
func TestIntegrationCustomerNullableTaxFields(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	row := createCustomer(t, env, token, "空税务客户", nil)
	if row["taxPercent"] != nil {
		t.Fatalf("未提交 taxPercent 应为 null，实际 %v", row["taxPercent"])
	}
	if row["taxNo"] != nil || row["bankName"] != nil {
		t.Fatalf("未提交 taxNo/bankName 应为 null，实际 %v/%v", row["taxNo"], row["bankName"])
	}
}
