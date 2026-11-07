//go:build integration

// ERP 首页统计 HTTP 集成测试（issue #95，docs/erp-mvp-spec.md §5/§6 +
// §8.2 统计行）：口径（已审核出库/入库 − 已审核退货、按 out_time/in_time/
// return_time 落区间）、今日/昨日/本月/今年四值、近 N 月趋势（默认 6、空月补
// 零、当前月累计至今）、owner/角色数据范围过滤（与列表同口径）、跨租户隔离、
// 空数据零值不报错、权限码 erp:statistics:query（普通角色 403）。
// 金额一律十进制字符串（internal/pkg/money）；月份按业务时区（ADR 0006）。
package erp_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// erpStatisticsMenuID 是种子中的 ERP 首页统计菜单（权限码 erp:statistics:query）。
const erpStatisticsMenuID = int64(701)

// ---------- 时间/金额助手 ----------

// statsNow 返回业务时区的当前时刻（与 config.Location 同口径）。
func statsNow(env *testutil.IntegrationEnv) time.Time {
	return time.Now().In(env.Config.Location())
}

// statsDayDate 返回业务时区下相对今天偏移 dayOffset 天的 yyyy-MM-dd。
func statsDayDate(env *testutil.IntegrationEnv, dayOffset int) string {
	return statsNow(env).AddDate(0, 0, dayOffset).Format("2006-01-02")
}

// statsMonthDate 返回业务时区下相对当前月偏移 monthOffset 个月、指定日的日期。
func statsMonthDate(env *testutil.IntegrationEnv, monthOffset, day int) string {
	now := statsNow(env)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).
		AddDate(0, monthOffset, 0)
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, now.Location()).
		Format("2006-01-02")
}

// statsMonthKey 返回业务时区下相对当前月偏移 monthOffset 个月的 yyyy-MM。
func statsMonthKey(env *testutil.IntegrationEnv, monthOffset int) string {
	now := statsNow(env)
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).
		AddDate(0, monthOffset, 0).Format("2006-01")
}

// statsMoneyCents 把响应金额（必须是十进制字符串）解析为分。
func statsMoneyCents(t *testing.T, value any) int64 {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("统计金额应为十进制字符串，实际 %T（%v）", value, value)
	}
	parsed, err := money.ParseMoney(text)
	if err != nil {
		t.Fatalf("统计金额 %q 解析失败: %v", text, err)
	}
	return parsed.Cents()
}

// fetchStatistics 调用统计接口并返回 data 对象。
func fetchStatistics(t *testing.T, env *testutil.IntegrationEnv, token, path string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, path, token, nil)
	if status != http.StatusOK || api.Code != 0 {
		t.Fatalf("查询 %s：HTTP %d code %d msg %q，期望 HTTP 200 code 0",
			path, status, api.Code, api.Msg)
	}
	var data map[string]any
	if err := json.Unmarshal(api.Data, &data); err != nil {
		t.Fatalf("解析 %s 响应: %v（data=%s）", path, err, string(api.Data))
	}
	return data
}

// statisticsSummary 调用 summary 并解析四值（分）。
func statisticsSummary(t *testing.T, env *testutil.IntegrationEnv, token, domain string) (today, yesterday, month, year int64) {
	t.Helper()
	data := fetchStatistics(t, env, token, "/erp/"+domain+"-statistics/summary")
	return statsMoneyCents(t, data["today"]), statsMoneyCents(t, data["yesterday"]),
		statsMoneyCents(t, data["month"]), statsMoneyCents(t, data["year"])
}

// statisticsTrend 调用 time-summary 并返回月份顺序与月份→金额（分）。
func statisticsTrend(t *testing.T, env *testutil.IntegrationEnv, token, domain, query string) ([]string, map[string]int64) {
	t.Helper()
	data := fetchStatistics(t, env, token, "/erp/"+domain+"-statistics/time-summary"+query)
	raw, ok := data["list"].([]any)
	if !ok {
		t.Fatalf("%s time-summary 缺少 list 数组：%v", domain, data)
	}
	order := make([]string, 0, len(raw))
	amounts := make(map[string]int64, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%s 趋势元素类型不符：%v", domain, item)
		}
		key, _ := row["month"].(string)
		if key == "" {
			t.Fatalf("%s 趋势元素缺少 month：%v", domain, row)
		}
		order = append(order, key)
		amounts[key] = statsMoneyCents(t, row["amount"])
	}
	return order, amounts
}

