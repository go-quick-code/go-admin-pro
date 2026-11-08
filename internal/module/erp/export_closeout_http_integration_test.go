//go:build integration

// Excel 导出收口 HTTP 集成测试（issue #96，docs/erp-mvp-spec.md §4.11/§5/§8.2）：
// 全量端点清单（12 类单据 + 库存余额 + 出入库流水 + 6 类主数据；结算账户不导出）、
// 列头契约、中文文件名双编码、export 权限码独立 403、数据范围与租户隔离。
package erp_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// erpExportEndpoint 是一个导出端点的收口契约：路径 + 必含列。
type erpExportEndpoint struct {
	name    string
	path    string
	columns []string
}

// erpMasterExportEndpoints 是主数据导出（6 类；spec §5）。
var erpMasterExportEndpoints = []erpExportEndpoint{
	{"产品", "/erp/product/export-excel", []string{"产品名称", "商品条码", "采购参考价"}},
	{"产品分类", "/erp/product-category/export-excel", []string{"分类名称", "分类编码"}},
	{"产品单位", "/erp/product-unit/export-excel", []string{"单位名称", "状态"}},
	{"仓库", "/erp/warehouse/export-excel", []string{"仓库名称", "负责人"}},
	{"客户", "/erp/customer/export-excel", []string{"客户名称", "税率(%)"}},
	{"供应商", "/erp/supplier/export-excel", []string{"供应商名称", "税率(%)"}},
}

// erpDocExportEndpoints 是十二类单据导出（spec §4.11；负责人/销售员导出昵称）。
var erpDocExportEndpoints = []erpExportEndpoint{
	{"采购订单", "/erp/purchase-order/export-excel", []string{"单号", "供应商", "负责人"}},
	{"采购入库单", "/erp/purchase-in/export-excel", []string{"单号", "供应商", "采购订单号", "单据金额", "负责人"}},
	{"采购退货单", "/erp/purchase-return/export-excel", []string{"单号", "供应商", "采购入库单号", "单据金额", "负责人"}},
	{"销售订单", "/erp/sale-order/export-excel", []string{"订单号", "客户", "销售员", "负责人"}},
	{"销售出库单", "/erp/sale-out/export-excel", []string{"出库单号", "客户", "出库总金额", "负责人"}},
	{"销售退货单", "/erp/sale-return/export-excel", []string{"退货单号", "客户", "退货总金额", "负责人"}},
	{"其它入库单", "/erp/stock-in/export-excel", []string{"单号", "仓库", "类型", "负责人"}},
	{"其它出库单", "/erp/stock-out/export-excel", []string{"单号", "仓库", "类型", "负责人"}},
	{"库存调拨单", "/erp/stock-move/export-excel", []string{"单号", "调出仓库", "调入仓库", "负责人"}},
	{"库存盘点单", "/erp/stock-check/export-excel", []string{"单号", "仓库", "盘点时间", "负责人"}},
	{"收款单", "/erp/finance-receipt/export-excel", []string{"收款单号", "客户", "结算账户", "核销金额合计", "负责人"}},
	{"付款单", "/erp/finance-payment/export-excel", []string{"付款单号", "供应商", "结算账户", "核销金额合计", "负责人"}},
}

// erpStockExportEndpoints 是库存余额 + 出入库流水导出（库存行无 owner 语义，spec §4.10）。
var erpStockExportEndpoints = []erpExportEndpoint{
	{"产品库存", "/erp/stock/export-excel", []string{"产品名称", "仓库", "库存数量", "更新时间"}},
	{"出入库流水", "/erp/stock-record/export-excel", []string{"业务类型", "单据编号", "变动数量", "结存数量", "入账时间"}},
}

