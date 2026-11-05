//go:build integration

// 合同 Excel 导出 HTTP 集成测试（issue #20）：导出列覆盖列表关键字段与金额/日期，
// 金额十进制字符串、日期为原生日期单元格，数据权限与列表一致。
package crm_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// TestIntegrationContractExcelExport 合同导出：表头、金额精度、原生日期、
// 搜索筛选、数据范围过滤与无导出码 403。
func TestIntegrationContractExcelExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	sales := newExcelRole(t, env, admin, "contract_excel_self", "contract_excel_self", 5,
		[]int64{crmCustomerMenuID, 4011, crmContractMenuID, 4101, 4102, btnContractExport}, 102)
	ownCustomer := createCustomer(t, env, sales, "合同导出客户A", nil)
	otherCustomer := createCustomer(t, env, admin, "合同导出客户B", nil)

	own := createContract(t, env, sales, "销售自己的合同", map[string]any{
		"customerId": ownCustomer["id"],
		"amount":     "1234.56",
		"signDate":   "2026-01-02",
		"startDate":  "2026-01-03",
		"endDate":    "2026-12-31",
		"remark":     "销售合同备注",
	})
	createContract(t, env, admin, "他人的合同", map[string]any{
		"customerId": otherCustomer["id"],
		"amount":     "99.99",
	})

	// admin 导出全部 2 条。
	adminFile := downloadExcel(t, env, admin, "/crm/contract/export-excel")
	assertHeader(t, adminFile, contractExportHeader)
	if rows := sheetRows(t, adminFile, ""); len(rows) != 3 {
		t.Fatalf("admin 应导出表头 + 2 行，实际 %d 行", len(rows))
	}

	// 仅本人视角只导出自己的 1 条。
	salesFile := downloadExcel(t, env, sales, "/crm/contract/export-excel")
	assertHeader(t, salesFile, contractExportHeader)
	rows := sheetRows(t, salesFile, "")
	if len(rows) != 2 {
		t.Fatalf("仅本人视角应导出 1 条合同，实际 %d 行：%+v", len(rows), rows)
	}
	row := rows[1]
	if row[0] != own["contractNo"] || row[1] != "销售自己的合同" || row[2] != "合同导出客户A" {
		t.Fatalf("合同导出关键字段不符：%+v", row)
	}
	if row[3] != "1234.56" {
		t.Fatalf("合同金额应精确输出 1234.56，实际 %q", row[3])
	}
	if row[4] != "草稿" {
		t.Fatalf("合同状态应为中文「草稿」，实际 %q", row[4])
	}
	// 签订日期为 Excel 原生日期：原始值是序列号。
	if raw := cellRaw(t, salesFile, "", "F2"); func() bool { _, err := strconv.ParseFloat(raw, 64); return err != nil }() {
		t.Fatalf("签订日期应为原生日期序列号，实际原始值 %q", raw)
	}
	// 签订日期序列号 2026-01-02 == 46024（Excel 1900 系统），验证墙上时间不漂移。
	if raw := cellRaw(t, salesFile, "", "F2"); raw != "46024" {
		t.Fatalf("2026-01-02 的 Excel 序列号应为 46024，实际 %q", raw)
	}

	// 搜索条件生效。
	filtered := downloadExcel(t, env, admin, "/crm/contract/export-excel?name=销售自己的合同")
	if rows := sheetRows(t, filtered, ""); len(rows) != 2 || rows[1][1] != "销售自己的合同" {
		t.Fatalf("按名称筛选导出结果不符：%+v", rows)
	}

	// 无导出码 403。
	noExport := newExcelRole(t, env, admin, "contract_excel_no_export", "contract_excel_no_export", 1,
		[]int64{crmContractMenuID}, 102)
	if status, api := env.Request(t, http.MethodGet, "/crm/contract/export-excel", noExport, nil); status != http.StatusForbidden {
		t.Fatalf("无合同导出权限：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
}
