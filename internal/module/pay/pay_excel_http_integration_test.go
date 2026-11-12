//go:build integration

// pay Excel 导出 HTTP 集成测试（issue #138，docs/pay-mvp-spec.md §4.12/§8.2
// Excel 行）：五个管理列表导出（订单/退款/转账/钱包流水/充值单）复用列表筛选
// 与租户隔离口径；文件头 attachment + xlsx MIME + 中文文件名（filename* 双编码）；
// Money 经 TextMarshaler 输出十进制字符串（无科学计数/浮点尾差）；日期按业务
// 时区（NativeDates，墙上时间与分页 JSON 一致）；权限 pay:<entity>:export
// （普通角色 403）。
package pay_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"github.com/xuri/excelize/v2"
)

// getPayExportSheet 请求导出接口并解析「数据」工作表，返回表头与数据行（文本形态）。
// 允许空列表导出（只有表头）：租户隔离用例会断言 0 数据行。同时校验双编码文件名：
// ASCII filename="..." 兼容旧客户端，filename*=UTF-8”<percent-encoded> 中文名。
func getPayExportSheet(t *testing.T, env *testutil.IntegrationEnv, token, path string) ([]string, [][]string) {
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
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: HTTP %d，body=%s", path, resp.StatusCode, string(raw))
	}
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment") || !strings.Contains(disposition, ".xlsx") {
		t.Fatalf("GET %s: Content-Disposition=%q，期望 attachment 且带 .xlsx 文件名", path, disposition)
	}
	const starPrefix = "filename*=UTF-8''"
	starIdx := strings.Index(disposition, starPrefix)
	if starIdx < 0 {
		t.Fatalf("GET %s: Content-Disposition=%q，缺少 filename*=UTF-8'' 中文文件名", path, disposition)
	}
	encoded := disposition[starIdx+len(starPrefix):]
	if i := strings.IndexByte(encoded, ';'); i >= 0 {
		encoded = encoded[:i]
	}
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		t.Fatalf("GET %s: filename* 中文文件名为空：%q", path, disposition)
	}
	for _, r := range encoded {
		if r > 127 {
			t.Fatalf("GET %s: filename* 应为百分号编码的 ASCII，实际含 %q：%q", path, r, disposition)
		}
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
	if err != nil || len(rows) < 1 {
		t.Fatalf("GET %s: 数据工作表应至少含表头，rows=%d err=%v", path, len(rows), err)
	}
	return rows[0], rows[1:]
}

// payExportRows 请求导出接口并要求至少 1 条数据行。
func payExportRows(t *testing.T, env *testutil.IntegrationEnv, token, path string) ([]string, [][]string) {
	t.Helper()
	header, rows := getPayExportSheet(t, env, token, path)
	if len(rows) == 0 {
		t.Fatalf("GET %s: 期望至少 1 条数据行，实际只有表头 %v", path, header)
	}
	return header, rows
}

// payColumn 按表头取行内列值（缺列或越界 Fatal）。
func payColumn(t *testing.T, phase string, row []string, header []string, col string) string {
	t.Helper()
	for i, name := range header {
		if name == col {
			if i < len(row) {
				return row[i]
			}
			return ""
		}
	}
	t.Fatalf("%s表头缺少 %q 列：%v", phase, col, header)
	return ""
}

// payHasCell 断言行内指定列的值等于期望。
func payHasCell(t *testing.T, phase string, row []string, header []string, col, want string) {
	t.Helper()
	if got := payColumn(t, phase, row, header, col); got != want {
		t.Fatalf("%s：列 %q = %q，期望 %q（行 %v）", phase, col, got, want, row)
	}
}

// payTimeLayout 是 Excel 导出日期单元格的展示格式（excel.DefaultTimeLayout）。
const payTimeLayout = "2006-01-02 15:04:05"

