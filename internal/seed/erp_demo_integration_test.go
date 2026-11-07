//go:build integration

// ERP 演示数据集成测试（issue #97）：真实 MySQL 上验证
//   - seed.Run(db, true) 写入完整 ERP 演示链路：主数据 → 采购订单/入库 →
//     盘点 → 销售订单/出库 → 收款/付款，且各域至少一张已审核单据；
//   - 重复执行按表哨兵幂等，不重复插入，也不回退 erp_sequence；
//   - 不变量成立：明细金额 = 单价 × 数量、主表金额 = 明细合计、订单进度只计
//     已审核单、收付款回写等于已审核核销合计、库存余额 = 流水代数和且快照逐笔
//     正确、全程无负库存；
//   - 单号格式与 erp_sequence 覆盖（后续 UI 新建不撞号）；
//   - 管理员可经 HTTP 看到统计数据与各页面数据；内置普通角色默认无任何 ERP
//     权限（菜单与接口双重确认，spec §7/§9）。
package seed_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"testing"

	erpmodel "github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// erpDemoCounts 是一次 ERP 演示数据写入后的行数快照（幂等断言用）。
type erpDemoCounts struct {
	Categories         int64
	Units              int64
	Products           int64
	Warehouses         int64
	Customers          int64
	Suppliers          int64
	Accounts           int64
	PurchaseOrders     int64
	PurchaseOrderItems int64
	PurchaseIns        int64
	PurchaseInItems    int64
	StockChecks        int64
	StockCheckItems    int64
	SaleOrders         int64
	SaleOrderItems     int64
	SaleOuts           int64
	SaleOutItems       int64
	Receipts           int64
	ReceiptItems       int64
	Payments           int64
	PaymentItems       int64
	Stocks             int64
	StockRecords       int64
	Sequences          int64
}

func countERPDemo(t *testing.T, db *gorm.DB) erpDemoCounts {
	t.Helper()
	count := func(model any) int64 {
		var total int64
		if err := tenant.Skip(db).Model(model).Count(&total).Error; err != nil {
			t.Fatalf("统计 %T: %v", model, err)
		}
		return total
	}
	var sequences int64
	if err := tenant.Skip(db).Table("erp_sequence").Count(&sequences).Error; err != nil {
		t.Fatalf("统计 erp_sequence: %v", err)
	}
	return erpDemoCounts{
		Categories:         count(&erpmodel.ProductCategory{}),
		Units:              count(&erpmodel.ProductUnit{}),
		Products:           count(&erpmodel.Product{}),
		Warehouses:         count(&erpmodel.Warehouse{}),
		Customers:          count(&erpmodel.Customer{}),
		Suppliers:          count(&erpmodel.Supplier{}),
		Accounts:           count(&erpmodel.FinanceAccount{}),
		PurchaseOrders:     count(&erpmodel.PurchaseOrder{}),
		PurchaseOrderItems: count(&erpmodel.PurchaseOrderItem{}),
		PurchaseIns:        count(&erpmodel.PurchaseIn{}),
		PurchaseInItems:    count(&erpmodel.PurchaseInItem{}),
		StockChecks:        count(&erpmodel.StockCheck{}),
		StockCheckItems:    count(&erpmodel.StockCheckItem{}),
		SaleOrders:         count(&erpmodel.SaleOrder{}),
		SaleOrderItems:     count(&erpmodel.SaleOrderItem{}),
		SaleOuts:           count(&erpmodel.SaleOut{}),
		SaleOutItems:       count(&erpmodel.SaleOutItem{}),
		Receipts:           count(&erpmodel.FinanceReceipt{}),
		ReceiptItems:       count(&erpmodel.FinanceReceiptItem{}),
		Payments:           count(&erpmodel.FinancePayment{}),
		PaymentItems:       count(&erpmodel.FinancePaymentItem{}),
		Stocks:             count(&erpmodel.Stock{}),
		StockRecords:       count(&erpmodel.StockRecord{}),
		Sequences:          sequences,
	}
}

