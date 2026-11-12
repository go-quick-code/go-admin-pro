//go:build integration

// pay 演示数据集成测试（issue #139，docs/pay-mvp-spec.md §7 演示数据/§9 验收 3）：
// 真实 MySQL 上验证
//   - seed.Run(db, true) 一键写入完整 pay 演示链路：演示应用（app_key=demo，
//     绑定 mock/wallet）→ 充值套餐 2 档 → 充值（套餐赠送入账）→ 充值退款
//     （全额扣回）→ 示例下单（wallet 支付 + mock 支付 + 待支付）→ 部分退款
//     → 全额退款 → 示例提现（1 单审批转账成功 + 2 单等待审批）；
//   - 全部业务行经 pay service 真实入口落库（下单/收银台/模拟回调/退款/审批），
//     钱包余额与流水快照可重放：balance = 流水代数和 = 末笔快照，
//     total_recharge/total_expense 与 biz_type 联动一致；
//   - demo 联动列（示例订单支付/退款回执、提现转账回执）经
//     HandleDemoNotify 服务入口回写（与 HTTP 回调端点同一路径），回调任务
//     建立且指向本地 demo 回调端点（make run-demo 后由回调 job 投递）；
//   - 表哨兵幂等：重复执行不重复插入；
//   - 单号前缀与序列表覆盖（ZF/TK/ZZ/LS）。
//
// 回调任务状态刻意不断言（集成测试环境的调度器在跑，seed 建立的待投递任务
// 可能被分钟级 cron 标成 22——任务生命周期断言由 #134 的回调集成测试覆盖）。
package seed_test

import (
	"regexp"
	"slices"
	"testing"

	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	paychannel "github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
	"gorm.io/gorm"
)

// payDemoCounts 是一次 pay 演示数据写入后的行数快照（幂等断言用）。
// 不含 pay_notify_log（job 可能已投递若干次，计数不稳定）。
type payDemoCounts struct {
	Apps          int64
	AppChannels   int64
	Packages      int64
	DemoOrders    int64
	DemoWithdraws int64
	Orders        int64
	Extensions    int64
	Refunds       int64
	Transfers     int64
	Wallets       int64
	WalletTxs     int64
	NotifyTasks   int64
	Sequences     int64
}

func countPayDemo(t *testing.T, db *gorm.DB) payDemoCounts {
	t.Helper()
	count := func(model any) int64 {
		var total int64
		if err := tenant.Skip(db).Model(model).Count(&total).Error; err != nil {
			t.Fatalf("统计 %T: %v", model, err)
		}
		return total
	}
	var sequences int64
	if err := tenant.Skip(db).Table("pay_sequence").Count(&sequences).Error; err != nil {
		t.Fatalf("统计 pay_sequence: %v", err)
	}
	return payDemoCounts{
		Apps:          count(&paymodel.App{}),
		AppChannels:   count(&paymodel.AppChannel{}),
		Packages:      count(&paymodel.WalletRechargePackage{}),
		DemoOrders:    count(&paymodel.DemoOrder{}),
		DemoWithdraws: count(&paymodel.DemoWithdraw{}),
		Orders:        count(&paymodel.Order{}),
		Extensions:    count(&paymodel.OrderExtension{}),
		Refunds:       count(&paymodel.Refund{}),
		Transfers:     count(&paymodel.Transfer{}),
		Wallets:       count(&paymodel.Wallet{}),
		WalletTxs:     count(&paymodel.WalletTransaction{}),
		NotifyTasks:   count(&paymodel.NotifyTask{}),
		Sequences:     sequences,
	}
}

