//go:build integration

// 联系人 Excel 导入 HTTP 集成测试（issue #20）：模板、用客户编号关联客户、
// 范围外客户按行错误拒绝、整批拒绝不落库、权限 403。
package crm_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationContactExcelImport 联系人导入：成功按客户编号关联；未知客户、
// 非法手机号与范围外客户都返回行级错误且整批不写库。
func TestIntegrationContactExcelImport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	file := downloadExcel(t, env, admin, "/crm/contact/import-template")
	assertHeader(t, file, contactTemplateHeader)

	customerA := createCustomer(t, env, admin, "联系人导入客户A", nil)
	customerB := createCustomer(t, env, admin, "联系人导入客户B", nil)
	noA := fmt.Sprint(customerA["customerNo"])
	noB := fmt.Sprint(customerB["customerNo"])

	content := buildWorkbook(t, contactImportHeader, [][]any{
		{noA, "张三", "采购经理", "13800000001", "zhangsan@example.com", "1", "决策人", "首行"},
		{noB, "李四", "", "", "", nil, "", ""},
	})
	status, api := uploadExcel(t, env, admin, "/crm/contact/import", "contacts.xlsx", content)
	mustOK(t, "导入联系人", status, api)
	if got := importCount(t, api); got != 2 {
		t.Fatalf("导入联系人条数=%d，期望 2", got)
	}

	list, total := pageContacts(t, env, admin, "&name=张三")
	if total != 1 || len(list) != 1 {
		t.Fatalf("导入后联系人应可按姓名查到，total=%d", total)
	}
	if list[0]["customerId"] != customerA["id"] || list[0]["customerName"] != "联系人导入客户A" {
		t.Fatalf("联系人应挂到客户 A，实际 customerId=%v customerName=%v", list[0]["customerId"], list[0]["customerName"])
	}
	if list[0]["gender"] != float64(1) || list[0]["email"] != "zhangsan@example.com" {
		t.Fatalf("联系人字段未落库：gender=%v email=%v", list[0]["gender"], list[0]["email"])
	}

	// 未知客户编号：第 2 行 [客户编号] 行错误，整批拒绝。
	unknown := buildWorkbook(t, contactImportHeader, [][]any{
		{"KH000000000000", "未知客户联系人", "", "", "", nil, "", ""},
	})
	status, api = uploadExcel(t, env, admin, "/crm/contact/import", "unknown.xlsx", unknown)
	assertImportValidation(t, status, api, 2, "客户编号")
	if _, total := pageContacts(t, env, admin, "&name=未知客户联系人"); total != 0 {
		t.Fatalf("客户不存在的行不应落库，实际 total=%d", total)
	}

	// 非法手机号：第 2 行 [手机] 行错误。
	badMobile := buildWorkbook(t, contactImportHeader, [][]any{
		{noA, "手机号错误联系人", "", "123", "", nil, "", ""},
	})
	status, api = uploadExcel(t, env, admin, "/crm/contact/import", "bad-mobile.xlsx", badMobile)
	assertImportValidation(t, status, api, 2, "手机")
	if _, total := pageContacts(t, env, admin, "&name=手机号错误联系人"); total != 0 {
		t.Fatalf("手机号非法的行不应落库，实际 total=%d", total)
	}
}

// TestIntegrationContactExcelImportScopeAndPermission 导入关联的客户必须可见：
// 仅本人视角销售导入他人客户按行错误拒绝、导入自己客户成功；无导入码 403。
func TestIntegrationContactExcelImportScopeAndPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	sales := newExcelRole(t, env, admin, "contact_excel_self", "contact_excel_self", 5, []int64{crmCustomerMenuID, 4011, btnContactImport}, 102)
	adminCustomer := createCustomer(t, env, admin, "他人客户", nil)
	ownCustomer := createCustomer(t, env, sales, "自己的客户", nil)

	outOfScope := buildWorkbook(t, contactImportHeader, [][]any{
		{fmt.Sprint(adminCustomer["customerNo"]), "越权联系人", "", "", "", nil, "", ""},
	})
	status, api := uploadExcel(t, env, sales, "/crm/contact/import", "scope.xlsx", outOfScope)
	assertImportValidation(t, status, api, 2, "客户编号")
	if _, total := pageContacts(t, env, admin, "&name=越权联系人"); total != 0 {
		t.Fatalf("范围外客户的联系人不应落库，实际 total=%d", total)
	}

	own := buildWorkbook(t, contactImportHeader, [][]any{
		{fmt.Sprint(ownCustomer["customerNo"]), "自己的联系人", "", "", "", nil, "", ""},
	})
	status, api = uploadExcel(t, env, sales, "/crm/contact/import", "own.xlsx", own)
	mustOK(t, "导入自己客户下的联系人", status, api)
	if got := importCount(t, api); got != 1 {
		t.Fatalf("导入条数=%d，期望 1", got)
	}

	readonly := newExcelRole(t, env, admin, "contact_excel_readonly", "contact_excel_readonly", 1, []int64{crmCustomerMenuID}, 102)
	if status, api := uploadExcel(t, env, readonly, "/crm/contact/import", "c.xlsx", own); status != http.StatusForbidden {
		t.Fatalf("无联系人导入权限：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	if status, api := env.Request(t, http.MethodGet, "/crm/contact/import-template", readonly, nil); status != http.StatusForbidden {
		t.Fatalf("无联系人导入权限下载模板：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
}
