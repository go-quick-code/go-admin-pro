package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/middleware"
)

func newCORSEngine(origins []string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CORS(origins))
	r.GET("/probe", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

const (
	allowedOrigin = "http://localhost:5173"
	deniedOrigin  = "http://evil.example.com"
)

// 白名单内的 Origin：返回与其对应的 CORS 头。
func TestCORSAllowlistedOriginGetsHeaders(t *testing.T) {
	r := newCORSEngine([]string{allowedOrigin})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Origin", allowedOrigin)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("白名单 Origin 应回显 Access-Control-Allow-Origin=%q，实际 %q", allowedOrigin, got)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("应返回 Access-Control-Allow-Credentials: true")
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("应返回 Access-Control-Allow-Methods")
	}
	if w.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatal("应返回 Access-Control-Allow-Headers")
	}
}

// 非白名单 Origin：不返回任何 CORS 头（浏览器侧拦截）。
func TestCORSNonAllowlistedOriginGetsNoHeaders(t *testing.T) {
	r := newCORSEngine([]string{allowedOrigin})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Origin", deniedOrigin)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("非白名单 Origin 不应返回 CORS 头，实际 Access-Control-Allow-Origin=%q", got)
	}
}

// 白名单内预检请求：204 + CORS 头。
func TestCORSPreflightAllowlisted(t *testing.T) {
	r := newCORSEngine([]string{allowedOrigin})

	req := httptest.NewRequest(http.MethodOptions, "/probe", nil)
	req.Header.Set("Origin", allowedOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("白名单预检应返回 204，实际 HTTP %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("预检响应应带 Access-Control-Allow-Origin=%q，实际 %q", allowedOrigin, got)
	}
}

// 非白名单预检请求：不返回 CORS 头，也不放行。
func TestCORSPreflightNonAllowlistedGetsNoHeaders(t *testing.T) {
	r := newCORSEngine([]string{allowedOrigin})

	req := httptest.NewRequest(http.MethodOptions, "/probe", nil)
	req.Header.Set("Origin", deniedOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("非白名单预检不应返回 CORS 头，实际 Access-Control-Allow-Origin=%q", got)
	}
	if w.Code == http.StatusNoContent && w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("非白名单预检不应以 204 短路放行")
	}
}

// 无 Origin（同源 / 服务端调用）：不加 CORS 头，请求照常处理。
func TestCORSNoOriginHeaderUntouched(t *testing.T) {
	r := newCORSEngine([]string{allowedOrigin})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("无 Origin 请求不应被拦截，实际 HTTP %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("无 Origin 不应返回 CORS 头")
	}
}

// 白名单为空：语义为「不放行任何跨域」，所有 Origin 都拿不到 CORS 头。
func TestCORSEmptyAllowlistDeniesAll(t *testing.T) {
	r := newCORSEngine(nil)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Origin", allowedOrigin)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("空白名单应拒绝所有跨域，实际 Access-Control-Allow-Origin=%q", got)
	}
}