// TestIntegrationERPDemoSeed 验证 ERP 演示链路的完整性、幂等性与不变量。
func TestIntegrationERPDemoSeed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 测试基座默认关闭演示数据；显式写入第一遍（CRM + OA + ERP + 平台）。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("写入 ERP 演示数据: %v", err)
	}
	first := countERPDemo(t, env.DB)
	want := erpDemoCounts{
		Categories: 2, Units: 3, Products: 3, Warehouses: 2,
		Customers: 2, Suppliers: 2, Accounts: 2,
		PurchaseOrders: 1, PurchaseOrderItems: 2,
		PurchaseIns: 1, PurchaseInItems: 2,
		StockChecks: 1, StockCheckItems: 2,
		SaleOrders: 1, SaleOrderItems: 2,
		SaleOuts: 1, SaleOutItems: 2,
		Receipts: 1, ReceiptItems: 1,
		Payments: 1, PaymentItems: 1,
		Stocks: 2, StockRecords: 6,
		// 7 类单据各一张，日期键相同 → 7 行 erp_sequence。
		Sequences: 7,
	}
	if first != want {
		t.Fatalf("ERP 演示数据行数不符：\n got %+v\nwant %+v", first, want)
	}

	// 重复执行必须幂等：所有 ERP 表计数完全不变（含序列表）。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("重复写入 ERP 演示数据: %v", err)
	}
	second := countERPDemo(t, env.DB)
	if second != first {
		t.Fatalf("重复 seed 不应新增行：\n before %+v\n after %+v", first, second)
	}

	assertERPDemoChain(t, env.DB)
	assertERPDemoStockReplay(t, env.DB)
	assertERPDemoSequences(t, env.DB)
	assertERPDemoPages(t, env, admin)
	assertERPDemoStatistics(t, env, admin)
	assertERPDemoOrdinaryRole(t, env)
}

