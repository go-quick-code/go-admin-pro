package api

import (
	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- Excel 导出（issue #88，docs/erp-mvp-spec.md §4.11）----------
// ERP 只做导出、不做导入（spec §1 差异记清单）；导出返回 xlsx 二进制
//（Content-Disposition 带中英文文件名）。各实体的 handler 分文件见
// product.go / product_category.go / product_unit.go 的 Export* 方法，本文件只放
// 共享的发文件助手（响应构造单点化为 response.SendXLSX，spec #144）。

// sendExcel 输出 xlsx 附件：ASCII 文件名兼容旧客户端，filename* 提供中文名。
// 文件名日期键按业务时区（ADR 0006），不依赖服务器 TZ。
// 响应构造已单点化为 response.SendXLSX（spec #144），此处仅保留一行薄委托，
// 调用点零改动。
func (h *Handler) sendExcel(c *gin.Context, base, title string, data []byte) {
	response.SendXLSX(c, h.location, base, title, data)
}
