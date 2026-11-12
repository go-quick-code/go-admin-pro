package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 退款单（租户级，issue #135） ----------

// RefundCreateReq 创建退款单（POST /pay/refund/create，登录）。
//
// 调用方契约（同 #133 下单）：本端点的调用方是「消费方业务服务端」（示例
// 订单/充值等业务流转发，或测试直调）。订单按 appKey + merchantOrderId 定位，
// 渠道一律原路退回（channel_code = 订单支付渠道，请求不收渠道）；refundPrice
// 由消费方服务端传入并自负其责（累计 ≤ 实付由服务端裁决 409）；notifyUrl
// 缺省取应用 refund_notify_url 快照；TK 单号服务端发放（请求不收 no）。
type RefundCreateReq struct {
	AppKey           string      `json:"appKey" binding:"required,max=64"`
	MerchantOrderID  string      `json:"merchantOrderId" binding:"required,max=64"`
	MerchantRefundID string      `json:"merchantRefundId" binding:"required,max=64"`
	RefundPrice      money.Money `json:"refundPrice"`
	Reason           string      `json:"reason" binding:"max=255"`
	NotifyURL        string      `json:"notifyUrl" binding:"max=500"`
}

// RefundPageReq 退款单分页筛选（管理端 GET /pay/refund/page，权限 pay:refund:query）。
type RefundPageReq struct {
	PageNo           int    `form:"pageNo"`
	PageSize         int    `form:"pageSize"`
	No               string `form:"no"`
	MerchantRefundID string `form:"merchantRefundId"`
	MerchantOrderID  string `form:"merchantOrderId"`
	AppID            int64  `form:"appId"`
	OrderID          int64  `form:"orderId"`
	ChannelCode      string `form:"channelCode"`
	Status           *int8  `form:"status"`
}

// RefundDetail 退款单详情（GET /pay/refund/:id）：退款单 + 关联支付订单
// （前端详情 drawer 展示关联订单信息，spec §6）。
type RefundDetail struct {
	*model.Refund
	Order *model.Order `json:"order"`
}
