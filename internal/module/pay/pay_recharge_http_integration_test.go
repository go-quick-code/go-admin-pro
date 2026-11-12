//go:build integration

// pay 充值与套餐域 HTTP/服务级集成测试（issue #136，spec §4.7/§8.2 充值行）：
//   - 套餐 CRUD + 引用禁删 409 + 快照隔离（改套餐不影响已建充值单）；
//   - 发起充值：套餐快照（pay/bonus 服务端取）与自定义（bonus 恒 0）、同事务
//     挂支付订单（RCH-<充值单 ID> 幂等键）、挂单应用缺失 409 引导配置；
//   - 充值禁用钱包渠道 submit 409（含直构 pay_order 的防绕过对照：无充值
//     关联的订单 wallet 渠道照常成功）；
//   - mock 支付成功联动：balance += pay+bonus、total_recharge 累计、
//     pay_status=1；重复回调不重复入账（充值单 pay_status 条件更新认领）；
//   - 充值退款：部分退款 409、余额不足 409 不发起、成功扣回三列回写 +
//     total_recharge 调减 + 订单置 20；mid-flight 余额被并发消费 → 退款 20
//     失败、余额不动、重试成功可覆盖。
package pay_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payservice "github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// rechargeOrderNoRe 支付订单号格式（spec §4.9：ZF + yyyyMMdd + 至少 4 位序号）。
var rechargeOrderNoRe = regexp.MustCompile(`^ZF\d{8}\d{4,}$`)

// ---------- 助手 ----------

// payCreatePackage 创建充值套餐（断言成功返回 ID）。
func payCreatePackage(t *testing.T, env *testutil.IntegrationEnv, token, name, payPrice, bonusPrice string) int64 {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/wallet-recharge-package", token, map[string]any{
		"name": name, "payPrice": payPrice, "bonusPrice": bonusPrice,
	})
	mustOK(t, "创建套餐 "+name, status, api)
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil || row.ID == 0 {
		t.Fatalf("解析套餐响应: %v（data=%s）", err, api.Data)
	}
	return row.ID
}

// payCreateRechargeRaw 发起充值（返回原始 HTTP 状态与信封供 4xx 断言）。
func payCreateRechargeRaw(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) (int, testutil.APIResponse) {
	t.Helper()
	return env.Request(t, http.MethodPost, "/pay/wallet-recharge/create", token, body)
}

// payCreateRecharge 发起充值（断言成功），返回响应 JSON。
func payCreateRecharge(t *testing.T, env *testutil.IntegrationEnv, token string, body map[string]any) map[string]any {
	t.Helper()
	status, api := payCreateRechargeRaw(t, env, token, body)
	mustOK(t, "发起充值", status, api)
	resp := parseJSON(t, string(api.Data))
	if resp["recharge"] == nil || resp["appKey"] == "" {
		t.Fatalf("充值响应缺少 recharge/appKey：%s", api.Data)
	}
	return resp
}

// rechargeID 提取充值响应里的充值单 ID。
func rechargeID(t *testing.T, resp map[string]any) int64 {
	t.Helper()
	recharge := resp["recharge"].(map[string]any)
	return int64(recharge["id"].(float64))
}

// rechargeDBRow 读充值单 DB 行（支付/退款回执列）。
func rechargeDBRow(t *testing.T, env *testutil.IntegrationEnv, id int64) struct {
	PayStatus        int8
	PayOrderID       int64
	PayChannelCode   string
	PayTime          *string
	RefundStatus     int8
	PayRefundID      int64
	RefundTotalPrice string
	RefundPayPrice   string
	RefundBonusPrice string
	RefundTime       *string
	TotalPrice       string
	PayPrice         string
	BonusPrice       string
} {
	t.Helper()
	var row struct {
		PayStatus        int8
		PayOrderID       int64
		PayChannelCode   string
		PayTime          *string
		RefundStatus     int8
		PayRefundID      int64
		RefundTotalPrice string
		RefundPayPrice   string
		RefundBonusPrice string
		RefundTime       *string
		TotalPrice       string
		PayPrice         string
		BonusPrice       string
	}
	if err := env.DB.Raw(
		"SELECT pay_status, pay_order_id, pay_channel_code, pay_time, refund_status, pay_refund_id, "+
			"refund_total_price, refund_pay_price, refund_bonus_price, refund_time, "+
			"total_price, pay_price, bonus_price "+
			"FROM pay_wallet_recharge WHERE id = ?", id,
	).Scan(&row).Error; err != nil {
		t.Fatalf("读充值单 %d: %v", id, err)
	}
	return row
}

