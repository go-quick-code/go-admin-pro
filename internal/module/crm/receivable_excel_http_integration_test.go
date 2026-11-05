//go:build integration

// 回款计划 Excel 导出 HTTP 集成测试（issue #20）：导出含计划/已收金额、到期日与
// 派生状态（逾期），数据权限 join 合同。
package crm_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/lautung/go-admin-pro/internal/testutil"
)

// TestIntegrationReceivablePlanExcelExport 回款计划导出：金额/日期/派生状态、
// 筛选、数据范围与无导出码 403。
func TestIntegrationReceivablePlanExcelExport(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	sales := newExcelRole(t, env, admin, "rv_excel_self", "rv_excel_self", 5,
		[]int64{crmCustomerMenuID, 4011, crmContractMenuID, 4101, 4102, crmReceivableMenuID, 4121, 4122, btnReceivableExport}, 102)
	ownCustomer := createCustomer(t, env, sales, "回款导出客户A", nil)
	otherCustomer := createCustomer(t, env, admin, "回款导出客户B", nil)

	ownContract := createContract(t, env, sales, "回款导出合同A", map[string]any{
		"customerId": ownCustomer["id"], "amount": "1000.00", "signDate": "2026-01-02",
	})
	otherContract := createContract(t, env, admin, "回款导出合同B", map[string]any{
		"customerId": otherCustomer["id"], "amount": "500.00",
	})
	// 到期日取未来日期，保证「部分回款」不会被派生为「逾期」。
	ownPlan := createReceivablePlan(t, env, sales, map[string]any{
		"contractId": ownContract["id"], "periodNo": 1, "amount": "1000.00", "dueDate": "2027-06-30", "remark": "首期",
	})
	createReceivablePlan(t, env, admin, map[string]any{
		"contractId": otherContract["id"], "periodNo": 1, "amount": "500.00", "dueDate": "2027-07-31",
	})
	// 部分回款：派生状态「部分回款」。
	createReceivableRecord(t, env, sales, map[string]any{
		"planId": ownPlan["id"], "amount": "400.00", "receiptDate": "2026-03-01",
	})

	adminFile := downloadExcel(t, env, admin, "/crm/receivable/plan/export-excel")
	assertHeader(t, adminFile, receivableExportHeader)
	if rows := sheetRows(t, adminFile, ""); len(rows) != 3 {
		t.Fatalf("admin 应导出表头 + 2 行，实际 %d 行", len(rows))
	}

	salesFile := downloadExcel(t, env, sales, "/crm/receivable/plan/export-excel")
	assertHeader(t, salesFile, receivableExportHeader)
	rows := sheetRows(t, salesFile, "")
	if len(rows) != 2 {
		t.Fatalf("仅本人视角应导出 1 条计划，实际 %d 行：%+v", len(rows), rows)
	}
	row := rows[1]
	if row[0] != ownContract["contractNo"] || row[1] != "回款导出合同A" || row[2] != "回款导出客户A" {
		t.Fatalf("回款计划导出关键字段不符：%+v", row)
	}
	if row[3] != "1" || row[4] != "1000.00" || row[5] != "1000.00" || row[6] != "400.00" {
		t.Fatalf("期次/合同金额/计划金额/已收金额不符：%+v", row)
	}
	if row[8] != "部分回款" || row[9] != "否" {
		t.Fatalf("派生状态应为「部分回款」且未逾期，实际 status=%q overdue=%q", row[8], row[9])
	}
	// 到期日为原生日期（2027-06-30 == 46568）。
	if raw := cellRaw(t, salesFile, "", "H2"); raw != "46568" {
		t.Fatalf("2027-06-30 的 Excel 序列号应为 46568，实际 %q", raw)
	}

	// 逾期口径：到期日在过去的计划导出为逾期。单开一张金额足够的合同，
	// 避免“0 元计划”被派生为已回款，也不突破计划金额 ≤ 合同金额的不变量。
	overdueContract := createContract(t, env, admin, "回款导出逾期合同", map[string]any{
		"customerId": otherCustomer["id"], "amount": "100.00",
	})
	createReceivablePlan(t, env, admin, map[string]any{
		"contractId": overdueContract["id"], "periodNo": 1, "amount": "100.00", "dueDate": "2000-01-01",
	})
	overdueFile := downloadExcel(t, env, admin, "/crm/receivable/plan/export-excel")
	overdueRows := sheetRows(t, overdueFile, "")
	foundOverdue := false
	for _, r := range overdueRows[1:] {
		if r[0] == fmt.Sprint(overdueContract["contractNo"]) && r[8] == "逾期" && r[9] == "是" {
			foundOverdue = true
		}
	}
	if !foundOverdue {
		t.Fatalf("到期日在过去的计划应导出为逾期，实际 %+v", overdueRows)
	}

	// 搜索条件：按合同筛选。
	filtered := downloadExcel(t, env, admin, "/crm/receivable/plan/export-excel?contractId="+fmt.Sprint(ownContract["id"]))
	if rows := sheetRows(t, filtered, ""); len(rows) != 2 {
		t.Fatalf("按合同筛选应导出 1 条计划，实际 %d 行", len(rows))
	}

	noExport := newExcelRole(t, env, admin, "rv_excel_no_export", "rv_excel_no_export", 1,
		[]int64{crmReceivableMenuID}, 102)
	if status, api := env.Request(t, http.MethodGet, "/crm/receivable/plan/export-excel", noExport, nil); status != http.StatusForbidden {
		t.Fatalf("无回款导出权限：HTTP %d code %d msg %q，期望 403", status, api.Code, api.Msg)
	}
}
