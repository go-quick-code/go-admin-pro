package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// ---------- 支付订单与收银台（租户级，issue #133） ----------

// OrderCreateReq 创建支付订单（POST /pay/order/create，登录）。
//
// 调用方契约（金额服务端定，spec §4.2）：本端点的调用方是「消费方业务
// 服务端」（示例订单/充值等业务流转发，或测试直调）——price 即业务价，
// 由消费方服务端传入并自负其责；收银台与 submit 不收金额，浏览器侧没有
// 任何覆盖价格的路径。notifyUrl 缺省取应用 order_notify_url 快照；
// expireTime 服务端设定（请求不收）。
type OrderCreateReq struct {
	AppKey          string      `json:"appKey" binding:"required,max=64"`
	MerchantOrderID string      `json:"merchantOrderId" binding:"required,max=64"`
	Subject         string      `json:"subject" binding:"required,max=100"`
	Body            string      `json:"body" binding:"max=500"`
	Price           money.Money `json:"price"`
	NotifyURL       string      `json:"notifyUrl" binding:"max=500"`
}

// CashierChannel 是收银台可用渠道（应用绑定 ∧ 平台启用 ∧ 渠道已实现，spec §4.2）。
type CashierChannel struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// CashierOrderResp 收银台数据（GET /pay/cashier/order?appKey&merchantOrderId）：
// 订单本人可见 + 可用渠道清单（前端渲染渠道选择与 mock 模拟回调按钮）。
type CashierOrderResp struct {
	*model.Order
	Channels []CashierChannel `json:"channels"`
}

// OrderSubmitReq 发起支付（POST /pay/order/submit，本人订单）：四重校验
// （订单待支付未过期 / 应用已绑定 / 平台启用 / 渠道已实现）后创建 extension
// 并调用渠道 Order。不收金额（spec §4.2 金额服务端定）。
type OrderSubmitReq struct {
	OrderID     int64  `json:"orderId" binding:"required"`
	ChannelCode string `json:"channelCode" binding:"required,max=32"`
}

// OrderSubmitResp 发起支付结果：wallet 渠道同步成功（status=10）；
// mock 渠道停在待支付（status=0，displayMode=none，前端展示模拟回调按钮）。
type OrderSubmitResp struct {
	OrderID     int64  `json:"orderId"`
	ExtensionID int64  `json:"extensionId"`
	ExtensionNo string `json:"extensionNo"`
	Status      int8   `json:"status"`
	DisplayMode string `json:"displayMode"`
	Content     string `json:"content"`
}

// MockNotifyReq 模拟渠道回调（POST /pay/mock/notify，登录 + 本人订单 + 渠道
// 为 mock）：收银台「模拟回调」按钮触发；成功分支走与真实渠道回调完全相同
// 的解析 → 入账 → 建任务链路，失败分支置 extension 失败（订单保持待支付）。
type MockNotifyReq struct {
	OrderID int64  `json:"orderId" binding:"required"`
	Result  string `json:"result" binding:"required,oneof=success failure"`
}

// OrderPageReq 订单分页筛选（管理端 GET /pay/order/page，权限 pay:order:query）。
type OrderPageReq struct {
	PageNo          int    `form:"pageNo"`
	PageSize        int    `form:"pageSize"`
	No              string `form:"no"`
	MerchantOrderID string `form:"merchantOrderId"`
	AppID           int64  `form:"appId"`
	ChannelCode     string `form:"channelCode"`
	Status          *int8  `form:"status"`
}

// OrderDetail 订单详情（GET /pay/order/:id）：订单 + 最近一次发起 extension。
type OrderDetail struct {
	*model.Order
	Extension *model.OrderExtension `json:"extension"`
}
