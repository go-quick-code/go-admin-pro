// Package api 是 crm 模块的 HTTP 入口：路由、参数绑定与统一响应。
package api

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- 通用绑定助手（与 system 模块同构，保持模块自包含，便于后续固化为生成器样板）----------

func bindJSON(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

func bindQuery(c *gin.Context, v interface{}) bool {
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

// requireAnyPermission 是「任一权限码」中间件：客户详情/负责人历史的读路径
// 同时接受 crm:customer:query 与 crm:customer:pool:view（PR #39 审查 P2：
// 只有公海菜单的销售也能从公海列表点进详情；非公海客户仍由 service 按数据
// 范围返回 404，不会因此扩大可见范围）。其余权限仍由 requirePermission 单码校验。
func (h *Handler) requireAnyPermission(codes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, code := range codes {
			ok, err := h.svc.HasPermission(c.Request.Context(), code)
			if err != nil {
				response.Abort(c, err)
				return
			}
			if ok {
				c.Next()
				return
			}
		}
		response.Abort(c, xerr.Forbidden("没有操作权限："+strings.Join(codes, " 或 ")))
	}
}
