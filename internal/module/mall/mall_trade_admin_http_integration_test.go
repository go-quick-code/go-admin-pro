//go:build integration

// mall 管理端交易 HTTP 集成测试（issue #180，T7；docs/mall-mvp-spec.md §4
// 管理端交易部分）：
//   - 管理员取消待支付单：状态 0→30 + cancel_reason 固定「管理员取消」+
//     cancel_time 落列 + 库存回补（同事务，spec §3.4）；重复取消 409（终态
//     幂等拒绝，不二次回补）；
//   - 对已支付单取消 409（票面指定手法：repo 原语 mallrepo.New(env.DB) 直构
//     目标态单——真实扣减库存 + 落目标态订单/订单项），状态不变、不回补；
//   - 管理员完成已支付单：状态 10→20（走状态机 + 条件 UPDATE）；对未支付/
//     已完成单完成 409；
//   - 管理员备注任意状态可补注（待支付/已取消/已完成），≤500 字；不存在
//     404；
//   - 分页过滤：状态/编号模糊/会员 ID/时间范围（含 end 纯日期含当日语义）；
//   - 越权（租户隔离）：第二租户管理员对租户 1 订单详情/取消/完成/备注一律
//     404 不泄露存在性（ADR 0019 租户插件口径）。
//
// 权限码收敛由 internal/seed TestPermSetsConverge 静态覆盖（mall:trade:*
// 与菜单 920/921/9211–9213 双向比对），此处不重复。
package mall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// tradeAdminOrderPage 管理端订单分页（断言成功并解析 list）。
func tradeAdminOrderPage(t *testing.T, env *testutil.IntegrationEnv, adminToken, query string) []map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/mall/trade/order/page?"+query, adminToken, nil)
	mustOK(t, "管理端订单分页 "+query, status, api)
	return mallPageList(t, api)
}

// tradeAdminGetOrder 管理端订单详情（断言成功并解析 data）。
func tradeAdminGetOrder(t *testing.T, env *testutil.IntegrationEnv, adminToken string, orderID int64) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/mall/trade/order/"+itoa(orderID), adminToken, nil)
	mustOK(t, "管理端订单详情", status, api)
	return parseJSON(t, string(api.Data))
}

// tradeAdminInsertOrder repo 直构目标态订单 + 订单项（票面指定手法，T6 非法
// 迁移用例同款：mallrepo.New(env.DB) 直构——库存扣减走真实扣减原语、单号走
// mall_sequence 事务形状同款发放，构造时即为目标态）。每单 1 件、单价 2.00。
func tradeAdminInsertOrder(t *testing.T, env *testutil.IntegrationEnv, spuID, skuID, userID int64, status int8, payNo string) *mallmodel.TradeOrder {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	repo := mallrepo.New(env.DB)
	rows, err := repo.DeductSKUStock(ctx, tenant.DefaultID, skuID, 1)
	if err != nil || rows != 1 {
		t.Fatalf("直构订单扣库存: rows=%d err=%v", rows, err)
	}
	seq, err := repo.NextSequenceValue(ctx, tenant.DefaultID, mallservice.SeqOrder, env.DateKey())
	if err != nil {
		t.Fatalf("发放序列: %v", err)
	}
	now := time.Now()
	order := &mallmodel.TradeOrder{
		TenantID:       tenant.DefaultID,
		No:             "SO" + env.DateKey() + itoa64(seq),
		UserID:         userID,
		Status:         status,
		TotalQuantity:  1,
		ItemMoney:      money.FromCents(200),
		PayMoney:       money.FromCents(200),
		PayOrderNo:     payNo,
		PayChannelCode: "mock",
		ReceiverName:   "管理端用户",
		ReceiverMobile: "13800001234",
		ReceiverArea:   "广东省 深圳市 南山区",
		ReceiverDetail: "科技园 1 号",
	}
	switch status {
	case mallmodel.TradeOrderStatusPaid, mallmodel.TradeOrderStatusCompleted:
		order.PayTime = &now
	case mallmodel.TradeOrderStatusCanceled:
		order.CancelTime = &now
		order.CancelReason = "测试预置取消"
	case mallmodel.TradeOrderStatusClosed:
		order.CloseTime = &now
		order.CloseReason = "超时关闭（测试构造）"
	}
	if err := repo.CreateOrder(ctx, order); err != nil {
		t.Fatalf("直构订单: %v", err)
	}
	if err := repo.CreateOrderItems(ctx, []mallmodel.TradeOrderItem{{
		TenantID: tenant.DefaultID, OrderID: order.ID, UserID: userID,
		SPUID: spuID, SKUID: skuID,
		SPUName: "管理端直构商品", SKUProperties: json.RawMessage("[]"),
		Price: money.FromCents(200), Count: 1, Subtotal: money.FromCents(200),
	}}); err != nil {
		t.Fatalf("直构订单项: %v", err)
	}
	return order
}

