// Package api 是 crm 模块的 HTTP 入口：路由、参数绑定与统一响应。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
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
