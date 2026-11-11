package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// order.go 是交易与回调域实体（issue #133，docs/pay-mvp-spec.md §3.2/§3.3）：
// 支付订单（merchant_order_id 幂等）、支付发起 extension（一次尝试一条）、
// 商户回调任务（支付成功入账函数创建 type=1；job 重试与日志是 #134）。
// 退款单/转账单/回调日志三表随迁移 00037 建齐（schema only），模型与业务
// 由 #135/#136 落地。全部租户级：租户插件自动隔离，跨租户业务 ID 一律 404。

// 订单状态（status，spec §3.2，对齐芋道枚举）。
const (
	OrderStatusPending  int8 = 0  // 待支付
	OrderStatusSuccess  int8 = 10 // 成功
	OrderStatusRefunded int8 = 20 // 已退款
	OrderStatusClosed   int8 = 30 // 已关闭
)

// 支付发起 extension 状态（spec §3.2）。
const (
	ExtensionStatusPending int8 = 0  // 处理中
	ExtensionStatusSuccess int8 = 10 // 成功
	ExtensionStatusFail    int8 = 20 // 失败
)

// 商户回调任务类型（type，spec §3.3）与状态。
const (
	NotifyTypePayment  int8 = 1 // 支付
	NotifyTypeRefund   int8 = 2 // 退款（#135）
	NotifyTypeTransfer int8 = 3 // 转账（#136）

	NotifyTaskStatusWaiting int8 = 0 // 等待
	NotifyTaskStatusSuccess int8 = 10
)

// NotifyTaskMaxTimes 是商户回调最大通知次数（首次 + 8 重试，spec §4.5）。
const NotifyTaskMaxTimes = 9

// Order 是支付订单（租户级）：merchant_order_id 是消费方单号幂等键
// （UNIQUE(tenant_id, app_id, merchant_order_id)，撞键返回原单不报错）。
// price 实付金额服务端定（消费方服务端传入并自负其责，spec §4.2——收银台/
// submit 不收金额）；no 为 ZF 单号（pay_sequence 事务内发放，客户端提交的
// no 一律不采纳，spec §4.9）。channel_code/channel_user_id/channel_order_no/
// extension_id 在 submit 时回写（下单时为空/0）。
type Order struct {
	BaseEntity
	TenantID        int64       `gorm:"not null;default:1" json:"tenantId"`
	AppID           int64       `gorm:"not null" json:"appId"`
	No              string      `gorm:"size:32;not null" json:"no"`
	MerchantOrderID string      `gorm:"size:64;not null" json:"merchantOrderId"`
	UserID          int64       `gorm:"not null" json:"userId"`
	UserType        int8        `gorm:"not null" json:"userType"`
	Subject         string      `gorm:"size:100;not null" json:"subject"`
	Body            string      `gorm:"size:500" json:"body"`
	NotifyURL       string      `gorm:"size:500" json:"notifyUrl"`
	Price           money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	Status          int8        `gorm:"not null;default:0" json:"status"`
	ChannelCode     string      `gorm:"size:32;not null;default:''" json:"channelCode"`
	ChannelUserID   string      `gorm:"size:64;not null;default:''" json:"channelUserId"`
	ChannelOrderNo  string      `gorm:"size:64;not null;default:''" json:"channelOrderNo"`
	ChannelFeeRate  string      `gorm:"type:decimal(5,2);not null;default:0.00" json:"channelFeeRate"`
	ChannelFeePrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"channelFeePrice"`
	ExtensionID     int64       `gorm:"not null;default:0" json:"extensionId"`
	UserIP          string      `gorm:"size:64" json:"userIp"`
	ExpireTime      *time.Time  `gorm:"" json:"expireTime"`
	SuccessTime     *time.Time  `gorm:"" json:"successTime"`
	RefundPrice     money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"refundPrice"`
}

func (Order) TableName() string { return "pay_order" }

// OrderExtension 是一次支付发起尝试（submit 一条）：no = 订单号-发起序
// （ZF...-1，租户内 UNIQUE）；status 处理中 → 成功/失败；channel_notify_data
// 落末次回调原文（mock 模拟回调与真实渠道回调同口径）。
type OrderExtension struct {
	BaseEntity
	TenantID          int64  `gorm:"not null;default:1" json:"tenantId"`
	No                string `gorm:"size:64;not null" json:"no"`
	OrderID           int64  `gorm:"not null" json:"orderId"`
	ChannelCode       string `gorm:"size:32;not null" json:"channelCode"`
	UserIP            string `gorm:"size:64" json:"userIp"`
	Status            int8   `gorm:"not null;default:0" json:"status"`
	ChannelExtras     string `gorm:"type:text" json:"channelExtras"`
	ChannelErrorCode  string `gorm:"size:64" json:"channelErrorCode"`
	ChannelErrorMsg   string `gorm:"size:255" json:"channelErrorMsg"`
	ChannelNotifyData string `gorm:"type:text" json:"channelNotifyData"`
}

func (OrderExtension) TableName() string { return "pay_order_extension" }

// NotifyTask 是商户回调任务（spec §3.3/§4.5）：支付/退款/转账成功统一建任务
// （type=1/2/3），notify_url 为下单时快照；max_notify_times=9，重试序列与
// 执行日志是 #134 的 job 职责。
type NotifyTask struct {
	BaseEntity
	TenantID           int64      `gorm:"not null;default:1" json:"tenantId"`
	AppID              int64      `gorm:"not null" json:"appId"`
	Type               int8       `gorm:"not null" json:"type"`
	DataID             int64      `gorm:"not null" json:"dataId"`
	MerchantOrderID    string     `gorm:"size:64" json:"merchantOrderId"`
	MerchantRefundID   string     `gorm:"size:64" json:"merchantRefundId"`
	MerchantTransferID string     `gorm:"size:64" json:"merchantTransferId"`
	Status             int8       `gorm:"not null;default:0" json:"status"`
	NotifyURL          string     `gorm:"size:500;not null" json:"notifyUrl"`
	NextNotifyTime     *time.Time `gorm:"" json:"nextNotifyTime"`
	LastExecuteTime    *time.Time `gorm:"" json:"lastExecuteTime"`
	NotifyTimes        int        `gorm:"not null;default:0" json:"notifyTimes"`
	MaxNotifyTimes     int        `gorm:"not null;default:9" json:"maxNotifyTimes"`
}

func (NotifyTask) TableName() string { return "pay_notify_task" }
