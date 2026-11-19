package service_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
)

// trade_rules_test.go 是订单状态机与金额不变量的纯函数表驱动测试（外部测试
// 包，照 product_spu_rules_test 形状；issue #179，docs/mall-mvp-spec.md
// §3.3/§3.4）。状态迁移的持久化裁决是条件 UPDATE（repo 层），这里锁定
// 读路径快判语义与后续票（T7/T8/T9）复用的迁移表：
//   - 状态机迁移合法性（0→10/20/30/40 各自的触发方与终态）；
//   - 金额试算（pay_money = item_money − discount_money + postage；订单项
//     subtotal = price × count；客户端金额不采纳）；
//   - 数量正整数。

// TestTradeValidOrderTransition 状态机迁移表（spec §3.4）：
//
//	待支付(0) →(用户取消) 已取消(30)；→(job 超时) 已关闭(40)；→(支付成功) 已支付(10)；
//	已支付(10) →(管理员完成) 已完成(20)。
//	其余迁移一律非法（已支付/已完成不可取消不可关闭；已取消/已关闭终态）。
func TestTradeValidOrderTransition(t *testing.T) {
	legal := []struct {
		from, to int8
		why      string
	}{
		{mallmodel.TradeOrderStatusPending, mallmodel.TradeOrderStatusPaid, "支付成功（T7）"},
		{mallmodel.TradeOrderStatusPending, mallmodel.TradeOrderStatusCanceled, "用户取消（T6）"},
		{mallmodel.TradeOrderStatusPending, mallmodel.TradeOrderStatusClosed, "超时关单（T9）"},
		{mallmodel.TradeOrderStatusPaid, mallmodel.TradeOrderStatusCompleted, "管理员完成（T3）"},
	}
	for _, tc := range legal {
		if !mallservice.ValidOrderTransition(tc.from, tc.to) {
			t.Fatalf("%d→%d（%s）应为合法迁移", tc.from, tc.to, tc.why)
		}
	}
	// 全表穷举非法对：唯一放行上表四条。
	illegal := 0
	for _, from := range mallmodel.TradeOrderStatuses {
		for _, to := range mallmodel.TradeOrderStatuses {
			legalHit := false
			for _, tc := range legal {
				if tc.from == from && tc.to == to {
					legalHit = true
					break
				}
			}
			if legalHit {
				if mallservice.ValidOrderTransition(from, to) != true {
					t.Fatalf("%d→%d 应合法", from, to)
				}
				continue
			}
			if mallservice.ValidOrderTransition(from, to) {
				t.Fatalf("%d→%d 应为非法迁移", from, to)
			}
			illegal++
		}
	}
	if illegal != len(mallmodel.TradeOrderStatuses)*len(mallmodel.TradeOrderStatuses)-len(legal) {
		t.Fatalf("非法迁移计数不符：%d", illegal)
	}
	// 未知状态（域外值）一律非法。
	if mallservice.ValidOrderTransition(99, mallmodel.TradeOrderStatusPaid) ||
		mallservice.ValidOrderTransition(mallmodel.TradeOrderStatusPending, 99) {
		t.Fatalf("未知状态参与迁移应非法")
	}
}

// TestTradeCanCancelOrder 可取消状态：仅待支付；已支付/已完成/已取消/已关闭
// 一律不可取消（spec §3.4「已支付/已完成 不可取消不可关闭」+ 终态）。
func TestTradeCanCancelOrder(t *testing.T) {
	if !mallservice.CanCancelOrder(mallmodel.TradeOrderStatusPending) {
		t.Fatalf("待支付应可取消")
	}
	for _, status := range []int8{
		mallmodel.TradeOrderStatusPaid, mallmodel.TradeOrderStatusCompleted,
		mallmodel.TradeOrderStatusCanceled, mallmodel.TradeOrderStatusClosed,
	} {
		if mallservice.CanCancelOrder(status) {
			t.Fatalf("状态 %d 不可取消，CanCancelOrder 却放行", status)
		}
	}
	if mallservice.IsTerminalOrderStatus(mallmodel.TradeOrderStatusPending) ||
		mallservice.IsTerminalOrderStatus(mallmodel.TradeOrderStatusPaid) ||
		mallservice.IsTerminalOrderStatus(mallmodel.TradeOrderStatusCompleted) {
		t.Fatalf("待支付/已支付/已完成非终态")
	}
	if !mallservice.IsTerminalOrderStatus(mallmodel.TradeOrderStatusCanceled) ||
		!mallservice.IsTerminalOrderStatus(mallmodel.TradeOrderStatusClosed) {
		t.Fatalf("已取消/已关闭应为终态")
	}
}