// assertSummary 断言四值（分）。
func assertSummary(t *testing.T, domain string, today, yesterday, month, year,
	wantToday, wantYesterday, wantMonth, wantYear int64) {
	t.Helper()
	if today != wantToday || yesterday != wantYesterday || month != wantMonth || year != wantYear {
		t.Fatalf("%s 汇总 = 今日 %d / 昨日 %d / 本月 %d / 今年 %d 分，期望 %d/%d/%d/%d 分",
			domain, today, yesterday, month, year, wantToday, wantYesterday, wantMonth, wantYear)
	}
}

// statsExpect 按 spec §5 的区间语义累积期望值（测试侧独立翻译，不复用实现）：
// 今日/昨日自然日；本月/今年期初累计至今（未来日期不计）。
type statsExpect struct {
	today, yesterday, month, year int64
}

// add 把一笔金额（退货为负）按业务日期归入期望桶。
func (e *statsExpect) add(t *testing.T, env *testutil.IntegrationEnv, date string, cents int64) {
	t.Helper()
	loc := env.Config.Location()
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		t.Fatalf("解析期望日期 %q: %v", date, err)
	}
	now := statsNow(env)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if d.Equal(day) {
		e.today += cents
	}
	if d.Equal(day.AddDate(0, 0, -1)) {
		e.yesterday += cents
	}
	if d.Year() == now.Year() && d.Month() == now.Month() && !d.After(day) {
		e.month += cents
	}
	if d.Year() == now.Year() && !d.After(day) {
		e.year += cents
	}
}

// ---------- 单据构造助手（复用 #91/#92 的既有载荷助手） ----------

// statsSetupSale 建仓库/分类/单位/产品/客户，垫库存并返回一张已审核大额销售订单。
func statsSetupSale(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (warehouseID, productID, customerID, orderID int64) {
	t.Helper()
	warehouse := createWarehouse(t, env, token, tag+"仓", nil)
	category := createCategory(t, env, token, tag+"分类", nil)
	unit := createUnit(t, env, token, tag+"单位", nil)
	product := createProduct(t, env, token, tag+"产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	customer := createCustomer(t, env, token, tag+"客户", nil)
	warehouseID = int64(warehouse["id"].(float64))
	productID = int64(product["id"].(float64))
	customerID = int64(customer["id"].(float64))
	padSaleStock(t, env, token, warehouseID, productID, "1000")
	order := createSaleOrderDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"orderTime":  env.Date() + " 08:00:00",
		"items":      []map[string]any{saleItemJSON(productID, "100000", "1.00")},
	})
	orderID = int64(order["id"].(float64))
	mustApproveSale(t, env, token, "sale-order", orderID, 20)
	return warehouseID, productID, customerID, orderID
}

// statsCreateSaleOut 建销售出库单并可选审核，返回单据 ID 与行金额（分）。
func statsCreateSaleOut(t *testing.T, env *testutil.IntegrationEnv, token string,
	warehouseID, productID, customerID, orderID int64, date, count, price string, approve bool) (int64, int64) {
	t.Helper()
	doc := createSaleOutDoc(t, env, token, map[string]any{
		"customerId":  customerID,
		"orderId":     orderID,
		"warehouseId": warehouseID,
		"outTime":     date + " 10:00:00",
		"items":       []map[string]any{saleItemJSON(productID, count, price)},
	})
	id := int64(doc["id"].(float64))
	total := statsMoneyCents(t, doc["totalPrice"])
	if approve {
		mustApproveSale(t, env, token, "sale-out", id, 20)
	}
	return id, total
}

// statsCreateSaleReturn 建销售退货单并可选审核，返回单据 ID 与行金额（分）。
func statsCreateSaleReturn(t *testing.T, env *testutil.IntegrationEnv, token string,
	customerID, productID, outID int64, date, count, price string, approve bool) (int64, int64) {
	t.Helper()
	doc := createSaleReturnDoc(t, env, token, map[string]any{
		"customerId": customerID,
		"outId":      outID,
		"returnTime": date + " 11:00:00",
		"items":      []map[string]any{saleItemJSON(productID, count, price)},
	})
	id := int64(doc["id"].(float64))
	total := statsMoneyCents(t, doc["totalPrice"])
	if approve {
		mustApproveSale(t, env, token, "sale-return", id, 20)
	}
	return id, total
}

