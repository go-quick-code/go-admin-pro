// document.go 是文档目录与 ACL 的 HTTP 入口（issue #58，
// docs/oa-mvp-spec.md §4.12/§5）：
//
//   - 目录树/详情：oa:document:query（菜单 526 管理入口 / 537 我的文档），
//     可见性由服务层的单条 ACL 谓词收口——不可见目录不出现在树里，详情 404；
//   - 创建目录：oa:document:create（5261）；顶层目录必须带显式 ACL；
//   - 改名/删除/ACL 设置：目录管理权（owner 或 oa:document:manage）；不在路由
//     上挂独立权限码，否则目录 owner 会被挡在自己的目录外（spec §4.12）。
//     服务层对「可见但无管理权」返回 403、「不可见」返回 404；
//   - 删除非空目录（有子目录或文件）返回 409。
//
// 静态路由 /tree 先于 /:id 注册。
package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 文档权限码（菜单种子 526 段：526 查询、5261 创建、5262 管理、5263 上传；
// 员工侧菜单 537 复用查询码，issue #58）。
const (
	documentQuery = "oa:document:query"
)

// CreateDocumentFolder 创建目录（POST /oa/document/folder，
// oa:document:create）；顶层目录必须至少一条 ACL。
func (h *Handler) CreateDocumentFolder(c *gin.Context) {
	var req dto.DocumentFolderCreateReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.CreateDocumentFolder(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// UpdateDocumentFolder 目录改名/排序（PUT /oa/document/folder）；需要目录
// 管理权（owner 或 oa:document:manage）。
func (h *Handler) UpdateDocumentFolder(c *gin.Context) {
	var req dto.DocumentFolderUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.UpdateDocumentFolder(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// DeleteDocumentFolder 删除目录（DELETE /oa/document/folder/:id）；需要目录
// 管理权；有子目录或文件返回 409。
func (h *Handler) DeleteDocumentFolder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteDocumentFolder(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// ListDocumentFolderTree 可见目录树（GET /oa/document/folder/tree，
// oa:document:query）：不可见目录不出现；父目录不可见时子目录提升为根节点。
func (h *Handler) ListDocumentFolderTree(c *gin.Context) {
	items, err := h.svc.ListDocumentFolderTree(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, items)
}

// GetDocumentFolder 目录详情（GET /oa/document/folder/:id，
// oa:document:query）；范围外/跨租户 404。
func (h *Handler) GetDocumentFolder(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.GetDocumentFolder(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// GetDocumentFolderACL 读取目录 ACL（GET /oa/document/folder/:id/acl）：
// 需要目录管理权；响应包含自身显式条目与最近祖先的有效条目。
func (h *Handler) GetDocumentFolderACL(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetDocumentFolderACL(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// SetDocumentFolderACL 整体替换目录 ACL（PUT /oa/document/folder/:id/acl）：
// 需要目录管理权；顶层目录不允许清空（400）。
func (h *Handler) SetDocumentFolderACL(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req dto.DocumentFolderACLSetReq
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.svc.SetDocumentFolderACL(c.Request.Context(), id, &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}
