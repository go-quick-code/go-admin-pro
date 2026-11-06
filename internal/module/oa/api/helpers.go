// helpers.go 是 oa api 层的绑定与路径参数速记（与 system/crm/bpm 模块同构，
// 模块自包含样板）：统一 400 文案，避免各 handler 重复。
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// bindJSON 绑定 JSON 请求体，失败写 400 并返回 false。
func bindJSON(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

// bindQuery 绑定查询参数，失败写 400 并返回 false。
func bindQuery(c *gin.Context, v any) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

// pathID 解析路径参数 :id，非法写 400 并返回 false。
func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}
