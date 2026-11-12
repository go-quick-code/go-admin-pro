//go:build integration

// pay 转账域与示例业务 HTTP 集成测试（issue #137，docs/pay-mvp-spec.md §8.2
// 转账行 + §8.1 job seam）：真实 router + 真实 MySQL + #134 回调链路 E2E。
//
// 覆盖：转账幂等（同 merchant_transfer_id 返回原单）、ZZ 编号正则与伪造 no
// 不采纳、wallet 渠道入账（biz=6 唯一入口、目标钱包懒创建、并发快照重放）、
// 未知目标 409、渠道校验矩阵（未知码/未实现/停用）、审批流（非等待 409、
// 关闭置 20 不建转账单、转账在途不可关）、全异步链路（审批 → 转账 → type=3
// 任务 → job POST → 公开 demo 端点（HMAC）→ 提现单 10 + transfer_time）、
// 示例订单（下单 → 收银台 wallet 支付 → type=1 联动 pay_status；退款 →
// #135 退款 → type=2 联动退款列）、demo 端点 kind 错投业务失败、应用删除
// 转账引用 409、管理页普通角色 403、跨租户 404。
package pay_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// noZZPattern 是转账单号格式（spec §4.9：ZZ + yyyyMMdd + 至少 4 位序号）。
var noZZPattern = regexp.MustCompile(`^ZZ\d{8}\d{4,}$`)

// ---------- 测试专用助手 ----------

// demoSeedApp 准备演示应用（app_key=demo，三个回调地址指向本服务 demo 公开
// 端点，绑定 mock/wallet——照 spec §7 演示应用的绑定）：挂单应用解析
// （pay.recharge-app-key 缺省 demo）与全链路回调联动的公共前置。
func demoSeedApp(t *testing.T, env *testutil.IntegrationEnv, token string) payApp {
	t.Helper()
	prefix := env.Server.URL + env.Config.App.APIPrefix
	status, api := env.Request(t, http.MethodPost, "/pay/app", token, map[string]any{
		"appKey": "demo", "name": "演示应用",
		"orderNotifyUrl":    prefix + "/pay/demo/notify/order",
		"refundNotifyUrl":   prefix + "/pay/demo/notify/refund",
		"transferNotifyUrl": prefix + "/pay/demo/notify/transfer",
	})
	mustOK(t, "创建演示应用", status, api)
	var row struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(api.Data, &row); err != nil || row.ID == 0 {
		t.Fatalf("解析应用响应: %v（data=%s）", err, api.Data)
	}
	payBindChannels(t, env, token, row.ID, []string{"mock", "wallet"})
	return payApp{ID: row.ID, AppKey: "demo"}
}

// payCreateTransfer 发起转账（断言成功），返回转账单 JSON。
func payCreateTransfer(t *testing.T, env *testutil.IntegrationEnv, token, appKey, merchantTransferID, channelCode, userAccount, price string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/transfer/create", token, map[string]any{
		"appKey": appKey, "merchantTransferId": merchantTransferID, "channelCode": channelCode,
		"subject": "集成测试转账", "price": price, "userAccount": userAccount, "userName": "测试收款人",
	})
	mustOK(t, "发起转账 "+merchantTransferID, status, api)
	row := parseJSON(t, string(api.Data))
	if row["id"].(float64) <= 0 {
		t.Fatalf("转账单响应缺少 id：%s", api.Data)
	}
	return row
}

// payNormRows 归一化 DB Raw 扫描行：decimal 列驱动返回 []byte，统一转 string
// （与 money JSON 字符串形态一致，供 walletAmount 等字符串断言使用）。
func payNormRows(rows []map[string]any) []map[string]any {
	for _, row := range rows {
		for k, v := range row {
			if b, ok := v.([]byte); ok {
				row[k] = string(b)
			}
		}
	}
	return rows
}

// payTransferByMerchant 查消费方转账单号对应的转账单行（幂等断言用）。
func payTransferByMerchant(t *testing.T, env *testutil.IntegrationEnv, merchantTransferID string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw(
		"SELECT id, no, app_id, merchant_transfer_id AS merchantTransferId, user_id, user_type, "+
			"channel_code AS channelCode, status, success_time AS successTime, channel_transfer_no AS channelTransferNo "+
			"FROM pay_transfer WHERE merchant_transfer_id = ?", merchantTransferID,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("查转账单: %v", err)
	}
	return payNormRows(rows)
}

