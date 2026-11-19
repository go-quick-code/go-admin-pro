//go:build integration

// mall 管理端 Excel 导出 HTTP 集成测试（docs/mall-mvp-spec.md §1 第 8 步
// 「Excel 导出」，PR #184 双轴审查补票；照 pay pay_excel_http_integration_test.go
// 同构的最小集合）：会员/商品 SPU/交易订单三个管理列表导出——
//   - 复用列表筛选（导出内容与权限一致，spec 硬约束）：状态/手机号/名称/
//     订单号过滤联动；
//   - 文件头 attachment + xlsx MIME + 中文文件名（filename* 双编码）；
//   - Money 经 TextMarshaler 输出十进制字符串；日期按业务时区（NativeDates，
//     墙上时间 yyyy-MM-dd HH:mm:ss）；
//   - 行数与造数一致（列表/导出同一谓词）；
//   - 租户隔离：第二租户只见自己的行；
//   - 权限码 mall:<entity>:export：普通角色（role id=2）未授任何 mall 菜单
//     → 三端点全 403。
//
// 权限码收敛（路由 ↔ seed 9015/9145/9214 双向比对）由 internal/seed
// TestPermSetsConverge 静态覆盖，此处不重复。
package mall_test

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"github.com/xuri/excelize/v2"
)

