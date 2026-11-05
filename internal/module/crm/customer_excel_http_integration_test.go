//go:build integration

// 客户 Excel 导入导出 HTTP 集成测试（issue #20）：
// 模板下载、全量校验整批拒绝、导入成功落库、导出表头/金额日期格式与数据权限过滤。
package crm_test

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationCustomerExcelTemplateAndImport 模板下载 + 导入成功 + 整批拒绝：
// 模板表头与导入行结构一致；成功导入的客户编号由服务端发放、状态缺省 1、负责人为当前用户；
// 任意一行不合法时整批拒绝（库内无新增）并返回行号 + 列名 + 原因。
func TestIntegrationCustomerExcelTemplateAndImport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 模板：数据表头 + 填写说明工作表。
	file := downloadExcel(t, env, admin, "/crm/customer/import-template")
	assertHeader(t, file, customerTemplateHeader)
	if sheets := file.GetSheetList(); len(sheets) < 2 {
		t.Fatalf("模板应包含数据与填写说明两个工作表，实际 %v", sheets)
	}

	// 成功导入：两行，一行状态留空（缺省 1），一行状态 2。
	content := buildWorkbook(t, customerImportHeader, [][]any{
		customerImportPayload("导入客户甲", "制造业", "A", "展会", "13800000001", "https://a.example.com", "上海市", nil, "首行"),
		customerImportPayload("导入客户乙", "", "", "", "", "", "", "2", ""),
	})
	status, api := uploadExcel(t, env, admin, "/crm/customer/import", "customers.xlsx", content)
	mustOK(t, "导入客户", status, api)
	if got := importCount(t, api); got != 2 {
		t.Fatalf("导入成功条数=%d，期望 2", got)
	}

	list, total := pageCustomers(t, env, admin, "&name=导入客户")
	if total != 2 || len(list) != 2 {
		t.Fatalf("导入后客户列表 total=%d（期望 2），list=%d", total, len(list))
	}
	byName := map[string]map[string]any{}
	for _, row := range list {
		byName[row["name"].(string)] = row
	}
	a := byName["导入客户甲"]
	if a == nil || !customerNoRe.MatchString(fmt.Sprint(a["customerNo"])) {
		t.Fatalf("导入客户应发放 KH 编号，实际 %v", a)
	}
	if a["status"] != float64(1) {
		t.Fatalf("状态留空应缺省 1（潜在），实际 %v", a["status"])
	}
	if a["ownerUserId"] != float64(1) || a["ownerDeptId"] != float64(100) {
		t.Fatalf("导入客户负责人应缺省当前用户并快照部门，实际 owner=%v dept=%v", a["ownerUserId"], a["ownerDeptId"])
	}
	if a["industry"] != "制造业" || a["phone"] != "13800000001" {
		t.Fatalf("导入字段未落库：industry=%v phone=%v", a["industry"], a["phone"])
	}
	if b := byName["导入客户乙"]; b == nil || b["status"] != float64(2) {
		t.Fatalf("状态 2 应落为活跃，实际 %v", b)
	}

	// 整批拒绝：第 2 行合法、第 3 行状态非法，返回第 3 行 [状态]，且不新增任何数据。
	_, beforeTotal := pageCustomers(t, env, admin, "")
	invalid := buildWorkbook(t, customerImportHeader, [][]any{
		customerImportPayload("不应导入的客户", "", "", "", "", "", "", nil, ""),
		customerImportPayload("非法状态客户", "", "", "", "", "", "", "9", ""),
	})
	status, api = uploadExcel(t, env, admin, "/crm/customer/import", "customers-invalid.xlsx", invalid)
	assertImportValidation(t, status, api, 3, "状态")
	if _, total := pageCustomers(t, env, admin, "&name=不应导入的客户"); total != 0 {
		t.Fatalf("整批拒绝时不应写入任何行，实际 total=%d", total)
	}
	if _, total := pageCustomers(t, env, admin, ""); total != beforeTotal {
		t.Fatalf("失败导入后客户总数应保持 %d，实际 %d", beforeTotal, total)
	}

	// 表头不匹配：400 业务错误，文案指向模板。
	badHeader := buildWorkbook(t, []string{"名称"}, [][]any{{"张三"}})
	status, api = uploadExcel(t, env, admin, "/crm/customer/import", "bad-header.xlsx", badHeader)
	if status != http.StatusBadRequest {
		t.Fatalf("错误表头：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 行数上限 5000：5001 行直接 400，不进入落库。
	rows := make([][]any, 0, 5001)
	for i := 0; i < 5001; i++ {
		rows = append(rows, customerImportPayload(fmt.Sprintf("超限客户%d", i), "", "", "", "", "", "", nil, ""))
	}
	overflow := buildWorkbook(t, customerImportHeader, rows)
	status, api = uploadExcel(t, env, admin, "/crm/customer/import", "overflow.xlsx", overflow)
	if status != http.StatusBadRequest {
		t.Fatalf("5001 行：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}

	// 文件大小上限 10MB：超 1 字节即 400。
	tooLarge := make([]byte, 10<<20+1)
	status, api = uploadExcel(t, env, admin, "/crm/customer/import", "too-large.xlsx", tooLarge)
	if status != http.StatusBadRequest {
		t.Fatalf("10MB+1：HTTP %d code %d msg %q，期望 400", status, api.Code, api.Msg)
	}
}

// TestIntegrationCustomerExcelImportPermission 导入/模板挂 crm:customer:import：
// 只有查询权限的角色请求导入与模板均 403；持有导入按钮码的角色可导入。
func TestIntegrationCustomerExcelImportPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	readonly := newExcelRole(t, env, admin, "cust_excel_readonly", "cust_excel_readonly", 1, []int64{crmCustomerMenuID}, 102)
	content := buildWorkbook(t, customerImportHeader, [][]any{
		customerImportPayload("无权限导入客户", "", "", "", "", "", "", nil, ""),
	})
	if status, api := uploadExcel(t, env, readonly, "/crm/customer/import", "c.xlsx", content); status != http.StatusForbidden {
		t.Fatalf("无导入权限上传：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	if status, api := env.Request(t, http.MethodGet, "/crm/customer/import-template", readonly, nil); status != http.StatusForbidden {
		t.Fatalf("无导入权限下载模板：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}

	importer := newExcelRole(t, env, admin, "cust_excel_importer", "cust_excel_importer", 1, []int64{crmCustomerMenuID, btnCustomerImport}, 102)
	status, api := uploadExcel(t, env, importer, "/crm/customer/import", "c.xlsx", content)
	mustOK(t, "持导入权限的角色导入客户", status, api)
	if got := importCount(t, api); got != 1 {
		t.Fatalf("导入条数=%d，期望 1", got)
	}
}

// TestIntegrationCustomerExcelExportScope 导出复用列表筛选与数据权限：
// 表头/金额/原生日期正确；不同视角导出行数不同；搜索条件生效；无导出码 403。
func TestIntegrationCustomerExcelExportScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 仅本人视角的销售：创建于市场部，只应导出自己名下的客户。
	sales := newExcelRole(t, env, admin, "cust_excel_self", "cust_excel_self", 5, []int64{crmCustomerMenuID, 4011, btnCustomerExport}, 102)

	createCustomer(t, env, admin, "导出客户A", map[string]any{"industry": "制造业", "status": 2})
	createCustomer(t, env, admin, "导出客户B", nil)
	// 销售自己创建的客户（负责人 = 销售）。
	createCustomer(t, env, sales, "销售自己的客户", map[string]any{"phone": "13800000002"})

	// admin（全部范围）导出全部 3 条；销售仅导出自己的 1 条。
	adminFile := downloadExcel(t, env, admin, "/crm/customer/export-excel")
	assertHeader(t, adminFile, customerExportHeader)
	adminRows := sheetRows(t, adminFile, "")
	if len(adminRows) != 4 {
		t.Fatalf("admin 应导出 3 条客户（含表头 4 行），实际 %d 行", len(adminRows))
	}

	salesFile := downloadExcel(t, env, sales, "/crm/customer/export-excel")
	salesRows := sheetRows(t, salesFile, "")
	if len(salesRows) != 2 || salesRows[1][1] != "销售自己的客户" {
		t.Fatalf("仅本人视角应只导出自己的 1 条客户，实际 %+v", salesRows)
	}

	// 金额列与日期列：状态输出中文，创建时间是 Excel 原生日期（原始值为序列号）。
	if salesRows[1][2] != "潜在" {
		t.Fatalf("状态列应为中文「潜在」，实际 %q", salesRows[1][2])
	}
	raw := cellRaw(t, salesFile, "", "M2")
	if _, err := strconv.ParseFloat(raw, 64); err != nil {
		t.Fatalf("创建时间应为原生日期序列号，实际原始值 %q（err=%v）", raw, err)
	}
	if formatted := sheetRows(t, salesFile, "")[1][12]; !strings.Contains(formatted, "-") {
		t.Fatalf("创建时间应按日期格式显示，实际 %q", formatted)
	}

	// 筛选条件生效：按名称导出只剩 1 条。
	filtered := downloadExcel(t, env, admin, "/crm/customer/export-excel?name=导出客户A")
	if rows := sheetRows(t, filtered, ""); len(rows) != 2 || rows[1][1] != "导出客户A" {
		t.Fatalf("按名称筛选的导出应只有「导出客户A」，实际 %+v", rows)
	}

	// 无导出码：403。
	noExport := newExcelRole(t, env, admin, "cust_excel_no_export", "cust_excel_no_export", 1, []int64{crmCustomerMenuID}, 102)
	if status, api := env.Request(t, http.MethodGet, "/crm/customer/export-excel", noExport, nil); status != http.StatusForbidden {
		t.Fatalf("无导出权限：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
}
