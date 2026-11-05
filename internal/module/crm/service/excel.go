package service

import (
	"fmt"
	"unicode/utf8"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// ---------- Excel 导入导出的模块内公共助手（issue #20）----------
// 限额与错误结构由 internal/pkg/excel 固定（10MB / 5000 行 / 100 条明细 / 80MB 解压），
// 本文件只做「行号 + 列名 + 原因」的组装与文本长度校验。

// importTextError 校验导入单元格文本长度（与 JSON binding 的 max 同口径：按字符计），
// 超限返回带列名的行级错误，未超限返回 nil。
func importTextError(row int, column, value string, max int) *excel.CellError {
	if n := utf8.RuneCountInString(value); n > max {
		return &excel.CellError{
			Row:    row,
			Column: column,
			Msg:    fmt.Sprintf("长度 %d 超过上限 %d 个字符", n, max),
		}
	}
	return nil
}

// newImportValidationError 把业务校验错误汇总成 helper 的 ValidationError：
// 明细最多保留 DefaultMaxErrors 条，Total 记录真实数量（与 excel.ImportError 语义一致）。
func newImportValidationError(errs []excel.CellError) error {
	total := len(errs)
	truncated := false
	if total > excel.DefaultMaxErrors {
		errs = errs[:excel.DefaultMaxErrors]
		truncated = true
	}
	return &excel.ValidationError{Errors: errs, Total: total, Truncated: truncated}
}
