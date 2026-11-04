package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const corsAllowedHeaders = "Origin, Content-Type, Authorization, tenant-id, X-Requested-With"

// CORS 按 allowlist 放行跨域：只有 Origin 精确命中 allowedOrigins 的请求
// 才返回 CORS 头；非白名单来源（含预检）不返回任何 CORS 头，由浏览器拦截。
// allowedOrigins 为空表示不放行任何跨域。条目匹配前去除首尾空白，
// 不支持通配符（与 Allow-Credentials 组合时浏览器本身也禁止 *）。
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if origin = strings.TrimSpace(origin); origin != "" {
			allow[origin] = struct{}{}
		}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		if _, ok := allow[origin]; !ok {
			// 非白名单：不加 CORS 头，也不做预检短路，让请求按普通路由处理。
			c.Next()
			return
		}

		c.Header("Vary", "Origin")
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		c.Header("Access-Control-Allow-Headers", corsAllowedHeaders)
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// AccessLog 记录 HTTP 访问日志。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		cost := time.Since(start)
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"cost", cost.String(),
			"ip", c.ClientIP(),
		)
	}
}

// Recovery 捕获 panic 并输出统一错误。
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		slog.Error("panic recovered", "err", recovered, "path", c.Request.URL.Path)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"data": nil,
			"msg":  "服务器内部错误",
		})
	})
}