// rechargeRefundCount 统计指定消费方退款单号的退款行数（0 = 未发起）。
func rechargeRefundCount(t *testing.T, env *testutil.IntegrationEnv, merchantRefundID string) int64 {
	t.Helper()
	var count int64
	if err := env.DB.Raw("SELECT COUNT(*) FROM pay_refund WHERE merchant_refund_id = ?", merchantRefundID).Scan(&count).Error; err != nil {
		t.Fatalf("统计退款单: %v", err)
	}
	return count
}

// rechargeSeedApp 准备充值挂单应用（app_key=demo，绑定 mock/wallet——照 spec
// §7 演示应用的绑定；缺省挂单应用解析见 service 层决策）。
func rechargeSeedApp(t *testing.T, env *testutil.IntegrationEnv, token string) payApp {
	t.Helper()
	return payCreateRefundApp(t, env, token, "demo", []string{"mock", "wallet"})
}

// rechargeInsertPendingRefund 直造「已发起待回调」的退款单（mid-flight 场景：
// 模拟异步渠道 create 后回调前，余额被并发消费），返回退款单 ID。
func rechargeInsertPendingRefund(t *testing.T, env *testutil.IntegrationEnv, appID, orderID int64, orderNo, merchantOrderID string) int64 {
	t.Helper()
	res := env.DB.Exec(
		"INSERT INTO pay_refund (tenant_id, no, app_id, order_id, order_no, merchant_order_id, merchant_refund_id, "+
			"user_id, user_type, channel_code, notify_url, status, pay_price, refund_price, reason, "+
			"created_at, updated_at, creator, updater) "+
			"VALUES (1, 'TKMIDFLIGHT0001', ?, ?, ?, ?, 'RFR-MIDFLIGHT', "+
			"1, 2, 'mock', '', 0, 80.00, 80.00, 'mid-flight 测试', NOW(), NOW(), '0', '0')",
		appID, orderID, orderNo, merchantOrderID,
	)
	if res.Error != nil {
		t.Fatalf("直插待回调退款单: %v", res.Error)
	}
	var id int64
	if err := env.DB.Raw("SELECT id FROM pay_refund WHERE merchant_refund_id = 'RFR-MIDFLIGHT'").Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("回读退款单 ID: %v", err)
	}
	return id
}

// rechargeMockPay 把充值订单 mock 支付到成功（submit + 模拟成功回调）。
func rechargeMockPay(t *testing.T, env *testutil.IntegrationEnv, token string, orderID int64) {
	t.Helper()
	payMockPayOrder(t, env, token, orderID)
}

// ---------- 套餐 CRUD + 引用禁删 + 快照隔离 ----------

