package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 会员（租户级，issue #174） ----------

// PageMembers 分页查询会员。
func (h *Handler) PageMembers(c *gin.Context) {
	var req dto.MemberPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListMembers(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

// GetMember 会员详情（含收货地址清单）。
func (h *Handler) GetMember(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetMember(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// CreateMember 创建会员（mobile 租户内唯一，撞键 409）。
func (h *Handler) CreateMember(c *gin.Context) {
	var req dto.MemberSaveReq
	if !bindJSON(c, &req) {
		return
	}
	row, err := h.svc.CreateMember(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, row)
}

// UpdateMember 修改会员（id 在 body；password 空 = 不改密码）。
func (h *Handler) UpdateMember(c *gin.Context) {
	var req dto.MemberUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateMember(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// UpdateMemberStatus 停启用会员（PUT /mall/member/:id/status）。
func (h *Handler) UpdateMemberStatus(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.MemberStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateMemberStatus(c.Request.Context(), id, *req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ListMemberAddresses 按会员查地址（GET /mall/address/member/:id，不分页，
// 会员详情抽屉数据源；分页视图走 /mall/address/page）。
func (h *Handler) ListMemberAddresses(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	list, err := h.svc.ListAddressesByMember(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// PageAddresses 地址分页（userId/receiver 过滤；userId 缺省列全租户）。
func (h *Handler) PageAddresses(c *gin.Context) {
	var req dto.AddressPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListAddresses(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