// payAssertBusinessTime 校验导出日期单元格的墙上时间与分页 JSON 一致：
// DSN loc 由 app.timezone（业务时区，ADR 0006）注入，DB 扫描出的 time.Time
// 已按业务时区解释，NativeDates 原样保留墙上时间。
// created_at 列为 datetime(3)：亚秒部分在 Excel 侧经 excelize 浮点天数往返
// 按显示格式四舍五入、JSON RFC3339 文本侧被布局截断——两侧展示可能相差
// 1 秒（纯显示口径差异，非时区/精度缺陷），断言按 ±1s 容差比较；时区错乱
//（如 UTC 偏移 8 小时）仍必然命中断言。
func payAssertBusinessTime(t *testing.T, env *testutil.IntegrationEnv, phase, cell, jsonTime string) {
	t.Helper()
	loc := env.Config.Location()
	excelWall, err := time.ParseInLocation(payTimeLayout, cell, loc)
	if err != nil {
		t.Fatalf("%s：导出日期 %q 不是 %s 格式：%v", phase, cell, payTimeLayout, err)
	}
	dbTime, err := time.Parse(time.RFC3339, jsonTime)
	if err != nil {
		t.Fatalf("%s：分页 JSON 时间 %q 解析失败: %v", phase, jsonTime, err)
	}
	diff := excelWall.Sub(dbTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Fatalf("%s：导出日期 %q 与业务时区墙上时间 %q 相差超过 1 秒（%s）",
			phase, cell, dbTime.In(loc).Format(payTimeLayout), diff)
	}
}

// payJSONCreateTime 取管理分页首行指定字段时间（RFC3339 文本）。
func payJSONCreateTime(t *testing.T, env *testutil.IntegrationEnv, token, path, field string) string {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, path, token, nil)
	mustOK(t, "分页 "+path, status, api)
	var page struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil {
		t.Fatalf("解析分页: %v（data=%s）", err, api.Data)
	}
	if len(page.List) == 0 {
		t.Fatalf("分页 %s 应至少 1 行", path)
	}
	raw, _ := page.List[0][field].(string)
	if raw == "" {
		t.Fatalf("分页首行缺 %s 字段：%v", field, page.List[0])
	}
	return raw
}

