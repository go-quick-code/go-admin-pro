//go:build integration

// mall 交易域 HTTP 集成测试（issue #179，T6；docs/mall-mvp-spec.md §3.3/§3.4/
// §4）：
//   - 购物车：加购合并（同 SKU upsert 递增）/数量校验（正整数）/选中切换/
//     删除/我的购物车（SKU 现价与库存展示）；
//   - 结算试算与实付一致（金额三列服务端定：pay_money = item − discount +
//     postage，discount/postage 恒 0 落列）+ 创建订单（收货快照、成交快照、
//     SO 单号）+ 下单扣库存 + 清购物车；
//   - 并发下单不超卖：20 并发同 SKU 库存 10，恰 10 成 10 拒 409，终态库存 0；
//   - 取消回补（库存恢复、状态 30、cancel_time/reason）；
//   - 非法迁移 409：直接构造已支付/已完成/已关闭单（SQL 直改，票面指定手法
//     ——repo 事务不可从测试包触达，同口径用裸 SQL 写状态位）后调 C 端取消；
//   - 越权 404 矩阵：订单/购物车/地址全部带 user_id 谓词（会员 A 拿不到
//     会员 B 的行，不泄露存在性）；
//   - 我的积分流水分页（T2 的 repo 复用，user_id 恒取当前会员）。
//
// 限速桶核对（tokenGroup 60 次/分钟，每用例独立 env 即独立桶）：并发用例
// 恰 40 次令牌端点请求（20 加购 + 20 下单），其余用例均 ≤ 20 次；增删请求
// 时同步核对，超限会以 429 假红。
package mall_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// tradeCreateSaleableSPU 建上架单规格 SPU（含一条空 properties 的 SKU），
// 返回 SPU/SKU ID。MVP 单规格：spec_type=0、单 SKU 空 properties（价格/库存
// 挂该 SKU）。SPU 一并建叶子分类（分类是 SPU 创建的必填归属）。
func tradeCreateSaleableSPU(t *testing.T, env *testutil.IntegrationEnv, adminToken, name, price string, stock int) (int64, int64) {
	t.Helper()
	categoryID := mallCreateCategory(t, env, adminToken, map[string]any{"name": name + "-分类"})
	spu := mallCreateSPU(t, env, adminToken, map[string]any{
		"categoryId": categoryID, "name": name,
		"picUrl":   "https://cdn.example.com/" + name + ".png",
		"specType": 0, "status": 1, // 直接上架（购买边界内的商品）
		"skus": []map[string]any{
			{"properties": []map[string]any{}, "price": price, "stock": stock, "first": true},
		},
	})
	spuID := int64(spu["id"].(float64))
	status, api := env.Request(t, http.MethodGet, "/mall/product/spu/"+itoa(spuID), adminToken, nil)
	mustOK(t, "SPU 详情回读", status, api)
	detail := parseJSON(t, string(api.Data))
	skus := detail["skus"].([]any)
	if len(skus) != 1 {
		t.Fatalf("上架 SPU 应恰 1 条 SKU：%s", api.Data)
	}
	return spuID, int64(skus[0].(map[string]any)["id"].(float64))
}

// tradeSKUStock 读 SKU 现库存（管理端详情回读，购买边界外验证）。
func tradeSKUStock(t *testing.T, env *testutil.IntegrationEnv, adminToken string, spuID int64) int {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/mall/product/spu/"+itoa(spuID), adminToken, nil)
	mustOK(t, "SPU 详情读库存", status, api)
	detail := parseJSON(t, string(api.Data))
	skus := detail["skus"].([]any)
	if len(skus) != 1 {
		t.Fatalf("读库存应恰 1 条 SKU：%s", api.Data)
	}
	return int(skus[0].(map[string]any)["stock"].(float64))
}

// tradeAdminCreateMember 管理端建会员（不设密码；C 端令牌由测试直构签名，
// 绕开注册/登录限速桶——并发用例的会员清单走这里）。
func tradeAdminCreateMember(t *testing.T, env *testutil.IntegrationEnv, adminToken, mobile string) int64 {
	t.Helper()
	member := mallCreateMember(t, env, adminToken, map[string]any{"mobile": mobile, "nickname": "会员-" + mobile})
	return int64(member["id"].(float64))
}

// tradeMemberToken 直构会员令牌（签名合法、tid/mid 自洽、1 小时有效——与
// 登录签发的令牌同规格，见 memberAuth 自校验口径）。
func tradeMemberToken(t *testing.T, env *testutil.IntegrationEnv, memberID int64) string {
	t.Helper()
	return signAppMemberToken(t, env, env.Config.Mall.JWTSecret, memberID, tenant.DefaultID, time.Now().Add(time.Hour))
}

