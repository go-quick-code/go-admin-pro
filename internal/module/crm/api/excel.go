package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/pkg/excel"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
)

// ---------- Excel 导入导出（issue #20，docs/crm-mvp-spec.md §7）----------
// 导入为 multipart（字段名 file），模板/导出返回 xlsx 二进制（Content-Disposition 带文件名）。
// 限额由 excel.ImportGuard 固定（10MB 文件 / 5000 行 / 100 条明细 / 80MB 解压），
// 这里额外用 MaxBytesReader 在表单解析前挡住超大请求体。
// 各实体的 handler 分文件见 customer_excel.go / contact_excel.go / product_excel.go /
// contract_excel.go / receivable_excel.go，本文件只放共享的收文件与发文件助手。

const (
	// excelContentType 是 xlsx 的 MIME 类型。
	excelContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	// importBodyMaxBytes 是 multipart 请求体上限：文件上限 10MB 加上表单开销余量。
	importBodyMaxBytes = excel.DefaultMaxBytes + (1 << 20)
)

// receiveImportFile 解析 multipart 文件字段 file：
// 先用 MaxBytesReader 在表单解析前挡住超大请求体，再交给 excel.ImportGuard 做
// 精确的 10MB/5000 行/80MB 解压上限校验。缺失文件与超大都返回 400 可读文案。
func (h *Handler) receiveImportFile(c *gin.Context) (io.ReadCloser, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importBodyMaxBytes)
	fh, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			// 请求体在 multipart 解析阶段就超限：与 guard 的文件过大共用业务码 4202。
			msg := fmt.Sprintf("文件超过大小上限：最大 %d MB", excel.DefaultMaxBytes>>20)
			c.Set("bizCode", response.ImportFileTooLargeCode)
			c.Set("bizMsg", msg)
			c.JSON(http.StatusBadRequest, response.Body{
				Code: response.ImportFileTooLargeCode,
				Msg:  msg,
			})
			return nil, false
		}
		response.BadRequest(c, "请选择要导入的 Excel 文件（multipart 字段名 file）")
		return nil, false
	}
	file, err := fh.Open()
	if err != nil {
		response.BadRequest(c, "读取上传文件失败："+err.Error())
		return nil, false
	}
	return file, true
}

// sendExcel 输出 xlsx 附件：ASCII 文件名兼容旧客户端，filename* 提供中文名。
func sendExcel(c *gin.Context, base, title string, data []byte) {
	dateKey := time.Now().Format("20060102")
	asciiName := base + "-" + dateKey + ".xlsx"
	chineseName := title + "-" + dateKey + ".xlsx"
	disposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		asciiName, url.PathEscape(chineseName))
	c.Header("Content-Disposition", disposition)
	c.Data(http.StatusOK, excelContentType, data)
}
