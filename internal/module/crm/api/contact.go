package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// PageContacts 联系人分页（客户/姓名/手机筛选 + 数据范围经父客户传导）。
func (h *Handler) PageContacts(c *gin.Context) {
	var req dto.ContactPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListContacts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetContact 联系人详情（客户数据范围外 404）。
func (h *Handler) GetContact(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	contact, err := h.svc.GetContact(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, contact)
}

// CreateContact 在客户下新建联系人，返回带所属客户名称的完整联系人。
func (h *Handler) CreateContact(c *gin.Context) {
	var req dto.ContactCreateReq
	if !bindJSON(c, &req) {
		return
	}
	contact, err := h.svc.CreateContact(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, contact)
}

// UpdateContact 修改联系人。
func (h *Handler) UpdateContact(c *gin.Context) {
	var req dto.ContactUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateContact(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// DeleteContact 删除联系人（软删除）。
func (h *Handler) DeleteContact(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteContact(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
