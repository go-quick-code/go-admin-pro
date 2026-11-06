package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 社交登录（issue #76，spec §5.3）：
//   - 公开端点：authorize-url / callback（登录页与三方回调使用）；
//   - 受保护端点：bind（登录即可）、绑定用户分页/解绑（system:social-user:*）、
//     客户端 CRUD（system:social:*，仅平台租户，路由组挂 RequirePlatformTenant）。

// SocialAuthorizeURL 生成三方授权跳转地址（公开）。
func (h *Handler) SocialAuthorizeURL(c *gin.Context) {
	var req dto.SocialAuthorizeReq
	if !bindQuery(c, &req) {
		return
	}
	resp, err := h.social.AuthorizeURL(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// SocialCallback 社交回调（公开）：登录模式签发 JWT（未绑定 409/1901），
// 绑定模式完成绑定。
func (h *Handler) SocialCallback(c *gin.Context) {
	var req dto.SocialCallbackReq
	if !bindQuery(c, &req) {
		return
	}
	resp, err := h.social.Callback(c.Request.Context(), &req, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// SocialBind 发起绑定（登录即可）：返回绑定模式的授权地址。
func (h *Handler) SocialBind(c *gin.Context) {
	var req dto.SocialBindReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.social.BindInitiate(c.Request.Context(), &req,
		c.GetInt64(ContextUserID), c.GetInt64(ContextTenantID))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// PageSocialUsers 绑定用户分页（当前租户）。
func (h *Handler) PageSocialUsers(c *gin.Context) {
	var req dto.SocialUserPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.social.ListSocialUsers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// DeleteSocialUser 解绑（system:social-user:delete）：跨租户/不存在 404。
func (h *Handler) DeleteSocialUser(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.social.Unbind(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ListSocialProviders 登录页可用社交平台（公开，issue #77）：
// 仅返回启用客户端且 provider 已注册的平台。
func (h *Handler) ListSocialProviders(c *gin.Context) {
	list, err := h.social.ListEnabledSocialProviders(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ListMySocialBindings 我的社交绑定列表（登录即可，个人中心 tab，issue #77）。
func (h *Handler) ListMySocialBindings(c *gin.Context) {
	list, total, err := h.social.ListMySocialUsers(c.Request.Context(), c.GetInt64(ContextUserID))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// UnbindMySocialBinding 解绑本人绑定（登录即可，个人中心，issue #77）：
// 非本人/不存在一律 404（spec §4.4.7：只能解绑本人）。
func (h *Handler) UnbindMySocialBinding(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.social.UnbindMySocialUser(c.Request.Context(), id, c.GetInt64(ContextUserID)); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// PageSocialClients 客户端分页（平台级，密钥脱敏）。
func (h *Handler) PageSocialClients(c *gin.Context) {
	var req dto.SocialClientPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.social.ListSocialClients(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// CreateSocialClient 创建客户端（仅平台租户）。
func (h *Handler) CreateSocialClient(c *gin.Context) {
	var req dto.SocialClientCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.social.CreateSocialClient(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateSocialClient 修改客户端（仅平台租户；****** 保留原密钥）。
func (h *Handler) UpdateSocialClient(c *gin.Context) {
	var req dto.SocialClientUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.social.UpdateSocialClient(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteSocialClient 删除客户端（仅平台租户）。
func (h *Handler) DeleteSocialClient(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.social.DeleteSocialClient(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
