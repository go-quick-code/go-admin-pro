package model

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// refund.go 是退款单实体（issue #135，docs/pay-mvp-spec.md §3.2/§4.4）：
// 挂 10 成功订单发起（原路退回，channel_code = 订单支付渠道），merchant_refund_id
// 是消费方退款单号幂等键（UNIQUE(tenant_id, app_id, merchant_refund_id)，撞键
// 返回原单不报错）。pay_price 为原单实付快照；no 为 TK 单号（pay_sequence
// 事务内发放，客户端提交的 no 一律不采纳，spec §4.9）。

// 退款单状态（status，spec §3.2/§4.4，对齐芋道枚举）。
const (
	RefundStatusPending int8 = 0  // 待处理（异步渠道预留：等渠道回调）
	RefundStatusSuccess int8 = 10 // 成功（wallet/mock 同步完成）
	RefundStatusFail    int8 = 20 // 失败（mock 失败分支供测试）
)

// Refund 是退款单（租户级）：user_id/user_type/channel_order_no 为原订单
// 快照（原路退回的定位与回执）；success_time 退款成功时间；channel_error_*
// 失败分支错误列；channel_notify_data 末次回调原文（mock 与真实渠道回调
// 同口径落库）。
type Refund struct {
	BaseEntity
	TenantID          int64       `gorm:"not null;default:1" json:"tenantId"`
	No                string      `gorm:"size:32;not null" json:"no"`
	AppID             int64       `gorm:"not null" json:"appId"`
	OrderID           int64       `gorm:"not null" json:"orderId"`
	OrderNo           string      `gorm:"size:32;not null" json:"orderNo"`
	MerchantOrderID   string      `gorm:"size:64;not null" json:"merchantOrderId"`
	MerchantRefundID  string      `gorm:"size:64;not null" json:"merchantRefundId"`
	UserID            int64       `gorm:"not null" json:"userId"`
	UserType          int8        `gorm:"not null" json:"userType"`
	ChannelCode       string      `gorm:"size:32;not null" json:"channelCode"`
	NotifyURL         string      `gorm:"size:500" json:"notifyUrl"`
	Status            int8        `gorm:"not null;default:0" json:"status"`
	PayPrice          money.Money `gorm:"type:decimal(12,2);not null" json:"payPrice"`
	RefundPrice       money.Money `gorm:"type:decimal(12,2);not null" json:"refundPrice"`
	Reason            string      `gorm:"size:255" json:"reason"`
	UserIP            string      `gorm:"size:64" json:"userIp"`
	ChannelOrderNo    string      `gorm:"size:64" json:"channelOrderNo"`
	ChannelRefundNo   string      `gorm:"size:64" json:"channelRefundNo"`
	SuccessTime       *time.Time  `gorm:"" json:"successTime"`
	ChannelErrorCode  string      `gorm:"size:64" json:"channelErrorCode"`
	ChannelErrorMsg   string      `gorm:"size:255" json:"channelErrorMsg"`
	ChannelNotifyData string      `gorm:"type:text" json:"channelNotifyData"`
}

func (Refund) TableName() string { return "pay_refund" }
