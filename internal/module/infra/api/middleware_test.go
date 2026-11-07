package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
)

// collectingSink 捕获中间件入队的条目。
type collectingSink struct{ entries []model.APILog }

func (s *collectingSink) Enqueue(e model.APILog) { s.entries = append(s.entries, e) }

// fakeAuth 模拟 Auth 中间件：把登录主体写进请求上下文（真实链路由 system
// 的 Auth 完成，APILog 挂在它之后）。
func fakeAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := authn.With(c.Request.Context(), authn.Principal{UserID: 7, TenantID: 3, Username: "alice"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func newAPILogRouter(sink *collectingSink) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	protected := r.Group("/admin-api")
	protected.Use(fakeAuth(), APILogMiddleware(sink))
	ping := protected.Group("/ping")
	ping.GET("", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	ping.POST("", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	return r
}

// 脱敏：token/access_token/password/secret/refresh_token 值替换为 ***，
// 其余参数原样保留（Encode 按参数名排序）。
func TestMaskQuerySensitiveParams(t *testing.T) {
	// 参数名匹配忽略大小写（SECRET 也脱敏），但保留原始参数名大小写。
	got := maskQuery("token=abc&x=1&password=p&access_token=at&SECRET=s&refresh_token=r&name=zhang")
	want := "SECRET=***&access_token=***&name=zhang&password=***&refresh_token=***&token=***&x=1"
	if got != want {
		t.Fatalf("maskQuery = %q, want %q", got, want)
	}
	if maskQuery("") != "" {
		t.Fatalf("空 query 应返回空串")
	}
}

// query 整体截断 1024。
func TestMaskQueryTruncates(t *testing.T) {
	long := maskQuery("k=" + strings.Repeat("a", 2000))
	if len(long) != 1024 {
		t.Fatalf("query 应截断到 1024，实际 %d", len(long))
	}
}

// request_id：优先读 X-Request-Id 头（限 64 字符），否则生成 UUID，回写响应头。
func TestRequestIDFromHeaderAndGenerated(t *testing.T) {
	long := strings.Repeat("r", 80)
	rid := resolveRequestID(&http.Request{Header: http.Header{"X-Request-Id": {long}}})
	if rid != strings.Repeat("r", 64) {
		t.Fatalf("request_id 应取头部并截断 64，实际 len=%d", len(rid))
	}
	rid = resolveRequestID(&http.Request{Header: http.Header{}})
	if len(rid) != 36 || strings.Count(rid, "-") != 4 {
		t.Fatalf("未提供头时应生成 UUID，实际 %q", rid)
	}
}

// requestPath：路由模板优先，未匹配路由（FullPath 为空）时退回原始 path。
func TestRequestPathFallsBackToRaw(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/admin-api/nope?a=1", nil)
	if got := requestPath(c); got != "/admin-api/nope" {
		t.Fatalf("未匹配路由应退回原始 path，实际 %q", got)
	}
}

// 中间件行为：受保护接口（含 GET）被记录；路径取路由模板；query 脱敏；
// 用户/租户来自 Auth 上下文；request_id 回写响应头并入库。
func TestAPILogMiddlewareRecordsProtectedGET(t *testing.T) {
	sink := &collectingSink{}
	r := newAPILogRouter(sink)

	req := httptest.NewRequest(http.MethodGet, "/admin-api/ping?token=abc&page=2", nil)
	req.Header.Set("X-Request-Id", "req-abc")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Header().Get("X-Request-Id") != "req-abc" {
		t.Fatalf("request_id 应回写响应头")
	}
	if len(sink.entries) != 1 {
		t.Fatalf("应记录 1 条，实际 %d", len(sink.entries))
	}
	e := sink.entries[0]
	if e.RequestMethod != http.MethodGet {
		t.Fatalf("应记录 GET，实际 %q", e.RequestMethod)
	}
	if e.RequestPath != "/admin-api/ping" {
		t.Fatalf("应记录路由模板，实际 %q", e.RequestPath)
	}
	if e.RequestQuery != "page=2&token=***" {
		t.Fatalf("query 应脱敏，实际 %q", e.RequestQuery)
	}
	if e.ResponseStatus != http.StatusOK {
		t.Fatalf("状态码不符：%d", e.ResponseStatus)
	}
	if e.UserID != 7 || e.Username != "alice" || e.TenantID != 3 {
		t.Fatalf("用户/租户应来自 Auth 上下文：%+v", e)
	}
	if e.RequestID != "req-abc" {
		t.Fatalf("request_id 不符：%q", e.RequestID)
	}
	if e.CreatedAt.IsZero() || e.DurationMs < 0 {
		t.Fatalf("采集时刻/耗时未定格：%+v", e)
	}
}

// UA 截断 500；不记录请求体（模型无 body 字段）。
func TestAPILogMiddlewareTruncatesUserAgent(t *testing.T) {
	sink := &collectingSink{}
	r := newAPILogRouter(sink)

	req := httptest.NewRequest(http.MethodPost, "/admin-api/ping", strings.NewReader(`{"secret":"x"}`))
	req.Header.Set("User-Agent", strings.Repeat("u", 600))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if len(sink.entries) != 1 {
		t.Fatalf("应记录 1 条，实际 %d", len(sink.entries))
	}
	if ua := sink.entries[0].UserAgent; len(ua) != 500 {
		t.Fatalf("UA 应截断 500，实际 %d", len(ua))
	}
}