// payWalletOfUser 按用户读钱包行（余额/累计列断言用；无钱包行返回 nil）。
func payWalletOfUser(t *testing.T, env *testutil.IntegrationEnv, userID int64) map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw(
		"SELECT id, balance, total_recharge AS totalRecharge, total_expense AS totalExpense "+
			"FROM pay_wallet WHERE user_id = ? AND user_type = 2", userID,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("查钱包: %v", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return payNormRows(rows)[0]
}

// payWalletTxsOfWallet 读钱包流水（biz_type/biz_id/快照断言用，id ASC）。
func payWalletTxsOfWallet(t *testing.T, env *testutil.IntegrationEnv, walletID int64) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw(
		"SELECT no, biz_type AS bizType, biz_id AS bizId, title, price, balance "+
			"FROM pay_wallet_transaction WHERE wallet_id = ? ORDER BY id ASC", walletID,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("查钱包流水: %v", err)
	}
	return payNormRows(rows)
}

// payNotifyTasksByData 按单据 ID 查回调任务行（type=2/3 联动链路断言用）。
func payNotifyTasksByData(t *testing.T, env *testutil.IntegrationEnv, dataID int64) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw(
		"SELECT id, type, data_id AS dataId, status, notify_times AS notifyTimes FROM pay_notify_task WHERE data_id = ?", dataID,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("查回调任务: %v", err)
	}
	return rows
}

// demoCreateWithdraw 申请提现（断言成功），返回提现单 JSON。
func demoCreateWithdraw(t *testing.T, env *testutil.IntegrationEnv, token, subject, price string) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/demo-withdraw/create", token, map[string]any{
		"subject": subject, "price": price,
	})
	mustOK(t, "申请提现 "+subject, status, api)
	row := parseJSON(t, string(api.Data))
	if row["id"].(float64) <= 0 {
		t.Fatalf("提现单响应缺少 id：%s", api.Data)
	}
	return row
}

// demoCreateOrder 创建示例订单（断言成功），返回订单视图 JSON。
func demoCreateOrder(t *testing.T, env *testutil.IntegrationEnv, token string, spuID int64) map[string]any {
	t.Helper()
	status, api := env.Request(t, http.MethodPost, "/pay/demo-order/create", token, map[string]any{
		"spuId": spuID,
	})
	mustOK(t, "创建示例订单", status, api)
	row := parseJSON(t, string(api.Data))
	if row["id"].(float64) <= 0 {
		t.Fatalf("示例订单响应缺少 id：%s", api.Data)
	}
	return row
}

// demoOrderRow 读示例订单行（联动列断言用）。
func demoOrderRow(t *testing.T, env *testutil.IntegrationEnv, id int64) map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw(
		"SELECT user_id AS userId, spu_id AS spuId, spu_name AS spuName, price, pay_status AS payStatus, "+
			"pay_order_id AS payOrderId, pay_time AS payTime, pay_channel_code AS payChannelCode, "+
			"pay_refund_id AS payRefundId, refund_price AS refundPrice, refund_time AS refundTime "+
			"FROM pay_demo_order WHERE id = ?", id,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("读示例订单 %d: %v", id, err)
	}
	if len(rows) != 1 {
		t.Fatalf("示例订单 %d 应存在，实际 %d 行", id, len(rows))
	}
	return payNormRows(rows)[0]
}

// demoWithdrawRow 读示例提现单行（联动列断言用）。
func demoWithdrawRow(t *testing.T, env *testutil.IntegrationEnv, id int64) map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := env.DB.Raw(
		"SELECT user_id AS userId, subject, price, user_account AS userAccount, user_name AS userName, "+
			"type, status, pay_transfer_id AS payTransferId, transfer_channel_code AS transferChannelCode, "+
			"transfer_time AS transferTime, transfer_error_msg AS transferErrorMsg "+
			"FROM pay_demo_withdraw WHERE id = ?", id,
	).Scan(&rows).Error; err != nil {
		t.Fatalf("读提现单 %d: %v", id, err)
	}
	if len(rows) != 1 {
		t.Fatalf("提现单 %d 应存在，实际 %d 行", id, len(rows))
	}
	return payNormRows(rows)[0]
}

// ---------- 转账发起：幂等 + ZZ 编号 + 渠道校验（spec §8.2 转账行） ----------

