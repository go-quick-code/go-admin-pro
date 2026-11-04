package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ListSimpleUsers 启用用户精简列表（表单选项用：CRM 负责人选择等）。
func (h *Handler) ListSimpleUsers(c *gin.Context) {
	users, err := h.svc.ListSimpleUsers(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, users)
}
