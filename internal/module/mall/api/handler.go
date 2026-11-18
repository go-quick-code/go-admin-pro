// Package api 是 mall 模块的 HTTP 入口（统一前缀 /mall，issue #174，
// docs/mall-mvp-spec.md §2/§4）。
//
// 管理端全部挂受保护组（RegisterProtected，调用方需已挂载 Auth 中间件）；
// C 端 /mall/app/* 公开组与会员令牌自校验随 T2/T5。权限码前缀
// mall:member:* / mall:level:*（spec §7），requirePermission 注入先例对照
// pay/crm（svc + requirePermission + location）。
package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// PermissionMiddleware 由 system 模块提供权限中间件。
type PermissionMiddleware func(code string) gin.HandlerFunc

// Handler 是 mall 模块的 HTTP 入口。
type Handler struct {
	svc  *service.Service
	perm PermissionMiddleware
	// location 是业务时区（ADR 0006）：T1 管理端无导出/单号日期键消费点，
	// 预留注入先例（对照 pay/crm 的 New 签名，后续票的导出与编号直接可用）。
	location *time.Location
}

// New 创建 mall Handler。perm 复用 system 的 RequirePermission。
func New(svc *service.Service, perm PermissionMiddleware, location *time.Location) *Handler {
	if location == nil {
		location = time.Local
	}
	return &Handler{svc: svc, perm: perm, location: location}
}

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