// TestIntegrationPayTransferIdempotent 幂等与编号：同 merchant_transfer_id
// 返回原单；跨应用同号不冲突；ZZ 正则；客户端伪造 no 不采纳（DTO 不收）；
// wallet 渠道入账目标钱包（biz=6，快照）。
func TestIntegrationPayTransferIdempotent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := demoSeedApp(t, env, admin)

	// 首笔：wallet 渠道转给 admin 本人（懒创建钱包）；no 客户端伪造不采纳。
	row := payCreateTransfer(t, env, admin, appRow.AppKey, "MT-100", "wallet", "1", "12.34")
	if got := payStr(row["no"]); !noZZPattern.MatchString(got) {
		t.Fatalf("转账单号应匹配 ^ZZ\\d{8}\\d{4,}$，实际 %q", got)
	}
	if row["no"] == "ZZ000000000000" {
		t.Fatalf("客户端伪造的 no 不应被采纳：%v", row["no"])
	}
	if got := payStr(row["channelCode"]); got != "wallet" {
		t.Fatalf("渠道应为 wallet，实际 %q", got)
	}

	// 同 merchant_transfer_id 重复发起：返回原单（同 id 同 no），不重复入账。
	again := payCreateTransfer(t, env, admin, appRow.AppKey, "MT-100", "wallet", "1", "12.34")
	if payNum(again["id"]) != payNum(row["id"]) || again["no"] != row["no"] {
		t.Fatalf("幂等应返回原单：%v vs %v", again, row)
	}

	// 跨应用同号不冲突：第二个应用同名 merchant_transfer_id 落独立转账单。
	app2 := payCreateApp(t, env, admin, "transfer-app2")
	row2 := payCreateTransfer(t, env, admin, app2.AppKey, "MT-100", "mock", "", "1.00")
	if payNum(row2["id"]) == payNum(row["id"]) {
		t.Fatalf("跨应用同号应互不冲突")
	}

	// wallet 入账：唯一入账入口 biz=6、biz_id=转账单 ID、快照 balance=12.34。
	wallet := payWalletOfUser(t, env, 1)
	if wallet == nil {
		t.Fatal("目标钱包应懒创建")
	}
	walletAmount(t, wallet, "balance", "12.34")
	txs := payWalletTxsOfWallet(t, env, payNum(wallet["id"]))
	if len(txs) != 1 {
		t.Fatalf("应恰好 1 笔流水，实际 %d", len(txs))
	}
	if payNum(txs[0]["bizType"]) != 6 || payNum(txs[0]["bizId"]) != payNum(row["id"]) {
		t.Fatalf("流水应 biz=6 转账、biz_id=转账单 ID：%v", txs[0])
	}
	if got := payStr(txs[0]["price"]); got != "12.34" {
		t.Fatalf("流水入账金额应 12.34，实际 %q", got)
	}
	// 渠道回执取目标钱包流水号（资金在本租户账本内移动，流水即回执）。
	if got := payStr(row["channelTransferNo"]); got != payStr(txs[0]["no"]) {
		t.Fatalf("渠道转账回执应为目标钱包流水号：%q vs %q", row["channelTransferNo"], txs[0]["no"])
	}

	// type=3 回调任务已建（转账成功入账函数）。
	tasks := payNotifyTasksByData(t, env, payNum(row["id"]))
	if len(tasks) != 1 || payNum(tasks[0]["type"]) != 3 {
		t.Fatalf("转账成功应建 1 条 type=3 任务：%v", tasks)
	}
}

