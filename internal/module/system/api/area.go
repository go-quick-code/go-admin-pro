package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 地区（issue #82，spec §4.8/§5.7）----------

// ListAreaTree 地区树（GET /system/area/tree，spec §5.7）：parentCode 为空
// 返回全量树（顶层省级），指定编码返回以其子级为根的子树。
// 读取开放给所有登录用户（spec §4.8.1，路由不设 RequirePermission）。
func (h *Handler) ListAreaTree(c *gin.Context) {
	list, err := h.svc.AreaTree(c.Request.Context(), c.Query("parentCode"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ListAreaChildren 直接子级（GET /system/area/children，spec §4.8.3）：
// parentCode 为空返回省级；平铺一跳，供级联选择器懒加载逐层下钻。
func (h *Handler) ListAreaChildren(c *gin.Context) {
	list, err := h.svc.AreaChildren(c.Request.Context(), c.Query("parentCode"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ImportAreas 平台导入（POST /system/area/import，spec §4.8.2）：
// 请求体是 JSON 数组（逐项校验 + 按 code upsert），单次 ≤5000 条；
// 平台租户限制在 service 层兜底（非平台租户 403，同 db-doc 模式）。
func (h *Handler) ImportAreas(c *gin.Context) {
	var items []dto.AreaImportItem
	if err := c.ShouldBindJSON(&items); err != nil {
		response.Fail(c, err)
		return
	}
	count, err := h.svc.ImportAreas(c.Request.Context(), items)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"count": count})
}
