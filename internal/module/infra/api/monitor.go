package api

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 监控面板（issue #84，spec §4.10/§5.8） ----------
//
// 两个端点都挂 infra:monitor:query；「仅平台租户」由 service 层收口
//（非平台租户即使持码也 403），与数据库文档同模式。只读聚合，
// 不引入 Prometheus/pprof/链路追踪（spec §4.10.3）。

// MonitorOverview 监控总览：应用/运行期/连接池/调度器/租户规模/流量/消息。
func (h *Handler) MonitorOverview(c *gin.Context) {
	data, err := h.svc.MonitorOverview(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, data)
}

// MonitorRequests 按小时分桶聚合访问日志。hours 非法时由 service 收敛到
// [1,168]（缺省 24），不报 400；空数据返回空数组。
func (h *Handler) MonitorRequests(c *gin.Context) {
	// hours 是可选整数参数：非数字按缺省 24 处理（宽松解析，避免测试工具
	// 传空串/缺参时 400）；越界由 service 收敛。
	hours, _ := strconv.Atoi(c.Query("hours"))
	data, err := h.svc.MonitorRequests(c.Request.Context(), hours)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, data)
}
