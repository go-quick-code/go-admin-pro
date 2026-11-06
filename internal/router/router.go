package router

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/config"
	bpmapi "github.com/go-quick-code/go-admin-pro/internal/module/bpm/api"
	"github.com/go-quick-code/go-admin-pro/internal/module/codegen"
	crmapi "github.com/go-quick-code/go-admin-pro/internal/module/crm/api"
	infraapi "github.com/go-quick-code/go-admin-pro/internal/module/infra/api"
	messageapi "github.com/go-quick-code/go-admin-pro/internal/module/message/api"
	notifyapi "github.com/go-quick-code/go-admin-pro/internal/module/notify/api"
	oaapi "github.com/go-quick-code/go-admin-pro/internal/module/oa/api"
	systemapi "github.com/go-quick-code/go-admin-pro/internal/module/system/api"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/middleware"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// New 构建 HTTP 引擎，完成公开接口、鉴权接口与静态资源的装配。
func New(cfg *config.Config, sysAPI *systemapi.Handler, infraAPI *infraapi.Handler, codegenAPI *codegen.Handler, crmAPI *crmapi.Handler, oaAPI *oaapi.Handler, bpmAPI *bpmapi.Handler, notifyAPI *notifyapi.Handler, messageAPI *messageapi.Handler) *gin.Engine {
	if cfg.App.Env == "dev" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(middleware.Recovery(), middleware.AccessLog(), middleware.CORS(cfg.App.CORS.AllowedOrigins))

	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "app": cfg.App.Name, "env": cfg.App.Env})
	})

	api := engine.Group(cfg.App.APIPrefix)
	sysAPI.RegisterPublic(api)
	// 实时推送流（issue #78，spec §4.5.3 / §5.4）：凭票据的公开端点
	//（GET /notify/push/sse|ws）。票据即凭证；浏览器 EventSource 不能带
	// 请求头，公开路由是设计使然（ADR 0013），推送关闭时注册为 no-op。
	notifyAPI.RegisterPublic(api)

	// 换票端点（POST /notify/push/ticket）：只需登录，不挂操作日志——
	// 每次建流都会换票，属读语义，避免写操作日志被连接心跳灌满。
	streams := api.Group("")
	streams.Use(sysAPI.Auth())
	notifyAPI.RegisterStream(streams)

	// 访问日志中间件（issue #80，spec §4.6，ADR 0014）挂在 Auth 之后、
	// OperateLog 之前：Auth 后才有用户/租户上下文；只记录受保护接口——
	// 公开接口（登录/验证码/社交回调）与仅 Auth 的推送流不在 protected 组，
	// 天然不记录。采集只做非阻塞入队，落库走 service.APILogWriter 异步批量。
	protected := api.Group("")
	protected.Use(sysAPI.Auth(), infraAPI.AccessLog(), sysAPI.OperateLog())
	sysAPI.RegisterProtected(protected)
	infraAPI.RegisterProtected(protected)
	codegenAPI.RegisterProtected(protected)
	crmAPI.RegisterProtected(protected)
	oaAPI.RegisterProtected(protected)
	// bpm（issue #44）：流程定义管理（实例/任务接口由 #46 追加注册）。
	bpmAPI.RegisterProtected(protected)
	notifyAPI.RegisterProtected(protected)
	// message（issue #72）：渠道/模板（仅平台租户，service 层收口）、
	// 短信/邮件发送与租户级发送日志。
	messageAPI.RegisterProtected(protected)

	// 上传文件不挂公开静态目录（PR #39 残余风险 A）：任何匿名用户都不能
	// 通过 /files/* 直链读取文件，唯一入口是 GET /infra/file/:id/download
	//（挂 infra:file:query，按租户过滤）。

	// 前端打包产物（生产环境由 Go 直接托管）
	webRoot := cfg.App.WebRoot
	indexFile := filepath.Join(webRoot, "index.html")
	if _, err := os.Stat(indexFile); err == nil {
		engine.Static("/assets", filepath.Join(webRoot, "assets"))
		engine.StaticFile("/favicon.ico", filepath.Join(webRoot, "favicon.ico"))
	}

	engine.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, cfg.App.APIPrefix) {
			response.Fail(c, xerr.NotFound("接口不存在"))
			return
		}
		if _, err := os.Stat(indexFile); err == nil {
			c.File(indexFile)
			return
		}
		c.String(http.StatusNotFound, "404 Not Found")
	})

	return engine
}
