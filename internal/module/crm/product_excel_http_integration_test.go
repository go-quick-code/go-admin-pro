//go:build integration

// 产品 Excel 导入导出 HTTP 集成测试（issue #20）：分类名称关联、价格十进制精确、
// 编码唯一（批内/库内）、整批拒绝、导出表头与权限。
package crm_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// TestIntegrationProductExcelImport 产品导入成功与各类行级错误：
// 分类按名称关联（可空）、价格按十进制字符串精确落库、批内/库内重复编码拒绝。
func TestIntegrationProductExcelImport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	file := downloadExcel(t, env, admin, "/crm/product/import-template")
	assertHeader(t, file, productTemplateHeader)

	category := createProductCategory(t, env, admin, "导入分类")
	categoryName := fmt.Sprint(category["name"])

	content := buildWorkbook(t, productImportHeader, [][]any{
		{"导入产品甲", "IMP-001", categoryName, "台", "1234.56", "1", "首行"},
		{"导入产品乙", "IMP-002", "", "", "0.01", nil, ""},
		{"导入产品丙", "IMP-007", "", "", "0.00", "0", ""},
	})
	status, api := uploadExcel(t, env, admin, "/crm/product/import", "products.xlsx", content)
	mustOK(t, "导入产品", status, api)
	if got := importCount(t, api); got != 3 {
		t.Fatalf("导入产品条数=%d，期望 3", got)
	}

	list, total := pageProducts(t, env, admin, "&code=IMP-")
	if total != 3 || len(list) != 3 {
		t.Fatalf("导入后产品列表 total=%d（期望 3），list=%d", total, len(list))
	}
	byCode := map[string]map[string]any{}
	for _, row := range list {
		byCode[fmt.Sprint(row["code"])] = row
	}
	if byCode["IMP-001"]["price"] != "1234.56" {
		t.Fatalf("价格应按十进制精确存储 1234.56，实际 %v", byCode["IMP-001"]["price"])
	}
	if byCode["IMP-001"]["categoryId"] != category["id"] {
		t.Fatalf("分类名称应解析为分类 ID，实际 %v（期望 %v）", byCode["IMP-001"]["categoryId"], category["id"])
	}
	if byCode["IMP-002"]["status"] != float64(1) {
		t.Fatalf("状态留空应缺省 1 上架，实际 %v", byCode["IMP-002"]["status"])
	}
	// 0 是合法的下架状态，不能被 GORM 的列默认值改写成 1。
	if byCode["IMP-007"]["status"] != float64(0) {
		t.Fatalf("显式状态 0 应落为下架，实际 %v", byCode["IMP-007"]["status"])
	}

	// 价格超两位小数：解析阶段行错误 [价格]，整批拒绝。
	badPrice := buildWorkbook(t, productImportHeader, [][]any{
		{"合法产品", "IMP-003", categoryName, "", "9.90", "1", ""},
		{"价格精度错误", "IMP-004", "", "", "1234.567", "1", ""},
	})
	status, api = uploadExcel(t, env, admin, "/crm/product/import", "bad-price.xlsx", badPrice)
	assertImportValidation(t, status, api, 3, "价格")
	if _, total := pageProducts(t, env, admin, "&code=IMP-003"); total != 0 {
		t.Fatalf("整批拒绝时合法行也不应落库，实际 total=%d", total)
	}

	// 批内重复编码：第 3 行 [产品编码] 行错误。
	dupInFile := buildWorkbook(t, productImportHeader, [][]any{
		{"重复产品一", "IMP-005", "", "", "1.00", "1", ""},
		{"重复产品二", "IMP-005", "", "", "1.00", "1", ""},
	})
	status, api = uploadExcel(t, env, admin, "/crm/product/import", "dup.xlsx", dupInFile)
	assertImportValidation(t, status, api, 3, "产品编码")

	// 库内已存在编码：第 2 行 [产品编码] 行错误。
	existing := buildWorkbook(t, productImportHeader, [][]any{
		{"已有编码产品", "IMP-001", "", "", "1.00", "1", ""},
	})
	status, api = uploadExcel(t, env, admin, "/crm/product/import", "existing.xlsx", existing)
	assertImportValidation(t, status, api, 2, "产品编码")

	// 分类名称不存在：第 2 行 [分类名称] 行错误。
	badCategory := buildWorkbook(t, productImportHeader, [][]any{
		{"分类错误产品", "IMP-006", "不存在的分类", "", "1.00", "1", ""},
	})
	status, api = uploadExcel(t, env, admin, "/crm/product/import", "bad-category.xlsx", badCategory)
	assertImportValidation(t, status, api, 2, "分类名称")
}

// TestIntegrationProductExcelExportAndPermission 产品导出：表头/分类名称/价格文本/状态；
// 筛选生效；无导出码 403，无导入码 403。
func TestIntegrationProductExcelExportAndPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	category := createProductCategory(t, env, admin, "导出分类")
	createProduct(t, env, admin, "导出产品A", map[string]any{
		"categoryId": category["id"], "code": "EXP-001", "price": "88.88", "status": 1,
	})
	createdB := createProduct(t, env, admin, "导出产品B", map[string]any{
		"categoryId": category["id"], "code": "EXP-002", "price": "0.01", "status": 0,
	})
	if createdB["status"] != float64(0) {
		t.Fatalf("创建产品 B 的状态应为 0 下架，实际 %v", createdB["status"])
	}

	file := downloadExcel(t, env, admin, "/crm/product/export-excel")
	assertHeader(t, file, productExportHeader)
	rows := sheetRows(t, file, "")
	if len(rows) != 3 {
		t.Fatalf("产品导出应为表头 + 2 行，实际 %d 行：%+v", len(rows), rows)
	}
	// 列表按 id DESC，后创建的产品 B 在前。
	if rows[1][1] != "EXP-002" || rows[1][2] != "导出分类" || rows[1][4] != "0.01" || rows[1][5] != "下架" {
		t.Fatalf("产品导出行字段不符：%+v", rows[1])
	}

	filtered := downloadExcel(t, env, admin, "/crm/product/export-excel?code=EXP-001")
	if rows := sheetRows(t, filtered, ""); len(rows) != 2 || rows[1][1] != "EXP-001" {
		t.Fatalf("按编码筛选导出应只有 EXP-001，实际 %+v", rows)
	}

	noExport := newExcelRole(t, env, admin, "prod_excel_no_export", "prod_excel_no_export", 1, []int64{crmProductMenuID}, 102)
	if status, api := env.Request(t, http.MethodGet, "/crm/product/export-excel", noExport, nil); status != http.StatusForbidden {
		t.Fatalf("无产品导出权限：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	if status, api := uploadExcel(t, env, noExport, "/crm/product/import", "p.xlsx", buildWorkbook(t, productImportHeader, [][]any{{"X", "X1", "", "", "1.00", "1", ""}})); status != http.StatusForbidden {
		t.Fatalf("无产品导入权限：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
	if status, api := env.Request(t, http.MethodGet, "/crm/product/import-template", noExport, nil); status != http.StatusForbidden {
		t.Fatalf("无产品导入权限下载模板：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
}
