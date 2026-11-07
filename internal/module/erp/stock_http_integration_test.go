//go:build integration

// 库存页 / 出入库流水页 HTTP 集成测试（issue #90，docs/erp-mvp-spec.md §5/§8.2）：
// 仓库×产品余额分页与筛选、get-count（单仓/全仓）、流水按 biz_type/仓库/产品/时间
// 筛选、导出列契约、普通角色 403（含 approve 独立权限码）。
package erp_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// seedStockData 建两仓两品并垫库存：仓库甲 8/3，仓库乙 5（数量字符串返回）。
func seedStockData(t *testing.T, env *testutil.IntegrationEnv, token, tag string) (whA, whB, prodA, prodB int64) {
	t.Helper()
	whA, prodA = setupStockEnv(t, env, token, tag+"甲")
	whB, prodB = setupStockEnv(t, env, token, tag+"乙")
	for _, doc := range []map[string]any{
		{
			"warehouseId": whA, "inTime": env.Date() + " 09:00:00", "type": 10,
			"items": []map[string]any{stockItemJSON(prodA, "8", "1.00"), stockItemJSON(prodB, "3", "1.00")},
		},
		{
			"warehouseId": whB, "inTime": env.Date() + " 09:30:00", "type": 30,
			"items": []map[string]any{stockItemJSON(prodA, "5", "1.00")},
		},
	} {
		row := createStockInDoc(t, env, token, doc)
		if status, api := approveDoc(t, env, token, "stock-in", int64(row["id"].(float64)), 20); status != http.StatusOK || api.Code != 0 {
			t.Fatalf("审核垫库存单据：HTTP %d code %d msg %q", status, api.Code, api.Msg)
		}
	}
	return whA, whB, prodA, prodB
}

// findStockRow 在库存分页里定位 仓库×产品 行，返回 count 字符串；不存在返回 ""。
func findStockRow(rows []map[string]any, warehouseID, productID int64) string {
	for _, row := range rows {
		if int64(row["warehouseId"].(float64)) == warehouseID &&
			int64(row["productId"].(float64)) == productID {
			return fmt.Sprint(row["count"])
		}
	}
	return ""
}

// TestIntegrationStockPageAndGetCount 库存页：仓库×产品余额分页、按仓库/产品筛选、
// get-count 单仓余额与全仓合计（spec §5）。
func TestIntegrationStockPageAndGetCount(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	whA, whB, prodA, prodB := seedStockData(t, env, token, "库存页")

	// 全量：两仓三行余额（甲 A=8 甲 B=3 乙 A=5）。
	rows, total := pageStockRows(t, env, token, "")
	if total != 3 || len(rows) != 3 {
		t.Fatalf("库存页 total=%d len=%d，期望 3", total, len(rows))
	}
	if got := findStockRow(rows, whA, prodA); got != "8.000" {
		t.Fatalf("甲仓 A 产品余额 = %q，期望 8.000", got)
	}
	if got := findStockRow(rows, whA, prodB); got != "3.000" {
		t.Fatalf("甲仓 B 产品余额 = %q，期望 3.000", got)
	}
	// 名称解析。
	if rows[0]["warehouseName"] == "" || rows[0]["productName"] == "" {
		t.Fatalf("库存行名称未解析：%v", rows[0])
	}

	// 按仓库筛选。
	rows, total = pageStockRows(t, env, token, fmt.Sprintf("&warehouseId=%d", whA))
	if total != 2 || findStockRow(rows, whB, prodA) != "" {
		t.Fatalf("按仓库筛选应只剩甲仓 2 行：total=%d rows=%v", total, rows)
	}
	// 按产品筛选。
	rows, total = pageStockRows(t, env, token, fmt.Sprintf("&productId=%d", prodB))
	if total != 1 || len(rows) != 1 {
		t.Fatalf("按产品筛选应 1 行：total=%d", total)
	}

	// get-count：单仓余额 / 全仓合计。
	if got := getStockCount(t, env, token, prodA, whA); got != "8.000" {
		t.Fatalf("单仓余额 = %s，期望 8.000", got)
	}
	if got := getStockCount(t, env, token, prodA, 0); got != "13.000" {
		t.Fatalf("全仓合计 = %s，期望 13.000（8 + 5）", got)
	}
	// 无余额行返回 0。
	if got := getStockCount(t, env, token, prodB, whB); got != "0.000" {
		t.Fatalf("无余额行合计 = %s，期望 0.000", got)
	}
}

