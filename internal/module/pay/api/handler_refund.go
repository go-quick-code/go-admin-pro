package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_refund.go 是退款单的 HTTP 入口（issue #135，spec §5/§4.4）：
//   - 发起退款是消费方服务端自助端点（只挂登录，同 #133 下单契约；订单归属
//     校验在 service 层强制——非本人 403，spec §4.11）；
//   - 管理端分页/详情挂 pay:refund:query（交易单据 append-only 无写入端点，
//     spec §4.10；导出随 #138 export-excel 补 pay:refund:export）。

// CreateRefund 创建退款单（登录）：merchant_refund_id 幂等，撞键返回原单。
// 见 RefundCreateReq 注释的调用方契约。
func (h *Handler) CreateRefund(c *gin.Context) {
	var req dto.RefundCreateReq
	if !bindJSON(c, &req) {
		return
	}
	refund, err := h.svc.CreateRefund(c.Request.Context(), &req, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, refund)
}

// PageRefunds 退款单分页（管理端，pay:refund:query）。
func (h *Handler) PageRefunds(c *gin.Context) {
	var req dto.RefundPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListRefunds(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetRefund 退款单详情（管理端，pay:refund:query；含关联支付订单）。
func (h *Handler) GetRefund(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetRefund(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}
