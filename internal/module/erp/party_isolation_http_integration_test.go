//go:build integration

// ERP 往来主数据租户隔离 + 跨域隔离集成测试（issue #89，spec §4.10/§8.2 +
// ADR 0015）：三张表已登记 TenantTables——跨租户业务 ID 一律 404（不泄露
// 存在性）、租户间列表互不可见；ERP 客户与 CRM 客户完全独立（不同表、不同
// 路由、互不可见、互不引用，零业务打通）。
package erp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationPartyTenantIsolation 租户隔离（仓库/客户/供应商）：
// 租户 2 看不到租户 1 的数据，跨租户详情/修改/删除一律 404；
// 租户 2 自建同主数据不受影响（无跨租户唯一键冲突）。
func TestIntegrationPartyTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 租户 1 建三实体。
	warehouse := createWarehouse(t, env, admin, "隔离仓库", nil)
	customer := createCustomer(t, env, admin, "隔离客户", nil)
	supplier := createSupplier(t, env, admin, "隔离供应商", nil)
	warehouseID := int64(warehouse["id"].(float64))
	customerID := int64(customer["id"].(float64))
	supplierID := int64(supplier["id"].(float64))

	// 租户 2（内置 admin/123456）。
	tenant2 := createTenantByAPI(t, env, admin, "ERP往来隔离测试租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 列表互不可见。
	for _, path := range []string{
		"/erp/warehouse/page?pageNo=1&pageSize=10",
		"/erp/customer/page?pageNo=1&pageSize=10",
		"/erp/supplier/page?pageNo=1&pageSize=10",
	} {
		status, api := env.Request(t, http.MethodGet, path, token2, nil)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("租户 2 查询 %s：HTTP %d code %d", path, status, api.Code)
		}
		var page struct {
			Total int64 `json:"total"`
		}
		if err := json.Unmarshal(api.Data, &page); err != nil {
			t.Fatalf("解析 %s 分页: %v", path, err)
		}
		if page.Total != 0 {
			t.Fatalf("租户 2 %s total=%d，期望 0（租户隔离）", path, page.Total)
		}
	}

	// 跨租户详情一律 404（不泄露存在性）。
	for _, path := range []string{
		fmt.Sprintf("/erp/warehouse/%d", warehouseID),
		fmt.Sprintf("/erp/customer/%d", customerID),
		fmt.Sprintf("/erp/supplier/%d", supplierID),
	} {
		status, _ := getJSON(t, env, token2, path)
		if status != http.StatusNotFound {
			t.Fatalf("租户 2 访问租户 1 详情 %s：HTTP %d，期望 404", path, status)
		}
	}

	// 跨租户修改/删除一律 404。
	status, _ := env.Request(t, http.MethodPut, "/erp/customer", token2, map[string]any{
		"id": customerID, "name": "越权改名",
	})
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 修改租户 1 客户：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, token2, fmt.Sprintf("/erp/warehouse/%d", warehouseID))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 仓库：HTTP %d，期望 404", status)
	}
	status, _ = deleteByID(t, env, token2, fmt.Sprintf("/erp/supplier/%d", supplierID))
	if status != http.StatusNotFound {
		t.Fatalf("租户 2 删除租户 1 供应商：HTTP %d，期望 404", status)
	}

	// 租户 2 自建同主数据不受租户 1 影响（无跨租户唯一键冲突）。
	own := createCustomer(t, env, token2, "隔离客户", nil)
	if own["id"] == customer["id"] {
		t.Fatal("租户 2 客户不应复用租户 1 的 ID 行")
	}

	// 租户 1 仍看到自己的三条。
	for _, page := range []struct {
		query string
		fn    func(t *testing.T, env *testutil.IntegrationEnv, token, query string) ([]map[string]any, int64)
	}{
		{"", pageWarehouses}, {"", pageCustomers}, {"", pageSuppliers},
	} {
		_, total := page.fn(t, env, admin, page.query)
		if total != 1 {
			t.Fatalf("租户 1 total=%d，期望 1", total)
		}
	}
}