// assertERPDemoChain 校验业务链路：采购订单 → 入库 → 盘点 → 销售订单 → 出库 →
// 收款/付款的挂单关系、审核状态、金额与进度回写。
func assertERPDemoChain(t *testing.T, db *gorm.DB) {
	t.Helper()
	adminID := erpDemoAdminID(t, db)

	var order erpmodel.PurchaseOrder
	if err := tenant.Skip(db).First(&order, 4001).Error; err != nil {
		t.Fatalf("读取采购订单: %v", err)
	}
	if order.Status != erpmodel.DocStatusApproved || order.SupplierID != 3001 || order.OwnerUserID != adminID {
		t.Fatalf("采购订单状态/供应商/owner 不符：%+v", order)
	}
	assertERPDemoNoFormat(t, order.No, "CG", "采购订单")
	var orderItems []erpmodel.PurchaseOrderItem
	if err := tenant.Skip(db).Where("purchase_order_id = ?", order.ID).Order("id ASC").Find(&orderItems).Error; err != nil {
		t.Fatalf("读取采购订单明细: %v", err)
	}
	if len(orderItems) != 2 {
		t.Fatalf("采购订单应有 2 行明细，实际 %d", len(orderItems))
	}
	if orderItems[0].InCount.Milli() != orderItems[0].Count.Milli() ||
		orderItems[1].InCount.Milli() != orderItems[1].Count.Milli() ||
		orderItems[0].ReturnCount.Milli() != 0 || orderItems[1].ReturnCount.Milli() != 0 {
		t.Fatalf("采购订单进度回写不符（in_count 应等于已审核入库量、return_count=0）：%+v", orderItems)
	}
	assertERPDemoItemAmount(t, orderItems[0].Count, orderItems[0].ProductPrice, orderItems[0].TotalPrice, "采购订单明细 1")
	assertERPDemoItemAmount(t, orderItems[1].Count, orderItems[1].ProductPrice, orderItems[1].TotalPrice, "采购订单明细 2")

	var in erpmodel.PurchaseIn
	if err := tenant.Skip(db).First(&in, 4002).Error; err != nil {
		t.Fatalf("读取采购入库单: %v", err)
	}
	if in.Status != erpmodel.DocStatusApproved || in.OrderID != order.ID ||
		in.SupplierID != order.SupplierID || in.WarehouseID != 3001 {
		t.Fatalf("采购入库挂单/状态/仓库不符：%+v", in)
	}
	assertERPDemoNoFormat(t, in.No, "RK", "采购入库单")
	if in.TotalPrice.Cents() != 1500000 || in.PaymentPrice.Cents() != 1500000 {
		t.Fatalf("采购入库金额/已付回写不符：total=%s payment=%s，期望 15000.00/15000.00",
			in.TotalPrice.String(), in.PaymentPrice.String())
	}
	var inItems []erpmodel.PurchaseInItem
	if err := tenant.Skip(db).Where("purchase_in_id = ?", in.ID).Order("id ASC").Find(&inItems).Error; err != nil {
		t.Fatalf("读取采购入库明细: %v", err)
	}
	assertERPDemoTotalEquals(t, "采购入库", in.TotalPrice, inItems[0].TotalPrice, inItems[1].TotalPrice)

	var check erpmodel.StockCheck
	if err := tenant.Skip(db).First(&check, 4003).Error; err != nil {
		t.Fatalf("读取盘点单: %v", err)
	}
	if check.Status != erpmodel.DocStatusApproved || check.WarehouseID != 3001 {
		t.Fatalf("盘点单状态/仓库不符：%+v", check)
	}
	assertERPDemoNoFormat(t, check.No, "PD", "盘点单")
	var checkItems []erpmodel.StockCheckItem
	if err := tenant.Skip(db).Where("stock_check_id = ?", check.ID).Order("id ASC").Find(&checkItems).Error; err != nil {
		t.Fatalf("读取盘点明细: %v", err)
	}
	if len(checkItems) != 2 ||
		checkItems[0].ActualCount.Milli() != 11000 || checkItems[0].BaseCount.Milli() != 10000 ||
		checkItems[1].ActualCount.Milli() != 19000 || checkItems[1].BaseCount.Milli() != 20000 {
		t.Fatalf("盘点明细应一盈一亏（11/10、19/20）：%+v", checkItems)
	}

	var saleOrder erpmodel.SaleOrder
	if err := tenant.Skip(db).First(&saleOrder, 4004).Error; err != nil {
		t.Fatalf("读取销售订单: %v", err)
	}
	if saleOrder.Status != erpmodel.DocStatusApproved || saleOrder.CustomerID != 3001 {
		t.Fatalf("销售订单状态/客户不符：%+v", saleOrder)
	}
	assertERPDemoNoFormat(t, saleOrder.OrderNo, "XS", "销售订单")
	var saleOrderItems []erpmodel.SaleOrderItem
	if err := tenant.Skip(db).Where("sale_order_id = ?", saleOrder.ID).Order("id ASC").Find(&saleOrderItems).Error; err != nil {
		t.Fatalf("读取销售订单明细: %v", err)
	}
	if len(saleOrderItems) != 2 ||
		saleOrderItems[0].OutCount.Milli() != saleOrderItems[0].Count.Milli() ||
		saleOrderItems[1].OutCount.Milli() != saleOrderItems[1].Count.Milli() ||
		saleOrderItems[0].ReturnCount.Milli() != 0 || saleOrderItems[1].ReturnCount.Milli() != 0 {
		t.Fatalf("销售订单出库进度回写不符：%+v", saleOrderItems)
	}

	var out erpmodel.SaleOut
	if err := tenant.Skip(db).First(&out, 4005).Error; err != nil {
		t.Fatalf("读取销售出库单: %v", err)
	}
	if out.Status != erpmodel.DocStatusApproved || out.OrderID != saleOrder.ID ||
		out.CustomerID != saleOrder.CustomerID || out.WarehouseID != 3001 {
		t.Fatalf("销售出库挂单/状态/仓库不符：%+v", out)
	}
	assertERPDemoNoFormat(t, out.No, "CK", "销售出库单")
	if out.TotalPrice.Cents() != 1099200 || out.ReceiptPrice.Cents() != 1099200 {
		t.Fatalf("销售出库金额/已收回写不符：total=%s receipt=%s，期望 10992.00/10992.00",
			out.TotalPrice.String(), out.ReceiptPrice.String())
	}
	var outItems []erpmodel.SaleOutItem
	if err := tenant.Skip(db).Where("sale_out_id = ?", out.ID).Order("id ASC").Find(&outItems).Error; err != nil {
		t.Fatalf("读取销售出库明细: %v", err)
	}
	assertERPDemoTotalEquals(t, "销售出库", out.TotalPrice, outItems[0].TotalPrice, outItems[1].TotalPrice)

	var receipt erpmodel.FinanceReceipt
	if err := tenant.Skip(db).First(&receipt, 4006).Error; err != nil {
		t.Fatalf("读取收款单: %v", err)
	}
	if receipt.Status != erpmodel.DocStatusApproved || receipt.CustomerID != out.CustomerID ||
		receipt.AccountID != 3001 {
		t.Fatalf("收款单状态/客户/账户不符：%+v", receipt)
	}
	assertERPDemoNoFormat(t, receipt.No, "SK", "收款单")
	var receiptItems []erpmodel.FinanceReceiptItem
	if err := tenant.Skip(db).Where("receipt_id = ?", receipt.ID).Find(&receiptItems).Error; err != nil {
		t.Fatalf("读取收款明细: %v", err)
	}
	if len(receiptItems) != 1 || receiptItems[0].BizType != erpmodel.FinanceBizSaleOut ||
		receiptItems[0].BizID != out.ID || receiptItems[0].Price.Cents() != out.TotalPrice.Cents() {
		t.Fatalf("收款单应全额核销已审核销售出库：%+v", receiptItems)
	}

	var payment erpmodel.FinancePayment
	if err := tenant.Skip(db).First(&payment, 4007).Error; err != nil {
		t.Fatalf("读取付款单: %v", err)
	}
	if payment.Status != erpmodel.DocStatusApproved || payment.SupplierID != in.SupplierID ||
		payment.AccountID != 3001 {
		t.Fatalf("付款单状态/供应商/账户不符：%+v", payment)
	}
	assertERPDemoNoFormat(t, payment.No, "FK", "付款单")
	var paymentItems []erpmodel.FinancePaymentItem
	if err := tenant.Skip(db).Where("payment_id = ?", payment.ID).Find(&paymentItems).Error; err != nil {
		t.Fatalf("读取付款明细: %v", err)
	}
	if len(paymentItems) != 1 || paymentItems[0].BizType != erpmodel.FinanceBizPurchaseIn ||
		paymentItems[0].BizID != in.ID || paymentItems[0].Price.Cents() != in.TotalPrice.Cents() {
		t.Fatalf("付款单应全额核销已审核采购入库：%+v", paymentItems)
	}
}

