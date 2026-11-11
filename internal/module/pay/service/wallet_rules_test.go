package service

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// wallet_rules_test.go 是钱包变动校验的纯函数表驱动测试（spec §8.1 次 seam：
// 钱包变动校验）。入账事务链路的并发/快照行为由 HTTP/服务级集成测试覆盖。

// TestValidateWalletChange 入账请求校验表：合法六类（5 预留拒绝）、金额非 0、
// 标题必填、归属合法。
func TestValidateWalletChange(t *testing.T) {
	valid := WalletChange{
		UserID: 1, UserType: model.WalletUserTypeAdmin,
		BizType: model.WalletBizPayment, Title: "演示支付", Price: money.FromCents(-100),
	}
	if err := validateWalletChange(valid); err != nil {
		t.Fatalf("合法入账请求不应报错: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(ch *WalletChange)
		wantMsg string
	}{
		{"缺归属用户", func(ch *WalletChange) { ch.UserID = 0 }, "归属用户"},
		{"归属类型非法", func(ch *WalletChange) { ch.UserType = 9 }, "归属类型不合法"},
		{"余额调整预留无入口", func(ch *WalletChange) { ch.BizType = model.WalletBizUpdateBalance }, "预留"},
		{"业务类型非法", func(ch *WalletChange) { ch.BizType = 9 }, "业务类型不合法"},
		{"标题为空", func(ch *WalletChange) { ch.Title = "  " }, "标题不能为空"},
		{"标题超长", func(ch *WalletChange) { ch.Title = strings.Repeat("字", 101) }, "100 字"},
		{"金额为 0", func(ch *WalletChange) { ch.Price = money.Money{} }, "不能为 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := valid
			tc.mutate(&ch)
			err := validateWalletChange(ch)
			if err == nil {
				t.Fatalf("期望报错含 %q，实际 nil", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("报错 %q 应含 %q", err.Error(), tc.wantMsg)
			}
			e, ok := xerr.As(err)
			if !ok || e.Status != 400 {
				t.Fatalf("入账请求校验失败应为 400 业务错误，实际 %v", err)
			}
		})
	}
}

// TestWalletCounterDeltas 统计列增量按 biz_type 分配（充值侧/支出侧对称，
// 正入负出）。
func TestWalletCounterDeltas(t *testing.T) {
	cases := []struct {
		name         string
		bizType      int8
		priceCents   int64
		wantRecharge int64
		wantExpense  int64
	}{
		{"充值入账", model.WalletBizRecharge, 5000, 5000, 0},
		{"充值退款扣回", model.WalletBizRechargeRefund, -3000, -3000, 0},
		{"支付扣款", model.WalletBizPayment, -2000, 0, 2000},
		{"支付退款回加", model.WalletBizPaymentRefund, 2000, 0, -2000},
		{"转账入账不动统计列", model.WalletBizTransfer, 1000, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recharge, expense := walletCounterDeltas(WalletChange{
				BizType: tc.bizType, Price: money.FromCents(tc.priceCents),
			})
			if recharge.Cents() != tc.wantRecharge || expense.Cents() != tc.wantExpense {
				t.Fatalf("biz=%d price=%d：recharge=%d expense=%d，期望 %d/%d",
					tc.bizType, tc.priceCents, recharge.Cents(), expense.Cents(), tc.wantRecharge, tc.wantExpense)
			}
		})
	}
}

// TestCheckWalletCounter 非负累计列锁内越减校验：减到负 → 409 文案；减到 0
// 或增量 ≥ 0 → 放行。
func TestCheckWalletCounter(t *testing.T) {
	if msg := checkWalletCounter(money.FromCents(3000), money.FromCents(-5000), "累计充值"); msg == "" {
		t.Fatal("累计充值越减为负应拒绝")
	} else if want := "累计充值不足以扣减：当前 30.00，需调减 50.00"; msg != want {
		t.Fatalf("409 文案 %q，期望 %q", msg, want)
	}
	if msg := checkWalletCounter(money.FromCents(5000), money.FromCents(-5000), "累计充值"); msg != "" {
		t.Fatalf("减到 0 应放行，实际拒绝：%s", msg)
	}
	if msg := checkWalletCounter(money.FromCents(100), money.FromCents(200), "累计充值"); msg != "" {
		t.Fatalf("正向增量应放行，实际拒绝：%s", msg)
	}
	if msg := checkWalletCounter(money.FromCents(0), money.FromCents(-1), "累计支出"); msg == "" {
		t.Fatal("零累计越减应拒绝")
	}
}
