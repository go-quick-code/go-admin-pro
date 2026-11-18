package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 会员等级（租户级，issue #174） ----------

// PageLevels 分页查询等级。
func (h *Handler) PageLevels(c *gin.Context) {
	var req dto.LevelPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListLevels(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// ListLevelOptions 启用等级清单（会员表单等级下拉数据源，挂 mall:member:query
// ——持有会员管理菜单即可读，同 pay channel/list 挂 pay:app:query 形状）。
func (h *Handler) ListLevelOptions(c *gin.Context) {
	options, err := h.svc.ListLevelOptions(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, options)
}

// GetLevel 等级详情。
func (h *Handler) GetLevel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetLevel(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateLevel 创建等级（name/level 租户内唯一，撞键 409）。
func (h *Handler) CreateLevel(c *gin.Context) {
	var req dto.LevelSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateLevel(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateLevel 修改等级（id 在 body）。
func (h *Handler) UpdateLevel(c *gin.Context) {
	var req dto.LevelUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateLevel(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteLevel 删除等级（被会员引用 409）。
func (h *Handler) DeleteLevel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteLevel(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
