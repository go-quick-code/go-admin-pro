// contract_file.go 是合同附件的 HTTP 入口（issue #17）：合同详情附件区
// 上传/列表/下载/删除。权限跟随合同（查看挂 crm:contract:query，
// 上传与删除挂 crm:contract:update），不要求 infra:file:* 权限码。
package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/pkg/response"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
)

// contractFileBodyMaxBytes 是合同附件上传的请求体上限：20MB 文件 + 1MB
// multipart 开销，与 infra 上传口径一致（infra/service.MaxUploadBodyBytes）。
// 在 multipart 解析前限制，避免超大请求先落临时文件/占用内存。
const contractFileBodyMaxBytes = 20<<20 + (1 << 20)

// ListContractFiles 合同附件列表（新上传的在前；合同不可见 404）。
func (h *Handler) ListContractFiles(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	files, err := h.svc.ListContractFiles(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, files)
}

// UploadContractFile 上传合同附件（multipart 字段名 file；作废合同 400）。
// multipart 解析前先用 MaxBytesReader 限制请求体（20MB 文件 + 1MB 表单开销）：
// 超限在解析阶段拒绝，不落盘、不写 infra_file 行（PR #39 审查加固）。
func (h *Handler) UploadContractFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, contractFileBodyMaxBytes)
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
	file, err := h.svc.UploadContractFile(c.Request.Context(), id, fh)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, file)
}

// DownloadContractFile 鉴权下载合同附件（PR #39 审查 P2）：合同可见 + 文件属于
// 该合同才流式返回内容；公开静态目录不再作为附件直链。Content-Disposition
// 用 attachment（ASCII 回退名 + RFC 5987 中文名），浏览器按原文件名保存。
func (h *Handler) DownloadContractFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	content, err := h.svc.DownloadContractFile(c.Request.Context(), id, fileID)
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

// DeleteContractFile 删除合同附件（文件必须属于该合同，否则 404）。
func (h *Handler) DeleteContractFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	if err := h.svc.DeleteContractFile(c.Request.Context(), id, fileID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}