// TestIntegrationStockRecordFilters 流水页筛选（spec §5：biz_type/仓库/产品/时间）。
func TestIntegrationStockRecordFilters(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	whA, _, prodA, _ := seedStockData(t, env, token, "流水")

	// 全量：3 条入账流水。
	_, total := pageStockRecords(t, env, token, "")
	if total != 3 {
		t.Fatalf("流水 total=%d，期望 3", total)
	}

	// biz_type 筛选：全部是 stock_in。
	rows, total := pageStockRecords(t, env, token, "&bizType=stock_in")
	if total != 3 {
		t.Fatalf("按 biz_type 筛选 total=%d，期望 3", total)
	}
	for _, row := range rows {
		if row["bizType"] != "stock_in" {
			t.Fatalf("流水 bizType = %v，期望 stock_in", row["bizType"])
		}
	}
	_, total = pageStockRecords(t, env, token, "&bizType=stock_out")
	if total != 0 {
		t.Fatalf("stock_out 筛选应空：total=%d", total)
	}

	// 仓库/产品筛选。
	_, total = pageStockRecords(t, env, token, fmt.Sprintf("&warehouseId=%d", whA))
	if total != 2 {
		t.Fatalf("按仓库筛选 total=%d，期望 2", total)
	}
	_, total = pageStockRecords(t, env, token, fmt.Sprintf("&productId=%d", prodA))
	if total != 2 {
		t.Fatalf("按产品筛选 total=%d，期望 2", total)
	}

	// 时间筛选（业务时区）：当日含全部；昨日区间为空。
	_, total = pageStockRecords(t, env, token, "&startTime="+env.Date()+"&endTime="+env.Date())
	if total != 3 {
		t.Fatalf("当日时间筛选 total=%d，期望 3（end 为纯日期含当日）", total)
	}
	_, total = pageStockRecords(t, env, token, "&startTime=20000101&endTime=20000102")
	if total != 0 {
		t.Fatalf("昨日区间筛选 total=%d，期望 0", total)
	}

	// 组合筛选：仓库 + biz_type。
	_, total = pageStockRecords(t, env, token, fmt.Sprintf("&warehouseId=%d&bizType=stock_in", whA))
	if total != 2 {
		t.Fatalf("组合筛选 total=%d，期望 2", total)
	}
}

// TestIntegrationStockExports 库存/流水导出列契约（spec §4.11）。
func TestIntegrationStockExports(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	seedStockData(t, env, token, "导出")

	header, rows := getExportRows(t, env, token, "/erp/stock/export-excel")
	for _, col := range []string{"产品名称", "仓库", "库存数量", "更新时间"} {
		expectColumn(t, "库存导出", header, col)
	}
	if len(rows) != 3 {
		t.Fatalf("库存导出应 3 行，实际 %d", len(rows))
	}

	header, rows = getExportRows(t, env, token, "/erp/stock-record/export-excel")
	for _, col := range []string{"业务类型", "单据编号", "产品名称", "仓库", "变动数量", "结存数量", "入账时间"} {
		expectColumn(t, "流水导出", header, col)
	}
	if len(rows) != 3 {
		t.Fatalf("流水导出应 3 行，实际 %d", len(rows))
	}
	if rows[0][0] != "其它入库" {
		t.Fatalf("流水导出业务类型 = %q，期望 其它入库", rows[0][0])
	}
}