// statsSetupPurchase 建仓库/分类/单位/产品/供应商，返回一张已审核大额采购订单。
func statsSetupPurchase(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (warehouseID, productID, supplierID, orderID int64) {
	t.Helper()
	warehouse := createWarehouse(t, env, token, tag+"仓", nil)
	category := createCategory(t, env, token, tag+"分类", nil)
	unit := createUnit(t, env, token, tag+"单位", nil)
	product := createProduct(t, env, token, tag+"产品", map[string]any{
		"categoryId": int64(category["id"].(float64)),
		"unitId":     int64(unit["id"].(float64)),
	})
	supplier := createSupplier(t, env, token, tag+"供应商", nil)
	warehouseID = int64(warehouse["id"].(float64))
	productID = int64(product["id"].(float64))
	supplierID = int64(supplier["id"].(float64))
	order := createPurchaseDoc(t, env, token, "purchase-order", map[string]any{
		"supplierId": supplierID,
		"orderTime":  env.Date() + " 08:00:00",
		"items":      []map[string]any{purchaseItemJSON(productID, "100000", "1.00")},
	})
	orderID = int64(order["id"].(float64))
	mustApprovePurchaseDoc(t, env, token, "purchase-order", orderID)
	return warehouseID, productID, supplierID, orderID
}

// statsCreatePurchaseIn 建采购入库单并可选审核，返回单据 ID 与行金额（分）。
func statsCreatePurchaseIn(t *testing.T, env *testutil.IntegrationEnv, token string,
	warehouseID, productID, supplierID, orderID int64, date, count, price string, approve bool) (int64, int64) {
	t.Helper()
	doc := createPurchaseDoc(t, env, token, "purchase-in", map[string]any{
		"supplierId":  supplierID,
		"orderId":     orderID,
		"warehouseId": warehouseID,
		"inTime":      date + " 10:00:00",
		"items":       []map[string]any{purchaseItemJSON(productID, count, price)},
	})
	id := int64(doc["id"].(float64))
	total := statsMoneyCents(t, doc["totalPrice"])
	if approve {
		mustApprovePurchaseDoc(t, env, token, "purchase-in", id)
	}
	return id, total
}

// statsCreatePurchaseReturn 建采购退货单并可选审核，返回单据 ID 与行金额（分）。
func statsCreatePurchaseReturn(t *testing.T, env *testutil.IntegrationEnv, token string,
	supplierID, productID, inID int64, date, count, price string, approve bool) (int64, int64) {
	t.Helper()
	doc := createPurchaseDoc(t, env, token, "purchase-return", map[string]any{
		"supplierId": supplierID,
		"inId":       inID,
		"returnTime": date + " 11:00:00",
		"items":      []map[string]any{purchaseItemJSON(productID, count, price)},
	})
	id := int64(doc["id"].(float64))
	total := statsMoneyCents(t, doc["totalPrice"])
	if approve {
		mustApprovePurchaseDoc(t, env, token, "purchase-return", id)
	}
	return id, total
}

// ---------- 空数据零值 ----------

// TestIntegrationStatisticsEmpty 无数据返回 0.00 不报错；趋势默认补齐 6 个月。
func TestIntegrationStatisticsEmpty(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	for _, domain := range []string{"sale", "purchase"} {
		today, yesterday, month, year := statisticsSummary(t, env, admin, domain)
		assertSummary(t, domain, today, yesterday, month, year, 0, 0, 0, 0)
		order, amounts := statisticsTrend(t, env, admin, domain, "")
		if len(order) != StatisticsDefaultMonthsForTest {
			t.Fatalf("%s 默认趋势应补齐 %d 个月，实际 %d", domain, StatisticsDefaultMonthsForTest, len(order))
		}
		for _, key := range order {
			if amounts[key] != 0 {
				t.Fatalf("%s 空数据趋势 %s = %d 分，期望 0", domain, key, amounts[key])
			}
		}
	}
}

// StatisticsDefaultMonthsForTest 是 spec §5 的默认月数（测试侧字面量）。
const StatisticsDefaultMonthsForTest = 6

// ---------- 口径与四时值 ----------

// TestIntegrationStatisticsSummaryCaliber 口径（spec §5）：已审核出库 −
// 已审核退货；已审核才计入；今日/昨日自然日、本月/今年累计至今（未来日期
// 不计）；去年数据不计入今年。采购镜像。
func TestIntegrationStatisticsSummaryCaliber(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	now := statsNow(env)
	saleWH, saleProduct, saleCustomer, saleOrder := statsSetupSale(t, env, admin, "统计口径")
	purWH, purProduct, supplier, purOrder := statsSetupPurchase(t, env, admin, "统计口径")

	today := env.Date()
	yesterday := statsDayDate(env, -1)
	var sale, purchase statsExpect

	// ---- 销售：今日（出库 100.00 − 退货 30.00）----
	outToday, outTodayTotal := statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, today, "100", "1.00", true)
	if outTodayTotal != 10000 {
		t.Fatalf("今日出库行金额 = %d 分，期望 10000", outTodayTotal)
	}
	_, retTodayTotal := statsCreateSaleReturn(t, env, admin,
		saleCustomer, saleProduct, outToday, today, "30", "1.00", true)
	if retTodayTotal != 3000 {
		t.Fatalf("今日退货行金额 = %d 分，期望 3000", retTodayTotal)
	}
	sale.add(t, env, today, outTodayTotal-retTodayTotal)

	// ---- 销售：昨日（出库 40.00 − 退货 5.00）----
	outY, outYTotal := statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, yesterday, "40", "1.00", true)
	_, retYTotal := statsCreateSaleReturn(t, env, admin,
		saleCustomer, saleProduct, outY, yesterday, "5", "1.00", true)
	sale.add(t, env, yesterday, outYTotal-retYTotal)

	// ---- 销售：本月更早一天（当月 1 日；今天 >= 3 号时才存在）----
	if now.Day() >= 3 {
		_, total := statsCreateSaleOut(t, env, admin,
			saleWH, saleProduct, saleCustomer, saleOrder, statsMonthDate(env, 0, 1), "7", "1.00", true)
		sale.add(t, env, statsMonthDate(env, 0, 1), total)
	}

	// ---- 销售：今年更早月份（1 月 15 日；当前月不是 1 月时才存在）----
	if now.Month() != time.January {
		early := statsMonthDate(env, -int(now.Month())+1, 15)
		_, total := statsCreateSaleOut(t, env, admin,
			saleWH, saleProduct, saleCustomer, saleOrder, early, "3", "1.00", true)
		sale.add(t, env, early, total)
	}

	// ---- 销售：去年 12 月（不计入今年）----
	lastYear := statsMonthDate(env, -int(now.Month()), 15)
	if _, total := statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, lastYear, "500", "1.00", true); total != 50000 {
		t.Fatalf("去年出库行金额 = %d 分，期望 50000", total)
	}

	// ---- 销售：未审核出库/退货（不计入）----
	if _, total := statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, today, "999", "1.00", false); total != 99900 {
		t.Fatalf("未审核出库行金额 = %d 分，期望 99900", total)
	}
	statsCreateSaleReturn(t, env, admin, saleCustomer, saleProduct, outToday, today, "60", "1.00", false)

	// ---- 销售：本月未来日期（累计至今口径不计入；仅当明天仍在本月）----
	tomorrow := statsDayDate(env, 1)
	futureInMonth := tomorrow[:7] == statsMonthKey(env, 0)
	if futureInMonth {
		if _, total := statsCreateSaleOut(t, env, admin,
			saleWH, saleProduct, saleCustomer, saleOrder, tomorrow, "55", "1.00", true); total != 5500 {
			t.Fatalf("未来日期出库行金额 = %d 分，期望 5500", total)
		}
	}

	// ---- 采购：今日（入库 200.00 − 退货 50.00）----
	inToday, inTodayTotal := statsCreatePurchaseIn(t, env, admin,
		purWH, purProduct, supplier, purOrder, today, "200", "1.00", true)
	if inTodayTotal != 20000 {
		t.Fatalf("今日入库行金额 = %d 分，期望 20000", inTodayTotal)
	}
	_, purRetTodayTotal := statsCreatePurchaseReturn(t, env, admin,
		supplier, purProduct, inToday, today, "50", "1.00", true)
	if purRetTodayTotal != 5000 {
		t.Fatalf("今日采购退货行金额 = %d 分，期望 5000", purRetTodayTotal)
	}
	purchase.add(t, env, today, inTodayTotal-purRetTodayTotal)

	// ---- 采购：昨日（入库 10.00）----
	_, inYTotal := statsCreatePurchaseIn(t, env, admin,
		purWH, purProduct, supplier, purOrder, yesterday, "10", "1.00", true)
	purchase.add(t, env, yesterday, inYTotal)

	// ---- 采购：本月更早一天、今年更早月份、去年、未审核、未来 ----
	if now.Day() >= 3 {
		_, total := statsCreatePurchaseIn(t, env, admin,
			purWH, purProduct, supplier, purOrder, statsMonthDate(env, 0, 1), "2", "1.00", true)
		purchase.add(t, env, statsMonthDate(env, 0, 1), total)
	}
	if now.Month() != time.January {
		early := statsMonthDate(env, -int(now.Month())+1, 15)
		_, total := statsCreatePurchaseIn(t, env, admin,
			purWH, purProduct, supplier, purOrder, early, "1", "1.00", true)
		purchase.add(t, env, early, total)
	}
	statsCreatePurchaseIn(t, env, admin,
		purWH, purProduct, supplier, purOrder, lastYear, "400", "1.00", true)
	statsCreatePurchaseIn(t, env, admin,
		purWH, purProduct, supplier, purOrder, today, "777", "1.00", false)
	if futureInMonth {
		statsCreatePurchaseIn(t, env, admin,
			purWH, purProduct, supplier, purOrder, tomorrow, "66", "1.00", true)
	}

	// ---- 断言 ----
	todayCents, yesterdayCents, monthCents, yearCents := statisticsSummary(t, env, admin, "sale")
	assertSummary(t, "销售", todayCents, yesterdayCents, monthCents, yearCents,
		sale.today, sale.yesterday, sale.month, sale.year)
	todayCents, yesterdayCents, monthCents, yearCents = statisticsSummary(t, env, admin, "purchase")
	assertSummary(t, "采购", todayCents, yesterdayCents, monthCents, yearCents,
		purchase.today, purchase.yesterday, purchase.month, purchase.year)
}