// itoa64 int64 → 十进制字符串（seq 格式化用；单号固定位数由 formatNo 承担，
// 测试侧直拼时 seq < 10000 前补零）。
func itoa64(v int64) string {
	s := itoa(v)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// TestIntegrationTradeAdminCancelPending 管理员取消待支付单：状态 0→30 +
// cancel_reason「管理员取消」+ 库存回补同事务；重复取消 409（已取消终态）
// 且不二次回补。
func TestIntegrationTradeAdminCancelPending(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "管理取消商品", "2.00", 5)

	memberID := tradeAdminCreateMember(t, env, adminToken, "13895007001")
	memberToken := tradeMemberToken(t, env, memberID)
	addressID := tradeInsertAddress(t, env, memberID, "管理取消用户")
	tradeAddCart(t, env, memberToken, skuID, 2)
	order := tradeCreateOrder(t, env, memberToken, addressID, "")
	orderID := int64(order["id"].(float64))
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 3 {
		t.Fatalf("下单后库存应为 3，实际 %d", got)
	}

	// 管理员取消：200；状态 30 + 固定原因 + 取消时间 + 库存恢复 5。
	status, api := env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(orderID)+"/cancel", adminToken, nil)
	mustOK(t, "管理员取消", status, api)
	detail := tradeAdminGetOrder(t, env, adminToken, orderID)
	if detail["status"] != float64(mallmodel.TradeOrderStatusCanceled) || detail["cancelReason"] != "管理员取消" {
		t.Fatalf("管理员取消后状态/原因不符（应 30/管理员取消）：%s", api.Data)
	}
	if ct, _ := detail["cancelTime"].(string); ct == "" {
		t.Fatalf("取消时间应落列：%s", api.Data)
	}
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 5 {
		t.Fatalf("取消回补后库存应恢复 5，实际 %d", got)
	}

	// 重复取消：409（已取消终态），库存不二次回补。
	status, api = env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(orderID)+"/cancel", adminToken, nil)
	if status != http.StatusConflict || !strings.Contains(api.Msg, "已取消") {
		t.Fatalf("重复取消应 409「已取消」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 5 {
		t.Fatalf("重复取消不应二次回补，库存应仍 5，实际 %d", got)
	}
}

