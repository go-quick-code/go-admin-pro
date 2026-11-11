package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_order.go 是订单与收银台的 HTTP 入口（issue #133，spec §5/§4.2）：
//   - 下单/收银台/submit/mock 触发是自助端点（只挂登录，user_id 归属过滤在
//     service 层强制，spec §4.11）；
//   - 管理端分页/详情挂 pay:order:query。
//
// 金额服务端定（spec §4.2）：POST /pay/order/create 的调用方是消费方业务
// 服务端——price 即业务价，由消费方服务端传入并自负其责；收银台与 submit
// 的请求体不含任何金额字段，浏览器侧没有覆盖价格的路径。expire_time 一律
// 服务端设定（请求不收）。

// CreateOrder 创建支付订单（登录）：merchant_order_id 幂等，撞键返回原单。
// 见 OrderCreateReq 注释的调用方契约。
func (h *Handler) CreateOrder(c *gin.Context) {
	var req dto.OrderCreateReq
	if !bindJSON(c, &req) {
		return
	}
	order, err := h.svc.CreateOrder(c.Request.Context(), &req, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, order)
}

// GetCashierOrder 收银台数据（登录 + 本人订单）：订单 + 可用渠道。
func (h *Handler) GetCashierOrder(c *gin.Context) {
	resp, err := h.svc.GetCashierOrder(c.Request.Context(), c.Query("appKey"), c.Query("merchantOrderId"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// SubmitOrder 发起支付（登录 + 本人订单）：四重校验后建 extension 并调渠道。
func (h *Handler) SubmitOrder(c *gin.Context) {
	var req dto.OrderSubmitReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.SubmitOrder(c.Request.Context(), &req, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// MockNotifyOrder 模拟渠道回调（登录 + 本人订单 + 渠道为 mock；不公开端点）。
func (h *Handler) MockNotifyOrder(c *gin.Context) {
	var req dto.MockNotifyReq
	if !bindJSON(c, &req) {
		return
	}
	order, err := h.svc.MockNotifyOrder(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, order)
}

// PageOrders 订单分页（管理端，pay:order:query）。
func (h *Handler) PageOrders(c *gin.Context) {
	var req dto.OrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetOrder 订单详情（管理端，pay:order:query；含最近一次发起 extension）。
func (h *Handler) GetOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetOrder(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}
