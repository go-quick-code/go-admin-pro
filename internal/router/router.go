package router

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/middleware"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// Registrar 是模块挂载路由的必需能力（ADR 0024 模块注册）：模块 api 包结构化
// 实现本接口即可进装配处的注册器列表——模块 handler 的方法集零改动即满足，
// router 包不再 import 任何模块 api 包，新增模块 = 装配处 append 一行。
type Registrar interface {
	// RegisterProtected 注册需要登录的接口，rg 上已挂 Backbone 三中间件
	//（Auth → AccessLog → OperateLog，次序在 New 内固定）。
	RegisterProtected(rg *gin.RouterGroup)
}

// PublicRegistrar 是可选的公开端点能力（type assert 探测）：实现 RegisterPublic
// 的注册器由 New 额外挂进公开组——无登录、不在 protected 组内（不记访问日志）。
type PublicRegistrar interface {
	Registrar
	// RegisterPublic 注册无需登录的接口，挂公开组（受保护组之外）。
	RegisterPublic(rg *gin.RouterGroup)
}

// StreamRegistrar 是可选的换票流端点能力（type assert 探测）：实现 RegisterStream
// 的注册器由 New 额外挂进换票流组——只需登录（仅 Auth），不挂操作日志。
type StreamRegistrar interface {
	Registrar
	// RegisterStream 注册推送流端点，挂仅 Auth 的换票流组。
	RegisterStream(rg *gin.RouterGroup)
}

// Backbone 是受保护组与换票流组共用的骨干中间件，由装配处填充：Auth 与
// OperateLog 归 system、AccessLog 归 infra（ADR 0024）。这里只收函数值，
// 组中间件次序在 New 内固定，装配处不决定次序。
type Backbone struct {
	Auth       gin.HandlerFunc
	AccessLog  gin.HandlerFunc
	OperateLog gin.HandlerFunc
}

// New 构建 HTTP 引擎，完成公开接口、鉴权接口与静态资源的装配。
// registrars 的次序 = 受保护组的模块注册次序（行为零漂移约束：列表序即现网
// 次序）；公开组与换票流组由同一列表按可选能力过滤得出，相对次序一致。
func New(cfg *config.Config, backbone Backbone, registrars ...Registrar) *gin.Engine {
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
	// 公开组：列表序过滤 PublicRegistrar（system, notify, pay）。
	for _, r := range registrars {
		if public, ok := r.(PublicRegistrar); ok {
			public.RegisterPublic(api)
		}
	}

	// 换票流组（仅 Auth）：列表序过滤 StreamRegistrar（notify）。换票端点
	//（POST /notify/push/ticket）只需登录、不挂操作日志——每次建流都会换票，
	// 属读语义，避免写操作日志被连接心跳灌满。
	streams := api.Group("")
	streams.Use(backbone.Auth)
	for _, r := range registrars {
		if stream, ok := r.(StreamRegistrar); ok {
			stream.RegisterStream(streams)
		}
	}

	// 访问日志中间件（issue #80，spec §4.6，ADR 0014）挂在 Auth 之后、
	// OperateLog 之前：Auth 后才有用户/租户上下文；只记录受保护接口——
	// 公开接口（登录/验证码/社交回调）与仅 Auth 的推送流不在 protected 组，
	// 天然不记录。采集只做非阻塞入队，落库走 service.APILogWriter 异步批量。
	protected := api.Group("")
	protected.Use(backbone.Auth, backbone.AccessLog, backbone.OperateLog)
	for _, r := range registrars {
		r.RegisterProtected(protected)
	}

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
