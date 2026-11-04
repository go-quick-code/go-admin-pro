package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// PageUsers 用户分页。
func (h *Handler) PageUsers(c *gin.Context) {
	var req dto.UserPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListUsers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetUser 用户详情。
func (h *Handler) GetUser(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	user, err := h.svc.GetUser(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, user)
}

// CreateUser 新增用户。
func (h *Handler) CreateUser(c *gin.Context) {
	var req dto.UserCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateUser(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateUser 修改用户。
func (h *Handler) UpdateUser(c *gin.Context) {
	var req dto.UserUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateUser(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateUserStatus 启停用户。
func (h *Handler) UpdateUserStatus(c *gin.Context) {
	var req dto.UpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateUserStatus(c.Request.Context(), req.ID, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ResetUserPassword 重置用户密码。
func (h *Handler) ResetUserPassword(c *gin.Context) {
	var req dto.ResetPasswordReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.ResetUserPassword(c.Request.Context(), req.ID, req.Password); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteUser 删除用户。
func (h *Handler) DeleteUser(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteUser(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
