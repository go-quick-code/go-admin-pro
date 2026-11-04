package excel

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/xuri/excelize/v2"
)

// DefaultSheetName 是导出与模板默认使用的工作表名。
const DefaultSheetName = "数据"

// DefaultTimeLayout 是 time.Time 字段导出的默认格式。
const DefaultTimeLayout = "2006-01-02 15:04:05"

// Formatter 在导出时把字段值转换成单元格值，可返回字符串、数字、布尔值或 nil。
type Formatter func(value any) (any, error)

// ExportOptions 控制一次导出。
type ExportOptions struct {
	// Sheet 是工作表名，空值时使用 DefaultSheetName。
	Sheet string
	// Columns 选择要导出的列，可传字段名或表头；空值表示按声明顺序导出全部列。
	Columns []string
	// Formatters 按字段名或表头指定值转换函数。
	Formatters map[string]Formatter
}

// Export 把结构体切片写成 xlsx 文件内容。
//
// 表头固定在第一行并冻结，列顺序由 Columns 决定；未指定时按字段声明顺序导出全部列。
// 值为 nil 的指针字段与零值 time.Time 输出为空单元格。行数由调用方控制，
// 大结果集应分批导出，避免一次性占用过多内存。
func Export[T any](rows []T, opts ExportOptions) ([]byte, error) {
	s, err := schemaFor(reflect.TypeOf((*T)(nil)).Elem())
	if err != nil {
		return nil, err
	}
	colIdxs, err := selectColumns(s, opts.Columns)
	if err != nil {
		return nil, err
	}
	formatters, err := resolveFormatters(s, opts.Formatters)
	if err != nil {
		return nil, err
	}

	sheet := opts.Sheet
	if sheet == "" {
		sheet = DefaultSheetName
	}
	f, err := newFile(sheet)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	selected := make([]Column, len(colIdxs))
	for i, idx := range colIdxs {
		selected[i] = s.columns[idx]
	}
	if err := writeHeader(f, sheet, selected); err != nil {
		return nil, err
	}

	for rowIdx, item := range rows {
		rowValue, err := derefRow(reflect.ValueOf(&item).Elem(), rowIdx+2)
		if err != nil {
			return nil, err
		}
		for colPos, colIdx := range colIdxs {
			col := s.columns[colIdx]
			field := rowValue.FieldByIndex(s.indexes[colIdx])
			value, err := cellValue(field, formatters[colIdx])
			if err != nil {
				return nil, fmt.Errorf("第 %d 行 [%s] 导出失败：%w", rowIdx+2, col.Header, err)
			}
			cell, _ := excelize.CoordinatesToCellName(colPos+1, rowIdx+2)
			if err := f.SetCellValue(sheet, cell, value); err != nil {
				return nil, fmt.Errorf("第 %d 行 [%s] 写入失败：%w", rowIdx+2, col.Header, err)
			}
		}
	}
	if len(rows) > 0 && len(colIdxs) > 0 {
		lastCol, _ := excelize.ColumnNumberToName(len(colIdxs))
		ref := fmt.Sprintf("A1:%s%d", lastCol, len(rows)+1)
		if err := f.AutoFilter(sheet, ref, nil); err != nil {
			return nil, fmt.Errorf("设置自动筛选失败：%w", err)
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("生成 Excel 失败：%w", err)
	}
	return buf.Bytes(), nil
}

// selectColumns 把字段名或表头解析成列下标，保持调用方给出的顺序。
func selectColumns(s *schema, selected []string) ([]int, error) {
	if len(selected) == 0 {
		out := make([]int, len(s.columns))
		for i := range s.columns {
			out[i] = i
		}
		return out, nil
	}
	seen := make(map[int]bool, len(selected))
	out := make([]int, 0, len(selected))
	for _, name := range selected {
		key := strings.TrimSpace(name)
		idx, err := s.lookup(key)
		if err != nil {
			return nil, err
		}
		if seen[idx] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateColumn, key)
		}
		seen[idx] = true
		out = append(out, idx)
	}
	return out, nil
}

// resolveFormatters 把字段名或表头解析成列下标到转换函数的映射。
func resolveFormatters(s *schema, format map[string]Formatter) (map[int]Formatter, error) {
	if len(format) == 0 {
		return nil, nil
	}
	out := make(map[int]Formatter, len(format))
	for name, fn := range format {
		idx, err := s.lookup(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, fmt.Errorf("%w: %s 的转换函数为空", ErrInvalidSchema, name)
		}
		if _, exists := out[idx]; exists {
			return nil, fmt.Errorf("%w: 列 %s 被多个键重复指定", ErrDuplicateColumn, s.columns[idx].Header)
		}
		out[idx] = fn
	}
	return out, nil
}

