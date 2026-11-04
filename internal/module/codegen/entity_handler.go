package codegen

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// GenerateEntityReq 与 EntitySpec 同形；直接反序列化 EntitySpec（JSON tag 齐全）。

// GenerateEntity 按实体元信息生成 owner-scoped 实体全栈脚手架并打包 ZIP 下载。
// 生成不落库、不改工程文件；接线步骤见产物内 INTEGRATION.md。
func (h *Handler) GenerateEntity(c *gin.Context) {
	var req EntitySpec
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return
	}
	files, err := GenerateEntity(&req)
	if err != nil {
		// 生成入口的全部错误都来自元信息校验（用户输入问题），统一 400。
		response.BadRequest(c, err.Error())
		return
	}

	var buf bytes.Buffer
	zw := newZipWriter(&buf)
	for _, name := range sortedFileNames(files) {
		if err := zw.add(name, []byte(files[name])); err != nil {
			response.Fail(c, err)
			return
		}
	}
	if err := zw.close(); err != nil {
		response.Fail(c, err)
		return
	}

	filename := fmt.Sprintf("%s-%s-scaffold.zip", req.Module, req.Entity)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}
