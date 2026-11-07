package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

func (h *Handler) PageTenantPackages(c *gin.Context) {
	var req dto.TenantPackagePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListTenantPackages(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) ListSimpleTenantPackages(c *gin.Context) {
	list, err := h.svc.ListSimpleTenantPackages(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

func (h *Handler) GetTenantPackage(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	pkg, err := h.svc.GetTenantPackage(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, pkg)
}

func (h *Handler) CreateTenantPackage(c *gin.Context) {
	var req dto.TenantPackageCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateTenantPackage(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateTenantPackage(c *gin.Context) {
	var req dto.TenantPackageUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateTenantPackage(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateTenantPackageStatus(c *gin.Context) {
	var req dto.UpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateTenantPackageStatus(c.Request.Context(), req.ID, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteTenantPackage(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteTenantPackage(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
