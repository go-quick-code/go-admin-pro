// document.go 是文档目录与 ACL 的 HTTP 入口（issue #58，
// docs/oa-mvp-spec.md §4.12/§5）：
//
//   - 目录树/详情：oa:document:query（菜单 526 管理入口 / 537 我的文档），
//     可见性由服务层的单条 ACL 谓词收口——不可见目录不出现在树里，详情 404；
//   - 创建目录：oa:document:create（5261）；顶层目录必须带显式 ACL；
//   - 改名/删除/ACL 设置：目录管理权（owner 或 oa:document:manage）；不在路由
//     上挂独立权限码，否则目录 owner 会被挡在自己的目录外（spec §4.12）。
//     服务层对「可见但无管理权」返回 403、「不可见」返回 404；
//   - 删除非空目录（有子目录或文件）返回 409；
//   - 文件（issue #59）：列表/下载挂 oa:document:query（服务层再按目录读
//     权限收口，不可读 404）；上传/删除只要求目录管理权（owner 或
//     oa:document:manage），与目录改名/删除同款，不额外挂按钮码。
//
// 静态路由 /tree 先于 /:id 注册。
package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// 文档权限码（菜单种子 526 段：526 查询、5261 创建、5262 管理、5263 上传；
// 员工侧菜单 537 复用查询码，issue #58）。
const (
	documentQuery = "oa:document:query"
)

// documentFileBodyMaxBytes 是文档文件上传的请求体上限：20MB 文件 + 1MB
// multipart 开销，与 infra/公告附件上传口径一致（issue #59）。
const documentFileBodyMaxBytes = 20<<20 + (1 << 20)

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

// ---------- 文件（issue #59） ----------

// PageDocumentFiles 目录内文件分页（GET /oa/document/file/page?folderId=，
// oa:document:query）：目录不可读/跨租户 404；只返回未软删文件。
func (h *Handler) PageDocumentFiles(c *gin.Context) {
	var req dto.DocumentFilePageReq
	if !bindQuery(c, &req) {
		return
	}
	files, total, err := h.svc.PageDocumentFiles(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, files, total)
}

// UploadDocumentFile 上传文件（POST /oa/document/file/upload）：
// multipart 字段 file + folderId；需要目录管理权（owner 或
// oa:document:manage），服务层 403/404 区分。
func (h *Handler) UploadDocumentFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, documentFileBodyMaxBytes)
	fh, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			response.BadRequest(c, "文件大小不能超过 20MB")
			return
		}
		response.BadRequest(c, "请选择要上传的文件")
		return
	}
	folderID, err := strconv.ParseInt(c.PostForm("folderId"), 10, 64)
	if err != nil || folderID <= 0 {
		response.BadRequest(c, "目录 ID 不能为空")
		return
	}
	file, err := h.svc.UploadDocumentFile(c.Request.Context(), folderID, fh)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, file)
}

// DownloadDocumentFile 鉴权下载（GET /oa/document/file/:id/download）：
// 文件所属目录必须可读才流式返回；直接走文件 ID 不能绕过目录 ACL，
// 也不使用任何公开静态地址。
func (h *Handler) DownloadDocumentFile(c *gin.Context) {
	fileID, ok := pathID(c)
	if !ok {
		return
	}
	content, err := h.svc.DownloadDocumentFile(c.Request.Context(), fileID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer content.Reader.Close()
	asciiName := fmt.Sprintf("file-%d%s", fileID, strings.ToLower(filepath.Ext(content.Name)))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		asciiName, url.PathEscape(content.Name)))
	c.DataFromReader(http.StatusOK, content.Size, content.ContentType, content.Reader, nil)
}

// DeleteDocumentFile 删除文件（DELETE /oa/document/file/:id）：需要文件所属
// 目录的管理权；软删归属行与 infra 行，不物理删除磁盘文件。
func (h *Handler) DeleteDocumentFile(c *gin.Context) {
	fileID, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteDocumentFile(c.Request.Context(), fileID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
