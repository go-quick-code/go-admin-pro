package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
)

// ---------- 通用绑定助手 ----------

func bindJSON(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

func bindQuery(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}

func currentUserID(c *gin.Context) int64 {
	return c.GetInt64(ContextUserID)
}

// ---------- 认证 ----------

// Login 账号密码登录。
func (h *Handler) Login(c *gin.Context) {
	var req dto.LoginReq
	if !bindJSON(c, &req) {
		return
	}
	result, err := h.svc.Login(c.Request.Context(), &req, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Captcha 获取图形验证码。
func (h *Handler) Captcha(c *gin.Context) {
	result, err := h.svc.GenerateCaptcha()
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Logout 退出登录。JWT 无状态，客户端丢弃 token 即可。
func (h *Handler) Logout(c *gin.Context) {
	response.OK(c, true)
}

// UserInfo 获取当前登录用户信息。
func (h *Handler) UserInfo(c *gin.Context) {
	info, err := h.svc.UserInfo(c.Request.Context(), currentUserID(c))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, info)
}

// UpdateProfile 修改个人资料。
func (h *Handler) UpdateProfile(c *gin.Context) {
	var req dto.ProfileUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateProfile(c.Request.Context(), currentUserID(c), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ChangePassword 修改个人密码。
func (h *Handler) ChangePassword(c *gin.Context) {
	var req dto.ChangePasswordReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.ChangePassword(c.Request.Context(), currentUserID(c), req.OldPassword, req.NewPassword); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