// TestIntegrationPayTransferWalletTarget wallet 渠道目标校验（spec §4.8）：
// 合法标识入账（懒创建任意正整数 user_id 的钱包）、非法标识 409、mock 渠道
// 无归属（user_id/user_type 留 0）。
func TestIntegrationPayTransferWalletTarget(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := demoSeedApp(t, env, admin)

	// 非法目标：非数字 / 0 / 负数一律 409（409 on unknown target）。
	for _, bad := range []string{"not-a-user", "0", "-5", ""} {
		status, api := env.Request(t, http.MethodPost, "/pay/transfer/create", admin, map[string]any{
			"appKey": appRow.AppKey, "merchantTransferId": "MT-BAD-" + bad, "channelCode": "wallet",
			"subject": "非法目标", "price": "1.00", "userAccount": bad,
		})
		if status != http.StatusConflict {
			t.Fatalf("wallet 渠道非法目标 %q 应 409，实际 HTTP %d（msg=%s）", bad, status, api.Msg)
		}
	}

	// 合法目标：新用户标识懒创建钱包入账。
	row := payCreateTransfer(t, env, admin, appRow.AppKey, "MT-TARGET", "wallet", "2", "5.00")
	if payNum(row["userId"]) != 2 {
		t.Fatalf("wallet 渠道转账单应落目标 user_id=2，实际 %v", row["userId"])
	}

	// mock 渠道：模拟成功（spec §4.8），外部账户无归属（user_id/user_type=0），
	// 渠道回执 MOCK-<ZZ 单号>。
	mockRow := payCreateTransfer(t, env, admin, appRow.AppKey, "MT-MOCK", "mock", "external-account", "3.00")
	if payNum(mockRow["userId"]) != 0 {
		t.Fatalf("mock 渠道外部账户应无归属（user_id=0），实际 %v", mockRow["userId"])
	}
	if got := payStr(mockRow["channelTransferNo"]); len(got) < 4 || got[:4] != "MOCK" {
		t.Fatalf("mock 渠道回执应为 MOCK-<单号>，实际 %q", got)
	}

	// 渠道校验矩阵：未知码 / 未实现渠道 409（文案可区分）。
	status, _ := env.Request(t, http.MethodPost, "/pay/transfer/create", admin, map[string]any{
		"appKey": appRow.AppKey, "merchantTransferId": "MT-UNKNOWN", "channelCode": "bitcoin",
		"subject": "未知渠道", "price": "1.00",
	})
	if status != http.StatusConflict {
		t.Fatalf("未知渠道码应 409，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodPost, "/pay/transfer/create", admin, map[string]any{
		"appKey": appRow.AppKey, "merchantTransferId": "MT-UNIMPL", "channelCode": "wx_pub",
		"subject": "未实现渠道", "price": "1.00",
	})
	if status != http.StatusConflict {
		t.Fatalf("未实现渠道应 409，实际 HTTP %d", status)
	}
}

// TestIntegrationPayTransferConcurrency 并发安全（spec §8.2 转账行）：并发
// 转账到同一目标钱包 → 全部成功（入账无上限约束），流水快照可重放（逐笔
// balance 单调递增、重放求和 = 终态余额），LS 流水号唯一。
func TestIntegrationPayTransferConcurrency(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	appRow := demoSeedApp(t, env, admin)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			status, api := env.Request(t, http.MethodPost, "/pay/transfer/create", admin, map[string]any{
				"appKey": appRow.AppKey, "merchantTransferId": fmt.Sprintf("MT-CC-%d", seq),
				"channelCode": "wallet", "subject": "并发转账", "price": "1.00", "userAccount": "1",
			})
			if status != http.StatusOK || api.Code != 0 {
				errs[seq] = fmt.Errorf("HTTP %d code %d msg %s", status, api.Code, api.Msg)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发转账 %d 失败: %v", i, err)
		}
	}

	wallet := payWalletOfUser(t, env, 1)
	if wallet == nil {
		t.Fatal("目标钱包应存在")
	}
	txs := payWalletTxsOfWallet(t, env, payNum(wallet["id"]))
	if len(txs) != n {
		t.Fatalf("应恰好 %d 笔流水，实际 %d", n, len(txs))
	}
	seen := map[string]bool{}
	var replay float64
	for i, tx := range txs {
		no := payStr(tx["no"])
		if seen[no] {
			t.Fatalf("流水号应唯一：%q 重复", no)
		}
		seen[no] = true
		var price, snapshot float64
		if _, err := fmt.Sscanf(payStr(tx["price"]), "%f", &price); err != nil {
			t.Fatalf("解析流水金额 %q: %v", tx["price"], err)
		}
		if _, err := fmt.Sscanf(payStr(tx["balance"]), "%f", &snapshot); err != nil {
			t.Fatalf("解析流水快照 %q: %v", tx["balance"], err)
		}
		replay += price
		if snapshot != replay {
			t.Fatalf("第 %d 笔快照 %v 与重放 %v 不一致（快照重放断裂）", i, snapshot, replay)
		}
	}
	var final float64
	if _, err := fmt.Sscanf(payStr(wallet["balance"]), "%f", &final); err != nil {
		t.Fatalf("解析终态余额 %q: %v", wallet["balance"], err)
	}
	if final != replay {
		t.Fatalf("终态余额 %v 应等于快照重放和 %v", final, replay)
	}
}

// ---------- 示例提现：审批 → 转账 → 回调联动全异步链路（spec §4.8/§8.2） ----------

