package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 收款单（SK，issue #94，docs/erp-mvp-spec.md §3.6/§4.7/§5）----------
// 明细只挂已审核销售出库/退货（bizType=sale_out/sale_return）；单号服务端发放；
// 审核时校验核销上限并回写被核销单据已收/已退金额，反审核重算释放额度。

// PageFinanceReceipts 收款单分页。
func (h *Handler) PageFinanceReceipts(c *gin.Context) {
	var req dto.FinanceReceiptPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListFinanceReceipts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetFinanceReceipt 收款单详情（头 + 明细 + 被核销单据单号）。
func (h *Handler) GetFinanceReceipt(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetFinanceReceipt(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateFinanceReceipt 新建收款单（未审核）。
func (h *Handler) CreateFinanceReceipt(c *gin.Context) {
	var req dto.FinanceReceiptCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateFinanceReceipt(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateFinanceReceipt 修改收款单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdateFinanceReceipt(c *gin.Context) {
	var req dto.FinanceReceiptUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateFinanceReceipt(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateFinanceReceiptStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateFinanceReceiptStatus(c *gin.Context) {
	var req dto.FinanceDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateFinanceReceiptStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteFinanceReceipt 删除收款单（仅未审核）。
func (h *Handler) DeleteFinanceReceipt(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteFinanceReceipt(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportFinanceReceipts 导出收款单。
func (h *Handler) ExportFinanceReceipts(c *gin.Context) {
	var req dto.FinanceReceiptPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportFinanceReceipts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-finance-receipts", "收款单", data)
}

// RegisterFinanceReceiptRoutes 注册收款单路由（静态路由先于 /:id）。
func (h *Handler) RegisterFinanceReceiptRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/finance-receipt")
	doc.GET("/page", h.requirePermission("erp:finance-receipt:query"), h.PageFinanceReceipts)
	doc.GET("/export-excel", h.requirePermission("erp:finance-receipt:export"), h.ExportFinanceReceipts)
	doc.PUT("/update-status", h.requirePermission("erp:finance-receipt:approve"), h.UpdateFinanceReceiptStatus)
	doc.GET("/:id", h.requirePermission("erp:finance-receipt:query"), h.GetFinanceReceipt)
	doc.POST("", h.requirePermission("erp:finance-receipt:create"), h.CreateFinanceReceipt)
	doc.PUT("", h.requirePermission("erp:finance-receipt:update"), h.UpdateFinanceReceipt)
	doc.DELETE("/:id", h.requirePermission("erp:finance-receipt:delete"), h.DeleteFinanceReceipt)
}
