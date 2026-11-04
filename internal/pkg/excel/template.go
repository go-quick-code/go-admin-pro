package excel

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/xuri/excelize/v2"
)

const notesSheetName = "填写说明"

// TemplateOptions 控制导入模板的生成。
type TemplateOptions struct {
	// Sheet 是数据工作表名，空值时使用 DefaultSheetName。
	Sheet string
	// Notes 按字段名或表头指定填写说明，写入「填写说明」工作表。
	Notes map[string]string
}

// Template 生成只有表头与填写说明的导入模板。
func Template[T any](opts TemplateOptions) ([]byte, error) {
	s, err := schemaFor(reflect.TypeOf((*T)(nil)).Elem())
	if err != nil {
		return nil, err
	}
	sheet := opts.Sheet
	if sheet == "" {
		sheet = DefaultSheetName
	}
	if sheet == notesSheetName {
		return nil, fmt.Errorf("%w: 工作表名不能是 %q", ErrInvalidSchema, notesSheetName)
	}
	notes, err := resolveNotes(s, opts.Notes)
	if err != nil {
		return nil, err
	}

	f, err := newFile(sheet)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if err := writeHeader(f, sheet, s.columns); err != nil {
		return nil, err
	}
	if _, err := f.NewSheet(notesSheetName); err != nil {
		return nil, err
	}
	noteCols := []Column{
		{Header: "列名", Width: 24},
		{Header: "是否必填", Width: 12},
		{Header: "填写说明", Width: 50},
	}
	if err := writeHeader(f, notesSheetName, noteCols); err != nil {
		return nil, err
	}
	for i, col := range s.columns {
		row := i + 2
		required := "否"
		if col.Required {
			required = "是"
		}
		values := []string{col.Header, required, notes[i]}
		for j, value := range values {
			cell, _ := excelize.CoordinatesToCellName(j+1, row)
			if err := f.SetCellValue(notesSheetName, cell, value); err != nil {
				return nil, err
			}
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("生成 Excel 模板失败：%w", err)
	}
	return buf.Bytes(), nil
}

// resolveNotes 把字段名或表头解析成列下标到填写说明的映射。
func resolveNotes(s *schema, notes map[string]string) (map[int]string, error) {
	out := make(map[int]string, len(notes))
	for name, note := range notes {
		idx, err := s.lookup(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		if _, exists := out[idx]; exists {
			return nil, fmt.Errorf("%w: 列 %s 被多个键重复指定", ErrDuplicateColumn, s.columns[idx].Header)
		}
		out[idx] = note
	}
	return out, nil
}