// lookup 按字段名或表头定位列下标。
func (s *schema) lookup(name string) (int, error) {
	if idx, ok := s.byField[name]; ok {
		return idx, nil
	}
	if idx, ok := s.byHeader[name]; ok {
		return idx, nil
	}
	return 0, fmt.Errorf("%w: %s", ErrColumnNotFound, name)
}

// derefRow 把指针行解引用为可读的结构体值；指针链中任一层为 nil 都返回带行号的错误。
func derefRow(rv reflect.Value, row int) (reflect.Value, error) {
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return reflect.Value{}, fmt.Errorf("第 %d 行为 nil，无法导出", row)
		}
		rv = rv.Elem()
	}
	return rv, nil
}

// cellValue 计算单元格值，优先使用调用方提供的转换函数。
func cellValue(fv reflect.Value, format Formatter) (any, error) {
	var (
		value any
		err   error
	)
	if format != nil {
		value, err = format(unwrappedValue(fv))
	} else {
		value, err = exportValue(fv)
	}
	if err != nil {
		return nil, err
	}
	return normalizeCell(value)
}

// unwrappedValue 去掉指针包装，供转换函数使用。
func unwrappedValue(fv reflect.Value) any {
	for fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return nil
		}
		fv = fv.Elem()
	}
	if !fv.CanInterface() {
		return nil
	}
	return fv.Interface()
}

// exportValue 把字段值转换成可写入单元格的值。
func exportValue(fv reflect.Value) (any, error) {
	for fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return "", nil
		}
		fv = fv.Elem()
	}
	if fv.Type() == timeType {
		return fv.Interface(), nil
	}
	// time.Time 与 TextMarshaler 交给 normalizeCell 统一格式化。
	if fv.CanInterface() {
		if _, ok := fv.Interface().(encoding.TextMarshaler); ok {
			return fv.Interface(), nil
		}
	}
	if fv.CanAddr() {
		if _, ok := fv.Addr().Interface().(encoding.TextMarshaler); ok {
			return fv.Addr().Interface(), nil
		}
	}
	if fv.CanInterface() {
		if _, ok := fv.Interface().(encoding.TextUnmarshaler); ok {
			return nil, fmt.Errorf("类型 %s 只实现了 UnmarshalText，导出需要同时实现 MarshalText", fv.Type())
		}
	}
	if fv.CanAddr() {
		if _, ok := fv.Addr().Interface().(encoding.TextUnmarshaler); ok {
			return nil, fmt.Errorf("类型 %s 只实现了 UnmarshalText，导出需要同时实现 MarshalText", fv.Type())
		}
	}
	switch fv.Kind() {
	case reflect.String:
		return fv.String(), nil
	case reflect.Bool:
		return fv.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fv.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return fv.Float(), nil
	}
	return nil, fmt.Errorf("类型 %s 不支持导出", fv.Type())
}

// maxCellTextUnits 是 Excel 单元格文本上限，按 UTF-16 code unit 计。
const maxCellTextUnits = 32767

// normalizeCell 把转换函数的结果规整成适合写入单元格的值。
func normalizeCell(value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case time.Time:
		if v.IsZero() {
			return "", nil
		}
		return checkCellText(v.Format(DefaultTimeLayout))
	case encoding.TextMarshaler:
		text, err := v.MarshalText()
		if err != nil {
			return nil, fmt.Errorf("值转换失败：%w", err)
		}
		return checkCellText(string(text))
	case string:
		return checkCellText(v)
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("非有限浮点数 %v 无法导出", v)
		}
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("非有限浮点数 %v 无法导出", v)
		}
		return v, nil
	}
	return value, nil
}

// checkCellText 校验文本长度不超过 Excel 单元格上限。
func checkCellText(text string) (string, error) {
	// 按 UTF-16 code unit 计数：BMP 之外的一个字符占两个 unit。
	if units := len(utf16.Encode([]rune(text))); units > maxCellTextUnits {
		return "", fmt.Errorf("文本长度 %d 个 UTF-16 字符超过 Excel 单元格上限 %d", units, maxCellTextUnits)
	}
	return text, nil
}
