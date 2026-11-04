package router

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/module/codegen"
	infraapi "github.com/lautung/go-admin-pro/internal/module/infra/api"
	systemapi "github.com/lautung/go-admin-pro/internal/module/system/api"
	"github.com/lautung/go-admin-pro/internal/pkg/middleware"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// New 构建 HTTP 引擎，完成公开接口、鉴权接口与静态资源的装配。
func New(cfg *config.Config, sysAPI *systemapi.Handler, infraAPI *infraapi.Handler, codegenAPI *codegen.Handler) *gin.Engine {
	if cfg.App.Env == "dev" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(middleware.Recovery(), middleware.AccessLog(), middleware.CORS())

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

	// 本地文件访问
	if err := os.MkdirAll(cfg.Storage.Local.BasePath, 0o755); err != nil {
		panic(err)
	}
	engine.Static(cfg.Storage.Local.PublicPrefix, cfg.Storage.Local.BasePath)

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
