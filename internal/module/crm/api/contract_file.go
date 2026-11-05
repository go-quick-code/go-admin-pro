// contract_file.go 是合同附件的 HTTP 入口（issue #17）：合同详情附件区
// 上传/列表/下载/删除。权限跟随合同（查看挂 crm:contract:query，
// 上传与删除挂 crm:contract:update），不要求 infra:file:* 权限码。
package api

import (
	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/pkg/response"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
)

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
func (h *Handler) UploadContractFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
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
