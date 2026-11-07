//go:build integration

// 仓库/客户/供应商 Excel 导出 HTTP 集成测试（issue #89，docs/erp-mvp-spec.md
// §4.11/§5）：导出复用列表筛选与数据范围口径，税率经 TextMarshaler 输出十进制
// 字符串，日期按业务时区（NativeDates）；文件头 attachment + xlsx MIME + 中文
// 文件名。
package erp_test

import (
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationPartyExportExcel 三实体导出：列标签、税率十进制字符串、
// 状态中文展示、筛选联动。
func TestIntegrationPartyExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	createWarehouse(t, env, token, "导出仓库A", map[string]any{
		"address": "北京市仓储路 1 号", "principal": "王仓管", "sort": 2,
	})
	createWarehouse(t, env, token, "导出仓库B", nil)

	createCustomer(t, env, token, "导出客户A", map[string]any{
		"contact": "李销售", "mobile": "13800005555",
		"taxNo": "91310000MA1EXPORT01", "taxPercent": "13.00",
		"bankName": "工商银行上海分行", "bankAccount": "6222000000000555",
	})
	createCustomer(t, env, token, "导出客户B", nil)

	createSupplier(t, env, token, "导出供应商A", map[string]any{
		"contact": "周采购", "mobile": "13900006666", "taxPercent": "6.50",
	})

	// 仓库导出：列标签 + 行数 + 筛选联动。
	header, rows := getExportRows(t, env, token, "/erp/warehouse/export-excel")
	expectColumn(t, "仓库导出", header, "仓库名称")
	expectColumn(t, "仓库导出", header, "仓库地址")
	expectColumn(t, "仓库导出", header, "负责人")
	if len(rows) != 2 {
		t.Fatalf("仓库导出应含 2 行，实际 %d 行", len(rows))
	}
	_, rows = getExportRows(t, env, token, "/erp/warehouse/export-excel?name=导出仓库A")
	if len(rows) != 1 {
		t.Fatalf("按名称筛选导出应含 1 行，实际 %d 行", len(rows))
	}
	if !rowHas(rows[0], header, "仓库地址", "北京市仓储路 1 号") || !rowHas(rows[0], header, "负责人", "王仓管") {
		t.Fatalf("仓库导出行不符：%v", rows[0])
	}
	if !rowHas(rows[0], header, "状态", "启用") {
		t.Fatalf("状态应为中文展示：%v", rows[0])
	}

	// 客户导出：税务/开户字段列 + 税率十进制字符串。
	header, rows = getExportRows(t, env, token, "/erp/customer/export-excel")
	expectColumn(t, "客户导出", header, "客户名称")
	expectColumn(t, "客户导出", header, "税号")
	expectColumn(t, "客户导出", header, "税率(%)")
	expectColumn(t, "客户导出", header, "开户行")
	expectColumn(t, "客户导出", header, "开户账号")
	if len(rows) != 2 {
		t.Fatalf("客户导出应含 2 行，实际 %d 行", len(rows))
	}
	_, rows = getExportRows(t, env, token, "/erp/customer/export-excel?name=导出客户A")
	if len(rows) != 1 {
		t.Fatalf("按名称筛选导出应含 1 行，实际 %d 行", len(rows))
	}
	if !rowHas(rows[0], header, "税号", "91310000MA1EXPORT01") {
		t.Fatalf("税号列不符：%v", rows[0])
	}
	if !rowHas(rows[0], header, "税率(%)", "13.00") {
		t.Fatalf("税率应为十进制字符串 13.00：%v", rows[0])
	}
	if !rowHas(rows[0], header, "开户行", "工商银行上海分行") || !rowHas(rows[0], header, "开户账号", "6222000000000555") {
		t.Fatalf("开户信息列不符：%v", rows[0])
	}

	// 供应商导出：同构字段 + 税率。
	header, rows = getExportRows(t, env, token, "/erp/supplier/export-excel")
	expectColumn(t, "供应商导出", header, "供应商名称")
	expectColumn(t, "供应商导出", header, "税率(%)")
	if len(rows) != 1 {
		t.Fatalf("供应商导出应含 1 行，实际 %d 行", len(rows))
	}
	if !rowHas(rows[0], header, "税率(%)", "6.50") || !rowHas(rows[0], header, "联系人", "周采购") {
		t.Fatalf("供应商导出行不符：%v", rows[0])
	}
}

// TestIntegrationPartyExportScopeExport 导出与列表同一数据范围口径（spec §4.10）：
// 仅本人角色导出三实体各只见自己的一条。
func TestIntegrationPartyExportScopeExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	observer := setupPartyScopeEnv(t, env, admin, "erp_party_ds_export", 5)
	token := env.Login(t, observer, "123456")

	_, rows := getExportRows(t, env, token, "/erp/warehouse/export-excel")
	if len(rows) != 1 || !rowHas(rows[0], []string{"仓库名称"}, "仓库名称", "仓库_of_"+observer) {
		t.Fatalf("仅本人仓库导出应只含自己的 1 行：%v", rows)
	}
	_, rows = getExportRows(t, env, token, "/erp/customer/export-excel")
	if len(rows) != 1 || !rowHas(rows[0], []string{"客户名称"}, "客户名称", "客户_of_"+observer) {
		t.Fatalf("仅本人客户导出应只含自己的 1 行：%v", rows)
	}
	_, rows = getExportRows(t, env, token, "/erp/supplier/export-excel")
	if len(rows) != 1 || !rowHas(rows[0], []string{"供应商名称"}, "供应商名称", "供应商_of_"+observer) {
		t.Fatalf("仅本人供应商导出应只含自己的 1 行：%v", rows)
	}
}
