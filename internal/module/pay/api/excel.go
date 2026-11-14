package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- Excel 导出（issue #138，docs/pay-mvp-spec.md §4.12）----------
// 五个管理列表导出（订单/退款/转账/钱包流水/充值单）：复用 internal/pkg/excel
// 的 Export[T]（Money 经 TextMarshaler 输出十进制字符串，日期 NativeDates 按业务
// 时区），导出复用各自列表的筛选 DTO 与租户隔离口径；权限码 pay:<entity>:export
//（spec §7）。渠道凭证/回调报文/发起 extension 等内部列不进导出（spec §7 安全
// 审查「日志与 Excel 不落凭证」）。本文件只放共享的发文件助手（响应构造单点化
// 为 response.SendXLSX，spec #144）与五个导出 handler。

// sendExcel 输出 xlsx 附件：ASCII 文件名兼容旧客户端，filename* 提供中文名。
// 文件名日期键按业务时区（ADR 0006），不依赖服务器 TZ。
// 响应构造已单点化为 response.SendXLSX（spec #144），此处仅保留一行薄委托，
// 调用点零改动。
func (h *Handler) sendExcel(c *gin.Context, base, title string, data []byte) {
	response.SendXLSX(c, h.location, base, title, data)
}

// ExportOrders 导出支付订单列表（pay:order:export，复用订单列表筛选）。
func (h *Handler) ExportOrders(c *gin.Context) {
	var req dto.OrderPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportOrders(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "pay-orders", "支付订单", data)
}

// ExportRefunds 导出退款单列表（pay:refund:export，复用退款列表筛选）。
func (h *Handler) ExportRefunds(c *gin.Context) {
	var req dto.RefundPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportRefunds(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "pay-refunds", "退款单", data)
}

// ExportTransfers 导出转账单列表（pay:transfer:export，复用转账列表筛选）。
func (h *Handler) ExportTransfers(c *gin.Context) {
	var req dto.TransferPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportTransfers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "pay-transfers", "转账单", data)
}

// ExportWalletTransactions 导出钱包流水列表（pay:wallet:export，spec §7 流水
// 导出挂钱包权限码对；复用流水分页筛选）。
func (h *Handler) ExportWalletTransactions(c *gin.Context) {
	var req dto.WalletTransactionPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportWalletTransactions(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "pay-wallet-transactions", "钱包流水", data)
}

// ExportRecharges 导出充值单列表（pay:wallet-recharge:export，复用充值单列表筛选）。
func (h *Handler) ExportRecharges(c *gin.Context) {
	var req dto.RechargePageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportRecharges(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "pay-wallet-recharges", "充值单", data)
}