// assertERPDemoStockReplay 校验库存余额 = 流水代数和：逐笔重放确认
// total_count 快照正确、任何时点无负数、最终余额等于 erp_stock。
func assertERPDemoStockReplay(t *testing.T, db *gorm.DB) {
	t.Helper()
	var records []erpmodel.StockRecord
	if err := tenant.Skip(db).Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("读取库存流水: %v", err)
	}
	if len(records) != 6 {
		t.Fatalf("库存流水应为 6 笔（入库 2 + 盘点 2 + 出库 2），实际 %d", len(records))
	}
	type stockKey struct {
		productID   int64
		warehouseID int64
	}
	running := make(map[stockKey]qty.Qty, 2)
	bizCounts := make(map[string]int, 3)
	for _, record := range records {
		key := stockKey{productID: record.ProductID, warehouseID: record.WarehouseID}
		current := running[key]
		next, err := current.Add(record.Count)
		if err != nil {
			t.Fatalf("流水 %d 重放失败: %v", record.ID, err)
		}
		if next.Milli() != record.TotalCount.Milli() {
			t.Fatalf("流水 %d 结存快照不符：重放 %s，快照 %s", record.ID, next.String(), record.TotalCount.String())
		}
		if next.Milli() < 0 {
			t.Fatalf("流水 %d 出现负库存：%s", record.ID, next.String())
		}
		running[key] = next
		bizCounts[record.BizType]++
	}
	if bizCounts[erpmodel.StockRecordBizPurchaseIn] != 2 ||
		bizCounts[erpmodel.StockRecordBizStockCheck] != 2 ||
		bizCounts[erpmodel.StockRecordBizSaleOut] != 2 {
		t.Fatalf("流水业务类型数量不符：%v", bizCounts)
	}

	var stocks []erpmodel.Stock
	if err := tenant.Skip(db).Order("id ASC").Find(&stocks).Error; err != nil {
		t.Fatalf("读取库存余额: %v", err)
	}
	if len(stocks) != 2 {
		t.Fatalf("库存余额应有 2 行，实际 %d", len(stocks))
	}
	want := map[stockKey]int64{
		{productID: 3001, warehouseID: 3001}: 6000,  // 10 + 1 − 5
		{productID: 3002, warehouseID: 3001}: 11000, // 20 − 1 − 8
	}
	for _, stock := range stocks {
		key := stockKey{productID: stock.ProductID, warehouseID: stock.WarehouseID}
		if stock.Count.Milli() != want[key] {
			t.Fatalf("库存余额不符：product=%d count=%s，期望 %d 毫单位",
				stock.ProductID, stock.Count.String(), want[key])
		}
		if running[key].Milli() != stock.Count.Milli() {
			t.Fatalf("库存余额与流水代数和不等：product=%d 余额 %s，流水 %s",
				stock.ProductID, stock.Count.String(), running[key].String())
		}
	}
}

