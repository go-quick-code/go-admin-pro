package api

import (
	"github.com/gin-gonic/gin"

	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- 联系人 Excel 导入（issue #20）----------

// ImportContacts 导入联系人（multipart：file）。
func (h *Handler) ImportContacts(c *gin.Context) {
	file, ok := h.receiveImportFile(c)
	if !ok {
		return
	}
	defer file.Close()
	count, err := h.svc.ImportContacts(c.Request.Context(), file, h.location)
	if err != nil {
		response.ImportFailed(c, err)
		return
	}
	response.OK(c, gin.H{"count": count})
}

// ContactImportTemplate 下载联系人导入模板。
func (h *Handler) ContactImportTemplate(c *gin.Context) {
	data, err := h.svc.ContactImportTemplate()
	if err != nil {
		response.Fail(c, err)
		return
	}
	sendExcel(c, "contact-import-template", "联系人导入模板", data)
}