// TestIntegrationTradeAdminCancelIllegal 管理员对已支付单取消 409（repo 直构
// 目标态单）：状态不变、cancel_time 不落、库存不回补。已取消单的 409 在
// CancelPending 用例覆盖（重复取消分支）。
func TestIntegrationTradeAdminCancelIllegal(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "管理取消非法迁移商品", "2.00", 10)
	memberID := tradeAdminCreateMember(t, env, adminToken, "13895007002")

	paid := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusPaid, "PAY-CANCEL-409")
	status, api := env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(paid.ID)+"/cancel", adminToken, nil)
	if status != http.StatusConflict || !strings.Contains(api.Msg, "不可取消") {
		t.Fatalf("对已支付单取消应 409「不可取消」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var dbStatus int
	var cancelTime *time.Time
	if err := env.DB.Raw("SELECT status, cancel_time FROM trade_order WHERE id = ?", paid.ID).Row().Scan(&dbStatus, &cancelTime); err != nil {
		t.Fatalf("读订单状态: %v", err)
	}
	if int8(dbStatus) != mallmodel.TradeOrderStatusPaid || cancelTime != nil {
		t.Fatalf("非法取消不得改状态/落取消时间：status=%d cancelTime=%v", dbStatus, cancelTime)
	}
	// 库存不回补（构造扣减后保持已扣口径）。
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 9 {
		t.Fatalf("非法取消不回补，库存应 9，实际 %d", got)
	}
}

// TestIntegrationTradeAdminComplete 管理员完成订单：已支付（repo 直构，带
// 支付单号）→ 200 状态 20；重复完成 409「已完成」；对待支付单完成 409。
func TestIntegrationTradeAdminComplete(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "管理完成商品", "2.00", 10)
	memberID := tradeAdminCreateMember(t, env, adminToken, "13895007003")

	// 已支付单：详情带管理面支付字段全量（payOrderNo/payChannelCode/payTime）。
	paid := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusPaid, "PAY-20261120-0001")
	detail := tradeAdminGetOrder(t, env, adminToken, paid.ID)
	if detail["payOrderNo"] != "PAY-20261120-0001" || detail["payChannelCode"] != "mock" {
		t.Fatalf("管理端详情应含支付单号/渠道：%s", detail)
	}
	if _, ok := detail["payTime"].(string); !ok {
		t.Fatalf("已支付单详情应含支付时间：%s", detail)
	}

	status, api := env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(paid.ID)+"/complete", adminToken, nil)
	mustOK(t, "管理员完成", status, api)
	detail = tradeAdminGetOrder(t, env, adminToken, paid.ID)
	if detail["status"] != float64(mallmodel.TradeOrderStatusCompleted) {
		t.Fatalf("完成后状态应为 20：%s", detail)
	}

	// 重复完成：409（已完成，幂等拒绝）。
	status, api = env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(paid.ID)+"/complete", adminToken, nil)
	if status != http.StatusConflict || !strings.Contains(api.Msg, "已完成") {
		t.Fatalf("重复完成应 409「已完成」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 对待支付单完成：409（状态机仅 10→20）。
	pending := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusPending, "")
	status, api = env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(pending.ID)+"/complete", adminToken, nil)
	if status != http.StatusConflict || !strings.Contains(api.Msg, "不可完成") {
		t.Fatalf("对待支付单完成应 409「不可完成」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	var dbStatus int
	if err := env.DB.Raw("SELECT status FROM trade_order WHERE id = ?", pending.ID).Row().Scan(&dbStatus); err != nil {
		t.Fatalf("读订单状态: %v", err)
	}
	if int8(dbStatus) != mallmodel.TradeOrderStatusPending {
		t.Fatalf("非法完成不得改状态：status=%d", dbStatus)
	}
}

// TestIntegrationTradeAdminRemark 管理员备注任意状态可补注（待支付/已取消
// 终态/已完成），详情回读 adminRemark；不存在订单 404。
func TestIntegrationTradeAdminRemark(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "管理备注商品", "2.00", 10)
	memberID := tradeAdminCreateMember(t, env, adminToken, "13895007004")

	// 待支付单备注。
	pending := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusPending, "")
	status, api := env.Request(t, http.MethodPut, "/mall/trade/order/"+itoa(pending.ID)+"/remark", adminToken, map[string]any{"remark": "客户催发货"})
	mustOK(t, "待支付单备注", status, api)
	detail := tradeAdminGetOrder(t, env, adminToken, pending.ID)
	if detail["adminRemark"] != "客户催发货" {
		t.Fatalf("待支付单备注应落列：%s", detail)
	}

	// 已取消（终态）单补注——备注不走状态机，终态也允许。
	canceled := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusCanceled, "")
	status, api = env.Request(t, http.MethodPut, "/mall/trade/order/"+itoa(canceled.ID)+"/remark", adminToken, map[string]any{"remark": "终态补注归档"})
	mustOK(t, "已取消单备注", status, api)
	detail = tradeAdminGetOrder(t, env, adminToken, canceled.ID)
	if detail["adminRemark"] != "终态补注归档" {
		t.Fatalf("已取消单补注应落列：%s", detail)
	}

	// 已完成单补注。
	completed := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusCompleted, "PAY-RM-1")
	status, api = env.Request(t, http.MethodPut, "/mall/trade/order/"+itoa(completed.ID)+"/remark", adminToken, map[string]any{"remark": "已完成对账"})
	mustOK(t, "已完成单备注", status, api)
	detail = tradeAdminGetOrder(t, env, adminToken, completed.ID)
	if detail["adminRemark"] != "已完成对账" {
		t.Fatalf("已完成单补注应落列：%s", detail)
	}

	// 备注覆盖保存（同一单二次备注覆盖旧值）。
	status, api = env.Request(t, http.MethodPut, "/mall/trade/order/"+itoa(pending.ID)+"/remark", adminToken, map[string]any{"remark": "已联系客户"})
	mustOK(t, "覆盖备注", status, api)
	detail = tradeAdminGetOrder(t, env, adminToken, pending.ID)
	if detail["adminRemark"] != "已联系客户" {
		t.Fatalf("二次备注应覆盖：%s", detail)
	}

	// 超长备注 400（>500 字，对齐 admin_remark 列宽）。
	status, api = env.Request(t, http.MethodPut, "/mall/trade/order/"+itoa(pending.ID)+"/remark", adminToken, map[string]any{"remark": strings.Repeat("长", 501)})
	if status != http.StatusBadRequest {
		t.Fatalf("超长备注应 400，实际 HTTP %d msg %q", status, api.Msg)
	}

	// 不存在订单 404。
	status, api = env.Request(t, http.MethodPut, "/mall/trade/order/999999999/remark", adminToken, map[string]any{"remark": "x"})
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("不存在订单备注：HTTP %d code %d，期望 404", status, api.Code)
	}
}