// TestIntegrationPayDemoWithdrawChain 端到端：申请（0 等待）→ 管理端审批
// （创建转账单 WD-<id>，wallet 渠道入账申请人钱包，pay_transfer_id 回写、
// status 仍 0）→ type=3 任务 → job POST 公开 demo 端点（HMAC 有效）→ 提现单
// 10 成功 + transfer_time/transfer_channel_code；重复审批/非等待审批 409。
func TestIntegrationPayDemoWithdrawChain(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")
	demoSeedApp(t, env, admin)

	// 申请（自助端点）：user_account/user_name 服务端取登录主体，status 0。
	withdraw := demoCreateWithdraw(t, env, demo, "-demo 提现", "20.00")
	wdID := payNum(withdraw["id"])
	if got := payStr(withdraw["userAccount"]); got != "2" {
		t.Fatalf("user_account 应服务端取登录主体（2），实际 %q", got)
	}
	if got := payStr(withdraw["userName"]); got != "demo" {
		t.Fatalf("user_name 应为登录用户名，实际 %q", got)
	}
	if payNum(withdraw["status"]) != 0 {
		t.Fatalf("新提现单应 0 等待，实际 %v", withdraw["status"])
	}

	// 审批前关闭/审批路由的普通角色 403 在管理页矩阵用例覆盖；这里走主链。
	status, api := env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/approve", wdID), admin, nil)
	mustOK(t, "审批提现", status, api)

	row := demoWithdrawRow(t, env, wdID)
	if payNum(row["status"]) != 0 {
		t.Fatalf("审批后提现单应仍 0 等待（转账成功回调联动才置 10），实际 %v", row["status"])
	}
	transferID := payNum(row["payTransferId"])
	if transferID == 0 {
		t.Fatal("审批应创建转账单并回写 pay_transfer_id")
	}
	// 转账单：merchant_transfer_id = WD-<id>、wallet 渠道、同步成功、入账
	// 申请人钱包（biz=6）。
	var transfer struct {
		No                 string
		MerchantTransferID string
		ChannelCode        string
		Status             int8
		UserID             int64
		ChannelTransferNo  string
	}
	if err := env.DB.Raw(
		"SELECT no, merchant_transfer_id AS merchantTransferId, channel_code AS channelCode, "+
			"status, user_id AS userId, channel_transfer_no AS channelTransferNo FROM pay_transfer WHERE id = ?", transferID,
	).Scan(&transfer).Error; err != nil {
		t.Fatalf("读转账单: %v", err)
	}
	if transfer.MerchantTransferID != fmt.Sprintf("WD-%d", wdID) {
		t.Fatalf("消费方转账单号应为 WD-<提现单 ID>，实际 %q", transfer.MerchantTransferID)
	}
	if !noZZPattern.MatchString(transfer.No) {
		t.Fatalf("转账单号应匹配 ZZ 正则，实际 %q", transfer.No)
	}
	if transfer.ChannelCode != "wallet" || transfer.Status != 10 || transfer.UserID != 2 {
		t.Fatalf("转账单应为 wallet 渠道同步成功且目标为申请人：%+v", transfer)
	}
	wallet := payWalletOfUser(t, env, 2)
	if wallet == nil {
		t.Fatal("申请人钱包应懒创建")
	}
	walletAmount(t, wallet, "balance", "20.00")

	// type=3 任务 → job → 公开 demo 端点（HMAC 有效）→ 提现单 10。
	tasks := payNotifyTasksByData(t, env, transferID)
	if len(tasks) != 1 || payNum(tasks[0]["type"]) != 3 {
		t.Fatalf("转账成功应建 1 条 type=3 任务：%v", tasks)
	}
	taskID := payNum(tasks[0]["id"])
	payRunNotifyJob(t, env)
	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 10 {
		t.Fatalf("demo 端点应答 code=0，type=3 任务应 10 成功，实际 %v", task["status"])
	}
	row = demoWithdrawRow(t, env, wdID)
	if payNum(row["status"]) != 10 {
		t.Fatalf("回调联动后提现单应 10 成功，实际 %v", row["status"])
	}
	if row["transferTime"] == nil || payStr(row["transferChannelCode"]) != "wallet" {
		t.Fatalf("联动应回写 transfer_time 与 transfer_channel_code：%v", row)
	}

	// 非等待状态审批 409（已成功）。
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/approve", wdID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("已成功提现单再审批应 409，实际 HTTP %d", status)
	}

	// 第二笔：关闭流（不建转账单）。
	wd2 := demoCreateWithdraw(t, env, demo, "-关闭流", "5.00")
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/close", payNum(wd2["id"])), admin, nil)
	mustOK(t, "关闭提现", status, api)
	row2 := demoWithdrawRow(t, env, payNum(wd2["id"]))
	if payNum(row2["status"]) != 20 || payNum(row2["payTransferId"]) != 0 {
		t.Fatalf("关闭应置 20 且不建转账单：%v", row2)
	}
	if rows := payTransferByMerchant(t, env, fmt.Sprintf("WD-%d", payNum(wd2["id"]))); len(rows) != 0 {
		t.Fatalf("关闭不应产生转账单：%v", rows)
	}
	// 关闭后再审批/再关闭 409。
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/approve", payNum(wd2["id"])), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("已关闭提现单审批应 409，实际 HTTP %d", status)
	}
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/close", payNum(wd2["id"])), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("已关闭提现单再关闭应 409，实际 HTTP %d", status)
	}

	// 转账在途（已审批未联动）不可关闭：第三笔审批后（不跑 job）close 409。
	wd3 := demoCreateWithdraw(t, env, demo, "-在途不可关", "1.00")
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/approve", payNum(wd3["id"])), admin, nil)
	mustOK(t, "审批第三笔", status, api)
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/close", payNum(wd3["id"])), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("转账在途的提现单关闭应 409，实际 HTTP %d", status)
	}

	// type=2 预留类型：创建即拒绝（无渠道可转）。
	status, _ = env.Request(t, http.MethodPost, "/pay/demo-withdraw/create", demo, map[string]any{
		"subject": "支付宝预留", "price": "1.00", "type": 2,
	})
	if status != http.StatusConflict {
		t.Fatalf("支付宝预留类型创建应 409，实际 HTTP %d", status)
	}
}

