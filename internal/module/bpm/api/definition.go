// definition.go 承载流程定义管理的 HTTP 入口（issue #44，spec #42 §5 bpm 节）：
// 页面路由挂 bpm:definition:* 权限码（与菜单种子 510/511/5111+ 一致）；
// 发布与停用共用发布码 bpm:definition:publish（同一个人事动作权限）。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// PageDefinitions 定义分页（key 模糊/名称模糊/业务类型/状态筛选；
// 按 key 查询即版本历史）。
func (h *Handler) PageDefinitions(c *gin.Context) {
	var req dto.DefinitionPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListDefinitions(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetDefinition 定义详情。
func (h *Handler) GetDefinition(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.GetDefinition(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// CreateDefinition 新建流程定义草稿（同 key 再建即新版本草稿）。
func (h *Handler) CreateDefinition(c *gin.Context) {
	var req dto.DefinitionCreateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateDefinition(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateDefinition 编辑草稿（key/businessType 不可变；仅草稿可编辑）。
func (h *Handler) UpdateDefinition(c *gin.Context) {
	var req dto.DefinitionUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.UpdateDefinition(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// DeleteDefinition 删除草稿（仅草稿且无实例；已发布/停用只能停用）。
func (h *Handler) DeleteDefinition(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteDefinition(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// PublishDefinition 发布草稿（同 key 旧发布版本自动停用）。
func (h *Handler) PublishDefinition(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.PublishDefinition(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// DisableDefinition 停用发布版本（停用后不可发起新实例，#46 校验）。
func (h *Handler) DisableDefinition(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	row, err := h.svc.DisableDefinition(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// bindJSON/bindQuery/pathID 与 system/crm 模块同构（模块自包含样板）。
func bindJSON(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

func bindQuery(c *gin.Context, v any) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}