// TestIntegrationTradeAdminPageFilters 管理端订单分页过滤：状态/编号模糊/
// 会员 ID/时间范围（end 纯日期含当日语义），无过滤列全租户（含 repo 直构
// 的他人订单）。
func TestIntegrationTradeAdminPageFilters(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "管理分页商品", "2.00", 20)

	// 会员 A：C 端真实下一单（待支付）。
	memberA := tradeAdminCreateMember(t, env, adminToken, "13895007005")
	tokenA := tradeMemberToken(t, env, memberA)
	addressA := tradeInsertAddress(t, env, memberA, "分页用户A")
	tradeAddCart(t, env, tokenA, skuID, 1)
	orderA := tradeCreateOrder(t, env, tokenA, addressA, "")
	orderAID := int64(orderA["id"].(float64))
	orderANo := orderA["no"].(string)

	// 会员 B：repo 直构一待支付 + 一已支付（编号/状态/归属各异，供过滤比对）。
	memberB := tradeAdminCreateMember(t, env, adminToken, "13895007006")
	pendingB := tradeAdminInsertOrder(t, env, spuID, skuID, memberB, mallmodel.TradeOrderStatusPending, "")
	paidB := tradeAdminInsertOrder(t, env, spuID, skuID, memberB, mallmodel.TradeOrderStatusPaid, "PAY-FILTER-1")

	// 无过滤：三单全可见（管理端面向本租户全部会员订单）。
	all := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50")
	ids := map[int64]bool{}
	for _, row := range all {
		ids[int64(row["id"].(float64))] = true
	}
	if !ids[orderAID] || !ids[pendingB.ID] || !ids[paidB.ID] {
		t.Fatalf("无过滤应含三单：%s", all)
	}

	// 状态过滤：status=0 恰两单（A 待支付 + B 待支付）；status=10 恰一单。
	pending := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&status=0")
	if len(pending) != 2 {
		t.Fatalf("status=0 应恰 2 单，实际 %d：%s", len(pending), pending)
	}
	paidList := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&status=10")
	if len(paidList) != 1 || int64(paidList[0]["id"].(float64)) != paidB.ID {
		t.Fatalf("status=10 应恰已支付单：%s", paidList)
	}
	// 非法状态过滤 400（五态枚举校验）。
	status, _ := env.Request(t, http.MethodGet, "/mall/trade/order/page?pageNo=1&pageSize=10&status=99", adminToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法状态过滤应 400，实际 HTTP %d", status)
	}

	// 编号模糊：完整单号唯一命中；SO 日期键前缀命中全部三单。
	exact := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&no="+orderANo)
	if len(exact) != 1 || int64(exact[0]["id"].(float64)) != orderAID {
		t.Fatalf("完整单号应唯一命中：%s", exact)
	}
	fuzzy := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&no=SO"+env.DateKey())
	if len(fuzzy) < 3 {
		t.Fatalf("SO+日期键模糊应命中全部三单，实际 %d：%s", len(fuzzy), fuzzy)
	}
	none := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&no=NO-SUCH-XXXX")
	if len(none) != 0 {
		t.Fatalf("不存在的编号应为空集：%s", none)
	}

	// 会员 ID 过滤：A 恰一单；B 恰两单。
	byA := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&userId="+itoa(memberA))
	if len(byA) != 1 || int64(byA[0]["id"].(float64)) != orderAID {
		t.Fatalf("userId=A 应恰 1 单：%s", byA)
	}
	byB := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&userId="+itoa(memberB))
	if len(byB) != 2 {
		t.Fatalf("userId=B 应恰 2 单，实际 %d：%s", len(byB), byB)
	}

	// 时间范围：end=昨日（纯日期）空集（含当日外推后上界在今日 0 点前）；
	// start=今日（纯日期）含当日创建的全部三单。日期一律按业务时区取
	//（env.Date 口径，防 UTC/业务时区跨日错位）。
	yesterday := time.Now().In(env.Config.Location()).AddDate(0, 0, -1).Format("2006-01-02")
	today := env.Date()
	old := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&endTime="+yesterday)
	if len(old) != 0 {
		t.Fatalf("end=昨日应空集（无历史单）：%s", old)
	}
	recent := tradeAdminOrderPage(t, env, adminToken, "pageNo=1&pageSize=50&startTime="+today)
	if len(recent) < 3 {
		t.Fatalf("start=今日（含当日）应命中全部三单，实际 %d：%s", len(recent), recent)
	}

	// 分页项不带 items（items 随详情端点），主行带管理面字段 adminRemark。
	for _, row := range all {
		if items, ok := row["items"].([]any); ok && len(items) > 0 {
			t.Fatalf("分页项不应带 items（详情端点才带）：%s", row)
		}
		if _, ok := row["adminRemark"]; !ok {
			t.Fatalf("分页主行应带管理面字段 adminRemark：%s", row)
		}
	}
}

