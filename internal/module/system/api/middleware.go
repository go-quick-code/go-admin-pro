package api

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// Handler 是 system 模块的 HTTP 入口。
type Handler struct {
	svc *service.Service
	cfg *config.Config
}

func New(svc *service.Service, cfg *config.Config) *Handler {
	return &Handler{svc: svc, cfg: cfg}
}

// ---------- 中间件 ----------

// Auth 校验 JWT 并把登录信息写入请求上下文。
func (h *Handler) Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			response.Abort(c, xerr.Unauthorized(""))
			return
		}
		claims, err := h.svc.ParseToken(token)
		if err != nil {
			response.Abort(c, xerr.Unauthorized("登录状态已失效，请重新登录"))
			return
		}
		// fail-closed：没有合法租户的 token 无法提供租户上下文，
		// 直接拒绝，避免带着空租户进入业务链路。
		if claims.TenantID <= 0 {
			response.Abort(c, xerr.Unauthorized("登录状态已失效，请重新登录"))
			return
		}

		ctx := tenant.WithTenantID(c.Request.Context(), claims.TenantID)
		ctx = authn.With(ctx, authn.Principal{
			UserID:   claims.UserID,
			TenantID: claims.TenantID,
			Username: claims.Username,
		})
		user, err := h.svc.GetActiveUser(ctx, claims.UserID)
		if err != nil {
			response.Abort(c, err)
			return
		}

		c.Request = c.Request.WithContext(ctx)
		c.Set(ContextUserID, user.ID)
		c.Set(ContextTenantID, claims.TenantID)
		c.Set(ContextUsername, user.Username)
		c.Set(ContextUser, user)
		c.Next()
	}
}

// PlatformTenantID 是平台租户（内置管理后台）的 ID。
// 租户 CRUD 只允许平台租户调用：租户管理员在自己租户内是 super_admin（"*"），
// 仅靠权限码无法区分平台与业务租户（PR #39 残余风险 B）。
const PlatformTenantID int64 = tenant.DefaultID

// RequirePlatformTenant 限制接口只能由平台租户调用，非平台租户返回 403。
// super_admin 在本租户内的 system:* 管理权限保持不动，本中间件只收口
// /system/tenant/*（跨租户的平台级管理面）。
func (h *Handler) RequirePlatformTenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		if tenant.ID(c.Request.Context()) != PlatformTenantID {
			response.Abort(c, xerr.Forbidden("仅平台租户可管理租户"))
			return
		}
		c.Next()
	}
}

// RequirePermission 校验接口权限码。
func (h *Handler) RequirePermission(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetInt64(ContextUserID)
		_, codes, err := h.svc.Permissions(c.Request.Context(), userID)
		if err != nil {
			response.Abort(c, err)
			return
		}
		if !service.HasPermission(codes, code) {
			response.Abort(c, xerr.Forbidden("没有操作权限："+code))
			return
		}
		c.Set(ContextPermCode, code)
		c.Next()
	}
}

var maskPasswordRe = regexp.MustCompile(`(?i)"(password|oldPassword|newPassword)"\s*:\s*"[^"]*"`)

// OperateLog 记录写操作日志。
func (h *Handler) OperateLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method != "POST" && method != "PUT" && method != "DELETE" && method != "PATCH" {
			c.Next()
			return
		}

		var body []byte
		if strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			body, _ = io.ReadAll(io.LimitReader(c.Request.Body, 64<<10))
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
		}

		start := time.Now()
		c.Next()
		cost := time.Since(start)

		ctx := c.Request.Context()
		log := &model.OperateLog{
			TenantID:      tenant.ID(ctx),
			UserID:        c.GetInt64(ContextUserID),
			Username:      c.GetString(ContextUsername),
			RequestMethod: method,
			RequestURL:    c.Request.URL.RequestURI(),
			RequestParams: truncate(maskedParams(body, c.Request.URL.RawQuery), 4000),
			OperateIP:     c.ClientIP(),
			UserAgent:     truncate(c.GetHeader("User-Agent"), 500),
			Duration:      cost.Milliseconds(),
			OperateTime:   time.Now(),
		}
		log.Module, log.Name, log.Type = logTarget(c)
		if c.Writer.Status() >= 400 || c.GetInt("bizCode") > 0 {
			log.Status = 1
			log.ErrorMsg = truncate(c.GetString("bizMsg"), 1000)
		}
		h.svc.RecordOperateLog(ctx, log)
	}
}

func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(header)
}

func maskedParams(body []byte, query string) string {
	params := string(body)
	if params == "" {
		params = query
	}
	return maskPasswordRe.ReplaceAllString(params, `"$1":"***"`)
}

func logTarget(c *gin.Context) (module, name string, opType int8) {
	perm := c.GetString(ContextPermCode)
	parts := strings.Split(perm, ":")
	if len(parts) >= 2 {
		module, name = parts[0], parts[1]
	}
	if len(parts) >= 3 {
		switch parts[2] {
		case "create":
			opType = 1
		case "update", "status", "password":
			opType = 2
		case "delete", "clear":
			opType = 3
		case "query", "list", "get":
			opType = 4
		case "export":
			opType = 5
		case "import":
			opType = 6
		}
	}
	if name == "" {
		segs := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
		for i, seg := range segs {
			if seg == "system" || seg == "infra" || seg == "codegen" {
				module = seg
				if i+1 < len(segs) {
					name = segs[i+1]
				}
				break
			}
		}
	}
	return module, name, opType
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// 上下文键。
const (
	ContextUserID   = "userId"
	ContextTenantID = "tenantId"
	ContextUsername = "username"
	ContextUser     = "currentUser"
	ContextPermCode = "permCode"
)
