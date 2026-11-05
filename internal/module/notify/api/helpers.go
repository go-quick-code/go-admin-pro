// Package api 是 notify 模块的 HTTP 入口：路由、参数绑定与统一响应。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// bindQuery 解析查询参数（与 system/crm 同构，保持模块自包含）。
func bindQuery(c *gin.Context, v any) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

// pathID 解析路径参数中的 ID。
func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}
