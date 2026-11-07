package api

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
)

// ---------- Excel 导出（issue #88，docs/erp-mvp-spec.md §4.11）----------
// ERP 只做导出、不做导入（spec §1 差异记清单）；导出返回 xlsx 二进制
//（Content-Disposition 带中英文文件名）。各实体的 handler 分文件见
// product.go / product_category.go / product_unit.go 的 Export* 方法，本文件只放
// 共享的发文件助手（与 crm api/excel.go 的 sendExcel 同构）。

const (
	// excelContentType 是 xlsx 的 MIME 类型。
	excelContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

// sendExcel 输出 xlsx 附件：ASCII 文件名兼容旧客户端，filename* 提供中文名。
// 文件名日期键按业务时区（ADR 0006），不依赖服务器 TZ。
func (h *Handler) sendExcel(c *gin.Context, base, title string, data []byte) {
	dateKey := time.Now().In(h.location).Format("20060102")
	asciiName := base + "-" + dateKey + ".xlsx"
	chineseName := title + "-" + dateKey + ".xlsx"
	disposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		asciiName, url.PathEscape(chineseName))
	c.Header("Content-Disposition", disposition)
	c.Data(http.StatusOK, excelContentType, data)
}