// TestIntegrationPayOrderExportExcel 订单导出：列标签、金额十进制字符串（含大额
// 无科学计数）、状态中文、筛选联动、日期业务时区、租户隔离。
func TestIntegrationPayOrderExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	app := payCreateApp(t, env, admin, "excel-order-app")
	payBindChannels(t, env, admin, app.ID, []string{"mock", "wallet"})
	payCreateOrder(t, env, admin, app.AppKey, "excel-order-1", "99.90")
	payCreateOrder(t, env, admin, app.AppKey, "excel-order-2", "12345678.90")

	// 全量导出：表头 + 行数 + 金额/状态/日期格式。
	header, rows := payExportRows(t, env, admin, "/pay/order/export-excel")
	joinedHeader := strings.Join(header, ",")
	for _, col := range []string{"订单号", "消费方单号", "商品标题", "实付金额", "已退金额", "状态", "支付渠道", "渠道单号", "归属用户", "创建时间"} {
		if !strings.Contains(joinedHeader, col) {
			t.Fatalf("订单导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("订单导出应含 2 行，实际 %d 行", len(rows))
	}
	// id DESC：第一行是后建的单（大额）。
	big := rows[0]
	if !strings.HasPrefix(payColumn(t, "订单导出", big, header, "订单号"), "ZF") {
		t.Fatalf("订单号应 ZF 前缀：%v", big)
	}
	payHasCell(t, "订单导出", big, header, "消费方单号", "excel-order-2")
	payHasCell(t, "订单导出", big, header, "实付金额", "12345678.90")
	payHasCell(t, "订单导出", big, header, "已退金额", "0.00")
	payHasCell(t, "订单导出", big, header, "状态", "待支付")
	payHasCell(t, "订单导出", big, header, "归属用户", "后台用户 #1")
	small := rows[1]
	payHasCell(t, "订单导出", small, header, "实付金额", "99.90")

	// 日期为业务时区格式（yyyy-MM-dd HH:mm:ss），墙上时间与分页 JSON 一致。
	createCell := payColumn(t, "订单导出", big, header, "创建时间")
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`).MatchString(createCell) {
		t.Fatalf("创建时间应按业务时区 %s 输出，实际 %q", payTimeLayout, createCell)
	}
	jsonTime := payJSONCreateTime(t, env, admin, "/pay/order/page?pageNo=1&pageSize=1", "createTime")
	payAssertBusinessTime(t, env, "订单导出", createCell, jsonTime)

	// 筛选联动（复用列表筛选口径）。
	_, rows = payExportRows(t, env, admin, "/pay/order/export-excel?merchantOrderId=excel-order-1")
	if len(rows) != 1 {
		t.Fatalf("按消费方单号筛选导出应 1 行，实际 %d 行", len(rows))
	}
	payHasCell(t, "订单导出筛选", rows[0], header, "消费方单号", "excel-order-1")

	// 状态筛选。
	_, rows = getPayExportSheet(t, env, admin, "/pay/order/export-excel?status=10")
	if len(rows) != 0 {
		t.Fatalf("status=10 筛选导出应 0 行（无成功单），实际 %d 行", len(rows))
	}

	// 租户隔离：租户 B 的导出只见自己的单。
	otherTenant := createTenantByAPI(t, env, admin, "pay-excel-order-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	otherApp := payCreateApp(t, env, other, "excel-order-b-app")
	payCreateOrder(t, env, other, otherApp.AppKey, "excel-order-b-1", "10.00")
	_, rows = payExportRows(t, env, other, "/pay/order/export-excel")
	if len(rows) != 1 {
		t.Fatalf("租户 B 导出应只含自己的 1 行，实际 %d 行", len(rows))
	}
	payHasCell(t, "租户隔离", rows[0], header, "消费方单号", "excel-order-b-1")
}

// TestIntegrationPayRefundExportExcel 退款单导出：TK 单号、原单实付/退款金额
// 十进制字符串、状态中文、渠道原路退回列。
func TestIntegrationPayRefundExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	app := payCreateRefundApp(t, env, admin, "excel-refund-app", []string{"mock", "wallet"})
	order := payCreateOrder(t, env, admin, app.AppKey, "excel-refund-order", "50.00")
	payMockPayOrder(t, env, admin, payOrderID(t, order))
	status, api := payRefund(t, env, admin, app.AppKey, "excel-refund-order", "excel-refund-1", "20.00")
	mustOK(t, "发起退款", status, api)

	header, rows := payExportRows(t, env, admin, "/pay/refund/export-excel")
	for _, col := range []string{"退款单号", "消费方退款单号", "支付订单号", "消费方订单号", "原单实付", "退款金额", "状态", "支付渠道", "退款原因", "创建时间"} {
		if !strings.Contains(strings.Join(header, ","), col) {
			t.Fatalf("退款导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("退款导出应含 1 行，实际 %d 行", len(rows))
	}
	row := rows[0]
	if !strings.HasPrefix(payColumn(t, "退款导出", row, header, "退款单号"), "TK") {
		t.Fatalf("退款单号应 TK 前缀：%v", row)
	}
	payHasCell(t, "退款导出", row, header, "消费方退款单号", "excel-refund-1")
	payHasCell(t, "退款导出", row, header, "原单实付", "50.00")
	payHasCell(t, "退款导出", row, header, "退款金额", "20.00")
	payHasCell(t, "退款导出", row, header, "状态", "成功")
	payHasCell(t, "退款导出", row, header, "支付渠道", "mock")
	payHasCell(t, "退款导出", row, header, "退款原因", "集成测试退款")

	// 租户隔离：租户 B 导出 0 行（未发生退款）。
	otherTenant := createTenantByAPI(t, env, admin, "pay-excel-refund-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	_, emptyRows := getPayExportSheet(t, env, other, "/pay/refund/export-excel")
	if len(emptyRows) != 0 {
		t.Fatalf("租户 B 退款导出应 0 行，实际 %d 行", len(emptyRows))
	}
}

// TestIntegrationPayTransferExportExcel 转账单导出：ZZ 单号、金额十进制字符串、
// 收款账户快照、状态中文。
func TestIntegrationPayTransferExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	app := payCreateRefundApp(t, env, admin, "excel-transfer-app", []string{"mock", "wallet"})
	payCreateTransfer(t, env, admin, app.AppKey, "excel-transfer-1", "mock", "user-1", "88.00")

	header, rows := payExportRows(t, env, admin, "/pay/transfer/export-excel")
	for _, col := range []string{"转账单号", "消费方转账单号", "转账标题", "转账金额", "收款账户", "收款人", "状态", "支付渠道", "创建时间"} {
		if !strings.Contains(strings.Join(header, ","), col) {
			t.Fatalf("转账导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("转账导出应含 1 行，实际 %d 行", len(rows))
	}
	row := rows[0]
	if !strings.HasPrefix(payColumn(t, "转账导出", row, header, "转账单号"), "ZZ") {
		t.Fatalf("转账单号应 ZZ 前缀：%v", row)
	}
	payHasCell(t, "转账导出", row, header, "消费方转账单号", "excel-transfer-1")
	payHasCell(t, "转账导出", row, header, "转账金额", "88.00")
	payHasCell(t, "转账导出", row, header, "收款账户", "user-1")
	payHasCell(t, "转账导出", row, header, "收款人", "测试收款人")
	payHasCell(t, "转账导出", row, header, "状态", "成功")
	payHasCell(t, "转账导出", row, header, "支付渠道", "mock")

	// 租户隔离：租户 B 导出 0 行。
	otherTenant := createTenantByAPI(t, env, admin, "pay-excel-transfer-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	_, emptyRows := getPayExportSheet(t, env, other, "/pay/transfer/export-excel")
	if len(emptyRows) != 0 {
		t.Fatalf("租户 B 转账导出应 0 行，实际 %d 行", len(emptyRows))
	}
}

// TestIntegrationPayWalletTransactionExportExcel 钱包流水导出：LS 单号、业务类型
// 中文、变动金额正负十进制字符串、结存快照、租户隔离。
func TestIntegrationPayWalletTransactionExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: paymodel.WalletUserTypeAdmin,
		BizType: paymodel.WalletBizRecharge, Title: "excel-recharge", Price: money.FromCents(50_00),
	}); err != nil {
		t.Fatalf("充值入账: %v", err)
	}
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: paymodel.WalletUserTypeAdmin,
		BizType: paymodel.WalletBizPayment, Title: "excel-pay", Price: money.FromCents(-10_00),
	}); err != nil {
		t.Fatalf("支付扣款: %v", err)
	}
	wallet := findWalletByUser(t, env, admin, 1)

	header, rows := payExportRows(t, env, admin, fmt.Sprintf("/pay/wallet-transaction/export-excel?walletId=%d", int64(wallet["id"].(float64))))
	for _, col := range []string{"流水号", "钱包ID", "业务类型", "标题", "变动金额", "结存快照", "入账时间"} {
		if !strings.Contains(strings.Join(header, ","), col) {
			t.Fatalf("流水导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("流水导出应含 2 行，实际 %d 行", len(rows))
	}
	// id DESC：第一行是支付扣款（负数金额）。
	payHasCell(t, "流水导出", rows[0], header, "业务类型", "支付")
	payHasCell(t, "流水导出", rows[0], header, "变动金额", "-10.00")
	payHasCell(t, "流水导出", rows[0], header, "结存快照", "40.00")
	if !strings.HasPrefix(payColumn(t, "流水导出", rows[0], header, "流水号"), "LS") {
		t.Fatalf("流水号应 LS 前缀：%v", rows[0])
	}
	payHasCell(t, "流水导出", rows[1], header, "业务类型", "充值")
	payHasCell(t, "流水导出", rows[1], header, "变动金额", "50.00")
	payHasCell(t, "流水导出", rows[1], header, "结存快照", "50.00")

	// bizType 筛选联动（复用列表筛选口径）。
	_, rows = payExportRows(t, env, admin, fmt.Sprintf("/pay/wallet-transaction/export-excel?walletId=%d&bizType=3", int64(wallet["id"].(float64))))
	if len(rows) != 1 {
		t.Fatalf("bizType=3 筛选导出应 1 行，实际 %d 行", len(rows))
	}
	payHasCell(t, "流水导出筛选", rows[0], header, "变动金额", "-10.00")

	// 租户隔离：租户 B 的钱包流水不出现在租户 A 的导出。
	otherTenant := createTenantByAPI(t, env, admin, "pay-excel-wallet-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	if _, err := applyWallet(env, 1, otherTenant, payservice.WalletChange{
		UserID: 1, UserType: paymodel.WalletUserTypeAdmin,
		BizType: paymodel.WalletBizRecharge, Title: "excel-other-tenant", Price: money.FromCents(70_00),
	}); err != nil {
		t.Fatalf("租户 B 充值入账: %v", err)
	}
	_, _, otherWallets := pageWallets(t, env, other, "")
	if len(otherWallets) != 1 {
		t.Fatalf("租户 B 应恰有 1 个钱包，实际 %d 个", len(otherWallets))
	}
	otherWalletID := int64(otherWallets[0]["id"].(float64))
	_, allRows := getPayExportSheet(t, env, admin, "/pay/wallet-transaction/export-excel")
	for _, row := range allRows {
		if got := payColumn(t, "租户隔离", row, header, "钱包ID"); got == fmt.Sprintf("%d", otherWalletID) {
			t.Fatalf("租户 A 导出不应含租户 B 钱包 %d 的流水：%v", otherWalletID, row)
		}
	}
}

// TestIntegrationPayRechargeExportExcel 充值单导出：金额十进制字符串、支付状态
// 中文、套餐快照（自定义/套餐档位）、挂单订单号回填、租户隔离。
func TestIntegrationPayRechargeExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	rechargeSeedApp(t, env, admin)
	packageID := payCreatePackage(t, env, admin, "excel 套餐 100 送 20", "100.00", "20.00")
	payCreateRecharge(t, env, admin, map[string]any{"packageId": packageID})
	payCreateRecharge(t, env, admin, map[string]any{"payPrice": "66.66"})

	header, rows := payExportRows(t, env, admin, "/pay/wallet-recharge/export-excel")
	for _, col := range []string{"充值金额", "实付金额", "赠送金额", "套餐", "支付状态", "支付渠道", "支付订单号", "退款状态", "已退金额", "创建时间"} {
		if !strings.Contains(strings.Join(header, ","), col) {
			t.Fatalf("充值导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("充值导出应含 2 行，实际 %d 行", len(rows))
	}
	// id DESC：第一行是自定义充值（66.66）。
	payHasCell(t, "充值导出", rows[0], header, "实付金额", "66.66")
	payHasCell(t, "充值导出", rows[0], header, "赠送金额", "0.00")
	payHasCell(t, "充值导出", rows[0], header, "充值金额", "66.66")
	payHasCell(t, "充值导出", rows[0], header, "套餐", "自定义")
	payHasCell(t, "充值导出", rows[0], header, "支付状态", "未支付")
	payHasCell(t, "充值导出", rows[0], header, "退款状态", "无退款")
	if payColumn(t, "充值导出", rows[0], header, "支付订单号") == "" {
		t.Fatalf("自定义充值也应回填挂单订单号：%v", rows[0])
	}
	payHasCell(t, "充值导出", rows[1], header, "实付金额", "100.00")
	payHasCell(t, "充值导出", rows[1], header, "赠送金额", "20.00")
	payHasCell(t, "充值导出", rows[1], header, "充值金额", "120.00")
	payHasCell(t, "充值导出", rows[1], header, "套餐", "excel 套餐 100 送 20")

	// 支付状态筛选联动。
	_, rows = getPayExportSheet(t, env, admin, "/pay/wallet-recharge/export-excel?payStatus=1")
	if len(rows) != 0 {
		t.Fatalf("payStatus=1 筛选导出应 0 行（未支付），实际 %d 行", len(rows))
	}

	// 租户隔离：租户 B 导出 0 行。
	otherTenant := createTenantByAPI(t, env, admin, "pay-excel-recharge-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	_, emptyRows := getPayExportSheet(t, env, other, "/pay/wallet-recharge/export-excel")
	if len(emptyRows) != 0 {
		t.Fatalf("租户 B 充值导出应 0 行，实际 %d 行", len(emptyRows))
	}
}

// TestIntegrationPayExportPermissionForbidden 导出权限：普通角色（role id=2）
// 未授任何 pay:<entity>:export → 五个导出端点全 403。
func TestIntegrationPayExportPermissionForbidden(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	demo := env.Login(t, "demo", "123456")
	for _, path := range []string{
		"/pay/order/export-excel",
		"/pay/refund/export-excel",
		"/pay/transfer/export-excel",
		"/pay/wallet-transaction/export-excel",
		"/pay/wallet-recharge/export-excel",
	} {
		status, _ := env.Request(t, http.MethodGet, path, demo, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色 %s 应 403，实际 HTTP %d", path, status)
		}
	}
}