// tradeInsertAddress 直插默认收货地址（C 端地址管理端点不在本票范围——票面
// C 端 API 清单无地址 CRUD，下单只收 addressId；测试经 gorm 模型构造地址行，
// ctx 显式携带默认租户——租户插件的取值源，快照口径由 service 层从
// member_address 读出）。
func tradeInsertAddress(t *testing.T, env *testutil.IntegrationEnv, userID int64, receiver string) int64 {
	t.Helper()
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	row := mallmodel.MemberAddress{
		UserID:         userID,
		ReceiverName:   receiver,
		ReceiverMobile: "13800001234",
		Province:       "广东省",
		City:           "深圳市",
		District:       "南山区",
		Detail:         "科技园 1 号",
		IsDefault:      1,
	}
	if err := env.DB.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("插入地址: %v", err)
	}
	return row.ID
}

// tradeAddCart 加购（断言成功）。
func tradeAddCart(t *testing.T, env *testutil.IntegrationEnv, memberToken string, skuID int64, count int) {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/app/cart", memberToken, map[string]any{"skuId": skuID, "count": count})
	mustOK(t, "加购", status, api)
}

// tradeCreateOrder 创建订单（断言成功，返回 data JSON：id/no/payMoney）。
func tradeCreateOrder(t *testing.T, env *testutil.IntegrationEnv, memberToken string, addressID int64, remark string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/mall/app/trade/order", memberToken, map[string]any{
		"addressId": addressID, "userRemark": remark,
	})
	mustOK(t, "创建订单", status, api)
	return parseJSON(t, string(api.Data))
}

// tradeCartFirstID 取购物车首行 ID（无行时 Fatal）。
func tradeCartFirstID(t *testing.T, env *testutil.IntegrationEnv, memberToken string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodGet, "/mall/app/cart", memberToken, nil)
	mustOK(t, "购物车列表", status, api)
	cart := parseJSON(t, string(api.Data))
	items := cart["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("购物车应非空：%s", api.Data)
	}
	return int64(items[0].(map[string]any)["id"].(float64))
}

