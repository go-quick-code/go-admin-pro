package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 仓库（issue #89，docs/erp-mvp-spec.md §3.1/§5）----------
// owner = 创建人（spec §4.10），列表/导出按数据范围过滤；principal 仅展示。

// PageWarehouses 仓库分页（筛选）。
func (h *Handler) PageWarehouses(c *gin.Context) {
	var req dto.WarehousePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListWarehouses(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetWarehouse 仓库详情（不存在 404）。
func (h *Handler) GetWarehouse(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetWarehouse(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateWarehouse 新建仓库，返回完整实体。
func (h *Handler) CreateWarehouse(c *gin.Context) {
	var req dto.WarehouseCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateWarehouse(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateWarehouse 修改仓库（PUT 契约 id 在 body，与 #88 产品/单位/分类一致）。
func (h *Handler) UpdateWarehouse(c *gin.Context) {
	var req dto.WarehouseUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateWarehouse(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteWarehouse 删除仓库（软删除；引用约束骨架见 party_refs.go）。
func (h *Handler) DeleteWarehouse(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteWarehouse(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ExportWarehouses 导出仓库（复用列表筛选与数据范围口径，见 party_excel.go）。
func (h *Handler) ExportWarehouses(c *gin.Context) {
	var req dto.WarehousePageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportWarehouses(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "erp-warehouses", "仓库列表", data)
}

// RegisterWarehouseRoutes 注册仓库路由：在模块 router 的 RegisterProtected 中调用一次。
// 权限码与菜单种子保持一致（erp:warehouse:*）；静态路由先于 /:id 注册。
func (h *Handler) RegisterWarehouseRoutes(rg *gin.RouterGroup) {
	warehouse := rg.Group("/erp/warehouse")
	warehouse.GET("/page", h.requirePermission("erp:warehouse:query"), h.PageWarehouses)
	warehouse.GET("/export-excel", h.requirePermission("erp:warehouse:export"), h.ExportWarehouses)
	warehouse.GET("/:id", h.requirePermission("erp:warehouse:query"), h.GetWarehouse)
	warehouse.POST("", h.requirePermission("erp:warehouse:create"), h.CreateWarehouse)
	warehouse.PUT("", h.requirePermission("erp:warehouse:update"), h.UpdateWarehouse)
	warehouse.DELETE("/:id", h.requirePermission("erp:warehouse:delete"), h.DeleteWarehouse)
}