// mallExportSheet 请求导出接口并解析「数据」工作表，返回表头与数据行（文本
// 形态）。允许空列表导出（只有表头）：筛选/租户隔离用例会断言 0 数据行。同时
// 校验双编码文件名（ASCII filename 兼容旧客户端 + filename*=UTF-8” 中文名）
// 与 xlsx MIME（pay getPayExportSheet 同口径）。
func mallExportSheet(t *testing.T, env *testutil.IntegrationEnv, token, path string) ([]string, [][]string) {
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

// mallExportRows 请求导出接口并要求至少 1 条数据行。
func mallExportRows(t *testing.T, env *testutil.IntegrationEnv, token, path string) ([]string, [][]string) {
	t.Helper()
	header, rows := mallExportSheet(t, env, token, path)
	if len(rows) == 0 {
		t.Fatalf("GET %s: 期望至少 1 条数据行，实际只有表头 %v", path, header)
	}
	return header, rows
}

// mallColumn 按表头取行内列值（缺列 Fatal；行尾空单元格被 excelize 裁剪时
// 返回空串——与 pay payColumn 同口径）。
func mallColumn(t *testing.T, phase string, row []string, header []string, col string) string {
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

// mallHasCell 断言行内指定列的值等于期望。
func mallHasCell(t *testing.T, phase string, row []string, header []string, col, want string) {
	t.Helper()
	if got := mallColumn(t, phase, row, header, col); got != want {
		t.Fatalf("%s：列 %q = %q，期望 %q（行 %v）", phase, col, got, want, row)
	}
}

// mallTimeCellRe 是导出日期单元格的展示格式（excel.DefaultTimeLayout，业务时区
// 墙上时间，NativeDates 数字格式 yyyy-mm-dd hh:mm:ss）。
var mallTimeCellRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)

// mallCreateTimeLevel 创建等级（断言成功返回 id）。
func mallCreateTimeLevel(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/level", token, map[string]any{"name": name, "level": 3})
	mustOK(t, "创建等级 "+name, status, api)
	return int64(parseJSON(t, string(api.Data))["id"].(float64))
}

// mallCreateBrand 创建品牌（断言成功返回 id）。
func mallCreateBrand(t *testing.T, env *testutil.IntegrationEnv, token, name string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/product/brand", token, map[string]any{"name": name})
	mustOK(t, "创建品牌 "+name, status, api)
	return int64(parseJSON(t, string(api.Data))["id"].(float64))
}

// TestIntegrationMemberExportExcel 会员导出：列标签、等级名批量解析（含未分配
// 与已删等级回落）、积分余额、状态中文、筛选联动（手机号/状态）、租户隔离。
func TestIntegrationMemberExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	levelID := mallCreateTimeLevel(t, env, admin, "黄金会员")
	m1 := mallCreateMember(t, env, admin, map[string]any{"mobile": "13900000001", "nickname": "张三", "levelId": levelID})
	if status, api := env.Request(t, http.MethodPost, "/mall/member/adjust-point", admin, map[string]any{"userId": m1["id"], "point": 5}); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("调整积分: HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	mallCreateMember(t, env, admin, map[string]any{"mobile": "13900000002", "nickname": "李四"})

	header, rows := mallExportRows(t, env, admin, "/mall/member/export-excel")
	for _, col := range []string{"ID", "手机号", "昵称", "等级", "积分余额", "状态", "注册时间"} {
		if !strings.Contains(strings.Join(header, ","), col) {
			t.Fatalf("会员导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("会员导出应含 2 行，实际 %d 行", len(rows))
	}
	// id DESC：第一行是后建的会员（李四，未分配等级）。
	zhang := rows[1]
	li := rows[0]
	mallHasCell(t, "会员导出", li, header, "手机号", "13900000002")
	mallHasCell(t, "会员导出", li, header, "昵称", "李四")
	mallHasCell(t, "会员导出", li, header, "等级", "-")
	mallHasCell(t, "会员导出", li, header, "积分余额", "0")
	mallHasCell(t, "会员导出", li, header, "状态", "启用")
	mallHasCell(t, "会员导出", zhang, header, "手机号", "13900000001")
	mallHasCell(t, "会员导出", zhang, header, "等级", "黄金会员")
	mallHasCell(t, "会员导出", zhang, header, "积分余额", "5")
	if !mallTimeCellRe.MatchString(mallColumn(t, "会员导出", zhang, header, "注册时间")) {
		t.Fatalf("注册时间应按业务时区 yyyy-MM-dd HH:mm:ss 输出，实际 %q", mallColumn(t, "会员导出", zhang, header, "注册时间"))
	}

	// 筛选联动（复用列表筛选口径）：手机号精确过滤 1 行；status=1（停用）0 行。
	_, rows = mallExportRows(t, env, admin, "/mall/member/export-excel?mobile=13900000001")
	if len(rows) != 1 {
		t.Fatalf("按手机号筛选导出应 1 行，实际 %d 行", len(rows))
	}
	mallHasCell(t, "会员导出筛选", rows[0], header, "昵称", "张三")
	_, rows = mallExportSheet(t, env, admin, "/mall/member/export-excel?status=1")
	if len(rows) != 0 {
		t.Fatalf("status=1 筛选导出应 0 行（全部启用），实际 %d 行", len(rows))
	}

	// 租户隔离：租户 B 的导出只见自己的会员。
	otherTenant := createTenantByAPI(t, env, admin, "mall-export-member-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	mallCreateMember(t, env, other, map[string]any{"mobile": "13900009999", "nickname": "租户B会员"})
	_, rows = mallExportRows(t, env, other, "/mall/member/export-excel")
	if len(rows) != 1 {
		t.Fatalf("租户 B 会员导出应只含自己的 1 行，实际 %d 行", len(rows))
	}
	mallHasCell(t, "租户隔离", rows[0], header, "手机号", "13900009999")
}

// TestIntegrationSPUExportExcel SPU 导出：列标签、分类/品牌名批量解析（未关联
// 品牌输出空）、规格类型/上下架中文、市场价十进制字符串、筛选联动、租户隔离。
func TestIntegrationSPUExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	categoryID := mallCreateCategory(t, env, admin, map[string]any{"name": "导出分类"})
	brandID := mallCreateBrand(t, env, admin, "导出品牌")
	mallCreateSPU(t, env, admin, map[string]any{
		"categoryId": categoryID, "brandId": brandID, "name": "单规格商品",
		"specType": 0, "status": 1, "marketPrice": "12.50",
		"skus": []map[string]any{{"properties": []map[string]any{}, "price": "9.90", "stock": 5, "first": true}},
	})
	propID := mallCreateProperty(t, env, admin, "导出颜色")
	redID := mallCreatePropertyValue(t, env, admin, propID, "红")
	blueID := mallCreatePropertyValue(t, env, admin, propID, "蓝")
	mallCreateSPU(t, env, admin, map[string]any{
		"categoryId": categoryID, "name": "多规格商品",
		"specType": 1, "status": 1, "marketPrice": "30.00",
		"skus": []map[string]any{
			{"properties": []map[string]any{{"propertyId": propID, "valueId": redID}}, "price": "29.00", "stock": 3, "first": true},
			{"properties": []map[string]any{{"propertyId": propID, "valueId": blueID}}, "price": "28.00", "stock": 2},
		},
	})

	header, rows := mallExportRows(t, env, admin, "/mall/product/spu/export-excel")
	for _, col := range []string{"ID", "商品名称", "分类", "品牌", "规格类型", "市场价", "状态", "创建时间"} {
		if !strings.Contains(strings.Join(header, ","), col) {
			t.Fatalf("SPU 导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("SPU 导出应含 2 行，实际 %d 行", len(rows))
	}
	// id DESC：第一行是后建的多规格 SPU（未关联品牌）。
	multi := rows[0]
	single := rows[1]
	mallHasCell(t, "SPU 导出", multi, header, "商品名称", "多规格商品")
	mallHasCell(t, "SPU 导出", multi, header, "分类", "导出分类")
	mallHasCell(t, "SPU 导出", multi, header, "品牌", "")
	mallHasCell(t, "SPU 导出", multi, header, "规格类型", "多规格")
	mallHasCell(t, "SPU 导出", multi, header, "市场价", "30.00")
	mallHasCell(t, "SPU 导出", multi, header, "状态", "上架")
	mallHasCell(t, "SPU 导出", single, header, "商品名称", "单规格商品")
	mallHasCell(t, "SPU 导出", single, header, "品牌", "导出品牌")
	mallHasCell(t, "SPU 导出", single, header, "规格类型", "单规格")
	mallHasCell(t, "SPU 导出", single, header, "市场价", "12.50")
	if !mallTimeCellRe.MatchString(mallColumn(t, "SPU 导出", single, header, "创建时间")) {
		t.Fatalf("创建时间应按业务时区 yyyy-MM-dd HH:mm:ss 输出，实际 %q", mallColumn(t, "SPU 导出", single, header, "创建时间"))
	}

	// 筛选联动（复用列表筛选口径）：名称模糊 1 行；status=0（下架）0 行。
	_, rows = mallExportRows(t, env, admin, "/mall/product/spu/export-excel?name=单规格")
	if len(rows) != 1 {
		t.Fatalf("按名称筛选导出应 1 行，实际 %d 行", len(rows))
	}
	mallHasCell(t, "SPU 导出筛选", rows[0], header, "商品名称", "单规格商品")
	_, rows = mallExportSheet(t, env, admin, "/mall/product/spu/export-excel?status=0")
	if len(rows) != 0 {
		t.Fatalf("status=0 筛选导出应 0 行（全部上架），实际 %d 行", len(rows))
	}

	// 租户隔离：租户 B 的导出 0 行（未建商品）。
	otherTenant := createTenantByAPI(t, env, admin, "mall-export-spu-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	_, emptyRows := mallExportSheet(t, env, other, "/mall/product/spu/export-excel")
	if len(emptyRows) != 0 {
		t.Fatalf("租户 B SPU 导出应 0 行，实际 %d 行", len(emptyRows))
	}
}

// TestIntegrationTradeOrderExportExcel 订单导出：列标签、单号/会员解析/状态
// 文案/金额十进制字符串/支付时间（待支付为空）、筛选联动（状态/编号）、
// 租户隔离。
func TestIntegrationTradeOrderExportExcel(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	spuID, skuID := tradeCreateSaleableSPU(t, env, admin, "导出订单商品", "2.00", 5)
	userID := int64(mallCreateMember(t, env, admin, map[string]any{"mobile": "13800009901", "nickname": "导出会员"})["id"].(float64))
	pending := tradeAdminInsertOrder(t, env, spuID, skuID, userID, mallmodel.TradeOrderStatusPending, "")
	paid := tradeAdminInsertOrder(t, env, spuID, skuID, userID, mallmodel.TradeOrderStatusPaid, "PAY-EXPORT-001")

	header, rows := mallExportRows(t, env, admin, "/mall/trade/order/export-excel")
	for _, col := range []string{"订单号", "会员", "状态", "总件数", "商品总额", "实付金额", "支付渠道", "下单时间", "支付时间"} {
		if !strings.Contains(strings.Join(header, ","), col) {
			t.Fatalf("订单导出表头缺少 %q 列：%v", col, header)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("订单导出应含 2 行，实际 %d 行", len(rows))
	}
	// id DESC：第一行是后建的已支付单。
	paidRow := rows[0]
	pendingRow := rows[1]
	if !strings.HasPrefix(mallColumn(t, "订单导出", paidRow, header, "订单号"), "SO") {
		t.Fatalf("订单号应 SO 前缀：%v", paidRow)
	}
	mallHasCell(t, "订单导出", paidRow, header, "会员", "导出会员(13800009901)")
	mallHasCell(t, "订单导出", paidRow, header, "状态", "已支付")
	mallHasCell(t, "订单导出", paidRow, header, "总件数", "1")
	mallHasCell(t, "订单导出", paidRow, header, "商品总额", "2.00")
	mallHasCell(t, "订单导出", paidRow, header, "实付金额", "2.00")
	mallHasCell(t, "订单导出", paidRow, header, "支付渠道", "mock")
	if mallColumn(t, "订单导出", paidRow, header, "支付时间") == "" {
		t.Fatalf("已支付单应回填支付时间：%v", paidRow)
	}
	mallHasCell(t, "订单导出", pendingRow, header, "订单号", pending.No)
	mallHasCell(t, "订单导出", pendingRow, header, "状态", "待支付")
	mallHasCell(t, "订单导出", pendingRow, header, "支付时间", "")
	if !mallTimeCellRe.MatchString(mallColumn(t, "订单导出", paidRow, header, "下单时间")) {
		t.Fatalf("下单时间应按业务时区 yyyy-MM-dd HH:mm:ss 输出，实际 %q", mallColumn(t, "订单导出", paidRow, header, "下单时间"))
	}

	// 筛选联动（复用管理端订单筛选口径）：状态过滤 1 行；编号模糊命中已支付单。
	_, rows = mallExportRows(t, env, admin, "/mall/trade/order/export-excel?status=0")
	if len(rows) != 1 {
		t.Fatalf("status=0 筛选导出应 1 行，实际 %d 行", len(rows))
	}
	mallHasCell(t, "订单导出筛选", rows[0], header, "订单号", pending.No)
	_, rows = mallExportRows(t, env, admin, "/mall/trade/order/export-excel?no="+paid.No)
	if len(rows) != 1 {
		t.Fatalf("按编号筛选导出应 1 行，实际 %d 行", len(rows))
	}
	mallHasCell(t, "订单导出筛选", rows[0], header, "订单号", paid.No)

	// 租户隔离：租户 B 的导出 0 行（未发生交易）。
	otherTenant := createTenantByAPI(t, env, admin, "mall-export-order-b")
	other := loginTenant(t, env, "admin", "123456", otherTenant)
	_, emptyRows := mallExportSheet(t, env, other, "/mall/trade/order/export-excel")
	if len(emptyRows) != 0 {
		t.Fatalf("租户 B 订单导出应 0 行，实际 %d 行", len(emptyRows))
	}
}

// TestIntegrationMallExportPermissionForbidden 导出权限：普通角色（role id=2）
// 未授任何 mall 菜单/按钮 → 三个导出端点全 403。
func TestIntegrationMallExportPermissionForbidden(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	demo := env.Login(t, "demo", "123456")
	for _, path := range []string{
		"/mall/member/export-excel",
		"/mall/product/spu/export-excel",
		"/mall/trade/order/export-excel",
	} {
		status, _ := env.Request(t, http.MethodGet, path, demo, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色 %s 应 403，实际 HTTP %d", path, status)
		}
	}
}
