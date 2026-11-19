package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/ratelimit"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// handler_app.go 是 mall C 端公开端点的 HTTP 入口（issue #178，T5；
// docs/mall-mvp-spec.md §2/§4/§7 裁决 2）。挂公开组（RegisterPublic），
// **不动 router 三组结构**：
//   - 注册/登录（POST /mall/app/auth/register|login）：无令牌，按 IP 限速
//    （10 次/分钟，弱凭证暴力尝试面——mobile+password 是唯一防线）；
//   - 我的信息/商品浏览（GET /mall/app/auth/me、/mall/app/product/spu/*）：
//     Bearer 会员令牌在 handler 内自校验（memberAuth 中间件 → service
//     VerifyMemberToken：签名/过期/DB 查 member_user status），限速宽些
//    （60 次/分钟，照 system publicIPLimit 先例）。
//
// 会员上下文传递（CRM contract_approval 先例）：校验成功后以
//
//	authn.With(ctx, authn.Principal{UserID: memberID, TenantID: tenantID,
//	                                Username: "mall:member:" + mobile})
//
// 构造内部上下文（另注入 tenant.WithTenantID——租户插件查询的取值源）。
// C 端端点挂公开组不经过 RequirePermission，不存在「会员被 mall 管理端
// 权限中间件当 system 用户」的问题；service 层经 tenant.ID(ctx)/租户插件
// 取租户。
//
// 应答形状：401 照 system Auth 中间件（response.Abort + xerr.Unauthorized，
// 统一信封 {code:401,msg:"登录状态已失效，请重新登录"}）；429 照 pay demo
// 回调先例（gin.H 两键——公开限速应答不带 data 字段）。失败文案不泄露
// 存在性（登录统一「账号或密码错误」、令牌校验统一「登录状态已失效」、
// 商品详情下架/不存在统一 404）。

// appAuthIPLimit 是注册/登录端点按客户端 IP 的限速阈值：每分钟 10 次
// （issue #178 任务口径——注册/登录是弱凭证暴力尝试面）。进程内单实例
// 语义，多实例部署由反代层另配。
const appAuthIPLimit = 10

// appBrowseIPLimit 是令牌端点（我的信息/商品浏览）按客户端 IP 的限速阈值：
// 每分钟 60 次（浏览类放宽容忍正常翻页，照 system publicIPLimit 先例）。
const appBrowseIPLimit = 60

// limitAppIP 按直连对端地址（c.RemoteIP()）固定窗口限速（超限 429，照 pay
// demo 回调 limitDemoIP 先例）。限速键刻意不用 c.ClientIP()：仓库未配置
// SetTrustedProxies 时 gin 默认信任任意来源，ClientIP() 会采信请求方可随意
// 伪造的 X-Forwarded-For，每请求轮换 XFF 即可无限开新桶（PR #140 安全审查
// P3-2 同款结论）。部署注意（已知权衡）：反代之后所有客户端共享直连对端
// （反代 IP）一个桶——全局代理信任拓扑属部署级决策，不在本差异内收敛。
func (h *Handler) limitAppIP(limiter *ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter != nil && !limiter.Allow(c.RemoteIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"code": http.StatusTooManyRequests, "msg": "请求过于频繁，请稍后重试"})
			return
		}
		c.Next()
	}
}

// memberAuth 是 C 端会员令牌自校验中间件（spec §7 裁决 2：handler 内
// 自校验，无独立认证中间件体系）：Bearer 令牌 → service.VerifyMemberToken
// （签名/过期/DB 查 member_user：tenant 一致 + status 启用）→ 构造内部
// 上下文（tenant.WithTenantID + authn.With，CRM contract_approval 先例）。
// 全部失败形态统一 401 同答（无令牌/伪造签名/过期/停用/跨租户不可区分，
// 不泄露存在性，照登录统一文案口径）。
func (h *Handler) memberAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := authn.BearerToken(c.GetHeader("Authorization"))
		if token == "" {
			response.Abort(c, xerr.Unauthorized(service.MemberTokenUnauthorizedMsg))
			return
		}
		row, err := h.svc.VerifyMemberToken(c.Request.Context(), token)
		if err != nil {
			response.Abort(c, err)
			return
		}
		ctx := tenant.WithTenantID(c.Request.Context(), row.TenantID)
		ctx = authn.With(ctx, authn.Principal{
			UserID:   row.ID,
			TenantID: row.TenantID,
			Username: "mall:member:" + row.Mobile,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// ---------- 注册 / 登录（公开，IP 限速） ----------

// AppRegister C 端注册（POST /mall/app/auth/register）：mobile/password/
// nickname → member_user + 令牌（自动登录）。mobile 租户内唯一 409。
func (h *Handler) AppRegister(c *gin.Context) {
	var req dto.AppRegisterReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.AppRegister(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// AppLogin C 端登录（POST /mall/app/auth/login）：mobile/password → 会员
// 令牌。失败统一「账号或密码错误」（不泄露存在性）。
func (h *Handler) AppLogin(c *gin.Context) {
	var req dto.AppLoginReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.AppLogin(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// ---------- 我的信息 / 商品浏览（需会员令牌，memberAuth 自校验） ----------

// AppMe 我的信息（GET /mall/app/auth/me）：member_user 基本信息 + 等级名 +
// point_balance。memberID 取自 memberAuth 写入的主体（自校验通过即存在）。
func (h *Handler) AppMe(c *gin.Context) {
	principal, ok := authn.From(c.Request.Context())
	if !ok {
		// 防御路径：memberAuth 未跑（路由误挂公开组之外的组）——不把无主体
		// 请求送进 service。
		response.Abort(c, xerr.Unauthorized(service.MemberTokenUnauthorizedMsg))
		return
	}
	profile, err := h.svc.AppGetMe(c.Request.Context(), principal.UserID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, profile)
}

// AppPageProductSPUs C 端上架 SPU 分页（GET /mall/app/product/spu/page）：
// 分类/品牌/关键词过滤，恒上架过滤，收窄 DTO（不含 detail 大字段）。
func (h *Handler) AppPageProductSPUs(c *gin.Context) {
	var req dto.AppSPUPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.AppPageProductSPUs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// AppGetProductSPU C 端 SPU 详情（GET /mall/app/product/spu/:id）：仅上架
// 可见（下架/不存在/跨租户统一 404），含 SKU 全量（价格/库存/规格快照，
// 主 SKU 恒首位）与 detail 图文。
func (h *Handler) AppGetProductSPU(c *gin.Context) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return
	}
	row, err := h.svc.AppGetProductSPU(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}