// TestIntegrationTradeCartFlow 购物车全流程：加购合并（同 SKU upsert 递增）、
// 数量校验（0/负/超界 400）、选中切换（汇总随之变化）、删除、售卖边界
// （不存在 SKU 404 / 下架 SPU 409）。
func TestIntegrationTradeCartFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	_, skuID := tradeCreateSaleableSPU(t, env, adminToken, "交易购物车商品", "10.00", 50)

	status, api := env.Request(t, http.MethodPost, "/mall/app/auth/register", "", map[string]any{
		"mobile": "13895001001", "password": "secret77", "nickname": "购物车用户",
	})
	mustOK(t, "注册会员", status, api)
	memberToken, _ := appAuth(t, api)

	// 加购合并：2 + 3 = 5（UNIQUE(tenant,user,sku) upsert 递增）。
	tradeAddCart(t, env, memberToken, skuID, 2)
	tradeAddCart(t, env, memberToken, skuID, 3)
	status, api = env.Request(t, http.MethodGet, "/mall/app/cart", memberToken, nil)
	mustOK(t, "购物车列表", status, api)
	cart := parseJSON(t, string(api.Data))
	items := cart["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("同 SKU 加购应合并为 1 行，实际 %d 行", len(items))
	}
	item := items[0].(map[string]any)
	if item["count"] != float64(5) || item["selected"] != float64(1) {
		t.Fatalf("合并后 count=5 selected=1，实际 %s", api.Data)
	}
	// 我的购物车展示 SKU 现价与现库存（spec §4）：price "10.00"、stock 50、
	// subtotal = 10.00 × 5 = "50.00"（Money 十进制字符串）。
	if item["price"] != "10.00" || item["stock"] != float64(50) || item["subtotal"] != "50.00" || !item["onSale"].(bool) {
		t.Fatalf("购物车行应含 SKU 现价/现库存/小计/上架态：%s", api.Data)
	}
	if cart["summary"].(map[string]any)["itemMoney"] != "50.00" {
		t.Fatalf("选中项汇总应为 50.00：%s", api.Data)
	}

	// 数量校验：0/负/超界一律 400（正整数纯函数）。
	cartID := int64(item["id"].(float64))
	for _, count := range []int{0, -1, 1000} {
		status, api = env.Request(t, http.MethodPut, "/mall/app/cart/"+itoa(cartID)+"/count", memberToken, map[string]any{"count": count})
		if status != http.StatusBadRequest {
			t.Fatalf("count=%d 应 400，实际 HTTP %d msg %q", count, status, api.Msg)
		}
	}
	// 改数量为绝对值覆盖。
	status, api = env.Request(t, http.MethodPut, "/mall/app/cart/"+itoa(cartID)+"/count", memberToken, map[string]any{"count": 4})
	mustOK(t, "改数量", status, api)

	// 选中切换：取消选中后汇总清零，恢复后回到 40.00（10.00 × 4）。
	status, api = env.Request(t, http.MethodPut, "/mall/app/cart/"+itoa(cartID)+"/selected", memberToken, map[string]any{"selected": 0})
	mustOK(t, "取消选中", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/app/cart", memberToken, nil)
	cart = parseJSON(t, string(api.Data))
	if cart["summary"].(map[string]any)["itemMoney"] != "0.00" || cart["summary"].(map[string]any)["selectedCount"] != float64(0) {
		t.Fatalf("取消选中后汇总应清零：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodPut, "/mall/app/cart/"+itoa(cartID)+"/selected", memberToken, map[string]any{"selected": 1})
	mustOK(t, "恢复选中", status, api)
	// selected 非法取值 400。
	status, api = env.Request(t, http.MethodPut, "/mall/app/cart/"+itoa(cartID)+"/selected", memberToken, map[string]any{"selected": 2})
	if status != http.StatusBadRequest {
		t.Fatalf("selected=2 应 400，实际 HTTP %d", status)
	}

	// 售卖边界：不存在 SKU 404（不泄露存在性）；下架 SPU 409。
	status, api = env.Request(t, http.MethodPost, "/mall/app/cart", memberToken, map[string]any{"skuId": 999999999, "count": 1})
	if status != http.StatusNotFound {
		t.Fatalf("不存在 SKU 加购应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	offSpuID, offSKUID := tradeCreateSaleableSPU(t, env, adminToken, "下架商品", "3.00", 10)
	status, api = env.Request(t, http.MethodPut, "/mall/product/spu/"+itoa(offSpuID)+"/status", adminToken, map[string]any{"status": 0})
	mustOK(t, "SPU 下架", status, api)
	status, api = env.Request(t, http.MethodPost, "/mall/app/cart", memberToken, map[string]any{"skuId": offSKUID, "count": 1})
	if status != http.StatusConflict || !strings.Contains(api.Msg, "已下架") {
		t.Fatalf("下架 SPU 加购应 409「已下架」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 删除（批量，全或无）。
	status, api = env.Request(t, http.MethodPost, "/mall/app/cart/delete", memberToken, map[string]any{"ids": []int64{cartID}})
	mustOK(t, "删除购物车行", status, api)
	status, api = env.Request(t, http.MethodGet, "/mall/app/cart", memberToken, nil)
	cart = parseJSON(t, string(api.Data))
	if len(cart["items"].([]any)) != 0 {
		t.Fatalf("删除后购物车应为空：%s", api.Data)
	}
	// 删除他人/不存在行 404。
	status, api = env.Request(t, http.MethodPost, "/mall/app/cart/delete", memberToken, map[string]any{"ids": []int64{cartID}})
	if status != http.StatusNotFound {
		t.Fatalf("删除不存在行应 404，实际 HTTP %d", status)
	}
}

// TestIntegrationTradeSettlementAndOrder 结算试算与实付一致 + 创建订单全链路：
// 金额三列服务端定（pay_money = item − discount + postage，discount/postage
// 恒 0）、收货快照、成交快照（名称/价格/规格随单定格）、下单即扣库存、
// SO 单号（当天日期键）、清购物车、状态 0 待支付。
func TestIntegrationTradeSettlementAndOrder(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spu1ID, sku1ID := tradeCreateSaleableSPU(t, env, adminToken, "交易下单商品一", "12.34", 10)
	spu2ID, sku2ID := tradeCreateSaleableSPU(t, env, adminToken, "交易下单商品二", "5.00", 5)

	status, api := env.Request(t, http.MethodPost, "/mall/app/auth/register", "", map[string]any{
		"mobile": "13895002001", "password": "secret77",
	})
	mustOK(t, "注册会员", status, api)
	memberToken, member := appAuth(t, api)
	memberID := int64(member["id"].(float64))
	addressID := tradeInsertAddress(t, env, memberID, "张三")

	// 未选中结算项先试算：400（引导先加购）。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/settlement", memberToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("空选中项试算应 400，实际 HTTP %d msg %q", status, api.Msg)
	}

	tradeAddCart(t, env, memberToken, sku1ID, 2)
	tradeAddCart(t, env, memberToken, sku2ID, 3)

	// 结算试算（不下单）：2×12.34 + 3×5.00 = 24.68 + 15.00 = 39.68；
	// discount/postage 恒 "0.00"；pay = item − 0 + 0 = "39.68"；默认地址带出。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/settlement", memberToken, nil)
	mustOK(t, "结算试算", status, api)
	settle := parseJSON(t, string(api.Data))
	if settle["totalQuantity"] != float64(5) || settle["itemMoney"] != "39.68" ||
		settle["payMoney"] != "39.68" || settle["discountMoney"] != "0.00" || settle["postage"] != "0.00" {
		t.Fatalf("试算金额不符（应 39.68/0/0）： %s", api.Data)
	}
	if len(settle["items"].([]any)) != 2 {
		t.Fatalf("试算应含 2 条明细：%s", api.Data)
	}
	addr := settle["address"].(map[string]any)
	if addr == nil || addr["receiverName"] != "张三" || addr["province"] != "广东省" {
		t.Fatalf("试算应带默认地址：%s", api.Data)
	}

	// 创建订单（收货地址 id + 备注）。
	order := tradeCreateOrder(t, env, memberToken, addressID, "尽快发货")
	orderID := int64(order["id"].(float64))
	orderNo, _ := order["no"].(string)
	if !strings.HasPrefix(orderNo, "SO") || len(orderNo) < 8 {
		t.Fatalf("订单号应 SO 前缀：%s", api.Data)
	}
	if want := "SO" + env.DateKey(); !strings.HasPrefix(orderNo, want) {
		t.Fatalf("订单号应含业务时区日期键 %s：%s", want, orderNo)
	}
	if order["payMoney"] != "39.68" {
		t.Fatalf("实付金额应与试算一致（39.68）：%s", order)
	}

	// 订单详情：状态 0 待支付、收货快照、成交快照（spu_name/price/subtotal
	// 服务端定格）、items 齐。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/"+itoa(orderID), memberToken, nil)
	mustOK(t, "订单详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if detail["status"] != float64(0) || detail["userRemark"] != "尽快发货" {
		t.Fatalf("订单详情状态/备注不符：%s", api.Data)
	}
	if detail["receiverName"] != "张三" || detail["receiverMobile"] != "13800001234" ||
		detail["receiverArea"] != "广东省 深圳市 南山区" || detail["receiverDetail"] != "科技园 1 号" {
		t.Fatalf("收货快照不符：%s", api.Data)
	}
	orderItems := detail["items"].([]any)
	if len(orderItems) != 2 {
		t.Fatalf("订单项应 2 条：%s", api.Data)
	}
	// 快照完整性：spu_name/sku_properties/price/count/subtotal 全带出。
	first := orderItems[0].(map[string]any)
	if first["spuName"] != "交易下单商品一" || first["price"] != "12.34" ||
		first["count"] != float64(2) || first["subtotal"] != "24.68" {
		t.Fatalf("订单项成交快照不符：%s", api.Data)
	}
	if _, ok := first["skuProperties"].([]any); !ok {
		t.Fatalf("订单项应含规格 JSON 快照（单规格为 []）：%s", api.Data)
	}

	// 下单即扣库存：10−2=8、5−3=2。
	if got := tradeSKUStock(t, env, adminToken, spu1ID); got != 8 {
		t.Fatalf("SKU1 库存应为 8，实际 %d", got)
	}
	if got := tradeSKUStock(t, env, adminToken, spu2ID); got != 2 {
		t.Fatalf("SKU2 库存应为 2，实际 %d", got)
	}

	// 已结算的购物车选中项被清掉。
	status, api = env.Request(t, http.MethodGet, "/mall/app/cart", memberToken, nil)
	cart := parseJSON(t, string(api.Data))
	if len(cart["items"].([]any)) != 0 {
		t.Fatalf("下单后购物车应清空：%s", api.Data)
	}

	// 金额不变量落库口径（spec §3.4）：pay_money = item_money − discount_money
	// + postage，item_money = Σ subtotal。
	var itemMoney, discount, postage, payMoney, subtotalSum string
	if err := env.DB.Raw(
		"SELECT o.item_money, o.discount_money, o.postage, o.pay_money, "+
			"(SELECT COALESCE(SUM(subtotal),0) FROM trade_order_item WHERE order_id = o.id) "+
			"FROM trade_order o WHERE o.id = ?", orderID,
	).Row().Scan(&itemMoney, &discount, &postage, &payMoney, &subtotalSum); err != nil {
		t.Fatalf("读订单金额: %v", err)
	}
	if itemMoney != "39.68" || subtotalSum != "39.68" || payMoney != "39.68" || discount != "0.00" || postage != "0.00" {
		t.Fatalf("金额不变量不符：item=%s Σsubtotal=%s pay=%s discount=%s postage=%s",
			itemMoney, subtotalSum, payMoney, discount, postage)
	}

	// 我的订单分页（状态过滤）：status=0 命中该单；status=30 空集；非法值 400。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/page?pageNo=1&pageSize=10&status=0", memberToken, nil)
	mustOK(t, "订单分页", status, api)
	list := mallPageList(t, api)
	if len(list) != 1 || int64(list[0]["id"].(float64)) != orderID {
		t.Fatalf("待支付过滤应命中该单：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/page?pageNo=1&pageSize=10&status=30", memberToken, nil)
	if len(mallPageList(t, api)) != 0 {
		t.Fatalf("已取消过滤应为空集")
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/page?pageNo=1&pageSize=10&status=99", memberToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法状态过滤应 400，实际 HTTP %d", status)
	}
	// 明细口径：分页项不含 items（items 随详情端点）。
	if _, hasItems := list[0]["items"]; hasItems && list[0]["items"] != nil {
		if len(list[0]["items"].([]any)) != 0 {
			t.Fatalf("分页项不应带 items（详情端点才带）：%s", api.Data)
		}
	}

	// 库存不足 409：把 SKU2 库存改到 1，再下单一笔需 2 件的（选中后下单）。
	env.DB.Exec("UPDATE product_sku SET stock = 1 WHERE id = ?", sku1ID)
	tradeAddCart(t, env, memberToken, sku1ID, 2)
	status, api = env.Request(t, http.MethodPost, "/mall/app/trade/order", memberToken, map[string]any{"addressId": addressID})
	if status != http.StatusConflict || !strings.Contains(api.Msg, "库存不足") {
		t.Fatalf("库存不足应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	// 失败回滚不吞库存（扣减与落单同事务）：库存仍 1。
	if got := tradeSKUStock(t, env, adminToken, spu1ID); got != 1 {
		t.Fatalf("库存不足回滚后库存应保持 1，实际 %d", got)
	}
}

// TestIntegrationTradeConcurrentNoOversell 并发下单不超卖（spec §3.4 库存
// 不变量，票面验收）：20 并发同 SKU（库存 10，单价 1.00，各购 1 件），恰
// 10 成 10 拒 409「库存不足」；终态库存 0；订单项总数恰 10。
func TestIntegrationTradeConcurrentNoOversell(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	_, skuID := tradeCreateSaleableSPU(t, env, adminToken, "并发下单商品", "1.00", 10)

	// 20 名会员（管理端建号 + 测试直构令牌：注册/登录限速桶 10 次/分钟装不下
	// 20 人，直构令牌与登录签发同规格）。地址逐一插入（每人一条默认地址）。
	const members = 20
	memberIDs := make([]int64, members)
	addressIDs := make([]int64, members)
	tokens := make([]string, members)
	for i := 0; i < members; i++ {
		memberIDs[i] = tradeAdminCreateMember(t, env, adminToken, fmt.Sprintf("13895003%03d", i))
		addressIDs[i] = tradeInsertAddress(t, env, memberIDs[i], "并发用户")
		tokens[i] = tradeMemberToken(t, env, memberIDs[i])
	}

	// 并发下单：各加购 1 件 + 创建订单。令牌端点恰 40 次请求（桶 60/分钟内）。
	// goroutine 内只采集原始结果（t.Fatalf 只能在测试主 goroutine 调用），
	// 断言统一在 wg.Wait 之后。
	type outcome struct {
		phase  string
		status int
		code   int
		msg    string
		data   string
	}
	outcomes := make([]outcome, members)
	var wg sync.WaitGroup
	for i := 0; i < members; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			st, ap := env.Request(t, http.MethodPost, "/mall/app/cart", tokens[idx], map[string]any{"skuId": skuID, "count": 1})
			if st != http.StatusOK || ap.Code != 0 {
				outcomes[idx] = outcome{phase: "加购", status: st, code: ap.Code, msg: ap.Msg}
				return
			}
			st, ap = env.Request(t, http.MethodPost, "/mall/app/trade/order", tokens[idx], map[string]any{"addressId": addressIDs[idx]})
			outcomes[idx] = outcome{phase: "下单", status: st, code: ap.Code, msg: ap.Msg, data: string(ap.Data)}
		}(i)
	}
	wg.Wait()

	// 恰 10 成 10 拒：拒绝一律 409「库存不足」；成功应含订单号与实付金额。
	created := 0
	createdOrderIDs := make([]int64, 0, members)
	for i, oc := range outcomes {
		if oc.phase == "下单" && oc.status == http.StatusOK && oc.code == 0 {
			created++
			created := parseJSON(t, oc.data)
			createdOrderIDs = append(createdOrderIDs, int64(created["id"].(float64)))
			continue
		}
		if oc.status != http.StatusConflict || !strings.Contains(oc.msg, "库存不足") {
			t.Fatalf("并发下单第 %d 路失败形态不符（应 409 库存不足，phase=%s）：HTTP %d code %d msg %q",
				i, oc.phase, oc.status, oc.code, oc.msg)
		}
	}
	if created != 10 {
		t.Fatalf("库存 10 应恰 10 单成功，实际 %d", created)
	}

	// 终态库存 0（行锁条件 UPDATE 不超卖）+ 订单/订单项计数正确。
	var orderCount, itemCount, stock int
	if err := env.DB.Raw("SELECT COUNT(*) FROM trade_order WHERE id IN ?", createdOrderIDs).Row().Scan(&orderCount); err != nil {
		t.Fatalf("统计订单: %v", err)
	}
	if err := env.DB.Raw("SELECT COUNT(*) FROM trade_order_item WHERE sku_id = ?", skuID).Row().Scan(&itemCount); err != nil {
		t.Fatalf("统计订单项: %v", err)
	}
	if err := env.DB.Raw("SELECT stock FROM product_sku WHERE id = ?", skuID).Row().Scan(&stock); err != nil {
		t.Fatalf("读库存: %v", err)
	}
	if orderCount != 10 {
		t.Fatalf("并发下单应恰 10 笔订单，实际 %d", orderCount)
	}
	if itemCount != 10 {
		t.Fatalf("订单项应恰 10 条（每单 1 条 × count 1），实际 %d", itemCount)
	}
	if stock != 0 {
		t.Fatalf("终态库存应为 0，实际 %d", stock)
	}
}

// TestIntegrationTradeCancelRestoresStock 取消回补：取消待支付订单 → 状态 30
// + cancel_time/cancel_reason 落列 + 库存恢复（同形状 stock + ?）；重复取消
// 409（终态幂等拒绝）。
func TestIntegrationTradeCancelRestoresStock(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "取消回补商品", "2.00", 5)

	status, api := env.Request(t, http.MethodPost, "/mall/app/auth/register", "", map[string]any{
		"mobile": "13895004001", "password": "secret77",
	})
	mustOK(t, "注册会员", status, api)
	memberToken, member := appAuth(t, api)
	memberID := int64(member["id"].(float64))
	addressID := tradeInsertAddress(t, env, memberID, "取消用户")

	tradeAddCart(t, env, memberToken, skuID, 2)
	order := tradeCreateOrder(t, env, memberToken, addressID, "")
	orderID := int64(order["id"].(float64))
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 3 {
		t.Fatalf("下单后库存应为 3，实际 %d", got)
	}

	status, api = env.Request(t, http.MethodPost, "/mall/app/trade/order/"+itoa(orderID)+"/cancel", memberToken, map[string]any{"reason": "不想要了"})
	mustOK(t, "取消订单", status, api)

	// 状态 30 + 取消时间/原因落列 + 库存恢复 5（回补与状态迁移同事务）。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/"+itoa(orderID), memberToken, nil)
	detail := parseJSON(t, string(api.Data))
	if detail["status"] != float64(mallmodel.TradeOrderStatusCanceled) || detail["cancelReason"] != "不想要了" {
		t.Fatalf("取消后状态/原因不符：%s", api.Data)
	}
	if _, ok := detail["cancelTime"].(string); !ok || detail["cancelTime"].(string) == "" {
		t.Fatalf("取消时间应落列：%s", api.Data)
	}
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 5 {
		t.Fatalf("取消回补后库存应恢复 5，实际 %d", got)
	}

	// 重复取消：409（已取消终态）。
	status, api = env.Request(t, http.MethodPost, "/mall/app/trade/order/"+itoa(orderID)+"/cancel", memberToken, map[string]any{"reason": ""})
	if status != http.StatusConflict || !strings.Contains(api.Msg, "已取消") {
		t.Fatalf("重复取消应 409「已取消」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	// 再次回补不会发生（库存仍 5，幂等拒绝不重复回补）。
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 5 {
		t.Fatalf("重复取消不应再次回补，库存应仍 5，实际 %d", got)
	}

	// 我的订单分页 status=30 命中。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/page?pageNo=1&pageSize=10&status=30", memberToken, nil)
	if len(mallPageList(t, api)) != 1 {
		t.Fatalf("已取消过滤应命中该单：%s", api.Data)
	}
}

// TestIntegrationTradeCancelIllegalTransitions 非法迁移 409（票面指定手法：
// 直接构造非待支付单——不用 SQL 直改状态位，测试内经 mallrepo 原语写入
// （库存扣减 + 订单/订单项落库，构造时即为目标态），再调 C 端取消）：
// 已支付(10)/已完成(20)/已关闭(40) 一律 409，状态不变、取消时间不落、
// 库存不回补。
func TestIntegrationTradeCancelIllegalTransitions(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	spuID, skuID := tradeCreateSaleableSPU(t, env, adminToken, "非法迁移商品", "2.00", 10)

	status, api := env.Request(t, http.MethodPost, "/mall/app/auth/register", "", map[string]any{
		"mobile": "13895005001", "password": "secret77",
	})
	mustOK(t, "注册会员", status, api)
	memberToken, member := appAuth(t, api)
	memberID := int64(member["id"].(float64))

	// repo 直构句柄（tenant ctx 显式默认租户——租户插件取值源）；库存扣减走
	// 真实扣减原语（DeductSKUStock），单号走 mall_sequence（同生产发放形状）。
	ctx := tenant.WithTenantID(context.Background(), tenant.DefaultID)
	repo := mallrepo.New(env.DB)
	now := time.Now()
	for _, target := range []struct {
		status int8
		note   string
	}{
		{mallmodel.TradeOrderStatusPaid, "已支付"},
		{mallmodel.TradeOrderStatusCompleted, "已完成"},
		{mallmodel.TradeOrderStatusClosed, "已关闭"},
	} {
		// 每轮：扣库存 1（真实扣减）→ repo 落目标态订单 + 订单项 → C 端取消
		// 409（状态不变、cancel_time 不落、库存不回补）。单号 = SO + 日期键 +
		// mall_sequence 序号（与服务端 formatNo 同形状，测试侧组装）。
		rows, err := repo.DeductSKUStock(ctx, tenant.DefaultID, skuID, 1)
		if err != nil || rows != 1 {
			t.Fatalf("构造 %s 单扣库存: rows=%d err=%v", target.note, rows, err)
		}
		seq, err := repo.NextSequenceValue(ctx, tenant.DefaultID, mallservice.SeqOrder, env.DateKey())
		if err != nil {
			t.Fatalf("发放序列: %v", err)
		}
		order := &mallmodel.TradeOrder{
			TenantID:       tenant.DefaultID,
			No:             "SO" + env.DateKey() + fmt.Sprintf("%04d", seq),
			UserID:         memberID,
			Status:         target.status,
			TotalQuantity:  1,
			ItemMoney:      money.FromCents(200),
			PayMoney:       money.FromCents(200),
			ReceiverName:   "非法迁移用户",
			ReceiverMobile: "13800001234",
			ReceiverArea:   "广东省 深圳市 南山区",
			ReceiverDetail: "科技园 1 号",
		}
		switch target.status {
		case mallmodel.TradeOrderStatusPaid, mallmodel.TradeOrderStatusCompleted:
			order.PayTime = &now
		case mallmodel.TradeOrderStatusClosed:
			order.CloseTime = &now
			order.CloseReason = "超时关闭（测试构造）"
		}
		if err := repo.CreateOrder(ctx, order); err != nil {
			t.Fatalf("构造 %s 单: %v", target.note, err)
		}
		if err := repo.CreateOrderItems(ctx, []mallmodel.TradeOrderItem{{
			TenantID: tenant.DefaultID, OrderID: order.ID, UserID: memberID,
			SPUID: spuID, SKUID: skuID,
			SPUName: "非法迁移商品", SKUProperties: json.RawMessage("[]"),
			Price: money.FromCents(200), Count: 1, Subtotal: money.FromCents(200),
		}}); err != nil {
			t.Fatalf("构造 %s 订单项: %v", target.note, err)
		}
		status, api = env.Request(t, http.MethodPost, "/mall/app/trade/order/"+itoa(order.ID)+"/cancel", memberToken, map[string]any{"reason": "test"})
		// 409 文案按当前状态分型（不可取消 / 无法取消），统一断言
		// 「非待支付一律拒绝」语义：409 + 状态相关文案。
		if status != http.StatusConflict ||
			!(strings.Contains(api.Msg, "不可取消") || strings.Contains(api.Msg, "无法取消")) {
			t.Fatalf("对%s单取消应 409 且状态文案，实际 HTTP %d code %d msg %q", target.note, status, api.Code, api.Msg)
		}
		var dbStatus int
		var cancelTime *time.Time
		if err := env.DB.Raw("SELECT status, cancel_time FROM trade_order WHERE id = ?", order.ID).Row().Scan(&dbStatus, &cancelTime); err != nil {
			t.Fatalf("读订单状态: %v", err)
		}
		if int8(dbStatus) != target.status || cancelTime != nil {
			t.Fatalf("非法取消不得改状态/落取消时间：%s 单 status=%d cancelTime=%v", target.note, dbStatus, cancelTime)
		}
	}
	if got := tradeSKUStock(t, env, adminToken, spuID); got != 7 {
		t.Fatalf("三单各扣 1 后库存应 7，非法取消不回补，实际 %d", got)
	}
}

// TestIntegrationTradeOwnershipMatrix 越权 404 矩阵（spec §4）：会员 A 拿不到
// 会员 B 的订单（详情/取消/分页）/购物车行（改数量/选中/删除）/地址（下单用
// B 的地址 id 404）——全部 404 不泄露存在性；积分流水恒本人视角。
func TestIntegrationTradeOwnershipMatrix(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	adminToken := env.Login(t, "admin", "123456")
	_, skuID := tradeCreateSaleableSPU(t, env, adminToken, "越权矩阵商品", "3.00", 10)

	// 会员 A（注册走 C 端，令牌即签发）：下单一笔。
	status, api := env.Request(t, http.MethodPost, "/mall/app/auth/register", "", map[string]any{
		"mobile": "13895006001", "password": "secret77",
	})
	mustOK(t, "注册会员 A", status, api)
	tokenA, memberA := appAuth(t, api)
	memberAID := int64(memberA["id"].(float64))
	addressA := tradeInsertAddress(t, env, memberAID, "会员A")
	tradeAddCart(t, env, tokenA, skuID, 1)
	orderA := tradeCreateOrder(t, env, tokenA, addressA, "")
	orderAID := int64(orderA["id"].(float64))

	// 会员 B（管理端建号 + 直构令牌）：下单一笔（清掉选中行）+ 再加购一行
	//（留给购物车越权断言）+ 管理员调分。
	memberBID := tradeAdminCreateMember(t, env, adminToken, "13895006002")
	tokenB := tradeMemberToken(t, env, memberBID)
	addressB := tradeInsertAddress(t, env, memberBID, "会员B")
	tradeAddCart(t, env, tokenB, skuID, 1)
	orderB := tradeCreateOrder(t, env, tokenB, addressB, "")
	orderBID := int64(orderB["id"].(float64))
	tradeAddCart(t, env, tokenB, skuID, 1) // 下单清购物车后补一行（未结算）
	cartBID := tradeCartFirstID(t, env, tokenB)
	status, api = env.Request(t, http.MethodPost, "/mall/member/adjust-point", adminToken, map[string]any{"userId": memberBID, "point": 10, "remark": "越权矩阵"})
	mustOK(t, "调整 B 积分", status, api)

	// 订单越权：A 读/取消 B 的单 404；A 分页看不到 B 的单。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/"+itoa(orderBID), tokenA, nil)
	if status != http.StatusNotFound {
		t.Fatalf("A 读 B 订单应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, api = env.Request(t, http.MethodPost, "/mall/app/trade/order/"+itoa(orderBID)+"/cancel", tokenA, map[string]any{"reason": "attack"})
	if status != http.StatusNotFound {
		t.Fatalf("A 取消 B 订单应 404，实际 HTTP %d msg %q", status, api.Msg)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/order/page?pageNo=1&pageSize=50", tokenA, nil)
	list := mallPageList(t, api)
	if len(list) != 1 || int64(list[0]["id"].(float64)) != orderAID {
		t.Fatalf("A 的订单分页应只见自己的 1 单：%s", api.Data)
	}

	// 购物车越权：A 改数量/切换选中/删除 B 的购物车行 404。
	status, api = env.Request(t, http.MethodPut, "/mall/app/cart/"+itoa(cartBID)+"/count", tokenA, map[string]any{"count": 2})
	if status != http.StatusNotFound {
		t.Fatalf("A 改 B 购物车数量应 404，实际 HTTP %d", status)
	}
	status, api = env.Request(t, http.MethodPut, "/mall/app/cart/"+itoa(cartBID)+"/selected", tokenA, map[string]any{"selected": 0})
	if status != http.StatusNotFound {
		t.Fatalf("A 切换 B 购物车选中应 404，实际 HTTP %d", status)
	}
	status, api = env.Request(t, http.MethodPost, "/mall/app/cart/delete", tokenA, map[string]any{"ids": []int64{cartBID}})
	if status != http.StatusNotFound {
		t.Fatalf("A 删除 B 购物车行应 404，实际 HTTP %d", status)
	}

	// 地址越权：A 用 B 的地址下单 404（地址 id 必须属于当前会员）。
	status, api = env.Request(t, http.MethodPost, "/mall/app/trade/order", tokenA, map[string]any{"addressId": addressB})
	if status != http.StatusNotFound || !strings.Contains(api.Msg, "收货地址") {
		t.Fatalf("A 用 B 地址下单应 404「收货地址」，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	// 不存在的地址同样 404。
	status, api = env.Request(t, http.MethodPost, "/mall/app/trade/order", tokenA, map[string]any{"addressId": 999999999})
	if status != http.StatusNotFound {
		t.Fatalf("不存在地址下单应 404，实际 HTTP %d", status)
	}

	// 积分流水恒本人视角：A 列表为空（B 的调整不出现）；B 列表恰 1 条。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/point-records?pageNo=1&pageSize=10", tokenA, nil)
	mustOK(t, "A 积分流水", status, api)
	if len(mallPageList(t, api)) != 0 {
		t.Fatalf("A 的积分流水应为空（B 的调整不越权可见）：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/point-records?pageNo=1&pageSize=10", tokenB, nil)
	mustOK(t, "B 积分流水", status, api)
	bRecords := mallPageList(t, api)
	if len(bRecords) != 1 || int64(bRecords[0]["point"].(float64)) != 10 {
		t.Fatalf("B 的积分流水应恰 1 条 point=10：%s", api.Data)
	}
	// biz_type 过滤非法值 400。
	status, api = env.Request(t, http.MethodGet, "/mall/app/trade/point-records?pageNo=1&pageSize=10&bizType=hack", tokenB, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法 biz_type 过滤应 400，实际 HTTP %d", status)
	}
}
