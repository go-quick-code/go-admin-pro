package api

import (
	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 回款计划 Excel 导出（issue #20）----------

// ExportReceivablePlans 导出回款计划（复用列表筛选与数据范围）。
func (h *Handler) ExportReceivablePlans(c *gin.Context) {
	var req dto.ReceivablePlanPageReq
	if !bindQuery(c, &req) {
		return
	}
	data, err := h.svc.ExportReceivablePlans(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	sendExcel(c, "receivable-plans", "回款计划", data)
}
