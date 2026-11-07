package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 付款单（FK，issue #94，docs/erp-mvp-spec.md §3.6/§4.7/§5）----------
// 收款单镜像：明细只挂已审核采购入库/退货（bizType=purchase_in/purchase_return）；
// 审核时校验核销上限并回写被核销单据已付/已退金额，反审核重算释放额度。

// PageFinancePayments 付款单分页。
func (h *Handler) PageFinancePayments(c *gin.Context) {
	var req dto.FinancePaymentPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListFinancePayments(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetFinancePayment 付款单详情（头 + 明细 + 被核销单据单号）。
func (h *Handler) GetFinancePayment(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetFinancePayment(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateFinancePayment 新建付款单（未审核）。
func (h *Handler) CreateFinancePayment(c *gin.Context) {
	var req dto.FinancePaymentCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateFinancePayment(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateFinancePayment 修改付款单（仅未审核；PUT 契约 id 在 body）。
func (h *Handler) UpdateFinancePayment(c *gin.Context) {
	var req dto.FinancePaymentUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateFinancePayment(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateFinancePaymentStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateFinancePaymentStatus(c *gin.Context) {
	var req dto.FinanceDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateFinancePaymentStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteFinancePayment 删除付款单（仅未审核）。
func (h *Handler) DeleteFinancePayment(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteFinancePayment(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportFinancePayments 导出付款单。
func (h *Handler) ExportFinancePayments(c *gin.Context) {
	var req dto.FinancePaymentPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportFinancePayments(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-finance-payments", "付款单", data)
}

// RegisterFinancePaymentRoutes 注册付款单路由（静态路由先于 /:id）。
func (h *Handler) RegisterFinancePaymentRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/finance-payment")
	doc.GET("/page", h.requirePermission("erp:finance-payment:query"), h.PageFinancePayments)
	doc.GET("/export-excel", h.requirePermission("erp:finance-payment:export"), h.ExportFinancePayments)
	doc.PUT("/update-status", h.requirePermission("erp:finance-payment:approve"), h.UpdateFinancePaymentStatus)
	doc.GET("/:id", h.requirePermission("erp:finance-payment:query"), h.GetFinancePayment)
	doc.POST("", h.requirePermission("erp:finance-payment:create"), h.CreateFinancePayment)
	doc.PUT("", h.requirePermission("erp:finance-payment:update"), h.UpdateFinancePayment)
	doc.DELETE("/:id", h.requirePermission("erp:finance-payment:delete"), h.DeleteFinancePayment)
}
