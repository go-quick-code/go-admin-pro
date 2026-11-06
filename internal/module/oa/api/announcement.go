// announcement.go 承载公告的 HTTP 入口（issue #48，docs/oa-mvp-spec.md
// §3.4/§4.7/§5）：管理端列表/编辑/发布/撤回/附件，员工端我的公告与详情。
// 权限模型（菜单种子 521 段）：
//   - 管理列表/草稿预览 oa:announcement:query（菜单 521）；
//   - 创建/编辑/附件维护 oa:announcement:create/update（5211/5212）；
//   - 删除 oa:announcement:delete（5213），仅草稿；
//   - 发布/撤回 oa:announcement:publish（5214）；
//   - 我的公告与已发布详情登录即可（可见性由受众判定收口）。
package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// announcementFileBodyMaxBytes 是公告附件上传的请求体上限：20MB 文件 + 1MB
// multipart 开销，与 infra/合同附件上传口径一致。
const announcementFileBodyMaxBytes = 20<<20 + (1 << 20)

// PageAnnouncements 管理端公告分页（GET /oa/announcement/page，oa:announcement:query）。
func (h *Handler) PageAnnouncements(c *gin.Context) {
	var req dto.AnnouncementPageReq
	if !bindQuery(c, &req) {
		return
	}
	items, total, err := h.svc.PageAnnouncements(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, items, total)
}

// MyAnnouncements 员工「我的公告」（GET /oa/announcement/my/page，登录即可）：
// 只返回已发布且命中受众的公告，置顶在前。
func (h *Handler) MyAnnouncements(c *gin.Context) {
	var req dto.AnnouncementMyPageReq
	if !bindQuery(c, &req) {
		return
	}
	items, total, err := h.svc.PageMyAnnouncements(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, items, total)
}

// GetAnnouncement 公告详情（GET /oa/announcement/:id，登录即可）：
// 已发布按受众可见；草稿/撤回仅 oa:announcement:query 预览。
func (h *Handler) GetAnnouncement(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetAnnouncement(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// CreateAnnouncement 创建公告草稿（POST /oa/announcement，oa:announcement:create）。
func (h *Handler) CreateAnnouncement(c *gin.Context) {
	var req dto.AnnouncementCreateReq
	if !bindJSON(c, &req) {
		return
	}
	detail, err := h.svc.CreateAnnouncement(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// UpdateAnnouncement 编辑公告草稿（PUT /oa/announcement，oa:announcement:update）。
func (h *Handler) UpdateAnnouncement(c *gin.Context) {
	var req dto.AnnouncementUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	detail, err := h.svc.UpdateAnnouncement(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// DeleteAnnouncement 删除公告草稿（DELETE /oa/announcement/:id，
// oa:announcement:delete）：发布后不可删除，只能撤回。
func (h *Handler) DeleteAnnouncement(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteAnnouncement(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// PublishAnnouncement 发布/重新发布（POST /oa/announcement/:id/publish，
// oa:announcement:publish）：事务内为受众批量写 announcement 通知。
func (h *Handler) PublishAnnouncement(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.PublishAnnouncement(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// WithdrawAnnouncement 撤回公告（POST /oa/announcement/:id/withdraw，
// oa:announcement:publish）：不再出现在员工列表。
func (h *Handler) WithdrawAnnouncement(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.WithdrawAnnouncement(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ListAnnouncementFiles 公告附件列表（GET /oa/announcement/:id/files，登录即可）：
// 公告可见即可看附件。
func (h *Handler) ListAnnouncementFiles(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	files, err := h.svc.ListAnnouncementFiles(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, files)
}

// UploadAnnouncementFile 上传公告附件（POST /oa/announcement/:id/files，
// oa:announcement:update，仅草稿）。
func (h *Handler) UploadAnnouncementFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, announcementFileBodyMaxBytes)
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
	file, err := h.svc.UploadAnnouncementFile(c.Request.Context(), id, fh)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, file)
}

// DownloadAnnouncementFile 鉴权下载公告附件（GET /oa/announcement/:id/files/:fileId/download）：
// 公告可见 + 文件属于该公告才流式返回内容。
func (h *Handler) DownloadAnnouncementFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	content, err := h.svc.DownloadAnnouncementFile(c.Request.Context(), id, fileID)
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

// DeleteAnnouncementFile 删除公告附件（DELETE /oa/announcement/:id/files/:fileId，
// oa:announcement:update，仅草稿）。
func (h *Handler) DeleteAnnouncementFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	if err := h.svc.DeleteAnnouncementFile(c.Request.Context(), id, fileID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
