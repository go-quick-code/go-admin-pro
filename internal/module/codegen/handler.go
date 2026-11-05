package codegen

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// PermissionMiddleware 由 system 模块提供。
type PermissionMiddleware func(code string) gin.HandlerFunc

type Handler struct {
	gen  *Generator
	perm PermissionMiddleware
}

func NewHandler(gen *Generator, perm PermissionMiddleware) *Handler {
	return &Handler{gen: gen, perm: perm}
}

// ListTables 查询可生成的业务表。
func (h *Handler) ListTables(c *gin.Context) {
	list, err := h.gen.ListTables(c.Request.Context(), c.Query("keyword"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, list)
}

// GetTable 查询表结构。
func (h *Handler) GetTable(c *gin.Context) {
	table, err := h.gen.GetTable(c.Request.Context(), c.Param("tableName"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.OK(c, table)
}

// Generate 生成代码并下载 zip。
func (h *Handler) Generate(c *gin.Context) {
	var req GenerateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return
	}
	data, filename, err := h.gen.Generate(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(http.StatusOK, "application/zip", data)
}

// RegisterProtected 注册代码生成接口。
func (h *Handler) RegisterProtected(rg *gin.RouterGroup) {
	group := rg.Group("/infra/codegen")
	group.GET("/tables", h.perm("infra:codegen:query"), h.ListTables)
	group.GET("/tables/:tableName/columns", h.perm("infra:codegen:query"), h.GetTable)
	group.POST("/generate", h.perm("infra:codegen:generate"), h.Generate)
	group.POST("/generate-entity", h.perm("infra:codegen:generate"), h.GenerateEntity)
}