// assertERPDemoSequences 校验 7 类演示单号的格式与 erp_sequence 覆盖：从单号
// 解析日期键/序号，序列 last_value 必须 >= 已用序号（后续 UI 新建不撞号）。
func assertERPDemoSequences(t *testing.T, db *gorm.DB) {
	t.Helper()
	cases := []struct {
		model   any
		column  string
		bizType string
	}{
		{&erpmodel.PurchaseOrder{}, "no", "purchase_order"},
		{&erpmodel.PurchaseIn{}, "no", "purchase_in"},
		{&erpmodel.StockCheck{}, "no", "stock_check"},
		{&erpmodel.SaleOrder{}, "order_no", "sale_order"},
		{&erpmodel.SaleOut{}, "no", "sale_out"},
		{&erpmodel.FinanceReceipt{}, "no", "finance_receipt"},
		{&erpmodel.FinancePayment{}, "no", "finance_payment"},
	}
	for _, item := range cases {
		var nos []string
		if err := tenant.Skip(db).Model(item.model).Pluck(item.column, &nos).Error; err != nil {
			t.Fatalf("读取 %s 单号: %v", item.bizType, err)
		}
		if len(nos) != 1 {
			t.Fatalf("%s 应有 1 个演示单号，实际 %d", item.bizType, len(nos))
		}
		no := nos[0]
		dateKey := no[2:10]
		seq, err := strconv.ParseInt(no[10:], 10, 64)
		if err != nil {
			t.Fatalf("%s 单号 %q 序号解析失败: %v", item.bizType, no, err)
		}
		var lastValue int64
		if err := tenant.Skip(db).Raw(
			"SELECT `last_value` FROM `erp_sequence` WHERE `tenant_id` = ? AND `biz_type` = ? AND `date_key` = ?",
			1, item.bizType, dateKey,
		).Scan(&lastValue).Error; err != nil {
			t.Fatalf("读取 %s 序列: %v", item.bizType, err)
		}
		if lastValue < seq {
			t.Fatalf("%s 单号 %q 的序列 last_value=%d 小于已用序号 %d", item.bizType, no, lastValue, seq)
		}
	}
}

