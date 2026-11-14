// Package response 是 HTTP 响应构造的公共包：统一响应体与业务码（Body、
// PageResult、Excel 导入失败业务码）的落点；导入限额校验（ImportGuardHTTPStatus
// → HTTP 状态）与导出下载（SendXLSX 附件响应）在此完成 HTTP 边界——gin 适配
// （读写响应头、状态码、回写字节）集中于此，excel 包保持无 gin 的纯数据包
// （CODING_STANDARDS「gin 适配集中在 response、middleware」）。
package response

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
)

// excelContentType 是 xlsx 的 MIME 类型（收为包内单点：三份逐字拷贝的常量删除，spec #144）。
const excelContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// SendXLSX 输出导出下载的 xlsx 附件响应（spec #144，ADR 0022 提升规则补遗）：
// Content-Disposition 双文件名——ASCII 回退名 <base>-<日期键>.xlsx 兼容旧客户端，
// filename* 本地化名 <title>-<日期键>.xlsx（RFC 5987 UTF-8 百分号编码）供现代
// 浏览器解析出中文名；随后以 200 写回 xlsx 字节。落点 rationale：这是 HTTP 响应
// 构造而非 Excel 数据转换，故入 response 而非 excel（response → excel 依赖已存在），
// excel 包保持无 gin 的纯数据包。
//
// 日期键 = time.Now().In(loc) 按 "20060102" 格式化，格式内聚本包（无分叉诱因，
// ADR 0022 补遗）；时区为显式入参（业务时区，ADR 0006），pkg 不读全局时区配置。
// loc == nil 视为装配期编程错误：fail-closed 直接 panic——正常运行不可达（应用
// 时区启动校验保证非 nil），panic 把装配 bug 显性化而非静默用错时区。
//
// 不变量：base/title 必须为编译期常量或包内常量（先例：erp doctype 注册表的
// ExcelFile/ExcelTitle），不得引入用户输入——ASCII 名原样嵌入 filename="%s"
// 的引号内、不做引号转义，引号逃逸/头注入目前只靠调用点纪律拦截；title 侧
// 另有 RFC 5987 百分号编码与 net/http 头值净化兜底。codegen 从此把未来实体
// 的导出指到本单点，新增调用沿用常量入参即可维持该约束。
func SendXLSX(c *gin.Context, loc *time.Location, base, title string, data []byte) {
	if loc == nil {
		panic("response.SendXLSX: loc 为 nil（装配期编程错误：业务时区须显式传入）")
	}
	dateKey := time.Now().In(loc).Format("20060102")
	asciiName := base + "-" + dateKey + ".xlsx"
	chineseName := title + "-" + dateKey + ".xlsx"
	disposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		asciiName, url.PathEscape(chineseName))
	c.Header("Content-Disposition", disposition)
	c.Data(http.StatusOK, excelContentType, data)
}