// TestIntegrationPayDemoWithdrawKindMismatch demo 端点 kind 错投（task → job
// 链路的防御语义）：order 报文投到 transfer 端点 → 业务失败应答 → 任务 21
// （继续重试）；修正确认后（正确 kind 重放由 job 完成）任务转 10。
func TestIntegrationPayDemoWithdrawKindMismatch(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demoSeedApp(t, env, admin)

	// 用只挂 transfer 回调地址的应用发起一笔普通订单支付（订单为 type=1，
	// 报文投到 transfer 端点模拟错投）：kind↔type 校验先行拦截 → 任务 21。
	demoURL := env.Server.URL + env.Config.App.APIPrefix + "/pay/demo/notify/transfer"
	appRow := payCreateAppWithNotifyURL(t, env, admin, "kind-mismatch-app", demoURL)
	payBindChannels(t, env, admin, appRow.ID, []string{"mock"})
	_, taskID := payMockPaySuccess(t, env, admin, appRow.AppKey, "MO-KIND-MISMATCH", "2.00")
	payRunNotifyJob(t, env)

	// order 报文（type=1）错投 transfer 端点：业务失败 → 任务 21。
	task := payTaskRow(t, env, taskID)
	if payNum(task["status"]) != 21 {
		t.Fatalf("kind 错投应使任务 21（继续重试），实际 %v", task["status"])
	}
	logs := payNotifyLogs(t, env, taskID)
	if len(logs) != 1 || payNum(logs[0]["status"]) != 21 {
		t.Fatalf("应有一条 21 日志：%v", logs)
	}
}

// ---------- 示例订单：下单 → 收银台 wallet 支付 → 联动；退款 → 联动 ----------

