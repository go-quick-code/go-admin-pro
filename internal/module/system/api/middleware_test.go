package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/config"
	systemapi "github.com/lautung/go-admin-pro/internal/module/system/api"
	"github.com/lautung/go-admin-pro/internal/module/system/service"
	"github.com/lautung/go-admin-pro/internal/pkg/jwt"
)

// tokenWithoutTenant 签发一个 tenantId 非法的 token。
func tokenWithoutTenant(t *testing.T, mgr *jwt.Manager) string {
	t.Helper()
	token, _, err := mgr.Generate(1, 0, "attacker")
	if err != nil {
		t.Fatalf("签发 token: %v", err)
	}
	return token
}

// tenantId <= 0 的 token 无法提供租户上下文，fail-closed 下必须在中间件层拒绝，
// 不能让它继续进入业务链路（否则只会以 500 的形式暴露）。
func TestAuthRejectsTokenWithoutTenantID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mgr := jwt.NewManager("test-secret", time.Hour, "go-admin-pro")
	h := systemapi.New(service.New(nil, mgr, &config.Config{}), &config.Config{})

	r := gin.New()
	r.GET("/probe", h.Auth(), func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tokenWithoutTenant(t, mgr))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("tenantId=0 的 token 应被拒绝为 401，实际 HTTP %d body=%s", w.Code, w.Body.String())
	}
}