// TestTradeValidateOrderItemCount 数量正整数（spec §3.4）：0/负数 400；超界
// 400；文案说清字段与期望格式。
func TestTradeValidateOrderItemCount(t *testing.T) {
	if err := mallservice.ValidateOrderItemCount(1); err != nil {
		t.Fatalf("count=1 应合法: %v", err)
	}
	if err := mallservice.ValidateOrderItemCount(mallservice.MaxOrderItemCount); err != nil {
		t.Fatalf("上界 %d 应合法: %v", mallservice.MaxOrderItemCount, err)
	}
	for _, count := range []int{0, -1, mallservice.MaxOrderItemCount + 1} {
		err := mallservice.ValidateOrderItemCount(count)
		if err == nil {
			t.Fatalf("count=%d 应拒绝", count)
		}
		e, ok := xerr.As(err)
		if !ok || e.Status != 400 {
			t.Fatalf("count=%d 应 400，实际 %v", count, err)
		}
		if !strings.Contains(err.Error(), "数量") {
			t.Fatalf("文案应点名数量字段：%v", err)
		}
	}
}

// TestTradeOrderItemSubtotal 订单项小计：subtotal = price × count（Money 分
// 值整数乘，无浮点）。
func TestTradeOrderItemSubtotal(t *testing.T) {
	price := money.FromCents(12345) // 123.45
	if got := mallservice.OrderItemSubtotal(price, 3); got.Cents() != 12345*3 {
		t.Fatalf("subtotal = 123.45×3 应为 370.35，实际 %s", got.String())
	}
	if got := mallservice.OrderItemSubtotal(price, 1); got.Cents() != 12345 {
		t.Fatalf("count=1 小计应等于单价")
	}
}

// TestTradeComputeOrderAmount 金额三列试算（spec §3.4 金额不变量）：
// item_money = Σ subtotal；pay_money = item_money − discount_money + postage；
// total_quantity = Σ count。MVP 的 discount/postage 恒 0 落列（服务端定，
// 客户端金额不采纳）。
func TestTradeComputeOrderAmount(t *testing.T) {
	items := []mallservice.OrderItemLine{
		{Price: money.FromCents(10000), Count: 2}, // 100.00 × 2 = 200.00
		{Price: money.FromCents(333), Count: 3},   // 3.33 × 3 = 9.99
	}
	// 无优惠无邮费（MVP 口径）：pay_money = item_money。
	amounts := mallservice.ComputeOrderAmount(items, money.FromCents(0), money.FromCents(0))
	if amounts.ItemMoney.Cents() != 20000+999 {
		t.Fatalf("item_money 应为 Σ subtotal = 209.99，实际 %s", amounts.ItemMoney.String())
	}
	if amounts.PayMoney.Cents() != amounts.ItemMoney.Cents() {
		t.Fatalf("MVP 无优惠邮费：pay_money 应等于 item_money，实际 %s vs %s",
			amounts.PayMoney.String(), amounts.ItemMoney.String())
	}
	if amounts.TotalQuantity != 5 {
		t.Fatalf("total_quantity 应为 5，实际 %d", amounts.TotalQuantity)
	}
	if amounts.DiscountMoney.Cents() != 0 || amounts.Postage.Cents() != 0 {
		t.Fatalf("discount/postage 应按入参落列（MVP 恒 0）")
	}

	// 公式一般性（后续票优惠/邮费启用时不变式不翻工）：pay = item − discount + postage。
	withFee := mallservice.ComputeOrderAmount(items, money.FromCents(500), money.FromCents(1200))
	if withFee.PayMoney.Cents() != 20999-500+1200 {
		t.Fatalf("pay_money = item − discount + postage 公式不符，实际 %s", withFee.PayMoney.String())
	}

	// 空明细：金额全零（调用方先拦「无结算项」，纯函数不 panic）。
	empty := mallservice.ComputeOrderAmount(nil, money.FromCents(0), money.FromCents(0))
	if empty.ItemMoney.Cents() != 0 || empty.PayMoney.Cents() != 0 || empty.TotalQuantity != 0 {
		t.Fatalf("空明细应零金额，实际 %+v", empty)
	}
}