// ---------- 近 N 月趋势 ----------

// TestIntegrationStatisticsTrend 近 N 月（默认 6）：升序、空月补 0.00、
// 当前月与「本月」同口径（累计至今）、窗口外月份不出现、months 参数可调、
// 非法 months 400。
func TestIntegrationStatisticsTrend(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	saleWH, saleProduct, saleCustomer, saleOrder := statsSetupSale(t, env, admin, "统计趋势")
	purWH, purProduct, supplier, purOrder := statsSetupPurchase(t, env, admin, "统计趋势")

	// 销售：本月 10.00 − 退货 4.00 = 6.00；上月 20.00；7 个月前 30.00（窗口外）。
	outCur, _ := statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, env.Date(), "10", "1.00", true)
	statsCreateSaleReturn(t, env, admin,
		saleCustomer, saleProduct, outCur, env.Date(), "4", "1.00", true)
	statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, statsMonthDate(env, -1, 15), "20", "1.00", true)
	statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, statsMonthDate(env, -7, 15), "30", "1.00", true)

	// 采购：本月 5.00 − 退货 1.00 = 4.00；上月 8.00。
	inCur, _ := statsCreatePurchaseIn(t, env, admin,
		purWH, purProduct, supplier, purOrder, env.Date(), "5", "1.00", true)
	statsCreatePurchaseReturn(t, env, admin,
		supplier, purProduct, inCur, env.Date(), "1", "1.00", true)
	statsCreatePurchaseIn(t, env, admin,
		purWH, purProduct, supplier, purOrder, statsMonthDate(env, -1, 15), "8", "1.00", true)

	current := statsMonthKey(env, 0)
	previous := statsMonthKey(env, -1)

	// 默认 6 个月。
	order, amounts := statisticsTrend(t, env, admin, "sale", "")
	wantOrder := []string{
		statsMonthKey(env, -5), statsMonthKey(env, -4), statsMonthKey(env, -3),
		statsMonthKey(env, -2), previous, current,
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("销售趋势应返回 6 个月，实际 %d（%v）", len(order), order)
	}
	for i, key := range wantOrder {
		if order[i] != key {
			t.Fatalf("销售趋势第 %d 月 = %s，期望 %s（升序补齐）", i, order[i], key)
		}
	}
	if amounts[current] != 600 {
		t.Fatalf("销售本月趋势 = %d 分，期望 600（1000 − 400）", amounts[current])
	}
	if amounts[previous] != 2000 {
		t.Fatalf("销售上月趋势 = %d 分，期望 2000", amounts[previous])
	}
	for _, key := range order {
		if key != current && key != previous && amounts[key] != 0 {
			t.Fatalf("销售空月 %s = %d 分，期望 0（补零）", key, amounts[key])
		}
	}
	if _, ok := amounts[statsMonthKey(env, -6)]; ok {
		t.Fatalf("销售趋势不应包含窗口外月份 %s", statsMonthKey(env, -6))
	}

	// 采购趋势。
	purOrderKeys, purAmounts := statisticsTrend(t, env, admin, "purchase", "")
	if len(purOrderKeys) != 6 {
		t.Fatalf("采购趋势应返回 6 个月，实际 %d", len(purOrderKeys))
	}
	if purAmounts[current] != 400 || purAmounts[previous] != 800 {
		t.Fatalf("采购趋势本月/上月 = %d/%d 分，期望 400/800",
			purAmounts[current], purAmounts[previous])
	}

	// months=3：只含本月与之前两个月。
	order3, amounts3 := statisticsTrend(t, env, admin, "sale", "?months=3")
	want3 := []string{statsMonthKey(env, -2), previous, current}
	if len(order3) != 3 {
		t.Fatalf("months=3 应返回 3 个月，实际 %d（%v）", len(order3), order3)
	}
	for i, key := range want3 {
		if order3[i] != key {
			t.Fatalf("months=3 第 %d 月 = %s，期望 %s", i, order3[i], key)
		}
	}
	if amounts3[current] != 600 || amounts3[previous] != 2000 || amounts3[want3[0]] != 0 {
		t.Fatalf("months=3 金额 = %v，期望 %s:600/%s:2000/%s:0",
			amounts3, current, previous, want3[0])
	}

	// months=0 按默认 6。
	orderDefault, _ := statisticsTrend(t, env, admin, "sale", "?months=0")
	if len(orderDefault) != 6 {
		t.Fatalf("months=0 应按默认 6 个月，实际 %d", len(orderDefault))
	}

	// 非法 months → 400。
	for _, query := range []string{"?months=-1", "?months=25"} {
		status, _ := env.Request(t, http.MethodGet,
			"/erp/sale-statistics/time-summary"+query, admin, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("time-summary%s：HTTP %d，期望 400", query, status)
		}
	}
}

