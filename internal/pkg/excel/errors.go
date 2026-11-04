package excel

import (
	"errors"
	"fmt"
	"strings"
)

// 预定义的错误类别，调用方可用 errors.Is 判断。
var (
	// ErrInvalidSchema 表示结构体标签或字段类型不合法。
	ErrInvalidSchema = errors.New("excel: 字段映射不合法")
	// ErrUnsupportedType 表示 T 不是结构体，或字段类型不支持导入/导出。
	ErrUnsupportedType = errors.New("excel: 不支持的类型")
	// ErrFileTooLarge 表示上传文件超过大小上限。
	ErrFileTooLarge = errors.New("excel: 文件超过大小上限")
	// ErrTooManyRows 表示数据行超过行数上限。
	ErrTooManyRows = errors.New("excel: 数据超过行数上限")
	// ErrInvalidWorkbook 表示文件不是可解析的 xlsx，或缺少表头。
	ErrInvalidWorkbook = errors.New("excel: 无法解析的 Excel 文件")
	// ErrSheetNotFound 表示指定的工作表不存在。
	ErrSheetNotFound = errors.New("excel: 工作表不存在")
	// ErrColumnNotFound 表示导出时指定的列不存在。
	ErrColumnNotFound = errors.New("excel: 列不存在")
	// ErrDuplicateColumn 表示导出列选择中同一列出现多次。
	ErrDuplicateColumn = errors.New("excel: 列重复")
)

// CellError 是一条带行号与列名的校验错误。Row 从 1 开始，与 Excel 行号一致。
type CellError struct {
	Row    int    `json:"row"`
	Column string `json:"column,omitempty"`
	Msg    string `json:"msg"`
}

// ImportError 汇总一次导入中的行级错误。导入采用「有错整批拒绝」语义，
// 调用方在返回该错误时不应写入任何数据。
//
// 明细错误最多保留 MaxErrors 条；超出后 Truncated 为 true，Total 仍记录真实数量。
type ImportError struct {
	Errors    []CellError `json:"errors"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated"`
	max       int
}

func newImportError(max int) *ImportError {
	if max <= 0 {
		max = DefaultMaxErrors
	}
	return &ImportError{max: max}
}

func (e *ImportError) Error() string {
	if e.Total == 0 {
		return "excel: 导入校验失败"
	}
	const maxPreview = 5
	parts := make([]string, 0, maxPreview)
	for i, item := range e.Errors {
		if i == maxPreview {
			parts = append(parts, fmt.Sprintf("等 %d 处", len(e.Errors)))
			break
		}
		if item.Column == "" {
			parts = append(parts, fmt.Sprintf("第 %d 行：%s", item.Row, item.Msg))
			continue
		}
		parts = append(parts, fmt.Sprintf("第 %d 行 [%s]：%s", item.Row, item.Column, item.Msg))
	}
	prefix := fmt.Sprintf("导入校验失败（%d 处）", e.Total)
	if e.Truncated {
		prefix = fmt.Sprintf("导入校验失败（共 %d 处，仅显示前 %d 条）", e.Total, len(e.Errors))
	}
	return fmt.Sprintf("%s：%s", prefix, strings.Join(parts, "；"))
}

// add 追加一条错误，超过上限后只累加 Total。
func (e *ImportError) add(row int, column, msg string) {
	e.Total++
	if len(e.Errors) >= e.max {
		e.Truncated = true
		return
	}
	e.Errors = append(e.Errors, CellError{Row: row, Column: column, Msg: msg})
}
