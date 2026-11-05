package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// DashboardSummary 管理后台首页统计。
func (h *Handler) DashboardSummary(c *gin.Context) {
	data, err := h.svc.DashboardSummary(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, data)
}
