package api

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 字典类型 ----------

func (h *Handler) PageDictTypes(c *gin.Context) {
	var req dto.DictTypePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListDictTypes(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) CreateDictType(c *gin.Context) {
	var req dto.DictTypeCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateDictType(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateDictType(c *gin.Context) {
	var req dto.DictTypeUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateDictType(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteDictType(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteDictType(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 字典数据 ----------

func (h *Handler) ListDictData(c *gin.Context) {
	var status *int8
	if v := c.Query("status"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 8); err == nil {
			s := int8(n)
			status = &s
		}
	}
	list, err := h.svc.ListDictData(c.Request.Context(), c.Query("dictType"), c.Query("label"), status)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// ListDictDataByType 按字典类型返回启用中的字典项，供所有登录用户使用。
func (h *Handler) ListDictDataByType(c *gin.Context) {
	dictType := c.Param("dictType")
	status := int8(0)
	list, err := h.svc.ListDictData(c.Request.Context(), dictType, "", &status)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

func (h *Handler) CreateDictData(c *gin.Context) {
	var req dto.DictDataCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateDictData(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateDictData(c *gin.Context) {
	var req dto.DictDataUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateDictData(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteDictData(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteDictData(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 租户 ----------

func (h *Handler) PageTenants(c *gin.Context) {
	var req dto.TenantPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListTenants(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) GetTenant(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	t, err := h.svc.GetTenant(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, t)
}

func (h *Handler) CreateTenant(c *gin.Context) {
	var req dto.TenantCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateTenant(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateTenant(c *gin.Context) {
	var req dto.TenantUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateTenant(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateTenantStatus(c *gin.Context) {
	var req dto.UpdateStatusReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateTenantStatus(c.Request.Context(), req.ID, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteTenant(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteTenant(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 日志 ----------

func (h *Handler) PageLoginLogs(c *gin.Context) {
	var req dto.LoginLogPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListLoginLogs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) ClearLoginLogs(c *gin.Context) {
	if err := h.svc.ClearLoginLogs(c.Request.Context()); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) PageOperateLogs(c *gin.Context) {
	var req dto.OperateLogPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListOperateLogs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) DeleteOperateLogs(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.DeleteOperateLogs(c.Request.Context(), req.IDs); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) ClearOperateLogs(c *gin.Context) {
	if err := h.svc.ClearOperateLogs(c.Request.Context()); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
