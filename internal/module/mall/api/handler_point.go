package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// handler_point.go 是积分域的 HTTP 入口（issue #175，docs/mall-mvp-spec.md §4）：
// 调整（POST /mall/member/adjust-point）、流水分页
//（GET /mall/member/:id/point-records）、配置读写（GET/PUT /mall/point-config）。
// 路由挂码见 router.go；余额/校验语义在 service 层（唯一入账入口）。

// AdjustMemberPoint 管理员调整会员积分（point 可正可负，0 拒绝；余额不足
// 409；会员不存在 404）。
func (h *Handler) AdjustMemberPoint(c *gin.Context) {
	var req dto.PointAdjustReq
	if !bindJSON(c, &req) {
		return
	}
	record, err := h.svc.AdjustPoint(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, record)
}

// PageMemberPointRecords 按会员分页查积分流水（biz_type 过滤选填）。
func (h *Handler) PageMemberPointRecords(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.PointRecordPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListPointRecords(c.Request.Context(), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetPointConfig 读积分配置（租户级单行，行缺失惰性建行默认 1）。
func (h *Handler) GetPointConfig(c *gin.Context) {
	row, err := h.svc.GetPointConfig(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdatePointConfig 更新积分配置（point_per_yuan ≥ 0）。
func (h *Handler) UpdatePointConfig(c *gin.Context) {
	var req dto.PointConfigSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.UpdatePointConfig(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}
