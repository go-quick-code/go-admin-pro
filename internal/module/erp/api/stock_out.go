package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 其它出库单（issue #90，docs/erp-mvp-spec.md §3.5/§4.1/§5）----------
// 与其它入库单镜像同构；出库审核的余额不足 409 提示产品/仓库/当前库存/变更量
//（spec §4.2），审核/反审核权限码独立 erp:stock-out:approve。

// PageStockOuts 其它出库单分页。
func (h *Handler) PageStockOuts(c *gin.Context) {
	var req dto.StockOutPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListStockOuts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetStockOut 其它出库单详情（带头 + 明细）。
func (h *Handler) GetStockOut(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetStockOut(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateStockOut 新建其它出库单（未审核），返回完整主表。
func (h *Handler) CreateStockOut(c *gin.Context) {
	var req dto.StockOutCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateStockOut(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateStockOut 修改其它出库单（仅未审核）。
func (h *Handler) UpdateStockOut(c *gin.Context) {
	var req dto.StockOutUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockOut(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateStockOutStatus 审核（status 20）/反审核（status 10），幂等目标态。
func (h *Handler) UpdateStockOutStatus(c *gin.Context) {
	var req dto.StockDocUpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateStockOutStatus(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteStockOut 删除其它出库单（仅未审核）。
func (h *Handler) DeleteStockOut(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteStockOut(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportStockOuts 导出其它出库单（复用列表筛选与数据范围口径）。
func (h *Handler) ExportStockOuts(c *gin.Context) {
	var req dto.StockOutPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportStockOuts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-stock-outs", "其它出库单", data)
}

// RegisterStockOutRoutes 注册其它出库单路由（静态路由先于 /:id）。
func (h *Handler) RegisterStockOutRoutes(rg *gin.RouterGroup) {
	doc := rg.Group("/erp/stock-out")
	doc.GET("/page", h.requirePermission("erp:stock-out:query"), h.PageStockOuts)
	doc.GET("/export-excel", h.requirePermission("erp:stock-out:export"), h.ExportStockOuts)
	doc.PUT("/update-status", h.requirePermission("erp:stock-out:approve"), h.UpdateStockOutStatus)
	doc.GET("/:id", h.requirePermission("erp:stock-out:query"), h.GetStockOut)
	doc.POST("", h.requirePermission("erp:stock-out:create"), h.CreateStockOut)
	doc.PUT("", h.requirePermission("erp:stock-out:update"), h.UpdateStockOut)
	doc.DELETE("/:id", h.requirePermission("erp:stock-out:delete"), h.DeleteStockOut)
}
