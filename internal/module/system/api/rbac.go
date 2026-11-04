package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 角色 ----------

func (h *Handler) PageRoles(c *gin.Context) {
	var req dto.RolePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListRoles(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) ListSimpleRoles(c *gin.Context) {
	list, err := h.svc.ListSimpleRoles(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

func (h *Handler) GetRole(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	role, err := h.svc.GetRole(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, role)
}

func (h *Handler) CreateRole(c *gin.Context) {
	var req dto.RoleCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateRole(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateRole(c *gin.Context) {
	var req dto.RoleUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateRole(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteRole(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteRole(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 菜单 ----------

func (h *Handler) ListMenus(c *gin.Context) {
	list, err := h.svc.ListMenuTree(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

func (h *Handler) GetMenu(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	menu, err := h.svc.GetMenu(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, menu)
}

func (h *Handler) CreateMenu(c *gin.Context) {
	var req dto.MenuCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateMenu(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateMenu(c *gin.Context) {
	var req dto.MenuUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateMenu(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteMenu(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteMenu(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 部门 ----------

func (h *Handler) TreeDepts(c *gin.Context) {
	list, err := h.svc.ListDeptTree(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

func (h *Handler) GetDept(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	dept, err := h.svc.GetDept(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, dept)
}

func (h *Handler) CreateDept(c *gin.Context) {
	var req dto.DeptCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateDept(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateDept(c *gin.Context) {
	var req dto.DeptUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateDept(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteDept(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteDept(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 岗位 ----------

func (h *Handler) PagePosts(c *gin.Context) {
	var req dto.PostPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListPosts(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) ListSimplePosts(c *gin.Context) {
	list, err := h.svc.ListSimplePosts(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

func (h *Handler) CreatePost(c *gin.Context) {
	var req dto.PostCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreatePost(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdatePost(c *gin.Context) {
	var req dto.PostUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdatePost(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeletePost(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeletePost(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
