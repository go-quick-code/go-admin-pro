package api

import (
	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 客户 Excel 导入导出（issue #20）----------

// ImportCustomers 导入客户（multipart：file）。
func (h *Handler) ImportCustomers(c *gin.Context) {
	file, ok := h.receiveImportFile(c)
	if !ok {
		return
	}
	defer file.Close()
	count, err := h.svc.ImportCustomers(c.Request.Context(), file, h.location)
	if err != nil {
		response.ImportFailed(c, err)
		return
	}
	response.OK(c, gin.H{"count": count})
}

// CustomerImportTemplate 下载客户导入模板。
func (h *Handler) CustomerImportTemplate(c *gin.Context) {
	data, err := h.svc.CustomerImportTemplate()
	if err != nil {
		response.Fail(c, err)
		return
	}
	sendExcel(c, "customer-import-template", "客户导入模板", data)
}

// ExportCustomers 导出客户（复用列表筛选与数据范围）。
func (h *Handler) ExportCustomers(c *gin.Context) {
	var req dto.CustomerPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportCustomers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	sendExcel(c, "customers", "客户列表", data)
}