// TestIntegrationPayDemoSeed 验证 pay 演示链路的完整性、幂等性与快照可重放。
func TestIntegrationPayDemoSeed(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	// 测试基座默认关闭演示数据；显式写入第一遍（含 CRM/OA/ERP/平台演示，
	// 本用例只断言 pay 域）。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("写入 pay 演示数据: %v", err)
	}
	first := countPayDemo(t, env.DB)
	want := payDemoCounts{
		Apps:          1, // 演示应用（app_key=demo）
		AppChannels:   2, // mock + wallet 绑定
		Packages:      2, // 套餐 50+5 / 100+15
		DemoOrders:    3, // 2 笔已支付（其一全额退款）+ 1 笔待支付
		DemoWithdraws: 3, // 1 笔审批转账成功 + 2 笔等待审批
		Orders:        5, // 3 笔示例挂单 + 2 笔充值挂单
		Extensions:    4, // 待支付示例订单未发起支付，无 extension
		Refunds:       3, // 示例部分退款 + 示例全额退款 + 充值退款
		Transfers:     1, // 提现审批转账
		Wallets:       1, // admin 演示钱包（懒创建）
		WalletTxs:     6, // 充值/充值退款/充值/支付/支付退款/转账
		NotifyTasks:   8, // 4×支付 + 3×退款 + 1×转账
		Sequences:     4, // ZF/TK/ZZ/LS 各一个日期键
	}
	if first != want {
		t.Fatalf("pay 演示数据行数不符：\n got %+v\nwant %+v", first, want)
	}

	// 重复执行必须幂等：所有 pay 表计数完全不变（含序列表）。
	if err := seed.Run(env.DB, true); err != nil {
		t.Fatalf("重复写入 pay 演示数据: %v", err)
	}
	second := countPayDemo(t, env.DB)
	if second != first {
		t.Fatalf("重复 seed 不应新增行：\n before %+v\n after %+v", first, second)
	}

	assertPayDemoApp(t, env.DB)
	assertPayDemoPackages(t, env.DB)
	assertPayDemoDemoChain(t, env.DB)
	assertPayDemoWithdrawChain(t, env.DB)
	assertPayDemoRechargeChain(t, env.DB)
	assertPayDemoLedgerReplay(t, env.DB)
	assertPayDemoNotifyTasks(t, env.DB)
	assertPayDemoNoFormats(t, env.DB)
}

// assertPayDemoApp 校验演示应用：固定 ID 5001、app_key=demo、启用、绑定
// mock/wallet、回调地址指向本地 demo 回调三端点（pay.recharge-app-key 缺省
// 指向的应用——#136/#137 的演示租户依赖）。
func assertPayDemoApp(t *testing.T, db *gorm.DB) {
	t.Helper()
	var app paymodel.App
	if err := tenant.Skip(db).First(&app, 5001).Error; err != nil {
		t.Fatalf("读取演示应用: %v", err)
	}
	if app.AppKey != "demo" || app.Status != paymodel.StatusEnable {
		t.Fatalf("演示应用 app_key/状态不符：%+v", app)
	}
	if !endsWithNotifyPath(app.OrderNotifyURL, "order") ||
		!endsWithNotifyPath(app.RefundNotifyURL, "refund") ||
		!endsWithNotifyPath(app.TransferNotifyURL, "transfer") {
		t.Fatalf("演示应用回调地址未指向本地 demo 回调端点：%+v", app)
	}
	var codes []string
	if err := tenant.Skip(db).Model(&paymodel.AppChannel{}).
		Where("app_id = ?", app.ID).Order("id ASC").Pluck("channel_code", &codes).Error; err != nil {
		t.Fatalf("读取演示应用渠道绑定: %v", err)
	}
	slices.Sort(codes)
	if len(codes) != 2 || codes[0] != paychannel.CodeMock || codes[1] != paychannel.CodeWallet {
		t.Fatalf("演示应用应绑定 mock+wallet，实际 %v", codes)
	}
}

// endsWithNotifyPath 报告 notifyURL 是否指向 demo 回调端点 <kind>。
func endsWithNotifyPath(notifyURL, kind string) bool {
	return regexp.MustCompile(`/pay/demo/notify/` + kind + `$`).MatchString(notifyURL)
}

// assertPayDemoPackages 校验两档套餐：固定 ID 5001/5002、启用、实付+赠送档位
// 与 spec §7 示例一致。
func assertPayDemoPackages(t *testing.T, db *gorm.DB) {
	t.Helper()
	want := map[int64]struct {
		pay   int64
		bonus int64
	}{
		5001: {pay: 5000, bonus: 500},   // ¥50 + 赠 ¥5
		5002: {pay: 10000, bonus: 1500}, // ¥100 + 赠 ¥15
	}
	for id, w := range want {
		var pkg paymodel.WalletRechargePackage
		if err := tenant.Skip(db).First(&pkg, id).Error; err != nil {
			t.Fatalf("读取套餐 %d: %v", id, err)
		}
		if pkg.PayPrice.Cents() != w.pay || pkg.BonusPrice.Cents() != w.bonus ||
			pkg.Status != paymodel.StatusEnable || pkg.Name == "" {
			t.Fatalf("套餐 %d 档位/状态不符：%+v", id, pkg)
		}
	}
}

