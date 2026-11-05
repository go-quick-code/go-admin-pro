// workday.go 承载工作日历管理的 HTTP 入口（issue #50，docs/oa-mvp-spec.md §3.3/
// §5 OA 考勤节）：按月查看、单日设置、批量设置、清除恢复默认。
// 权限码 oa:workday:* 与菜单种子 520 段一致（520 查询、5201 单日、5202 批量、
// 5203 恢复默认）；日期/月份参数在 service 层做严格格式校验（400 文案可读）。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// GetWorkdayMonth 按月查看（GET /oa/workday/month?month=YYYY-MM）：
// 整月逐日三态（显式记录优先、无记录走默认规则）。
func (h *Handler) GetWorkdayMonth(c *gin.Context) {
	month := c.Query("month")
	if month == "" {
		response.BadRequest(c, "月份格式应为 YYYY-MM")
		return
	}
	resp, err := h.svc.GetWorkdayMonth(c.Request.Context(), month)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// SetWorkday 单日设置（PUT /oa/workday，upsert）。
func (h *Handler) SetWorkday(c *gin.Context) {
	var req dto.WorkdaySetReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.SetWorkday(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// BatchWorkday 批量设置（POST /oa/workday/batch）：日期列表或区间 + 星期过滤，
// 返回实际写入日期数。
func (h *Handler) BatchWorkday(c *gin.Context) {
	var req dto.WorkdayBatchReq
	if !bindJSON(c, &req) {
		return
	}
	count, err := h.svc.BatchWorkday(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dto.WorkdayBatchResp{Count: count})
}

// ClearWorkday 清除单日显式记录恢复默认（DELETE /oa/workday/:date）。
func (h *Handler) ClearWorkday(c *gin.Context) {
	date := c.Param("date")
	if err := h.svc.ClearWorkday(c.Request.Context(), date); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}