// allErpExportEndpoints 汇总 20 个导出端点（结算账户按 spec §4.11/§6 不导出，#37）。
func allErpExportEndpoints() []erpExportEndpoint {
	all := make([]erpExportEndpoint, 0, len(erpMasterExportEndpoints)+len(erpDocExportEndpoints)+len(erpStockExportEndpoints))
	all = append(all, erpMasterExportEndpoints...)
	all = append(all, erpDocExportEndpoints...)
	all = append(all, erpStockExportEndpoints...)
	return all
}

// erpMenuIDs 是 20 个导出端点对应页面的菜单 ID（菜单自身即 query 权限码，seed 700 段）。
var erpMenuIDs = []int64{710, 711, 712, 720, 730, 740, 721, 722, 723, 724, 731, 732, 733, 741, 742, 743, 751, 752, 725, 726}

// erpExportButtonIDs 是 20 个导出按钮 ID（菜单ID×10+n，seed 700 段）。
var erpExportButtonIDs = []int64{7104, 7114, 7124, 7204, 7304, 7404, 7211, 7221, 7235, 7245, 7315, 7325, 7335, 7415, 7425, 7435, 7515, 7525, 7255, 7265}

// TestIntegrationExportCloseoutContracts 全量导出端点：xlsx 附件 + 中文文件名
// 双编码（helper 内断言）+ 列头契约（空库也能断言表头，spec §4.11/§8.2）。
func TestIntegrationExportCloseoutContracts(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")

	for _, ep := range allErpExportEndpoints() {
		header, _ := getExportSheet(t, env, token, ep.path)
		for _, col := range ep.columns {
			expectColumn(t, ep.name+"导出", header, col)
		}
	}
}

// TestIntegrationExportCloseoutPermission export 是独立权限码（spec §7）：
// 内置普通角色默认无 ERP 权限；仅有页面菜单（query）而无导出按钮的角色，
// 页面可读但导出 403——证明 export 不被 query 牵连。
func TestIntegrationExportCloseoutPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	common := env.Login(t, "demo", "123456")
	for _, ep := range allErpExportEndpoints() {
		if status, _ := env.Request(t, http.MethodGet, ep.path, common, nil); status != http.StatusForbidden {
			t.Fatalf("普通角色导出 %s：HTTP %d，期望 403", ep.path, status)
		}
	}

	queryOnlyRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP 仅查询角色（导出收口）", "code": "erp_export_query_only_role",
		"dataScope": 1, "menuIds": erpMenuIDs,
	})
	createUserByAPI(t, env, admin, "erp_export_query_only", 101, []int64{queryOnlyRole})
	queryOnly := env.Login(t, "erp_export_query_only", "123456")

	// 页面可读：菜单授予了 query（分页/列表各域抽查）。
	for _, path := range []string{
		"/erp/product/page?pageNo=1&pageSize=10",
		"/erp/purchase-order/page?pageNo=1&pageSize=10",
		"/erp/sale-order/page?pageNo=1&pageSize=10",
		"/erp/stock/page?pageNo=1&pageSize=10",
		"/erp/stock-record/page?pageNo=1&pageSize=10",
		"/erp/finance-receipt/page?pageNo=1&pageSize=10",
	} {
		if status, _ := env.Request(t, http.MethodGet, path, queryOnly, nil); status != http.StatusOK {
			t.Fatalf("仅查询角色读 %s：HTTP %d，期望 200", path, status)
		}
	}
	for _, ep := range allErpExportEndpoints() {
		if status, _ := env.Request(t, http.MethodGet, ep.path, queryOnly, nil); status != http.StatusForbidden {
			t.Fatalf("仅查询角色导出 %s：HTTP %d，期望 403（export 独立权限码）", ep.path, status)
		}
	}
}