// assertERPDemoPages 通过 HTTP 确认管理员能看到完整链路数据。
func assertERPDemoPages(t *testing.T, env *testutil.IntegrationEnv, token string) {
	t.Helper()
	pages := []struct {
		path string
		want int64
		name string
	}{
		{"/erp/product/page", 3, "产品"},
		{"/erp/warehouse/page", 2, "仓库"},
		{"/erp/customer/page", 2, "客户"},
		{"/erp/supplier/page", 2, "供应商"},
		{"/erp/finance-account/page", 2, "结算账户"},
		{"/erp/stock/page", 2, "产品库存"},
		{"/erp/stock-record/page", 6, "出入库流水"},
		{"/erp/purchase-order/page", 1, "采购订单"},
		{"/erp/purchase-in/page", 1, "采购入库"},
		{"/erp/stock-check/page", 1, "库存盘点"},
		{"/erp/sale-order/page", 1, "销售订单"},
		{"/erp/sale-out/page", 1, "销售出库"},
		{"/erp/finance-receipt/page", 1, "收款单"},
		{"/erp/finance-payment/page", 1, "付款单"},
	}
	for _, page := range pages {
		assertPageTotal(t, env, token, page.path, page.want, "ERP 演示"+page.name)
	}

	// 详情页：采购入库带 2 行明细，证明挂单/明细装载链路可用。
	status, api := env.Request(t, http.MethodGet, "/erp/purchase-in/4002", token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询采购入库详情：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var detail struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(api.Data, &detail); err != nil {
		t.Fatalf("解析采购入库详情: %v（data=%s）", err, string(api.Data))
	}
	if len(detail.Items) != 2 {
		t.Fatalf("采购入库详情应有 2 行明细，实际 %d", len(detail.Items))
	}
}

// assertERPDemoStatistics 校验首页统计的今日/本月有值且金额等于演示单据合计。
func assertERPDemoStatistics(t *testing.T, env *testutil.IntegrationEnv, token string) {
	t.Helper()
	sale := fetchERPSummary(t, env, token, "/erp/sale-statistics/summary")
	if sale["today"] != int64(1099200) || sale["month"] != int64(1099200) || sale["year"] != int64(1099200) {
		t.Fatalf("销售统计今日/本月/今年应为 10992.00：%v", sale)
	}
	if sale["yesterday"] != int64(0) {
		t.Fatalf("销售统计昨日应为 0.00：%v", sale)
	}
	purchase := fetchERPSummary(t, env, token, "/erp/purchase-statistics/summary")
	if purchase["today"] != int64(1500000) || purchase["month"] != int64(1500000) || purchase["year"] != int64(1500000) {
		t.Fatalf("采购统计今日/本月/今年应为 15000.00：%v", purchase)
	}
	if purchase["yesterday"] != int64(0) {
		t.Fatalf("采购统计昨日应为 0.00：%v", purchase)
	}
}

// fetchERPSummary 调用统计 summary 并返回以分计的金额映射。
func fetchERPSummary(t *testing.T, env *testutil.IntegrationEnv, token, path string) map[string]int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, path, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询统计 %s：HTTP %d code %d msg %q", path, status, api.Code, api.Msg)
	}
	var row map[string]any
	if err := json.Unmarshal(api.Data, &row); err != nil {
		t.Fatalf("解析统计 %s: %v（data=%s）", path, err, string(api.Data))
	}
	out := make(map[string]int64, len(row))
	for _, key := range []string{"today", "yesterday", "month", "year"} {
		out[key] = moneyCents(t, row[key])
	}
	return out
}

