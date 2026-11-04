package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 公海租户配置（issue #14）----------
// 读挂 crm:customer:pool:view（能看公海即可读配置，前端配置弹窗展示现值）；
// 写挂 crm:pool-config:update（菜单种子 4050，编辑动作单独控权）。

// GetPoolConfig 读取当前租户的公海配置（行缺失返回默认值 30/50）。
func (h *Handler) GetPoolConfig(c *gin.Context) {
	values, err := h.svc.GetPoolConfig(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, values)
}

// UpdatePoolConfig 保存当前租户的公海配置（upsert，返回保存后的取值）。
func (h *Handler) UpdatePoolConfig(c *gin.Context) {
	var req dto.PoolConfigUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	values, err := h.svc.UpdatePoolConfig(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, values)
}
