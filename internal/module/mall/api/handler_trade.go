package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_trade.go 是 mall 管理端交易端点的 HTTP 入口（issue #180，T7；
// docs/mall-mvp-spec.md §4「交易：订单分页（状态/编号/会员/时间过滤）/
// 详情（含 items）/管理员备注/取消（待支付）/完成（已支付）」）。挂受保护
// 组（RegisterProtected），权限码 mall:trade:{query,cancel,complete,update}
// ——update 用于管理员备注（字段更新语义，无独立码）；取消/完成是业务状态
// 机动作，独立码（对齐 pay approve/close 先例）。租户隔离由租户插件保证
//（跨租户订单 ID 一律 404，不泄露存在性）。

// PageTradeOrders 管理端订单分页（状态/编号模糊/会员 ID/时间范围过滤）。
func (h *Handler) PageTradeOrders(c *gin.Context) {
	var req dto.AdminOrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.AdminPageOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetTradeOrder 管理端订单详情（含订单项成交快照与管理面支付字段全量）。
func (h *Handler) GetTradeOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	resp, err := h.svc.AdminGetOrder(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// UpdateTradeOrderRemark 管理员备注（任意状态可补注，终态也允许）。
func (h *Handler) UpdateTradeOrderRemark(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.AdminOrderRemarkReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.AdminUpdateOrderRemark(c.Request.Context(), id, req.Remark); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// CancelTradeOrder 管理员取消订单（仅待支付，其余 409；库存回补同事务，
// cancel_reason 固定「管理员取消」）。
func (h *Handler) CancelTradeOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.AdminCancelOrder(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// CompleteTradeOrder 管理员完成订单（仅已支付 10→20，走状态机，其余 409）。
func (h *Handler) CompleteTradeOrder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.AdminCompleteOrder(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