// ---------- 数据范围过滤 ----------

// TestIntegrationStatisticsDataScope 统计按 owner/角色数据范围过滤（spec §4.10
// 「统计接口同样过滤」，与列表同口径）：仅本人、本部门、全部三种范围；退货按
// 自身 owner 独立过滤。
func TestIntegrationStatisticsDataScope(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	saleWH, saleProduct, saleCustomer, saleOrder := statsSetupSale(t, env, admin, "统计范围")
	purWH, purProduct, supplier, purOrder := statsSetupPurchase(t, env, admin, "统计范围")

	// 造单角色：销售出库 + 采购入库的创建权限（订单由 admin 建并审核）。
	creatorRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "统计造单角色", "code": "stats_creator_role", "dataScope": 1,
		"menuIds": []int64{742, 7421, 732, 7321},
	})
	createUserByAPI(t, env, admin, "stats_owner_rnd", 101, []int64{creatorRoleID})
	createUserByAPI(t, env, admin, "stats_owner_mkt", 102, []int64{creatorRoleID})

	// 未审核草稿先由各 owner 创建，最后统一由 admin 审核（数据范围「全部」）。
	type statsDoc struct {
		docType string
		id      int64
	}
	var pending []statsDoc
	createOwnedDocs := func(user string, outCount, inCount string) {
		token := env.Login(t, user, "123456")
		outID, _ := statsCreateSaleOut(t, env, token,
			saleWH, saleProduct, saleCustomer, saleOrder, env.Date(), outCount, "1.00", false)
		inID, _ := statsCreatePurchaseIn(t, env, token,
			purWH, purProduct, supplier, purOrder, env.Date(), inCount, "1.00", false)
		pending = append(pending, statsDoc{"sale-out", outID}, statsDoc{"purchase-in", inID})
	}
	createOwnedDocs("stats_owner_rnd", "10", "5")
	createOwnedDocs("stats_owner_mkt", "20", "7")

	// 观察者角色：统计查询 + 造单按钮（仅本人范围下自建一单，让过滤有正例）。
	observerMenus := []int64{erpStatisticsMenuID, 7011, 742, 7421, 743, 7431, 732, 7321}
	selfRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "统计仅本人角色", "code": "stats_self_role", "dataScope": 5,
		"menuIds": observerMenus,
	})
	createUserByAPI(t, env, admin, "stats_scope_self", 102, []int64{selfRoleID})
	selfToken := env.Login(t, "stats_scope_self", "123456")
	selfOut, _ := statsCreateSaleOut(t, env, selfToken,
		saleWH, saleProduct, saleCustomer, saleOrder, env.Date(), "3", "1.00", false)
	// 退货必须挂已审核出库：先由 admin 审核该出库单。
	mustApproveSale(t, env, admin, "sale-out", selfOut, 20)
	selfRet, _ := statsCreateSaleReturn(t, env, selfToken,
		saleCustomer, saleProduct, selfOut, env.Date(), "1", "1.00", false)
	selfIn, _ := statsCreatePurchaseIn(t, env, selfToken,
		purWH, purProduct, supplier, purOrder, env.Date(), "2", "1.00", false)
	pending = append(pending,
		statsDoc{"sale-return", selfRet}, statsDoc{"purchase-in", selfIn})

	// admin 审核全部（admin 数据范围「全部」，可见并可操作）。
	for _, doc := range pending {
		switch doc.docType {
		case "sale-out", "sale-return":
			mustApproveSale(t, env, admin, doc.docType, doc.id, 20)
		case "purchase-in":
			mustApprovePurchaseDoc(t, env, admin, doc.docType, doc.id)
		default:
			t.Fatalf("未知单据类型 %s", doc.docType)
		}
	}

	// 仅本人：只看见自己创建的出库 3.00 − 退货 1.00、入库 2.00。
	today, _, month, _ := statisticsSummary(t, env, selfToken, "sale")
	if today != 200 || month != 200 {
		t.Fatalf("仅本人销售今日/本月 = %d/%d 分，期望 200/200", today, month)
	}
	today, _, month, _ = statisticsSummary(t, env, selfToken, "purchase")
	if today != 200 || month != 200 {
		t.Fatalf("仅本人采购今日/本月 = %d/%d 分，期望 200/200", today, month)
	}

	// 本部门（dept 102）：mkt 20.00 + self 3.00 − self 退货 1.00 = 22.00；
	// 采购 7.00 + 2.00 = 9.00；rnd（dept 101）不可见。
	deptRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "统计本部门角色", "code": "stats_dept_role", "dataScope": 3,
		"menuIds": []int64{erpStatisticsMenuID, 7011},
	})
	createUserByAPI(t, env, admin, "stats_scope_dept", 102, []int64{deptRoleID})
	deptToken := env.Login(t, "stats_scope_dept", "123456")
	today, _, _, _ = statisticsSummary(t, env, deptToken, "sale")
	if today != 2200 {
		t.Fatalf("本部门销售今日 = %d 分，期望 2200", today)
	}
	today, _, _, _ = statisticsSummary(t, env, deptToken, "purchase")
	if today != 900 {
		t.Fatalf("本部门采购今日 = %d 分，期望 900", today)
	}

	// 全部：10 + 20 + 3 − 1 = 32.00；采购 5 + 7 + 2 = 14.00。
	allRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "统计全部角色", "code": "stats_all_role", "dataScope": 1,
		"menuIds": []int64{erpStatisticsMenuID, 7011},
	})
	createUserByAPI(t, env, admin, "stats_scope_all", 101, []int64{allRoleID})
	allToken := env.Login(t, "stats_scope_all", "123456")
	today, _, _, _ = statisticsSummary(t, env, allToken, "sale")
	if today != 3200 {
		t.Fatalf("全部数据销售今日 = %d 分，期望 3200", today)
	}
	today, _, _, _ = statisticsSummary(t, env, allToken, "purchase")
	if today != 1400 {
		t.Fatalf("全部数据采购今日 = %d 分，期望 1400", today)
	}
}

