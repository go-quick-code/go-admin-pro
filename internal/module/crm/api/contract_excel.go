package api

import (
	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 合同 Excel 导出（issue #20）----------

// ExportContracts 导出合同（复用列表筛选与数据范围）。
func (h *Handler) ExportContracts(c *gin.Context) {
	var req dto.ContractPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportContracts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	h.sendExcel(c, "contracts", "合同列表", data)
}