// TestIntegrationPayDemoOrderFlow 示例订单全链路（spec §6）：字典清单 → 创建
// （金额服务端定 = 字典价）→ 收银台 wallet 支付 → type=1 联动 pay_status/
// pay_time/pay_channel_code → 发起退款（#135 全额）→ type=2 联动退款列 →
// 再退 409；非本人退款 403。
func TestIntegrationPayDemoOrderFlow(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")
	demoSeedApp(t, env, admin)

	// 字典清单：4 款演示商品（金额十进制字符串）。
	status, api := env.Request(t, http.MethodGet, "/pay/demo-order/spus", demo, nil)
	mustOK(t, "演示商品字典", status, api)
	var spus []map[string]any
	if err := json.Unmarshal(api.Data, &spus); err != nil || len(spus) != 4 {
		t.Fatalf("字典应 4 款商品：%s（err=%v）", api.Data, err)
	}

	// 未知 spuId 404。
	status, _ = env.Request(t, http.MethodPost, "/pay/demo-order/create", demo, map[string]any{"spuId": 99})
	if status != http.StatusNotFound {
		t.Fatalf("未知演示商品应 404，实际 HTTP %d", status)
	}

	// 创建（spu 1 = 19.90）：挂单定位 DEMO-<id>，payStatus 0。
	order := demoCreateOrder(t, env, demo, 1)
	orderID := payNum(order["id"])
	if got := payStr(order["price"]); got != "19.90" {
		t.Fatalf("示例订单金额应服务端定（字典价 19.90），实际 %q", got)
	}
	if got := payStr(order["merchantOrderId"]); got != fmt.Sprintf("DEMO-%d", orderID) {
		t.Fatalf("挂单消费方单号应为 DEMO-<id>，实际 %q", got)
	}
	if payStr(order["appKey"]) != "demo" {
		t.Fatalf("挂单应用应为 demo：%v", order["appKey"])
	}

	// 收银台 wallet 支付：先给 demo 用户入账（转账 biz=6 即资金来源），
	// 再 cashier → submit → 同步成功。
	payCreateTransfer(t, env, admin, "demo", "MT-DEMO-FUND", "wallet", "2", "30.00")
	status, api = env.Request(t, http.MethodGet, "/pay/cashier/order?appKey=demo&merchantOrderId=DEMO-"+fmt.Sprint(orderID), demo, nil)
	mustOK(t, "收银台", status, api)
	payOrderID := payNum(parseJSON(t, string(api.Data))["id"])
	status, api = env.Request(t, http.MethodPost, "/pay/order/submit", demo, map[string]any{"orderId": payOrderID, "channelCode": "wallet"})
	mustOK(t, "wallet 渠道发起支付", status, api)
	if got := payNum(parseJSON(t, string(api.Data))["status"]); got != 10 {
		t.Fatalf("wallet 渠道应同步支付成功（10），实际 %v", got)
	}

	// type=1 任务 → job → demo/order 端点 → pay_status 1 + pay_time +
	// pay_channel_code=wallet。
	payRunNotifyJob(t, env)
	row := demoOrderRow(t, env, orderID)
	if payNum(row["payStatus"]) != 1 {
		t.Fatalf("回调联动后示例订单应 1 已支付，实际 %v", row["payStatus"])
	}
	if row["payTime"] == nil || payStr(row["payChannelCode"]) != "wallet" {
		t.Fatalf("联动应回写 pay_time/pay_channel_code：%v", row)
	}

	// 发起退款（#135 全额原路退回，wallet 回加）：退款单 10 成功。
	status, api = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-order/%d/refund", orderID), demo, map[string]any{"reason": "示例退款"})
	mustOK(t, "发起示例退款", status, api)
	refund := parseJSON(t, string(api.Data))
	if payNum(refund["status"]) != 10 || payStr(refund["refundPrice"]) != "19.90" {
		t.Fatalf("退款应同步成功且全额：%v", refund)
	}

	// type=2 任务 → job → demo/refund 端点 → 退款列回写。
	payRunNotifyJob(t, env)
	row = demoOrderRow(t, env, orderID)
	if payNum(row["payRefundId"]) != payNum(refund["id"]) {
		t.Fatalf("联动应回写 pay_refund_id：%v vs %v", row["payRefundId"], refund["id"])
	}
	if got := payStr(row["refundPrice"]); got != "19.90" {
		t.Fatalf("联动应回写 refund_price=19.90，实际 %q", got)
	}
	if row["refundTime"] == nil {
		t.Fatalf("联动应回写 refund_time：%v", row)
	}

	// 已全额退款再退 409；非本人退款 403。
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-order/%d/refund", orderID), demo, nil)
	if status != http.StatusConflict {
		t.Fatalf("已全额退款再退应 409，实际 HTTP %d", status)
	}
	other := demoCreateOrder(t, env, demo, 2)
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-order/%d/refund", payNum(other["id"])), admin, nil)
	if status != http.StatusForbidden {
		t.Fatalf("非本人示例订单退款应 403，实际 HTTP %d", status)
	}
	// 未支付订单退款 409。
	status, _ = env.Request(t, http.MethodPut, fmt.Sprintf("/pay/demo-order/%d/refund", payNum(other["id"])), demo, nil)
	if status != http.StatusConflict {
		t.Fatalf("未支付示例订单退款应 409，实际 HTTP %d", status)
	}
}

// ---------- 管理端矩阵：分页/详情、普通角色 403、跨租户 404、应用删除守卫 ----------