// TestIntegrationTradeAdminCrossTenant404 租户隔离越权（票面：普通管理员
// 跨租户 404——用第二租户 admin 构造）：租户 2 管理员对租户 1 订单的
// 详情/取消/完成/备注一律 404 不泄露存在性；分页不见租户 1 订单。
func TestIntegrationTradeAdminCrossTenant404(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "跨租户隔离商品", "2.00", 10)
	memberID := tradeAdminCreateMember(t, env, adminToken, "13895007007")

	// 租户 1：直构一待支付 + 一已支付单。
	pending := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusPending, "")
	paid := tradeAdminInsertOrder(t, env, spuID, skuID, memberID, mallmodel.TradeOrderStatusPaid, "PAY-CROSS-1")

	// 第二租户（createTenantByAPI 不带套餐 → 绑定全部启用菜单，其 admin 持
	// mall:trade:* 码；权限面存在而业务行跨租户不可见，恰好隔离「越权 403
	//」与「租户隔离 404」两种形态）。
	tenant2 := createTenantByAPI(t, env, adminToken, "mall-trade-t7-t2")
	token2 := loginTenant(t, env, "admin", "123456", tenant2)

	// 详情 404（不泄露存在性）。
	status, api := env.Request(t, http.MethodGet, "/mall/trade/order/"+itoa(pending.ID), token2, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("跨租户订单详情：HTTP %d code %d msg %q，期望 404", status, api.Code, api.Msg)
	}
	// 取消/完成/备注 404（跨租户业务 ID 不可见，不泄露存在性）。
	status, api = env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(pending.ID)+"/cancel", token2, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("跨租户取消：HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPost, "/mall/trade/order/"+itoa(paid.ID)+"/complete", token2, nil)
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("跨租户完成：HTTP %d code %d，期望 404", status, api.Code)
	}
	status, api = env.Request(t, http.MethodPut, "/mall/trade/order/"+itoa(pending.ID)+"/remark", token2, map[string]any{"remark": "attack"})
	if status != http.StatusNotFound || api.Code != 404 {
		t.Fatalf("跨租户备注：HTTP %d code %d，期望 404", status, api.Code)
	}
	// 状态未被动过（跨租户操作不得改行）。
	var dbStatus int
	if err := env.DB.Raw("SELECT status FROM trade_order WHERE id = ?", pending.ID).Row().Scan(&dbStatus); err != nil {
		t.Fatalf("读订单状态: %v", err)
	}
	if int8(dbStatus) != mallmodel.TradeOrderStatusPending {
		t.Fatalf("跨租户操作不得改状态：status=%d", dbStatus)
	}
	// 租户 2 分页不见租户 1 订单。
	status, api = env.Request(t, http.MethodGet, "/mall/trade/order/page?pageNo=1&pageSize=50", token2, nil)
	mustOK(t, "租户 2 订单分页", status, api)
	for _, row := range mallPageList(t, api) {
		if int64(row["id"].(float64)) == pending.ID || int64(row["id"].(float64)) == paid.ID {
			t.Fatalf("租户 2 分页不得见租户 1 订单：%s", api.Data)
		}
	}
}