// assertPayDemoDemoChain 校验示例订单链路：2 笔已支付（wallet 支付后部分退款
// 5 元、mock 支付后全额退款）+ 1 笔待支付；联动列（pay_status/pay_channel_code/
// pay_refund_id/refund_price）与挂单/退款单一致（HandleDemoNotify 服务入口回写）。
func assertPayDemoDemoChain(t *testing.T, db *gorm.DB) {
	t.Helper()
	var rows []paymodel.DemoOrder
	if err := tenant.Skip(db).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("读取示例订单: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("示例订单应有 3 行，实际 %d", len(rows))
	}
	bySpu := make(map[int64]paymodel.DemoOrder, len(rows))
	for _, row := range rows {
		bySpu[row.SpuID] = row
	}
	paid, ok := bySpu[1] // 无线蓝牙耳机（wallet 支付 + 部分退款）
	if !ok || paid.PayStatus != paymodel.DemoOrderPayStatusSuccess ||
		paid.PayChannelCode != paychannel.CodeWallet || paid.PayRefundID == 0 ||
		paid.RefundPrice.Cents() != 500 || paid.RefundTime == nil || paid.PayTime == nil {
		t.Fatalf("示例订单 1（部分退款）联动列不符：%+v", paid)
	}
	refunded, ok := bySpu[2] // 机械键盘（mock 支付 + 全额退款）
	if !ok || refunded.PayStatus != paymodel.DemoOrderPayStatusSuccess ||
		refunded.PayChannelCode != paychannel.CodeMock || refunded.PayRefundID == 0 ||
		refunded.RefundPrice.Cents() != refunded.Price.Cents() || refunded.RefundTime == nil {
		t.Fatalf("示例订单 2（全额退款）联动列不符：%+v", refunded)
	}
	pending, ok := bySpu[3] // 便携咖啡杯（待支付，供收银台演示）
	if !ok || pending.PayStatus != paymodel.DemoOrderPayStatusPending ||
		pending.PayOrderID == 0 || pending.PayRefundID != 0 {
		t.Fatalf("示例订单 3（待支付）状态不符：%+v", pending)
	}

	// 挂单状态：部分退款保持 10、全额退款置 20、待支付 0；已退累计与退款单一致。
	paidOrder := payDemoPayOrder(t, db, paid.PayOrderID)
	if paidOrder.Status != paymodel.OrderStatusSuccess || paidOrder.RefundPrice.Cents() != 500 {
		t.Fatalf("示例订单 1 挂单应部分退款保持 10：%+v", paidOrder)
	}
	refundedOrder := payDemoPayOrder(t, db, refunded.PayOrderID)
	if refundedOrder.Status != paymodel.OrderStatusRefunded ||
		refundedOrder.RefundPrice.Cents() != refundedOrder.Price.Cents() {
		t.Fatalf("示例订单 2 挂单应全额退款置 20：%+v", refundedOrder)
	}
	pendingOrder := payDemoPayOrder(t, db, pending.PayOrderID)
	if pendingOrder.Status != paymodel.OrderStatusPending {
		t.Fatalf("示例订单 3 挂单应待支付：%+v", pendingOrder)
	}

	// 退款单 3 行全部成功：wallet 原路回加 1 笔 + mock 2 笔。
	var refunds []paymodel.Refund
	if err := tenant.Skip(db).Order("id ASC").Find(&refunds).Error; err != nil {
		t.Fatalf("读取退款单: %v", err)
	}
	if len(refunds) != 3 {
		t.Fatalf("退款单应有 3 行，实际 %d", len(refunds))
	}
	channels := make([]string, 0, len(refunds))
	for _, refund := range refunds {
		if refund.Status != paymodel.RefundStatusSuccess {
			t.Fatalf("退款单 %d 应成功：%+v", refund.ID, refund)
		}
		channels = append(channels, refund.ChannelCode)
	}
	// 时序：充值退款（mock，链路最先）→ 示例订单 1 部分退款（wallet 原路回加）
	// → 示例订单 2 全额退款（mock）。
	if channels[0] != paychannel.CodeMock || channels[1] != paychannel.CodeWallet ||
		channels[2] != paychannel.CodeMock {
		t.Fatalf("退款单渠道（原路退回）不符：%v", channels)
	}
}

