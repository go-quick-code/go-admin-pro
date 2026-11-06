package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 敏感词管理（issue #81，spec §5.6）：词库 CRUD 与检测（system:sensitive-word:*）。
// 词库是租户级资源（不挂 RequirePlatformTenant），跨租户业务 ID 由服务层按
// 404 收口；检测端点复用 query 权限码。

// PageSensitiveWords 敏感词分页（当前租户）。
func (h *Handler) PageSensitiveWords(c *gin.Context) {
	var req dto.SensitiveWordPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.sensitive.PageSensitiveWords(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// CreateSensitiveWord 创建敏感词。
func (h *Handler) CreateSensitiveWord(c *gin.Context) {
	var req dto.SensitiveWordCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.sensitive.CreateSensitiveWord(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateSensitiveWord 修改敏感词。
func (h *Handler) UpdateSensitiveWord(c *gin.Context) {
	var req dto.SensitiveWordUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.sensitive.UpdateSensitiveWord(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteSensitiveWord 删除敏感词（软删，同词可重新添加）。
func (h *Handler) DeleteSensitiveWord(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.sensitive.DeleteSensitiveWord(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// CheckSensitiveWord 检测文本：返回命中词（去重、按首次出现位置排序）与
// 打码文本。
func (h *Handler) CheckSensitiveWord(c *gin.Context) {
	var req dto.SensitiveWordCheckReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.sensitive.CheckSensitiveText(c.Request.Context(), req.Text)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}