// ---------- 跨租户隔离 ----------

// TestIntegrationStatisticsTenantIsolation 统计不跨租户：租户 2 新建时全零；
// 各自单据只进入各自统计。
func TestIntegrationStatisticsTenantIsolation(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	saleWH, saleProduct, saleCustomer, saleOrder := statsSetupSale(t, env, admin, "隔离")
	statsCreateSaleOut(t, env, admin,
		saleWH, saleProduct, saleCustomer, saleOrder, env.Date(), "100", "1.00", true)
	purWH, purProduct, supplier, purOrder := statsSetupPurchase(t, env, admin, "隔离")
	statsCreatePurchaseIn(t, env, admin,
		purWH, purProduct, supplier, purOrder, env.Date(), "200", "1.00", true)

	tenant2 := createTenantByAPI(t, env, admin, "统计隔离租户")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 租户 2 初始全零。
	for _, domain := range []string{"sale", "purchase"} {
		for _, field := range []string{"today", "yesterday", "month", "year"} {
			data := fetchStatistics(t, env, token2, "/erp/"+domain+"-statistics/summary")
			if got := statsMoneyCents(t, data[field]); got != 0 {
				t.Fatalf("租户 2 %s.%s = %d 分，期望 0（跨租户不可见）", domain, field, got)
			}
		}
		_, amounts := statisticsTrend(t, env, token2, domain, "")
		for key, amount := range amounts {
			if amount != 0 {
				t.Fatalf("租户 2 %s 趋势 %s = %d 分，期望 0", domain, key, amount)
			}
		}
	}

	// 租户 2 自建数据后只见自己的。
	wh2, product2, customer2, order2 := statsSetupSale(t, env, token2, "隔离二")
	statsCreateSaleOut(t, env, token2,
		wh2, product2, customer2, order2, env.Date(), "7", "1.00", true)
	purWH2, purProduct2, supplier2, purOrder2 := statsSetupPurchase(t, env, token2, "隔离二")
	statsCreatePurchaseIn(t, env, token2,
		purWH2, purProduct2, supplier2, purOrder2, env.Date(), "9", "1.00", true)
	today, _, _, _ := statisticsSummary(t, env, token2, "sale")
	if today != 700 {
		t.Fatalf("租户 2 销售今日 = %d 分，期望 700", today)
	}
	today, _, _, _ = statisticsSummary(t, env, token2, "purchase")
	if today != 900 {
		t.Fatalf("租户 2 采购今日 = %d 分，期望 900", today)
	}

	// 租户 1 统计不受租户 2 影响。
	today, _, _, _ = statisticsSummary(t, env, admin, "sale")
	if today != 10000 {
		t.Fatalf("租户 1 销售今日 = %d 分，期望 10000", today)
	}
	today, _, _, _ = statisticsSummary(t, env, admin, "purchase")
	if today != 20000 {
		t.Fatalf("租户 1 采购今日 = %d 分，期望 20000", today)
	}
}