// TestIntegrationExportCloseoutScopeAndTenant 导出与列表共用数据范围口径、
// 强制租户隔离（spec §4.10/§4.11）：仅本人角色导出 12 类单据与主数据 0 行
// （库存/流水按 #14 决策租户内全量，仍可见）；租户 2 管理员导出 20 个端点全 0 行。
func TestIntegrationExportCloseoutScopeAndTenant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	warehouseID, productID, customerID := setupSaleEnv(t, env, admin, "导出收口")
	supplier := createSupplier(t, env, admin, "导出收口供应商", nil)
	supplierID := int64(supplier["id"].(float64))

	// 采购链：订单 → 入库（审核落库存）→ 退货（未审核即可导出）。
	purchaseOrder := createPurchaseDoc(t, env, admin, "purchase-order", map[string]any{
		"supplierId": supplierID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "10", "2.00")},
	})
	purchaseOrderID := int64(purchaseOrder["id"].(float64))
	mustApprovePurchaseDoc(t, env, admin, "purchase-order", purchaseOrderID)
	purchaseIn := createPurchaseDoc(t, env, admin, "purchase-in", map[string]any{
		"supplierId": supplierID, "orderId": purchaseOrderID, "warehouseId": warehouseID,
		"inTime": env.Date() + " 10:00:00",
		"items":  []map[string]any{purchaseItemJSON(productID, "10", "2.00")},
	})
	purchaseInID := int64(purchaseIn["id"].(float64))
	mustApprovePurchaseDoc(t, env, admin, "purchase-in", purchaseInID)
	createPurchaseDoc(t, env, admin, "purchase-return", map[string]any{
		"supplierId": supplierID, "inId": purchaseInID, "returnTime": env.Date() + " 11:00:00",
		"items": []map[string]any{purchaseItemJSON(productID, "1", "2.00")},
	})

	// 销售链：订单 → 出库（审核扣库存）→ 退货（未审核即可导出）。
	saleOrder := createSaleOrderDoc(t, env, admin, map[string]any{
		"customerId": customerID, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{saleItemJSON(productID, "2", "3.00")},
	})
	saleOrderID := int64(saleOrder["id"].(float64))
	mustApproveSale(t, env, admin, "sale-order", saleOrderID, 20)
	saleOut := createSaleOutDoc(t, env, admin, map[string]any{
		"customerId": customerID, "orderId": saleOrderID, "warehouseId": warehouseID,
		"outTime": env.Date() + " 10:00:00",
		"items":   []map[string]any{saleItemJSON(productID, "2", "3.00")},
	})
	saleOutID := int64(saleOut["id"].(float64))
	mustApproveSale(t, env, admin, "sale-out", saleOutID, 20)
	createSaleReturnDoc(t, env, admin, map[string]any{
		"customerId": customerID, "outId": saleOutID,
		"returnTime": env.Date() + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "1", "3.00")},
	})

	// 财务：收款挂已审核出库、付款挂已审核入库（单头即可导出）。
	account := createFinanceAccountDoc(t, env, admin, "导出收口账户", nil)
	accountID := int64(account["id"].(float64))
	createFinanceDoc(t, env, admin, "finance-receipt", map[string]any{
		"customerId": customerID, "accountId": accountID,
		"receiptTime": env.Date() + " 12:00:00",
		"items":       []map[string]any{financeItemJSON("sale_out", saleOutID, "1.00")},
	})
	createFinanceDoc(t, env, admin, "finance-payment", map[string]any{
		"supplierId": supplierID, "accountId": accountID,
		"paymentTime": env.Date() + " 12:00:00",
		"items":       []map[string]any{financeItemJSON("purchase_in", purchaseInID, "1.00")},
	})

	// 其它出入库/调拨/盘点：未审核单即可导出。
	createStockInDoc(t, env, admin, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 13:00:00",
		"items": []map[string]any{stockItemJSON(productID, "1", "1.00")},
	})
	createStockOutDoc(t, env, admin, map[string]any{
		"warehouseId": warehouseID, "outTime": env.Date() + " 13:00:00",
		"items": []map[string]any{stockItemJSON(productID, "1", "1.00")},
	})
	secondWarehouse := createWarehouse(t, env, admin, "导出收口目标仓", nil)
	createStockMoveDoc(t, env, admin, map[string]any{
		"fromWarehouseId": warehouseID, "toWarehouseId": int64(secondWarehouse["id"].(float64)),
		"moveTime": env.Date() + " 13:00:00",
		"items":    []map[string]any{moveItemJSON(productID, "1")},
	})
	createStockCheckDoc(t, env, admin, map[string]any{
		"warehouseId": warehouseID, "checkTime": env.Date() + " 13:00:00",
		"items": []map[string]any{checkItemJSON(productID, "0")},
	})

	// 管理员导出：12 类单据各至少 1 行，且负责人/销售员列导出昵称而非 ID。
	for _, ep := range erpDocExportEndpoints {
		header, rows := getExportRows(t, env, admin, ep.path)
		if len(rows) == 0 {
			t.Fatalf("%s导出应至少 1 行", ep.name)
		}
		if !rowHas(rows[0], header, "负责人", "超级管理员") {
			t.Fatalf("%s导出的负责人列应为人名昵称，实际 %v（表头 %v）", ep.name, rows[0], header)
		}
	}

	// 仅本人角色（有 query + 全部 export 按钮，dataScope=5）：单据与主数据 0 行。
	menuIDs := append(append([]int64{}, erpMenuIDs...), erpExportButtonIDs...)
	selfRole := createRoleByAPI(t, env, admin, map[string]any{
		"name": "ERP 仅本人角色（导出收口）", "code": "erp_export_self_role",
		"dataScope": 5, "menuIds": menuIDs,
	})
	createUserByAPI(t, env, admin, "erp_export_self_only", 101, []int64{selfRole})
	selfToken := env.Login(t, "erp_export_self_only", "123456")

	for _, ep := range append(append([]erpExportEndpoint{}, erpMasterExportEndpoints...), erpDocExportEndpoints...) {
		header, rows := getExportSheet(t, env, selfToken, ep.path)
		if len(rows) != 0 {
			t.Fatalf("仅本人角色导出%s（%s）应 0 行，实际 %d 行（表头 %v）", ep.name, ep.path, len(rows), header)
		}
	}
	// 库存/流水无 owner 语义（#14 决策）：同一角色仍能看到租户内库存与流水。
	for _, ep := range erpStockExportEndpoints {
		_, rows := getExportRows(t, env, selfToken, ep.path)
		if len(rows) == 0 {
			t.Fatalf("库存类导出 %s 不做 owner 过滤，应至少 1 行", ep.path)
		}
	}

	// 租户隔离：租户 2 管理员导出 20 个端点全部 0 行（无数据外泄）。
	tenant2 := createTenantByAPI(t, env, admin, "导出收口隔离租户")
	admin2 := loginTenant(t, env, "admin", "123456", tenant2)
	for _, ep := range allErpExportEndpoints() {
		header, rows := getExportSheet(t, env, admin2, ep.path)
		if len(rows) != 0 {
			t.Fatalf("租户 2 导出%s应 0 行，实际 %d 行（表头 %v）", ep.name, len(rows), header)
		}
	}
}

// TestIntegrationExportCloseoutSaleUserNickname 销售订单的销售员列导出昵称
// （纯记录字段 saleUserId 的展示形态与列表一致）。
func TestIntegrationExportCloseoutSaleUserNickname(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	_, productID, customerID := setupSaleEnv(t, env, admin, "销售员导出")
	createSaleOrderDoc(t, env, admin, map[string]any{
		"customerId": customerID, "saleUserId": 1, "orderTime": env.Date() + " 09:00:00",
		"items": []map[string]any{saleItemJSON(productID, "1", "1.00")},
	})

	header, rows := getExportRows(t, env, admin, "/erp/sale-order/export-excel")
	if len(rows) != 1 {
		t.Fatalf("销售订单导出应 1 行，实际 %d", len(rows))
	}
	if !rowHas(rows[0], header, "销售员", "超级管理员") {
		t.Fatalf("销售员列应导出用户昵称（id=1 → 超级管理员），实际 %s", fmt.Sprint(rows[0]))
	}
}
