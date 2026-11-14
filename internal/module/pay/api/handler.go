// Package api 是 pay 模块的 HTTP 入口（统一前缀 /pay，spec §5）。
package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/ratelimit"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// PermissionMiddleware 由 system 模块提供权限中间件。
type PermissionMiddleware func(code string) gin.HandlerFunc

// Handler 是 pay 模块的 HTTP 入口。
type Handler struct {
	svc  *service.Service
	perm PermissionMiddleware
	// location 是业务时区（ADR 0006）：Excel 导出的文件名日期键按它划分
	//（#138，与 crm/erp 同经 response.SendXLSX 单点收口，spec #144）。nil 时回退
	// time.Local（生产装配固定传 config.Location()）。
	location *time.Location
	// demoLimiter 是 demo 商户回调端点的 IP 限速器（issue #134，照社交回调
	// 先例）：随 Handler 实例创建，公开端点防滥用（#85 安全审计同款定位）。
	demoLimiter *ratelimit.Limiter
}

// New 创建 pay Handler。perm 复用 system 的 RequirePermission。
func New(svc *service.Service, perm PermissionMiddleware, location *time.Location) *Handler {
	if location == nil {
		location = time.Local
	}
	return &Handler{svc: svc, perm: perm, location: location, demoLimiter: demoNotifyLimiter()}
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