// assertPayDemoWithdrawChain 校验示例提现链路：1 笔审批转账成功（挂转账单、
// wallet 渠道、联动列回写）+ 2 笔等待（审批演示入口）。
func assertPayDemoWithdrawChain(t *testing.T, db *gorm.DB) {
	t.Helper()
	var rows []paymodel.DemoWithdraw
	if err := tenant.Skip(db).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("读取示例提现: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("示例提现应有 3 行，实际 %d", len(rows))
	}
	success := rows[0]
	if success.Status != paymodel.DemoWithdrawStatusSuccess ||
		success.PayTransferID == 0 || success.TransferChannelCode != paychannel.CodeWallet ||
		success.TransferTime == nil {
		t.Fatalf("示例提现 1（审批转账成功）状态不符：%+v", success)
	}
	var transfer paymodel.Transfer
	if err := tenant.Skip(db).First(&transfer, success.PayTransferID).Error; err != nil {
		t.Fatalf("读取提现转账单: %v", err)
	}
	if transfer.Status != paymodel.TransferStatusSuccess ||
		transfer.ChannelCode != paychannel.CodeWallet ||
		transfer.Price.Cents() != success.Price.Cents() {
		t.Fatalf("提现转账单状态/渠道/金额不符：%+v", transfer)
	}
	for _, waiting := range rows[1:] {
		if waiting.Status != paymodel.DemoWithdrawStatusWaiting || waiting.PayTransferID != 0 {
			t.Fatalf("示例提现 %d 应保持等待（审批演示）：%+v", waiting.ID, waiting)
		}
	}
}

// assertPayDemoRechargeChain 校验充值链路：套餐充值 1（已退款，三列回写）+
// 套餐充值 2（已支付，赠送入账持久）；挂单金额 = 实付档位（快照）。
func assertPayDemoRechargeChain(t *testing.T, db *gorm.DB) {
	t.Helper()
	var rows []paymodel.WalletRecharge
	if err := tenant.Skip(db).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("读取充值单: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("充值单应有 2 行，实际 %d", len(rows))
	}
	refunded := rows[0]
	if refunded.PayStatus != paymodel.RechargePayStatusSuccess ||
		refunded.RefundStatus != paymodel.RechargeRefundStatusSuccess ||
		refunded.RefundTotalPrice.Cents() != 5500 ||
		refunded.PayPrice.Cents() != 5000 || refunded.BonusPrice.Cents() != 500 {
		t.Fatalf("充值单 1（套餐 50+5 已退款）不符：%+v", refunded)
	}
	paid := rows[1]
	if paid.PayStatus != paymodel.RechargePayStatusSuccess ||
		paid.RefundStatus != paymodel.RechargeRefundStatusNone ||
		paid.PayPrice.Cents() != 10000 || paid.BonusPrice.Cents() != 1500 {
		t.Fatalf("充值单 2（套餐 100+15 已支付）不符：%+v", paid)
	}
	if refunded.PackageID == nil || *refunded.PackageID != 5001 ||
		paid.PackageID == nil || *paid.PackageID != 5002 {
		t.Fatalf("充值单套餐快照引用不符：%d / %d", refunded.PackageID, paid.PackageID)
	}
}

