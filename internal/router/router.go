package router

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/module/codegen"
	crmapi "github.com/go-quick-code/go-admin-pro/internal/module/crm/api"
	infraapi "github.com/go-quick-code/go-admin-pro/internal/module/infra/api"
	systemapi "github.com/go-quick-code/go-admin-pro/internal/module/system/api"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/middleware"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// New 构建 HTTP 引擎，完成公开接口、鉴权接口与静态资源的装配。
func New(cfg *config.Config, sysAPI *systemapi.Handler, infraAPI *infraapi.Handler, codegenAPI *codegen.Handler, crmAPI *crmapi.Handler) *gin.Engine {
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

	protected := api.Group("")
	protected.Use(sysAPI.Auth(), sysAPI.OperateLog())
	sysAPI.RegisterProtected(protected)
	infraAPI.RegisterProtected(protected)
	codegenAPI.RegisterProtected(protected)
	crmAPI.RegisterProtected(protected)

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
