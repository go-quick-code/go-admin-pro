package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// demo.go 是示例业务实体（issue #137，docs/pay-mvp-spec.md §3.5/§4.8）：
// 示例订单（下单→收银台支付→退款全链路演示）与示例提现（申请→审批→转账→
// 回调联动全链路演示）。两表是 pay 模块内唯一消费方（ADR 0021）的消费行：
// 状态联动由 #134 商户回调链路（demo 回调端点 → SetDemoNotifyHandler）回写
//——task → job → demo 端点是唯一权威链路，发起时直写联动列会绕过回调语义。

// 示例订单支付状态（pay_status，spec §3.5：与充值单同源 0/1 两态，不是
// pay_order 的 0/10/20/30 枚举）。
const (
	DemoOrderPayStatusPending int8 = 0 // 未支付
	DemoOrderPayStatusSuccess int8 = 1 // 已支付
)

// 示例提现状态（status，spec §3.5/§4.8）：审批不设状态中转——审批创建转账单
// 后提现单仍为 0 等待（转账成功回调联动置 10），关闭/拒绝直接置 20。
const (
	DemoWithdrawStatusWaiting int8 = 0  // 等待（申请后 / 转账在途）
	DemoWithdrawStatusSuccess int8 = 10 // 成功（转账成功回调联动）
	DemoWithdrawStatusClosed  int8 = 20 // 关闭（拒绝/关闭，不建转账单）
)

// 示例提现类型（type，spec §3.5）：2 支付宝预留，v1 创建即拒绝（无渠道可转）。
const (
	DemoWithdrawTypeWallet int8 = 1 // 钱包
	DemoWithdrawTypeAlipay int8 = 2 // 支付宝（预留）
)

// DemoOrder 是示例订单（租户级）：spu_id 取硬编码字典（不建表，spec §3.5），
// price 服务端定（字典价，请求不收金额）；pay_order_id 为创建时同事务挂的
// 支付订单（merchant_order_id = DEMO-<id>，照 #136 充值挂单形态）。退款/支付
// 回执列由回调联动回写：pay_refund_id/refund_price/refund_time（type=2）、
// pay_status/pay_time/pay_channel_code（type=1）。
type DemoOrder struct {
	BaseEntity
	TenantID       int64       `gorm:"not null;default:1" json:"tenantId"`
	UserID         int64       `gorm:"not null" json:"userId"`
	SpuID          int64       `gorm:"not null" json:"spuId"`
	SpuName        string      `gorm:"size:100;not null" json:"spuName"`
	Price          money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	PayStatus      int8        `gorm:"not null;default:0" json:"payStatus"`
	PayOrderID     int64       `gorm:"not null;default:0" json:"payOrderId"`
	PayTime        *time.Time  `gorm:"" json:"payTime"`
	PayChannelCode string      `gorm:"size:32;not null;default:''" json:"payChannelCode"`
	PayRefundID    int64       `gorm:"not null;default:0" json:"payRefundId"`
	RefundPrice    money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"refundPrice"`
	RefundTime     *time.Time  `gorm:"" json:"refundTime"`
}

func (DemoOrder) TableName() string { return "pay_demo_order" }

// DemoWithdraw 是示例提现（租户级）：申请（user_id/user_account/user_name
// 服务端取登录主体）→ 管理端审批（创建转账单，merchant_transfer_id =
// WD-<id>，pay_transfer_id 回写）→ 转账成功回调联动 10 成功（transfer_time/
// transfer_channel_code 回写）；关闭/拒绝置 20 不建转账单。
type DemoWithdraw struct {
	BaseEntity
	TenantID            int64       `gorm:"not null;default:1" json:"tenantId"`
	UserID              int64       `gorm:"not null" json:"userId"`
	Subject             string      `gorm:"size:100;not null" json:"subject"`
	Price               money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	UserAccount         string      `gorm:"size:64;not null" json:"userAccount"`
	UserName            string      `gorm:"size:64" json:"userName"`
	Type                int8        `gorm:"not null;default:1" json:"type"`
	Status              int8        `gorm:"not null;default:0" json:"status"`
	PayTransferID       int64       `gorm:"not null;default:0" json:"payTransferId"`
	TransferChannelCode string      `gorm:"size:32;not null;default:''" json:"transferChannelCode"`
	TransferTime        *time.Time  `gorm:"" json:"transferTime"`
	TransferErrorMsg    string      `gorm:"size:255" json:"transferErrorMsg"`
}

func (DemoWithdraw) TableName() string { return "pay_demo_withdraw" }