// TestIntegrationPayTransferDemoAdmin 管理页（转账单/示例订单/示例提现分页 +
// 转账单详情）、普通角色 403 矩阵、跨租户 404、应用删除转账引用 409
// （TransferRefChecker，spec §4.10）。
func TestIntegrationPayTransferDemoAdmin(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	admin := env.Login(t, "admin", "123456")
	demo := env.Login(t, "demo", "123456")
	appRow := demoSeedApp(t, env, admin)

	transfer := payCreateTransfer(t, env, admin, appRow.AppKey, "MT-ADMIN", "wallet", "2", "6.00")
	wd := demoCreateWithdraw(t, env, demo, "-管理页", "2.00")
	order := demoCreateOrder(t, env, demo, 1)

	// 分页 + 过滤（status 枚举非法 400）。
	status, api := env.Request(t, http.MethodGet, "/pay/transfer/page?pageNo=1&pageSize=10&merchantTransferId=MT-ADMIN", admin, nil)
	mustOK(t, "转账单分页", status, api)
	var page struct {
		Total int64            `json:"total"`
		List  []map[string]any `json:"list"`
	}
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total != 1 {
		t.Fatalf("转账单分页应命中 1 条：%s", api.Data)
	}
	status, _ = env.Request(t, http.MethodGet, "/pay/transfer/page?pageNo=1&pageSize=10&status=99", admin, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("转账单非法状态过滤应 400，实际 HTTP %d", status)
	}

	// 详情：转账单 + 关联应用。
	status, api = env.Request(t, http.MethodGet, fmt.Sprintf("/pay/transfer/%d", payNum(transfer["id"])), admin, nil)
	mustOK(t, "转账单详情", status, api)
	detail := parseJSON(t, string(api.Data))
	if detail["app"].(map[string]any)["appKey"] != "demo" {
		t.Fatalf("详情应携带关联应用：%s", api.Data)
	}

	// 示例提现/示例订单分页。
	status, api = env.Request(t, http.MethodGet, "/pay/demo-withdraw/page?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "示例提现分页", status, api)
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total < 1 {
		t.Fatalf("示例提现分页应有数据：%s", api.Data)
	}
	status, api = env.Request(t, http.MethodGet, "/pay/demo-order/page?pageNo=1&pageSize=10", admin, nil)
	mustOK(t, "示例订单分页", status, api)
	if err := json.Unmarshal(api.Data, &page); err != nil || page.Total < 1 {
		t.Fatalf("示例订单分页应有数据：%s", api.Data)
	}

	// 普通角色 403 矩阵：三个管理页 + 审批/关闭 + 转账单详情。
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/pay/transfer/page?pageNo=1&pageSize=10"},
		{http.MethodGet, fmt.Sprintf("/pay/transfer/%d", payNum(transfer["id"]))},
		{http.MethodGet, "/pay/demo-withdraw/page?pageNo=1&pageSize=10"},
		{http.MethodGet, "/pay/demo-order/page?pageNo=1&pageSize=10"},
		{http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/approve", payNum(wd["id"]))},
		{http.MethodPut, fmt.Sprintf("/pay/demo-withdraw/%d/close", payNum(wd["id"]))},
	} {
		status, _ := env.Request(t, tc.method, tc.path, demo, nil)
		if status != http.StatusForbidden {
			t.Fatalf("普通角色 %s %s 应 403，实际 HTTP %d", tc.method, tc.path, status)
		}
	}
	// 自助端点登录即可（demo 创建/申请）。
	status, _ = env.Request(t, http.MethodGet, "/pay/demo-order/spus", demo, nil)
	if status != http.StatusOK {
		t.Fatalf("demo 自助字典应登录可达，实际 HTTP %d", status)
	}

	// 跨租户 404：租户 2 管理员不可见租户 1 的转账单/示例订单/提现单。
	token2 := createTenantByAPI(t, env, admin, "transfer-tenant2")
	t2 := loginTenant(t, env, "admin", "123456", token2)
	for _, path := range []string{
		fmt.Sprintf("/pay/transfer/%d", payNum(transfer["id"])),
		fmt.Sprintf("/pay/demo-order/%d", payNum(order["id"])),
	} {
		status, _ := env.Request(t, http.MethodGet, path, t2, nil)
		if status != http.StatusNotFound {
			t.Fatalf("跨租户 %s 应 404，实际 HTTP %d", path, status)
		}
	}

	// 应用删除转账引用 409（TransferRefChecker；示例订单挂单引用经订单先行
	// 命中，转账引用是独立事实源）。
	status, api = env.Request(t, http.MethodDelete, fmt.Sprintf("/pay/app/%d", appRow.ID), admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("有转账单引用的应用删除应 409，实际 HTTP %d（msg=%s）", status, api.Msg)
	}
}