// assertERPDemoOrdinaryRole 校验内置普通角色（role id=2）默认没有任何 ERP
// 菜单/权限，普通账号访问 ERP 接口一律 403（spec §7/§9）。
func assertERPDemoOrdinaryRole(t *testing.T, env *testutil.IntegrationEnv) {
	t.Helper()
	var menuCount int64
	// ERP 号段：700–799（目录 700、菜单 701/710–752）与 7000–7999（按钮码
	// 7011/7101–7525）；其它模块的 ID（1014/5121 等）不在该号段。
	if err := tenant.Skip(env.DB).Model(&sysmodel.RoleMenu{}).
		Where("role_id = ? AND (menu_id BETWEEN ? AND ? OR menu_id BETWEEN ? AND ?)",
			2, 700, 799, 7000, 7999).
		Count(&menuCount).Error; err != nil {
		t.Fatalf("统计普通角色 ERP 菜单: %v", err)
	}
	if menuCount != 0 {
		t.Fatalf("内置普通角色不应授予任何 ERP 菜单（700 段），实际 %d 条", menuCount)
	}

	demo := env.Login(t, "demo", "123456")
	for _, path := range []string{
		"/erp/stock/page",
		"/erp/purchase-order/page",
		"/erp/sale-statistics/summary",
		"/erp/product/page",
	} {
		status, api := env.Request(t, http.MethodGet, path+"?pageNo=1&pageSize=10", demo, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通账号访问 %s 应 403，实际 HTTP %d code %d msg %q", path, status, api.Code, api.Msg)
		}
	}
}

// assertERPDemoItemAmount 校验行金额 = 单价 × 数量（Money.MulQty 同口径）。
func assertERPDemoItemAmount(t *testing.T, count qty.Qty, price, total money.Money, label string) {
	t.Helper()
	want, err := price.MulQty(count)
	if err != nil {
		t.Fatalf("%s 金额计算失败: %v", label, err)
	}
	if want.Cents() != total.Cents() {
		t.Fatalf("%s 行金额不符：单价 %s × 数量 %s = %s，实际 %s",
			label, price.String(), count.String(), want.String(), total.String())
	}
}

// assertERPDemoTotalEquals 校验主表金额 = 明细合计。
func assertERPDemoTotalEquals(t *testing.T, label string, total money.Money, parts ...money.Money) {
	t.Helper()
	sum := money.FromCents(0)
	for _, part := range parts {
		next, err := sum.Add(part)
		if err != nil {
			t.Fatalf("%s 明细合计失败: %v", label, err)
		}
		sum = next
	}
	if sum.Cents() != total.Cents() {
		t.Fatalf("%s 主表金额应等于明细合计：主表 %s，明细 %s", label, total.String(), sum.String())
	}
}

// assertERPDemoNoFormat 校验单号格式（spec §4.8：2 字母前缀 + yyyyMMdd +
// 至少 4 位序号）并确认前缀符合单据类型。
func assertERPDemoNoFormat(t *testing.T, no, prefix, label string) {
	t.Helper()
	pattern := regexp.MustCompile(`^[A-Z]{2}\d{8}\d{4,}$`)
	if !pattern.MatchString(no) {
		t.Fatalf("%s 单号 %q 不符合「2 字母前缀 + yyyyMMdd + 至少 4 位序号」", label, no)
	}
	if len(no) < 2 || no[:2] != prefix {
		t.Fatalf("%s 单号 %q 前缀应为 %s", label, no, prefix)
	}
}

// erpDemoAdminID 读取管理员 ID（演示数据 owner）。
func erpDemoAdminID(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var user sysmodel.User
	if err := tenant.Skip(db).Where("tenant_id = ? AND username = ?", 1, "admin").First(&user).Error; err != nil {
		t.Fatalf("读取管理员账号: %v", err)
	}
	return user.ID
}