// TestIntegrationPayRechargePackageCRUD 套餐 CRUD（spec §5/§4.10）：
// 金额校验 409（payPrice ≤ 0 / bonus < 0）、page/get/update、未引用可删、
// 被充值单引用禁删 409（引用计数 Unscoped）、普通角色 403。
func TestIntegrationPayRechargePackageCRUD(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")

	// 金额校验。
	status, api := env.Request(t, http.MethodPost, "/pay/wallet-recharge-package", admin, map[string]any{
		"name": "零元档", "payPrice": "0.00", "bonusPrice": "1.00",
	})
	if status != http.StatusConflict {
		t.Fatalf("payPrice=0 应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = env.Request(t, http.MethodPost, "/pay/wallet-recharge-package", admin, map[string]any{
		"name": "负赠送", "payPrice": "10.00", "bonusPrice": "-1.00",
	})
	if status != http.StatusConflict {
		t.Fatalf("bonusPrice<0 应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 正常创建 + 查询。
	pkgID := payCreatePackage(t, env, admin, "100元档", "100.00", "20.00")
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/wallet-recharge-package/%d", pkgID), admin, nil)
	mustOK(t, "套餐详情", status, api)
	row := parseJSON(t, string(api.Data))
	if row["name"] != "100元档" || row["payPrice"] != "100.00" || row["bonusPrice"] != "20.00" {
		t.Fatalf("套餐字段异常：%s", api.Data)
	}
	if row["status"].(float64) != 0 {
		t.Fatalf("新套餐应启用 0，实际 %v", row["status"])
	}

	// 修改档位（改价只影响后续建单）。
	status, api = env.Request(t, http.MethodPut, "/pay/wallet-recharge-package", admin, map[string]any{
		"id": pkgID, "name": "100元档", "payPrice": "120.00", "bonusPrice": "30.00",
	})
	mustOK(t, "修改套餐", status, api)

	// 自助清单只含启用档位（登录即可，demo 也能查）。
	status, api = env.Request(t, http.MethodGet, "/pay/wallet-recharge-package/list", demo, nil)
	mustOK(t, "自助套餐清单", status, api)
	var options []map[string]any
	if err := json.Unmarshal(api.Data, &options); err != nil || len(options) != 1 {
		t.Fatalf("自助清单应 1 个启用套餐（demo 可读），实际 %s（err=%v）", api.Data, err)
	}

	// 管理端权限：普通角色 403（page/create）。
	status, _ = env.Request(t, http.MethodGet, "/pay/wallet-recharge-package/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色查套餐分页应 403，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/pay/wallet-recharge-package", demo, map[string]any{
		"name": "越权", "payPrice": "1.00",
	})
	if status != http.StatusForbidden {
		t.Fatalf("普通角色建套餐应 403，实际 HTTP %d", status)
	}

	// 未引用可删（软删）：再建一个无引用套餐删除成功。
	freePkg := payCreatePackage(t, env, admin, "自由档", "1.00", "0.00")
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/wallet-recharge-package/%d", freePkg), admin, nil)
	mustOK(t, "删除未引用套餐", status, api)
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/wallet-recharge-package/%d", freePkg), admin, nil)
	if status != http.StatusNotFound {
		t.Fatalf("删除后详情应 404，实际 HTTP %d code %d", status, api.Code)
	}

	// 被充值单引用禁删（409，spec §4.10）：先建挂单应用与充值单引用 pkgID。
	rechargeSeedApp(t, env, admin)
	payCreateRecharge(t, env, admin, map[string]any{"packageId": pkgID})
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/wallet-recharge-package/%d", pkgID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("被充值单引用删套餐应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
}

// ---------- 发起充值（套餐快照 / 自定义）+ 挂单 + 页面 ----------

// TestIntegrationPayRechargeCreate 发起充值（spec §4.7）：挂单应用缺失 409
// 引导配置；套餐快照（pay/bonus 服务端取、请求不收金额）与自定义（bonus 恒 0）；
// 同事务挂 pay_order（subject=钱包充值、本人 user_id/user_type=2、
// merchant_order_id=RCH-<充值单 ID>、expire_time 服务端设定）；我的充值/管理
// 分页与归属过滤；套餐快照隔离（改套餐不影响已建充值单）。
func TestIntegrationPayRechargeCreate(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")

	// 挂单应用缺失：409 引导配置（缺省 recharge-app-key=demo）。
	status, api := payCreateRechargeRaw(t, env, admin, map[string]any{"payPrice": "10.00"})
	if status != http.StatusConflict {
		t.Fatalf("挂单应用缺失应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 准备：挂单应用 + 套餐。
	rechargeSeedApp(t, env, admin)
	pkgID := payCreatePackage(t, env, admin, "充值 100 送 20", "100.00", "20.00")

	// 套餐充值：金额服务端取（payPrice 提交被拒绝——请求不收金额）。
	status, api = payCreateRechargeRaw(t, env, admin, map[string]any{"packageId": pkgID, "payPrice": "1.00"})
	if status != http.StatusBadRequest {
		t.Fatalf("套餐充值提交 payPrice 应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	status, api = payCreateRechargeRaw(t, env, admin, map[string]any{})
	if status != http.StatusBadRequest {
		t.Fatalf("无套餐无金额应 400，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	resp := payCreateRecharge(t, env, admin, map[string]any{"packageId": pkgID})
	recharge := resp["recharge"].(map[string]any)
	rid := rechargeID(t, resp)
	if recharge["totalPrice"] != "120.00" || recharge["payPrice"] != "100.00" || recharge["bonusPrice"] != "20.00" {
		t.Fatalf("套餐快照金额异常：%s", api.Data)
	}
	if int64(recharge["packageId"].(float64)) != pkgID {
		t.Fatalf("packageId 应快照 %d，实际 %v", pkgID, recharge["packageId"])
	}
	if recharge["payStatus"].(float64) != 0 {
		t.Fatalf("新充值单应未支付 0，实际 %v", recharge["payStatus"])
	}
	wantMerchantID := fmt.Sprintf("RCH-%d", rid)
	if resp["merchantOrderId"] != wantMerchantID {
		t.Fatalf("merchant_order_id 应为 %s，实际 %v", wantMerchantID, resp["merchantOrderId"])
	}
	if resp["appKey"] != "demo" {
		t.Fatalf("appKey 应为挂单应用 demo，实际 %v", resp["appKey"])
	}
	orderID := int64(resp["orderId"].(float64))
	if no, _ := resp["orderNo"].(string); !rechargeOrderNoRe.MatchString(no) {
		t.Fatalf("挂单号 %v 应匹配 ZF 编号规则", resp["orderNo"])
	}

	// 挂单快照：price=实付档位（赠送不进订单）、subject=钱包充值、本人归属、
	// expire_time 服务端设定。
	var orderRow struct {
		AppID           int64
		Price           string
		Subject         string
		UserID          int64
		UserType        int8
		MerchantOrderID string
		Status          int8
		ExpireTime      *string
	}
	if err := env.DB.Raw(
		"SELECT app_id, price, subject, user_id, user_type, merchant_order_id, status, expire_time FROM pay_order WHERE id = ?", orderID,
	).Scan(&orderRow).Error; err != nil {
		t.Fatalf("读挂单: %v", err)
	}
	if orderRow.Price != "100.00" || orderRow.Subject != "钱包充值" || orderRow.UserID != 1 || orderRow.UserType != 2 {
		t.Fatalf("挂单快照异常：%+v", orderRow)
	}
	if orderRow.MerchantOrderID != wantMerchantID || orderRow.Status != 0 || orderRow.ExpireTime == nil {
		t.Fatalf("挂单幂等键/状态/过期时间异常：%+v", orderRow)
	}

	// 自定义充值：bonus_price 恒 0、package_id NULL。
	custom := payCreateRecharge(t, env, admin, map[string]any{"payPrice": "50.00"})
	customRow := custom["recharge"].(map[string]any)
	if customRow["bonusPrice"] != "0.00" || customRow["totalPrice"] != "50.00" {
		t.Fatalf("自定义充值金额异常：%v", customRow)
	}
	if customRow["packageId"] != nil {
		t.Fatalf("自定义充值 package_id 应为空，实际 %v", customRow["packageId"])
	}

	// 套餐快照隔离：改套餐档位，已建充值单不变（spec 验收）。
	status, api = env.Request(t, http.MethodPut, "/pay/wallet-recharge-package", admin, map[string]any{
		"id": pkgID, "name": "充值 100 送 20", "payPrice": "999.00", "bonusPrice": "0.00",
	})
	mustOK(t, "改套餐档位", status, api)
	row := rechargeDBRow(t, env, rid)
	if row.PayPrice != "100.00" || row.BonusPrice != "20.00" || row.TotalPrice != "120.00" {
		t.Fatalf("改套餐后已建充值单应保持快照：%+v", row)
	}

	// 我的充值：admin 2 笔（套餐 + 自定义），demo 0 笔（归属过滤）。
	status, api = env.Request(t, http.MethodGet, "/pay/wallet-recharge/my?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "我的充值 admin", status, api)
	var adminPage struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &adminPage); err != nil || adminPage.Total != 2 {
		t.Fatalf("admin 我的充值应 2 笔，实际 %s（err=%v）", api.Data, err)
	}
	// 视图回填：appKey/orderNo/merchantOrderId 供退款与收银台跳转。
	var myRows struct {
		List []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &myRows); err != nil || len(myRows.List) != 2 {
		t.Fatalf("我的充值列表应 2 行：%s", api.Data)
	}
	first := myRows.List[0]
	if first["appKey"] != "demo" || first["merchantOrderId"] == nil || first["orderNo"] == nil {
		t.Fatalf("我的充值视图缺 appKey/orderNo/merchantOrderId：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/pay/wallet-recharge/my?pageNo=1&pageSize=10", demo, nil)
	mustOK(t, "我的充值 demo", status, api)
	var demoPage struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &demoPage); err != nil || demoPage.Total != 0 {
		t.Fatalf("demo 我的充值应 0 笔（归属过滤），实际 %s（err=%v）", api.Data, err)
	}

	// 管理端分页：admin 可见 2 笔；普通角色 403。
	status, api = env.Request(t, http.MethodGet, "/pay/wallet-recharge/page?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "充值单管理分页", status, api)
	var mgmtPage struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(api.Data, &mgmtPage); err != nil || mgmtPage.Total != 2 {
		t.Fatalf("管理分页应 2 笔，实际 %s（err=%v）", api.Data, err)
	}
	status, _ = env.Request(t, http.MethodGet, "/pay/wallet-recharge/page?pageNo=1&pageSize=10", demo, nil)
	if status != http.StatusForbidden {
		t.Fatalf("普通角色查充值单分页应 403，实际 HTTP %d", status)
	}
}

// ---------- 充值禁用钱包渠道（submit 反查，防自引用套利） ----------

// TestIntegrationPayRechargeWalletChannelGuard 充值订单 submit wallet 409
// （spec §4.7/§8.2）：反查 pay_wallet_recharge.pay_order_id 校验，与余额无关
// （钱包余额充足同样 409）；防绕过——直接构造 pay_order（不经充值创建）挂上
// 充值关联同样 409；对照：无充值关联的订单 wallet 渠道照常扣减成功。
func TestIntegrationPayRechargeWalletChannelGuard(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")

	// 钱包余额充足（排除余额不足干扰：guard 在渠道扣减之前）。
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "余额准备", Price: money.FromCents(100_000),
	}); err != nil {
		t.Fatalf("余额准备: %v", err)
	}

	rechargeSeedApp(t, env, admin)

	// 充值订单：submit wallet → 409。
	recharge := payCreateRecharge(t, env, admin, map[string]any{"payPrice": "50.00"})
	rechargeOrderID := int64(recharge["orderId"].(float64))
	status, api := paySubmit(t, env, admin, rechargeOrderID, "wallet")
	if status != http.StatusConflict {
		t.Fatalf("充值订单 wallet submit 应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	for _, want := range []string{"充值订单", "钱包渠道"} {
		if !strings.Contains(api.Msg, want) {
			t.Fatalf("409 文案 %q 应含 %q", api.Msg, want)
		}
	}
	// 充值订单 mock 渠道不受限（可正常发起支付）。
	status, api = paySubmit(t, env, admin, rechargeOrderID, "mock")
	mustOK(t, "充值订单 mock submit", status, api)

	// 防绕过对照：直接构造 pay_order（POST /pay/order/create，不经充值创建）
	// + 直插充值关联行 → wallet submit 仍 409（校验走反查，不看订单来源）。
	bypass := payCreateOrder(t, env, admin, "demo", "MO-BYPASS-1", "30.00")
	bypassOrderID := payOrderID(t, bypass)
	if err := env.DB.Exec(
		"INSERT INTO pay_wallet_recharge (tenant_id, wallet_id, total_price, pay_price, bonus_price, "+
			"package_id, pay_status, pay_order_id, pay_channel_code, refund_status, pay_refund_id, "+
			"refund_total_price, refund_pay_price, refund_bonus_price, created_at, updated_at, creator, updater) "+
			"SELECT 1, id, 30.00, 30.00, 0.00, NULL, 0, ?, '', 0, 0, 0.00, 0.00, 0.00, NOW(), NOW(), '0', '0' "+
			"FROM pay_wallet WHERE tenant_id = 1 AND user_id = 1 AND user_type = 2",
		bypassOrderID,
	).Error; err != nil {
		t.Fatalf("直插充值关联行: %v", err)
	}
	status, api = paySubmit(t, env, admin, bypassOrderID, "wallet")
	if status != http.StatusConflict {
		t.Fatalf("直构 pay_order 挂充值关联后 wallet submit 仍应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}

	// 对照：无充值关联的订单 wallet 渠道照常成功（余额 1000.00 - 5.00）。
	normal := payCreateOrder(t, env, admin, "demo", "MO-NORMAL-1", "5.00")
	status, api = paySubmit(t, env, admin, payOrderID(t, normal), "wallet")
	mustOK(t, "普通订单 wallet submit", status, api)
	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "995.00")
}

// ---------- 支付成功联动（balance/total_recharge 入账 + 幂等） ----------

// TestIntegrationPayRechargeMockPaySuccess mock 支付成功联动（spec §4.7/§8.2）：
// balance += pay+bonus（biz=1）、total_recharge 累计、充值单 pay_status=1/
// pay_time/pay_channel_code 回执；HTTP 重复触发 409（extension 消费完）；
// 重复回调（重置订单+新 extension 直构并发窗）不重复入账——充值单 pay_status
// 条件更新是入账认领。
func TestIntegrationPayRechargeMockPaySuccess(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	rechargeSeedApp(t, env, admin)

	resp := payCreateRecharge(t, env, admin, map[string]any{"packageId": payCreatePackage(t, env, admin, "充值 80 送 20", "80.00", "20.00")})
	orderID := int64(resp["orderId"].(float64))
	rid := rechargeID(t, resp)

	rechargeMockPay(t, env, admin, orderID)

	// 钱包入账：balance 100.00、total_recharge 100.00（赠送计入）。
	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "100.00")
	walletAmount(t, row, "totalRecharge", "100.00")

	// 充值单回执。
	dbRow := rechargeDBRow(t, env, rid)
	if dbRow.PayStatus != 1 || dbRow.PayChannelCode != "mock" || dbRow.PayTime == nil {
		t.Fatalf("充值单支付回执异常：%+v", dbRow)
	}
	payTimeBefore := *dbRow.PayTime

	// 流水：恰 1 笔 biz=1，price=+100.00，biz_id=充值单 ID。
	flows := payWalletFlows(t, env, admin, "1")
	if len(flows) != 1 {
		t.Fatalf("充值入账应恰 1 笔 biz=1 流水，实际 %d", len(flows))
	}
	if flows[0]["price"] != "100.00" || int64(flows[0]["bizId"].(float64)) != rid {
		t.Fatalf("入账流水金额/bizId 异常：%v", flows[0])
	}

	// HTTP 重复触发：extension 已消费，409 拒绝（余额不变）。
	status, _ := env.Request(t, http.MethodPost, "/pay/mock/notify", admin, map[string]any{"orderId": orderID, "result": "success"})
	if status != http.StatusConflict {
		t.Fatalf("无处理中 extension 的重复 mock 触发应 409，实际 HTTP %d", status)
	}

	// 重复回调直构（模拟渠道重复推送与入账并发窗）：重置订单状态 + 新建
	// 处理中 extension → mock 触发再走 paymentSuccessTx——订单认领成功但
	// 充值单 pay_status 条件更新 0 行 → 幂等跳过入账。
	if err := env.DB.Exec("UPDATE pay_order SET status = 0 WHERE id = ?", orderID).Error; err != nil {
		t.Fatalf("重置订单状态: %v", err)
	}
	if err := env.DB.Exec(
		"INSERT INTO pay_order_extension (tenant_id, no, order_id, channel_code, status, "+
			"created_at, updated_at, creator, updater) VALUES (1, CONCAT((SELECT no FROM pay_order WHERE id = ?), '-9'), ?, 'mock', 0, NOW(), NOW(), '0', '0')",
		orderID, orderID,
	).Error; err != nil {
		t.Fatalf("直构处理中 extension: %v", err)
	}
	status, api := env.Request(t, http.MethodPost, "/pay/mock/notify", admin, map[string]any{"orderId": orderID, "result": "success"})
	mustOK(t, "重复回调触发", status, api)

	row = findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "100.00")
	walletAmount(t, row, "totalRecharge", "100.00")
	if flows := payWalletFlows(t, env, admin, "1"); len(flows) != 1 {
		t.Fatalf("重复回调不应重复入账（biz=1 仍 1 笔），实际 %d", len(flows))
	}
	dbRow = rechargeDBRow(t, env, rid)
	if dbRow.PayTime == nil || *dbRow.PayTime != payTimeBefore {
		t.Fatalf("重复回调不应改写支付回执：%+v vs %s", dbRow.PayTime, payTimeBefore)
	}
}

// ---------- 充值退款（发起守卫 + 成功扣回 + mid-flight 失败） ----------

// TestIntegrationPayRechargeRefundGuards 充值退款发起守卫（spec §4.7/§8.2）：
// 部分退款 409（仅全额）；余额不足 409 不发起（不落退款单）。
func TestIntegrationPayRechargeRefundGuards(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	rechargeSeedApp(t, env, admin)

	resp := payCreateRecharge(t, env, admin, map[string]any{"packageId": payCreatePackage(t, env, admin, "充值 80 送 20", "80.00", "20.00")})
	orderID := int64(resp["orderId"].(float64))
	rechargeMockPay(t, env, admin, orderID)
	merchantOrderID := resp["merchantOrderId"].(string)

	// 部分退款 409（充值单 refund_* 三列是单次全额回写，无部分退款累计）。
	status, api := payRefund(t, env, admin, "demo", merchantOrderID, "RFR-PARTIAL", "40.00")
	if status != http.StatusConflict {
		t.Fatalf("充值部分退款应 409，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	if rechargeRefundCount(t, env, "RFR-PARTIAL") != 0 {
		t.Fatalf("部分退款 409 不应落退款单")
	}

	// 余额不足 409 不发起（先校验后退款：扣回总额 = pay + bonus = 100 > 余额 30）。
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizPayment, Title: "消费到余额不足", Price: money.FromCents(-7_000),
	}); err != nil {
		t.Fatalf("消费扣减: %v", err)
	}
	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "30.00")

	status, api = payRefund(t, env, admin, "demo", merchantOrderID, "RFR-SHORT", "80.00")
	if status != http.StatusConflict {
		t.Fatalf("余额不足充值退款应 409 不发起，实际 HTTP %d code %d msg %q", status, api.Code, api.Msg)
	}
	for _, want := range []string{"余额不足", "100.00"} {
		if !strings.Contains(api.Msg, want) {
			t.Fatalf("409 文案 %q 应含 %q（扣回总额）", api.Msg, want)
		}
	}
	if rechargeRefundCount(t, env, "RFR-SHORT") != 0 {
		t.Fatalf("余额不足 409 不应落退款单（spec：不发起）")
	}
}

// TestIntegrationPayRechargeRefundSuccess 充值退款成功（spec §4.7/§8.2）：
// 扣回 refund_total_price（pay + bonus 全额，biz=2）、refund_* 三列回写、
// pay_refund_id/refund_status=10、total_recharge 调减、订单全额置 20。
func TestIntegrationPayRechargeRefundSuccess(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	rechargeSeedApp(t, env, admin)
	pkgID := payCreatePackage(t, env, admin, "充值 80 送 20", "80.00", "20.00")

	resp := payCreateRecharge(t, env, admin, map[string]any{"packageId": pkgID})
	orderID := int64(resp["orderId"].(float64))
	rid := rechargeID(t, resp)
	rechargeMockPay(t, env, admin, orderID)

	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "100.00")
	walletAmount(t, row, "totalRecharge", "100.00")

	// 全额退款（refundPrice = 订单实付 80；扣回 80 + 20 = 100）。
	status, api := payRefund(t, env, admin, "demo", resp["merchantOrderId"].(string), "RFR-OK-1", "80.00")
	mustOK(t, "充值全额退款", status, api)
	refund := parseJSON(t, string(api.Data))
	refundID := int64(refund["id"].(float64))
	if refund["status"].(float64) != float64(model.RefundStatusSuccess) {
		t.Fatalf("mock 退款应同步成功 10，实际 %v", refund["status"])
	}

	// 钱包：余额 100 - 100 = 0，total_recharge 100 - 100 = 0（biz=2 调减）。
	row = findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "0.00")
	walletAmount(t, row, "totalRecharge", "0.00")
	flows := payWalletFlows(t, env, admin, "2")
	if len(flows) != 1 || flows[0]["price"] != "-100.00" || int64(flows[0]["bizId"].(float64)) != refundID {
		t.Fatalf("充值退款流水异常：%v（期望 biz=2 price=-100.00）", flows)
	}

	// 充值单三列回写 + pay_refund_id + refund_status=10 + refund_time。
	dbRow := rechargeDBRow(t, env, rid)
	if dbRow.RefundStatus != 10 || dbRow.PayRefundID != refundID || dbRow.RefundTime == nil {
		t.Fatalf("充值单退款回执异常：%+v", dbRow)
	}
	if dbRow.RefundTotalPrice != "100.00" || dbRow.RefundPayPrice != "80.00" || dbRow.RefundBonusPrice != "20.00" {
		t.Fatalf("refund_* 三列异常：%+v", dbRow)
	}

	// 订单全额退款置 20。
	var orderStatus int8
	if err := env.DB.Raw("SELECT status FROM pay_order WHERE id = ?", orderID).Scan(&orderStatus).Error; err != nil {
		t.Fatalf("读订单状态: %v", err)
	}
	if orderStatus != model.OrderStatusRefunded {
		t.Fatalf("全额退款后订单应 20，实际 %d", orderStatus)
	}
}

// TestIntegrationPayRechargeRefundMidFlight mid-flight 余额不足（spec §4.7
// 先校验后退款）：发起后余额被并发消费 → 退款成功回调发现扣后 < 0 → 退款单
// 20 失败、余额不动、订单不累计、充值单记失败态；补足余额后重试成功可覆盖
// （refund_status 20→10，三列回写）。
func TestIntegrationPayRechargeRefundMidFlight(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	app := rechargeSeedApp(t, env, admin)
	pkgID := payCreatePackage(t, env, admin, "充值 80 送 20", "80.00", "20.00")

	resp := payCreateRecharge(t, env, admin, map[string]any{"packageId": pkgID})
	orderID := int64(resp["orderId"].(float64))
	rid := rechargeID(t, resp)
	rechargeMockPay(t, env, admin, orderID)

	// 直造「已发起待回调」退款单（status=0，模拟异步渠道 create 后回调前），
	// 随后余额被并发消费（100 → 0）。
	refundID := rechargeInsertPendingRefund(t, env, app.ID, orderID, resp["orderNo"].(string), resp["merchantOrderId"].(string))
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizPayment, Title: "回调前消费", Price: money.FromCents(-10_000),
	}); err != nil {
		t.Fatalf("回调前消费: %v", err)
	}

	// 退款成功回调处理：余额 0 < 扣回总额 100 → 退款 20 失败。
	claimed, err := env.App.Pay.ProcessChannelRefundSuccess(walletCtx(1, 1), refundID, "MOCK-RFR-MID", `{"result":"success"}`)
	if err != nil {
		t.Fatalf("mid-flight 回调处理: %v", err)
	}
	if !claimed {
		t.Fatalf("mid-flight 失败认领应成功（首触）")
	}

	refundRow := payRefundRow(t, env, refundID)
	if refundRow.Status != model.RefundStatusFail {
		t.Fatalf("mid-flight 退款应 20 失败，实际 %d（err=%s）", refundRow.Status, refundRow.ErrorMsg)
	}
	if !strings.Contains(refundRow.ErrorMsg, "余额不足") {
		t.Fatalf("失败文案应含余额不足：%q", refundRow.ErrorMsg)
	}
	// 余额/累计不动：balance 0.00、total_recharge 100.00；biz=2 无流水。
	row := findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "0.00")
	walletAmount(t, row, "totalRecharge", "100.00")
	if flows := payWalletFlows(t, env, admin, "2"); len(flows) != 0 {
		t.Fatalf("失败退款不应产生充值退款流水，实际 %d", len(flows))
	}
	// 订单不累计（保持 10）、充值单记失败态（refund_status=20，金额列不动）。
	var orderStatus int8
	if err := env.DB.Raw("SELECT status FROM pay_order WHERE id = ?", orderID).Scan(&orderStatus).Error; err != nil {
		t.Fatalf("读订单状态: %v", err)
	}
	if orderStatus != model.OrderStatusSuccess {
		t.Fatalf("mid-flight 失败订单应保持 10，实际 %d", orderStatus)
	}
	dbRow := rechargeDBRow(t, env, rid)
	if dbRow.RefundStatus != 20 || dbRow.RefundTotalPrice != "0.00" {
		t.Fatalf("mid-flight 充值单应记失败态且金额列不动：%+v", dbRow)
	}
	// 失败路径无退款回调任务（type=2）。
	var tasks int64
	if err := env.DB.Raw("SELECT COUNT(*) FROM pay_notify_task WHERE type = 2 AND data_id = ?", refundID).Scan(&tasks).Error; err != nil {
		t.Fatalf("统计退款回调任务: %v", err)
	}
	if tasks != 0 {
		t.Fatalf("失败退款不应建回调任务，实际 %d", tasks)
	}

	// 重复回调幂等：再处理一次 → 认领 0 行 → 退款单保持 20。
	claimed, err = env.App.Pay.ProcessChannelRefundSuccess(walletCtx(1, 1), refundID, "MOCK-RFR-MID", `{"result":"success"}`)
	if err != nil || claimed {
		t.Fatalf("重复回调应幂等返回（claimed=false），实际 claimed=%v err=%v", claimed, err)
	}

	// 补足余额后重试：新退款单 → 成功覆盖失败态（refund_status 20→10，
	// 三列回写、扣回 100、total_recharge 调减）。
	if _, err := applyWallet(env, 1, 1, payservice.WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizRecharge, Title: "补足余额", Price: money.FromCents(10_000),
	}); err != nil {
		t.Fatalf("补足余额: %v", err)
	}
	status, api := payRefund(t, env, admin, "demo", resp["merchantOrderId"].(string), "RFR-RETRY-1", "80.00")
	mustOK(t, "重试充值退款", status, api)

	row = findWalletByUser(t, env, admin, 1)
	walletAmount(t, row, "balance", "0.00")
	walletAmount(t, row, "totalRecharge", "100.00")
	dbRow = rechargeDBRow(t, env, rid)
	if dbRow.RefundStatus != 10 || dbRow.RefundTotalPrice != "100.00" || dbRow.RefundPayPrice != "80.00" || dbRow.RefundBonusPrice != "20.00" {
		t.Fatalf("重试成功后充值单三列应回写：%+v", dbRow)
	}
	if flows := payWalletFlows(t, env, admin, "2"); len(flows) != 1 {
		t.Fatalf("重试成功应恰 1 笔充值退款流水，实际 %d", len(flows))
	}
}
