package response

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/pkg/excel"
)

// Excel 导入失败的业务码（#32「错误映射」：逐行校验失败 200 + 业务码，
// 限额/内容错误 400 + 稳定业务码，前端据此区分「文件太大」「行数太多」「内容有误」）。
// 业务码落在 CRM 导入段 42xx，只增不改。
const (
	// ImportValidationCode 是逐行校验失败的业务码（HTTP 200，data 携带行级明细）。
	ImportValidationCode = 4201
	// ImportFileTooLargeCode 是文件超过大小上限（含解压后总大小）的业务码。
	ImportFileTooLargeCode = 4202
	// ImportTooManyRowsCode 是数据行数超过上限的业务码。
	ImportTooManyRowsCode = 4203
	// ImportInvalidWorkbookCode 是无法解析工作簿/缺少表头的业务码。
	ImportInvalidWorkbookCode = 4204
	// ImportSheetNotFoundCode 是工作表不存在的业务码。
	ImportSheetNotFoundCode = 4205
	// ImportInvalidSchemaCode 是字段映射非法的业务码。
	ImportInvalidSchemaCode = 4206
	// ImportInvalidHeaderCode 是表头不合法的业务码。
	ImportInvalidHeaderCode = 4207
)

// ImportGuardBizCode 把 excel guard 的错误类别映射为稳定业务码。
func ImportGuardBizCode(kind excel.GuardErrorKind) int {
	switch kind {
	case excel.GuardErrFileTooLarge:
		return ImportFileTooLargeCode
	case excel.GuardErrTooManyRows:
		return ImportTooManyRowsCode
	case excel.GuardErrInvalidWorkbook:
		return ImportInvalidWorkbookCode
	case excel.GuardErrSheetNotFound:
		return ImportSheetNotFoundCode
	case excel.GuardErrInvalidSchema:
		return ImportInvalidSchemaCode
	case excel.GuardErrInvalidHeader:
		return ImportInvalidHeaderCode
	default:
		return http.StatusInternalServerError
	}
}

// ImportFailed 输出 Excel 导入失败响应：
//   - *excel.ValidationError（逐行校验失败，业务结果）：HTTP 200 + 4201，
//     data 携带 Errors/Total/Truncated，前端可展示行号、列名与原因；
//   - *excel.GuardError（限额/内容错误）：HTTP 由 ImportGuardHTTPStatus 映射
//     （未预期为 500），业务码由 ImportGuardBizCode 映射；
//   - 其他错误交给 Fail（404/400/500 语义不变）。
func ImportFailed(c *gin.Context, err error) {
	var ve *excel.ValidationError
	if errors.As(err, &ve) {
		c.Set("bizCode", ImportValidationCode)
		c.Set("bizMsg", ve.Error())
		c.JSON(http.StatusOK, Body{Code: ImportValidationCode, Data: ve, Msg: ve.Error()})
		return
	}
	var ge *excel.GuardError
	if errors.As(err, &ge) {
		status := ImportGuardHTTPStatus(err)
		code := ImportGuardBizCode(ge.Kind)
		if code == http.StatusInternalServerError {
			Fail(c, err) // 未预期错误：统一 500，不泄露内部细节
			return
		}
		c.Set("bizCode", code)
		c.Set("bizMsg", ge.Msg)
		c.JSON(status, Body{Code: code, Data: nil, Msg: ge.Msg})
		return
	}
	Fail(c, err)
}