// TestIntegrationErpCustomerCrmCustomerZeroCrossing ERP 客户与 CRM 客户零交叉
// （ADR 0015：主数据独立、互不同步、零业务打通）：同名客户在两域各自独立成行
// （不同 ID、不同生命周期）；ERP 客户列表不含 CRM 客户，CRM 客户列表不含 ERP
// 客户；两域删除互不影响（互不引用——删除约束各自只看自己域的数据）。
func TestIntegrationErpCustomerCrmCustomerZeroCrossing(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 同名客户在两域各建一条。
	const sharedName = "同名零交叉客户"
	erpCustomer := createCustomer(t, env, admin, sharedName, map[string]any{
		"taxNo": "91310000MA1ERP0001",
	})
	erpID := int64(erpCustomer["id"].(float64))

	crmStatus, crmAPI := env.Request(t, http.MethodPost, "/crm/customer", admin, map[string]any{
		"name": sharedName, "phone": "13811112222", "remark": "CRM 侧",
	})
	if crmStatus != http.StatusOK || crmAPI.Code != 0 {
		t.Fatalf("创建 CRM 客户：HTTP %d code %d msg %q", crmStatus, crmAPI.Code, crmAPI.Msg)
	}
	var crmRow map[string]any
	if err := json.Unmarshal(crmAPI.Data, &crmRow); err != nil {
		t.Fatalf("解析 CRM 客户响应: %v（data=%s）", err, string(crmAPI.Data))
	}
	crmID := int64(crmRow["id"].(float64))

	// 两域是不同表（各自自增，ID 数值可能巧合相等——不能以 ID 判同表）；
	// 域特有字段形态才证明独立：ERP 行有税号（ERP-only），CRM 行有来源
	//（CRM-only）且无税号字段。
	if erpCustomer["taxNo"] != "91310000MA1ERP0001" {
		t.Fatalf("ERP 客户行应带税号（ERP-only 字段）：%v", erpCustomer["taxNo"])
	}
	if crmRow["source"] == nil || crmRow["taxNo"] != nil {
		t.Fatalf("CRM 客户行应有 CRM-only 字段（source）且无税号字段：%v", crmRow)
	}

	// ERP 客户列表只含 ERP 侧（CRM 同名客户不可见）；CRM 列表只含 CRM 侧。
	erpList, erpTotal := pageCustomers(t, env, admin, "&name="+sharedName)
	if erpTotal != 1 || len(erpList) != 1 || int64(erpList[0]["id"].(float64)) != erpID {
		t.Fatalf("ERP 客户列表应仅含 ERP 侧 1 条：total=%d list=%v", erpTotal, erpList)
	}
	crmPageStatus, crmPageAPI := env.Request(t, http.MethodGet, "/crm/customer/page?pageNo=1&pageSize=50&name="+sharedName, admin, nil)
	if crmPageStatus != http.StatusOK || crmPageAPI.Code != 0 {
		t.Fatalf("查询 CRM 客户分页：HTTP %d code %d msg %q", crmPageStatus, crmPageAPI.Code, crmPageAPI.Msg)
	}
	var crmPage struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(crmPageAPI.Data, &crmPage); err != nil {
		t.Fatalf("解析 CRM 客户分页: %v", err)
	}
	if crmPage.Total != 1 || len(crmPage.List) != 1 || int64(crmPage.List[0]["id"].(float64)) != crmID {
		t.Fatalf("CRM 客户列表应仅含 CRM 侧 1 条：total=%d list=%v", crmPage.Total, crmPage.List)
	}

	// 互不引用：删除 CRM 客户不受 ERP 侧同名客户影响，反之亦然；
	// 两域删除互不连带（各自只查自己域的引用）。
	delStatus, delAPI := deleteByID(t, env, admin, fmt.Sprintf("/crm/customer/%d", crmID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除 CRM 客户：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	// CRM 侧删除后 ERP 侧仍在。
	list, total := pageCustomers(t, env, admin, "&name="+sharedName)
	if total != 1 || len(list) != 1 || int64(list[0]["id"].(float64)) != erpID {
		t.Fatalf("CRM 删除不应影响 ERP 客户：total=%d", total)
	}
	delStatus, delAPI = deleteByID(t, env, admin, fmt.Sprintf("/erp/customer/%d", erpID))
	if delStatus != http.StatusOK || delAPI.Code != 0 {
		t.Fatalf("删除 ERP 客户：HTTP %d code %d msg %q", delStatus, delAPI.Code, delAPI.Msg)
	}
	// ERP 侧删除后（CRM 侧已删）两域均空。
	_, total = pageCustomers(t, env, admin, "&name="+sharedName)
	if total != 0 {
		t.Fatalf("两域删除后 ERP 列表应空：total=%d", total)
	}

	// ERP 仓库/供应商与 CRM 亦无交叉：CRM 客户删除约束不因 ERP 供应商存在而 409
	//（上面删除 CRM 客户已成功，此处补一个跨域删除后 ERP 供应商仍独立的断言）。
	_, supTotal := pageSuppliers(t, env, admin, "")
	if supTotal != 0 {
		t.Fatalf("本环境供应商应空（无跨域数据泄漏）：total=%d", supTotal)
	}
}
