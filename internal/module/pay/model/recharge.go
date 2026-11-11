package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// recharge.go 是充值域实体（issue #136，docs/pay-mvp-spec.md §3.4/§4.7）：
// 充值套餐（档位）与充值单（挂一笔支付订单、套餐快照进单）。
//
// 充值闭环（spec §4.7）：本人发起充值（套餐快照或自定义 pay_price）→ 同事务
// 挂 pay_order（subject=钱包充值，本人 user_id/user_type）→ 收银台支付（钱包
// 渠道被 submit 反查禁用，防自引用套利）→ 支付成功联动钱包入账
// pay_price + bonus_price（biz=1，total_recharge 累计）→ 充值退款走 #135
// 退款机制，成功扣回 refund_total_price（校验扣后 ≥ 0）、三列回写。
// 充值单不提供删除（资金事实 append-only，spec §4.10）。

// 充值单支付状态（pay_status，spec §4.7：支付成功联动置 1；与 pay_order 的
// 0/10/20/30 枚举不同源——芋道 recharge.pay_status 即 0/1 两态）。
const (
	RechargePayStatusPending int8 = 0 // 未支付
	RechargePayStatusSuccess int8 = 1 // 已支付（钱包已入账）
)

// 充值单退款状态（refund_status，spec §3.4）：0 无 / 10 成功 / 20 失败。
// 20 失败 = 退款成功回调发现余额不足以扣回（先校验后退款，spec §4.7），
// 充值单金额列不动、可重新发起；10 成功时 refund_* 三列与 refund_time 回写。
const (
	RechargeRefundStatusNone    int8 = 0  // 无退款
	RechargeRefundStatusSuccess int8 = 10 // 退款成功（已扣回）
	RechargeRefundStatusFail    int8 = 20 // 退款失败（余额不足以扣回，可重试）
)

// WalletRechargePackage 是充值套餐（租户级）：pay_price/bonus_price 档位，
// 建充值单时快照进单（后改套餐不影响已建单，spec 验收）。status 无 default
// 标签（同 App：GORM 零值省略带 default 的字段，0 启用会被 DB 默认值覆盖的坑）。
type WalletRechargePackage struct {
	BaseEntity
	TenantID   int64       `gorm:"not null;default:1" json:"tenantId"`
	Name       string      `gorm:"size:100;not null" json:"name"`
	PayPrice   money.Money `gorm:"type:decimal(12,2);not null" json:"payPrice"`
	BonusPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"bonusPrice"`
	Status     int8        `gorm:"not null" json:"status"`
}

func (WalletRechargePackage) TableName() string { return "pay_wallet_recharge_package" }

// WalletRecharge 是钱包充值单（租户级，append-only 资金事实）：套餐快照
// （package_id 可空 = 自定义充值）+ 支付订单联动（pay_order_id 同事务挂单，
// submit 反查禁钱包渠道）+ 支付/退款回执列。
type WalletRecharge struct {
	BaseEntity
	TenantID   int64       `gorm:"not null;default:1" json:"tenantId"`
	WalletID   int64       `gorm:"not null" json:"walletId"`
	TotalPrice money.Money `gorm:"type:decimal(12,2);not null" json:"totalPrice"`
	PayPrice   money.Money `gorm:"type:decimal(12,2);not null" json:"payPrice"`
	BonusPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"bonusPrice"`
	// PackageID 可空：NULL = 自定义充值（pay_price 服务端定、bonus_price 恒 0）。
	PackageID        *int64      `gorm:"" json:"packageId"`
	PayStatus        int8        `gorm:"not null;default:0" json:"payStatus"`
	PayOrderID       int64       `gorm:"not null" json:"payOrderId"`
	PayChannelCode   string      `gorm:"size:32;not null;default:''" json:"payChannelCode"`
	PayTime          *time.Time  `gorm:"" json:"payTime"`
	PayRefundID      int64       `gorm:"not null;default:0" json:"payRefundId"`
	RefundStatus     int8        `gorm:"not null;default:0" json:"refundStatus"`
	RefundTotalPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"refundTotalPrice"`
	RefundPayPrice   money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"refundPayPrice"`
	RefundBonusPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"refundBonusPrice"`
	RefundTime       *time.Time  `gorm:"" json:"refundTime"`
}

func (WalletRecharge) TableName() string { return "pay_wallet_recharge" }