// assertPayDemoLedgerReplay 校验钱包快照可重放（spec §4.3/验收 3）：
// 按流水 id 重放代数和逐笔等于 balance 快照、末笔快照等于钱包余额，
// total_recharge/total_expense 与 biz_type 口径一致。
func assertPayDemoLedgerReplay(t *testing.T, db *gorm.DB) {
	t.Helper()
	var wallet paymodel.Wallet
	if err := tenant.Skip(db).First(&wallet).Error; err != nil {
		t.Fatalf("读取演示钱包: %v", err)
	}
	var txs []paymodel.WalletTransaction
	if err := tenant.Skip(db).Where("wallet_id = ?", wallet.ID).
		Order("id ASC").Find(&txs).Error; err != nil {
		t.Fatalf("读取钱包流水: %v", err)
	}
	if len(txs) != 6 {
		t.Fatalf("钱包流水应有 6 行（充值/充值退款/充值/支付/支付退款/转账），实际 %d", len(txs))
	}
	// 期望的 biz_type 序列与金额（分）：套餐 50+5 充值 → 全额退款 →
	// 套餐 100+15 充值 → 耳机 wallet 支付 → 部分退款回加 → 提现转账入账。
	wantBiz := []struct {
		biz   int8
		cents int64
	}{
		{paymodel.WalletBizRecharge, 5500},
		{paymodel.WalletBizRechargeRefund, -5500},
		{paymodel.WalletBizRecharge, 11500},
		{paymodel.WalletBizPayment, -1990},
		{paymodel.WalletBizPaymentRefund, 500},
		{paymodel.WalletBizTransfer, 3000},
	}
	running := int64(0)
	rechargeTotal := int64(0)
	expenseTotal := int64(0)
	for i, tx := range txs {
		w := wantBiz[i]
		if tx.BizType != w.biz || tx.Price.Cents() != w.cents {
			t.Fatalf("流水 %d biz/金额不符：got biz=%d price=%s，want biz=%d cents=%d",
				tx.ID, tx.BizType, tx.Price, w.biz, w.cents)
		}
		running += w.cents
		if tx.Balance.Cents() != running {
			t.Fatalf("流水 %d 快照不可重放：快照 %s ≠ 重放 %d 分",
				tx.ID, tx.Balance, running)
		}
		switch w.biz {
		case paymodel.WalletBizRecharge, paymodel.WalletBizRechargeRefund:
			rechargeTotal += w.cents
		case paymodel.WalletBizPayment, paymodel.WalletBizPaymentRefund:
			expenseTotal += -w.cents
		}
	}
	if wallet.Balance.Cents() != running || running != 13010 {
		t.Fatalf("钱包余额应等于流水代数和（13010 分）：余额 %s，重放 %d 分", wallet.Balance, running)
	}
	if wallet.TotalRecharge.Cents() != rechargeTotal || rechargeTotal != 11500 {
		t.Fatalf("累计充值应为 11500 分（套餐 50+5 已退款调减）：实际 %s", wallet.TotalRecharge)
	}
	if wallet.TotalExpense.Cents() != expenseTotal || expenseTotal != 1490 {
		t.Fatalf("累计支出应为 1490 分（支付 19.90 − 退款回加 5.00）：实际 %s", wallet.TotalExpense)
	}
}

// assertPayDemoNotifyTasks 校验回调任务：8 行（4 支付 + 3 退款 + 1 转账），
// 全部有待投递回调地址（make run-demo 后由回调 job 投递、demo 联动幂等应答）。
// 状态不断言（测试环境调度器可能已投递一轮，见文件头注释）。
func assertPayDemoNotifyTasks(t *testing.T, db *gorm.DB) {
	t.Helper()
	var tasks []paymodel.NotifyTask
	if err := tenant.Skip(db).Order("id ASC").Find(&tasks).Error; err != nil {
		t.Fatalf("读取回调任务: %v", err)
	}
	if len(tasks) != 8 {
		t.Fatalf("回调任务应有 8 行，实际 %d", len(tasks))
	}
	byType := map[int8]int{1: 0, 2: 0, 3: 0}
	for _, task := range tasks {
		if task.NotifyURL == "" {
			t.Fatalf("回调任务 %d 缺少回调地址：%+v", task.ID, task)
		}
		if _, ok := byType[task.Type]; !ok {
			t.Fatalf("回调任务 %d 类型不合法：%d", task.ID, task.Type)
		}
		byType[task.Type]++
	}
	if byType[1] != 4 || byType[2] != 3 || byType[3] != 1 {
		t.Fatalf("回调任务类型分布应为 4 支付/3 退款/1 转账：%v", byType)
	}
}

// assertPayDemoNoFormats 校验单号格式（spec §4.9）：ZF/TK/ZZ/LS 前缀 +
// yyyyMMdd + 至少 4 位序号。
func assertPayDemoNoFormats(t *testing.T, db *gorm.DB) {
	t.Helper()
	pattern := regexp.MustCompile(`^(ZF|TK|ZZ|LS)\d{8}\d{4,}$`)
	check := func(model any, field string) {
		t.Helper()
		var nos []string
		if err := tenant.Skip(db).Model(model).Pluck(field, &nos).Error; err != nil {
			t.Fatalf("读取单号列 %s: %v", field, err)
		}
		for _, no := range nos {
			if !pattern.MatchString(no) {
				t.Fatalf("单号 %q 不符合前缀+日期+序号格式", no)
			}
		}
	}
	check(&paymodel.Order{}, "no")
	check(&paymodel.Refund{}, "no")
	check(&paymodel.Transfer{}, "no")
	check(&paymodel.WalletTransaction{}, "no")
}

// payDemoPayOrder 按 ID 读取示例订单挂的支付订单。
func payDemoPayOrder(t *testing.T, db *gorm.DB, id int64) paymodel.Order {
	t.Helper()
	var order paymodel.Order
	if err := tenant.Skip(db).First(&order, id).Error; err != nil {
		t.Fatalf("读取支付订单 %d: %v", id, err)
	}
	return order
}
