package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_transfer.go 是转账域的 HTTP 入口（issue #137，spec §5/§4.11）：
//   - 发起 POST /pay/transfer/create 只挂登录（消费方服务端自助端点，同
//     #133 下单契约——幂等/渠道校验/金额服务端定在 service 层收口）；
//   - 管理端分页/详情挂 pay:transfer:query（交易单据 append-only 无写入端点
//     之外的入口，spec §4.10；导出随 #138 export-excel 补 pay:transfer:export）。

// CreateTransfer 发起转账（消费方服务端端点，登录）。信任模型边界（PR #140
// 安全审查 P3-1，记录式接受）：本端点与订单发起同型，是「消费方服务端 ↔
// 支付服务端」的服务端到服务端契约（spec §4.8/§4.11），仅登录不设 pay 权限
// 码；wallet 渠道入账目标钱包即消费方职责内的资金指令。任何非演示渠道注册
// 前，消费方接入必须补独立权限码或应用级认证/签名（前置约束见
// service/transfer.go 头注释信任模型边界段）。
func (h *Handler) CreateTransfer(c *gin.Context) {
	var req dto.TransferCreateReq
	if !bindJSON(c, &req) {
		return
	}
	transfer, err := h.svc.CreateTransfer(c.Request.Context(), &req, c.ClientIP())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, transfer)
}

// PageTransfers 转账单分页（管理端）。
func (h *Handler) PageTransfers(c *gin.Context) {
	var req dto.TransferPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListTransfers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetTransfer 转账单详情（附关联支付应用）。
func (h *Handler) GetTransfer(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetTransfer(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}
