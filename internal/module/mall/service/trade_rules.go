package service

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// trade_rules.go 是交易域的纯校验规则（issue #179，docs/mall-mvp-spec.md
// §3.3/§3.4）：状态机迁移表、金额试算、数量校验。全部纯函数（不触碰 DB /
// 时钟 / context），外部测试包 trade_rules_test.go 锁定语义；后续票 T7（支付
// 成功 0→10）/ T8（管理端）/ T9（超时关单 0→40）复用同一迁移表，不另立口径。
//
// 注意分工：本文件的迁移表是「读路径快判 + 测试口径」，状态翻转的持久化
// 最终裁决是 repo 层条件 UPDATE（ClaimOrderCancel 等，WHERE 带 from 状态，
// 影响行数 0 = 并发改动 → 409）——两层同表，单一事实来源以条件 UPDATE 为准，
// 纯函数与其保持同构。

// MaxOrderItemCount 是订单项/购物车数量的上界（正整数校验的界，防御性收敛：
// 单项 ≤ 999 件——超过即视为异常输入，MVP 无批发场景）。
const MaxOrderItemCount = 999

// ValidOrderTransition 报告订单状态迁移 from→to 是否合法（spec §3.4 状态机）：
//
//	待支付(0) →(支付成功，T7) 已支付(10)
//	待支付(0) →(用户取消，T6) 已取消(30)
//	待支付(0) →(job 超时关单，T9) 已关闭(40)
//	已支付(10) →(管理员完成，T3/T8) 已完成(20)
//
// 其余迁移（含终态 30/40 的任何出边、已支付/已完成→取消/关闭）一律非法 409；
// 域外未知状态一律非法。
func ValidOrderTransition(from, to int8) bool {
	switch from {
	case model.TradeOrderStatusPending:
		return to == model.TradeOrderStatusPaid ||
			to == model.TradeOrderStatusCanceled ||
			to == model.TradeOrderStatusClosed
	case model.TradeOrderStatusPaid:
		return to == model.TradeOrderStatusCompleted
	default:
		// 已完成/已取消/已关闭与域外值：终态无出边。
		return false
	}
}

// CanCancelOrder 报告订单当前状态是否允许用户取消（C 端取消的快判）：仅
// 待支付。已支付/已完成 不可取消（spec §3.4）；已取消/已关闭 终态幂等拒绝。
func CanCancelOrder(status int8) bool {
	return status == model.TradeOrderStatusPending
}

// IsTerminalOrderStatus 报告状态是否终态（已取消/已关闭）：终态无出边，
// 后续票的联动（积分回扣/库存回补）只在终态边沿触发一次。
func IsTerminalOrderStatus(status int8) bool {
	return status == model.TradeOrderStatusCanceled || status == model.TradeOrderStatusClosed
}

// ValidateOrderItemCount 校验购买/加购数量：正整数且 ≤ MaxOrderItemCount
// （400，文案点名数量字段与合法范围）。
func ValidateOrderItemCount(count int) error {
	if count <= 0 {
		return xerr.BadRequest("数量必须为正整数")
	}
	if count > MaxOrderItemCount {
		return xerr.BadRequest("数量不能超过 999")
	}
	return nil
}

// OrderItemLine 是金额试算的最小明细（下单/试算共用：现价 × 数量）。
type OrderItemLine struct {
	Price money.Money
	Count int
}

// OrderItemSubtotal 订单项小计：subtotal = price × count（Money 分值整数乘，
// 无浮点；count 已过 ValidateOrderItemCount 正整数校验）。
func OrderItemSubtotal(price money.Money, count int) money.Money {
	return money.FromCents(price.Cents() * int64(count))
}

// OrderAmounts 是订单金额三列的试算结果（落库与结算页展示共用同一口径）。
type OrderAmounts struct {
	TotalQuantity int
	ItemMoney     money.Money
	DiscountMoney money.Money
	Postage       money.Money
	PayMoney      money.Money
}

// ComputeOrderAmount 金额三列试算（spec §3.4 金额不变量，服务端定，客户端
// 金额不采纳）：item_money = Σ subtotal；pay_money = item_money −
// discount_money + postage；total_quantity = Σ count。MVP 的 discount/postage
// 恒 0（spec §7 裁决 8）由调用方传零值落列，公式一般性不随票翻工。
func ComputeOrderAmount(items []OrderItemLine, discount, postage money.Money) OrderAmounts {
	var (
		totalQty  int
		itemCents int64
	)
	for _, it := range items {
		totalQty += it.Count
		itemCents += OrderItemSubtotal(it.Price, it.Count).Cents()
	}
	itemMoney := money.FromCents(itemCents)
	payCents := itemCents - discount.Cents() + postage.Cents()
	return OrderAmounts{
		TotalQuantity: totalQty,
		ItemMoney:     itemMoney,
		DiscountMoney: discount,
		Postage:       postage,
		PayMoney:      money.FromCents(payCents),
	}
}
