package api

import (
	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 产品 Excel 导入导出（issue #20）----------

// ImportProducts 导入产品（multipart：file）。
func (h *Handler) ImportProducts(c *gin.Context) {
	file, ok := h.receiveImportFile(c)
	if !ok {
		return
	}
	defer file.Close()
	count, err := h.svc.ImportProducts(c.Request.Context(), file, h.location)
	if err != nil {
		response.ImportFailed(c, err)
		return
	}
	response.OK(c, gin.H{"count": count})
}

// ProductImportTemplate 下载产品导入模板。
func (h *Handler) ProductImportTemplate(c *gin.Context) {
	data, err := h.svc.ProductImportTemplate()
	if err != nil {
		response.Fail(c, err)
		return
	}
	sendExcel(c, "product-import-template", "产品导入模板", data)
}

// ExportProducts 导出产品（复用列表筛选）。
func (h *Handler) ExportProducts(c *gin.Context) {
	var req dto.ProductPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportProducts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	sendExcel(c, "products", "产品列表", data)
}