// TestIntegrationStockPermission 普通角色 403（spec §7：内置普通角色默认不授
// ERP；数据范围角色按按钮授权）：无 ERP 菜单的角色访问库存页 403；仅 query
// 角色调用审核接口 403（approve 是独立权限码）。
func TestIntegrationStockPermission(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, admin, "权限")

	doc := createStockInDoc(t, env, admin, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "2", "0.00")},
	})
	docID := int64(doc["id"].(float64))

	// 角色 A：完全无 ERP 菜单（只有无关菜单）。
	roleA := createRoleByAPI(t, env, admin, map[string]any{
		"name": "无 ERP 角色A", "code": "erp_stock_no_menu_role", "dataScope": 1,
		"menuIds": []int64{},
	})
	createUserByAPI(t, env, admin, "stock_no_perm_user", 101, []int64{roleA})
	tokenA := env.Login(t, "stock_no_perm_user", "123456")

	status, _ := env.Request(t, http.MethodGet, "/erp/stock/page?pageNo=1&pageSize=10", tokenA, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无 ERP 菜单角色访问库存页：HTTP %d，期望 403", status)
	}
	status, _ = env.Request(t, http.MethodGet, "/erp/stock-record/page?pageNo=1&pageSize=10", tokenA, nil)
	if status != http.StatusForbidden {
		t.Fatalf("无 ERP 菜单角色访问流水页：HTTP %d，期望 403", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/erp/stock-in", tokenA, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "1", "0.00")},
	})
	if status != http.StatusForbidden {
		t.Fatalf("无 ERP 菜单角色创建单据：HTTP %d，期望 403", status)
	}

	// 角色 B：仅入库单 query（菜单 723，无按钮）→ 读得到，审核 403。
	roleB := createRoleByAPI(t, env, admin, map[string]any{
		"name": "仅查询角色B", "code": "erp_stock_query_only_role", "dataScope": 1,
		"menuIds": []int64{erpStockInMenuID},
	})
	createUserByAPI(t, env, admin, "stock_query_only_user", 101, []int64{roleB})
	tokenB := env.Login(t, "stock_query_only_user", "123456")

	status, _ = env.Request(t, http.MethodGet, "/erp/stock-in/page?pageNo=1&pageSize=10", tokenB, nil)
	if status != http.StatusOK {
		t.Fatalf("仅 query 角色读单据列表：HTTP %d，期望 200", status)
	}
	status, _ = approveDoc(t, env, tokenB, "stock-in", docID, 20)
	if status != http.StatusForbidden {
		t.Fatalf("仅 query 角色审核单据：HTTP %d，期望 403（approve 独立权限码）", status)
	}
}

// TestIntegrationStockAuditColumns 库存账裸 SQL 原语必须写审计列（spec §3 通用
// 底座 + ADR 0002）：EnsureStockRow 插入时写 created_at/updated_at/creator/updater，
// Increase/Decrease 更新时写 updated_at/updater。历史缺陷是裸 upsert 只用列默认值
// → creator/updater 恒 '0'、created_at NULL。
func TestIntegrationStockAuditColumns(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	token := env.Login(t, "admin", "123456")
	warehouseID, productID := setupStockEnv(t, env, token, "审计")
	adminID := findUserIDByUsername(t, env, token, "admin")
	wantUser := strconv.FormatInt(adminID, 10)

	in := createStockInDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "inTime": env.Date() + " 09:00:00",
		"items": []map[string]any{stockItemJSON(productID, "5", "1.00")},
	})
	inID := int64(in["id"].(float64))
	if status, api := approveDoc(t, env, token, "stock-in", inID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核入库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	assertStockAudit(t, env, warehouseID, productID, wantUser)

	// 出账方向更新：updater 仍为当前登录用户，updated_at 非空。
	out := createStockOutDoc(t, env, token, map[string]any{
		"warehouseId": warehouseID, "outTime": env.Date() + " 10:00:00",
		"items": []map[string]any{stockItemJSON(productID, "2", "1.00")},
	})
	outID := int64(out["id"].(float64))
	if status, api := approveDoc(t, env, token, "stock-out", outID, 20); status != http.StatusOK || api.Code != 0 {
		t.Fatalf("审核出库单：HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	assertStockAudit(t, env, warehouseID, productID, wantUser)
}

// assertStockAudit 断言 erp_stock 行的审计列已由入账原语补齐（无登录上下文时
// 允许 '0'，但 HTTP 路径必须写当前用户）。
func assertStockAudit(t *testing.T, env *testutil.IntegrationEnv, warehouseID, productID int64, wantUser string) {
	t.Helper()
	var row struct {
		Creator   string
		Updater   string
		CreatedAt sql.NullTime
		UpdatedAt sql.NullTime
	}
	if err := tenant.Skip(env.DB).Table("erp_stock").
		Select("creator, updater, created_at, updated_at").
		Where("warehouse_id = ? AND product_id = ?", warehouseID, productID).
		Take(&row).Error; err != nil {
		t.Fatalf("读取库存行审计列: %v", err)
	}
	if !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		t.Fatalf("库存行 created_at/updated_at 不应为 NULL：%+v", row)
	}
	if row.Creator != wantUser || row.Updater != wantUser {
		t.Fatalf("库存行 creator/updater = %q/%q，期望 %q/%q（当前登录用户）",
			row.Creator, row.Updater, wantUser, wantUser)
	}
}
