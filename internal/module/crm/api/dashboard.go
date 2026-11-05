package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// DashboardSummary CRM 仪表盘汇总（issue #19）：4 卡片 + 商机阶段漏斗 +
// 近 12 个月合同/回款趋势，全部套用当前用户数据范围（spec §4.1/§6）。
// 金额响应为十进制字符串（internal/pkg/money）。
func (h *Handler) DashboardSummary(c *gin.Context) {
	summary, err := h.svc.DashboardSummary(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, summary)
}
