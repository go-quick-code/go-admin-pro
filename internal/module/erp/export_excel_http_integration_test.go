//go:build integration

// 产品/分类/单位 Excel 导出 HTTP 集成测试（issue #88，docs/erp-mvp-spec.md §4.11/§5）：
// 导出复用列表筛选与数据范围口径，Money/qty 经 TextMarshaler 输出十进制字符串，
// 日期按业务时区（NativeDates）；文件头 attachment + xlsx MIME + 中文文件名。
package erp_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"github.com/xuri/excelize/v2"
)

// getExportRows 请求导出接口并解析「数据」工作表，返回表头与数据行（文本形态）。
func getExportRows(t *testing.T, env *testutil.IntegrationEnv, token, path string) ([]string, [][]string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.APIPrefixURL(path), nil)
	if err != nil {
		t.Fatalf("构造下载请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatalf("下载 Excel: %v", err)
	}
	defer resp.Body.Close()
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment") || !strings.Contains(disposition, ".xlsx") {
		t.Fatalf("GET %s: Content-Disposition=%q，期望 attachment 且带 .xlsx 文件名", path, disposition)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("GET %s: Content-Type=%q，期望 xlsx MIME 类型", path, ct)
	}
	f, err := excelize.OpenReader(resp.Body)
	if err != nil {
		t.Fatalf("解析下载的 xlsx: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	rows, err := f.GetRows("数据")
	if err != nil || len(rows) < 2 {
		t.Fatalf("GET %s: 数据工作表应含表头与数据行，rows=%d err=%v", path, len(rows), err)
	}
	return rows[0], rows[1:]
}

// TestIntegrationProductExportExcel 产品导出：列标签、金额/数量十进制字符串、
// 分类/单位名称解析、筛选联动。
func TestIntegrationProductExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	category := createCategory(t, env, token, "导出分类", nil)
	categoryID := int64(category["id"].(float64))
	unit := createUnit(t, env, token, "导出单位", nil)
	unitID := int64(unit["id"].(float64))
	createProduct(t, env, token, "导出产品A", map[string]any{
		"categoryId": categoryID, "unitId": unitID,
		"barCode": "6900000000001", "standard": "规格A",
		"purchasePrice": "10.00", "salePrice": "20.50", "minPrice": "15.00",
		"weight": "0.250",
	})
	createProduct(t, env, token, "导出产品B", map[string]any{
		"categoryId": categoryID, "unitId": unitID,
	})

	// 全量导出。
	header, rows := getExportRows(t, env, token, "/erp/product/export-excel")
	expectColumn(t, "产品导出", header, "产品名称")
	expectColumn(t, "产品导出", header, "商品条码")
	expectColumn(t, "产品导出", header, "采购参考价")
	if len(rows) != 2 {
		t.Fatalf("产品导出应含 2 行，实际 %d 行", len(rows))
	}

	// 筛选联动（导出复用列表筛选口径）。
	_, rows = getExportRows(t, env, token, "/erp/product/export-excel?name=导出产品A")
	if len(rows) != 1 {
		t.Fatalf("按名称筛选导出应含 1 行，实际 %d 行", len(rows))
	}
	rowByName := map[string][]string{}
	for _, row := range rows {
		rowByName[row[0]] = row
	}
	a := rowByName["导出产品A"]
	if a == nil {
		t.Fatalf("导出缺少产品 A：rows=%v", rows)
	}
	if !rowHas(a, header, "商品条码", "6900000000001") {
		t.Fatalf("条码列不符：%v", a)
	}
	if !rowHas(a, header, "销售参考价", "20.50") {
		t.Fatalf("销售参考价应为十进制字符串 20.50：%v", a)
	}
	if !rowHas(a, header, "重量", "0.250") {
		t.Fatalf("重量应为 qty 定点十进制字符串 0.250：%v", a)
	}
	if !rowHas(a, header, "产品分类", "导出分类") || !rowHas(a, header, "单位", "导出单位") {
		t.Fatalf("分类/单位应由服务端解析为名称：%v", a)
	}
	if !rowHas(a, header, "状态", "启用") {
		t.Fatalf("状态应为中文展示：%v", a)
	}
}

// TestIntegrationCategoryUnitExportExcel 分类与单位导出。
func TestIntegrationCategoryUnitExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	root := createCategory(t, env, token, "导出根分类", map[string]any{"code": "root-cat"})
	createCategory(t, env, token, "导出子分类", map[string]any{"parentId": int64(root["id"].(float64))})
	createUnit(t, env, token, "导出千克", nil)

	header, rows := getExportRows(t, env, token, "/erp/product-category/export-excel")
	expectColumn(t, "分类导出", header, "分类名称")
	expectColumn(t, "分类导出", header, "分类编码")
	if len(rows) != 2 {
		t.Fatalf("分类导出应含 2 行（全量列表口径），实际 %d 行", len(rows))
	}

	header, rows = getExportRows(t, env, token, "/erp/product-unit/export-excel")
	expectColumn(t, "单位导出", header, "单位名称")
	if len(rows) != 1 {
		t.Fatalf("单位导出应含 1 行，实际 %d 行", len(rows))
	}
	if !rowHas(rows[0], header, "单位名称", "导出千克") {
		t.Fatalf("单位导出行不符：%v", rows[0])
	}
}

func expectColumn(t *testing.T, phase string, header []string, want string) {
	t.Helper()
	for _, col := range header {
		if col == want {
			return
		}
	}
	t.Fatalf("%s表头缺少 %q 列：%v", phase, want, header)
}

func rowHas(row []string, header []string, col, want string) bool {
	for i, name := range header {
		if name == col {
			return i < len(row) && row[i] == want
		}
	}
	return false
}
