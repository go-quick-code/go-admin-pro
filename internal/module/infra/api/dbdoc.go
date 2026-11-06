package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 数据库文档（issue #83，spec §4.9/§5.8） ----------
//
// 三个端点都挂 infra:db-doc:query；「仅平台租户」由 service 层收口
//（非平台租户即使持码也 403），与 message 渠道/模板同模式。

// ListDBTables 表清单：含 system_*/infra_*，排除 goose_db_version，支持关键字。
func (h *Handler) ListDBTables(c *gin.Context) {
	list, err := h.svc.ListDBTables(c.Request.Context(), c.Query("keyword"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// GetDBTable 单表结构详情。
func (h *Handler) GetDBTable(c *gin.Context) {
	table, err := h.svc.GetDBTable(c.Request.Context(), c.Param("name"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, table)
}

// ExportDBDoc 导出全库 Markdown 文档（文件下载，非 JSON 信封）。
func (h *Handler) ExportDBDoc(c *gin.Context) {
	content, filename, err := h.svc.ExportDBDoc(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", content)
}