// ---------- 权限码 ----------

// TestIntegrationStatisticsPermission erp:statistics:query 才能访问四个接口：
// 内置普通角色（role 2）403；持其他 ERP 码但无统计码 403；授予菜单 701 后 200。
func TestIntegrationStatisticsPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	paths := []string{
		"/erp/sale-statistics/summary",
		"/erp/sale-statistics/time-summary",
		"/erp/purchase-statistics/summary",
		"/erp/purchase-statistics/time-summary",
	}

	common := env.Login(t, "demo", "123456")
	for _, path := range paths {
		status, _ := env.Request(t, http.MethodGet, path, common, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色访问 %s：HTTP %d，期望 403", path, status)
		}
	}

	otherRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "统计无码角色", "code": "stats_no_perm_role", "dataScope": 1,
		"menuIds": []int64{741, 7411},
	})
	createUserByAPI(t, env, admin, "stats_no_perm", 101, []int64{otherRoleID})
	noPermToken := env.Login(t, "stats_no_perm", "123456")
	for _, path := range paths {
		status, _ := env.Request(t, http.MethodGet, path, noPermToken, nil)
		if status != http.StatusForbidden {
			t.Fatalf("无统计码用户访问 %s：HTTP %d，期望 403", path, status)
		}
	}

	statsRoleID := createRoleByAPI(t, env, admin, map[string]any{
		"name": "统计查询角色", "code": "stats_query_role", "dataScope": 1,
		"menuIds": []int64{erpStatisticsMenuID, 7011},
	})
	createUserByAPI(t, env, admin, "stats_query_user", 101, []int64{statsRoleID})
	statsToken := env.Login(t, "stats_query_user", "123456")
	for _, path := range paths {
		status, api := env.Request(t, http.MethodGet, path, statsToken, nil)
		if status != http.StatusOK || api.Code != 0 {
			t.Fatalf("统计码用户访问 %s：HTTP %d code %d msg %q，期望 200",
				path, status, api.Code, api.Msg)
		}
	}
}
