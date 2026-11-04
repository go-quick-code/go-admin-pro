package excel

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// 导入默认上限。
const (
	// DefaultMaxBytes 是导入文件默认大小上限（10MB）。
	DefaultMaxBytes int64 = 10 << 20
	// DefaultMaxRows 是导入数据行默认上限（不含表头）。
	DefaultMaxRows = 5000
	// DefaultMaxErrors 是单次导入返回的明细错误默认上限。
	DefaultMaxErrors = 100
	// DefaultMaxUnzipBytes 是解压后总大小默认上限（压缩包的 20 倍）。
	DefaultMaxUnzipBytes int64 = DefaultMaxBytes * 20
)

// ImportOptions 控制一次导入。
type ImportOptions[T any] struct {
	// Sheet 是工作表名；留空时读取第一个工作表。
	Sheet string
	// MaxBytes 是文件大小上限，<=0 时使用 DefaultMaxBytes。
	MaxBytes int64
	// MaxUnzipBytes 是解压后总大小上限，<=0 时使用 MaxBytes 的 20 倍。
	MaxUnzipBytes int64
	// MaxRows 是数据行上限，<=0 时使用 DefaultMaxRows。
	MaxRows int
	// MaxErrors 是返回的明细错误上限，<=0 时使用 DefaultMaxErrors。
	// 超出后 Errors 截断，Total 仍记录真实数量。
	MaxErrors int
	// Location 解析无时区日期文本与 Excel 序列号时使用的时区，nil 时使用 time.Local。
	Location *time.Location
	// AllowUnknownHeaders 为 true 时忽略模板之外的列，否则未知列视为错误。
	AllowUnknownHeaders bool
	// Validate 在每行字段解析成功后执行，返回的错误会带上行号汇总。
	Validate func(row int, v *T) error
}

// Import 解析 Excel 并返回结构体切片。
//
// 第一行必须是表头，按结构体 `excel` 标签匹配列名。所有行都会先完成解析与校验，
// 只要有一处错误就返回 *ImportError（包含行号、列名与原因），且不返回任何数据，
// 调用方应整批拒绝。完全空白的行会被跳过。
//
// 单元格按原始值解析：日期为 Excel 序列号（自动识别 1900/1904 日期系统），
// 百分比 50% 读作 0.5，带千分位格式的金额读作底层数字。无时区文本与序列号
// 使用 ImportOptions.Location 解释，默认 time.Local。
func Import[T any](r io.Reader, opts ImportOptions[T]) ([]T, error) {
	s, err := schemaFor(reflect.TypeOf((*T)(nil)).Elem())
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("%w: 缺少文件内容", ErrInvalidWorkbook)
	}

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	maxErrors := opts.MaxErrors
	if maxErrors <= 0 {
		maxErrors = DefaultMaxErrors
	}
	location := opts.Location
	if location == nil {
		location = time.Local
	}
	maxUnzip := opts.MaxUnzipBytes
	if maxUnzip <= 0 {
		if maxBytes > math.MaxInt64/20 {
			maxUnzip = math.MaxInt64
		} else {
			maxUnzip = maxBytes * 20
		}
	}

	limit := maxBytes
	if limit < math.MaxInt64 {
		limit++
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("读取 Excel 失败：%w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("%w: 最大 %s", ErrFileTooLarge, humanSize(maxBytes))
	}
	if err := checkUnzipSize(raw, maxUnzip); err != nil {
		return nil, err
	}

	xmlLimit := int64(16 << 20)
	if xmlLimit > maxUnzip {
		xmlLimit = maxUnzip
	}
	f, err := excelize.OpenReader(bytes.NewReader(raw), excelize.Options{
		UnzipSizeLimit:    maxUnzip,
		UnzipXMLSizeLimit: xmlLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWorkbook, err)
	}
	defer f.Close()

	sheet, err := resolveSheet(f, opts.Sheet)
	if err != nil {
		return nil, err
	}
	props, err := f.GetWorkbookProps()
	if err != nil {
		return nil, fmt.Errorf("读取工作簿属性失败：%w", err)
	}
	date1904 := props.Date1904 != nil && *props.Date1904
	iter, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWorkbook, err)
	}
	defer iter.Close()

	var (
		rowNum     int
		dataRows   int
		headerSeen bool
		fileCols   []int // schema 列 -> 文件列下标，-1 表示该列缺失
		items      []T
	)
	importErrs := newImportError(maxErrors)

	for iter.Next() {
		rowNum++
		cells, rowErr := iter.Columns(excelize.Options{RawCellValue: true})
		if rowErr != nil {
			return nil, fmt.Errorf("读取第 %d 行失败：%w", rowNum, rowErr)
		}

		if !headerSeen {
			headerSeen = true
			fileErrs := newImportError(maxErrors)
			fileCols, err = matchHeader(s, cells, opts.AllowUnknownHeaders, fileErrs)
			if err != nil {
				return nil, err
			}
			continue
		}

		if !hasAnyValue(cells) {
			continue
		}
		dataRows++
		if dataRows > maxRows {
			return nil, fmt.Errorf("%w: 最多 %d 行", ErrTooManyRows, maxRows)
		}

		var item T
		itemValue := rowValue(&item)
		rowHasError := false

		for colIdx, fileIdx := range fileCols {
			if fileIdx < 0 {
				continue
			}
			text := ""
			if fileIdx < len(cells) {
				text = strings.TrimSpace(cells[fileIdx])
			}
			col := s.columns[colIdx]
			if text == "" {
				if col.Required {
					importErrs.add(rowNum, col.Header, "必填")
					rowHasError = true
				}
				continue
			}
			field := itemValue.FieldByIndex(s.indexes[colIdx])
			if err := setCellValue(field, text, location, date1904); err != nil {
				importErrs.add(rowNum, col.Header, err.Error())
				rowHasError = true
			}
		}

		if !rowHasError && opts.Validate != nil {
			if err := opts.Validate(rowNum, &item); err != nil {
				importErrs.add(rowNum, "", err.Error())
			}
		}
		items = append(items, item)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("读取 Excel 失败：%w", err)
	}
	if !headerSeen {
		return nil, fmt.Errorf("%w: 工作表为空", ErrInvalidWorkbook)
	}
	if importErrs.Total > 0 {
		return nil, importErrs
	}
	if items == nil {
		items = []T{}
	}
	return items, nil
}

// resolveSheet 返回目标工作表名。
func resolveSheet(f *excelize.File, name string) (string, error) {
	if name != "" {
		idx, err := f.GetSheetIndex(name)
		if err != nil || idx < 0 {
			return "", fmt.Errorf("%w: %s", ErrSheetNotFound, name)
		}
		return name, nil
	}
	list := f.GetSheetList()
	if len(list) == 0 {
		return "", fmt.Errorf("%w: 工作表为空", ErrInvalidWorkbook)
	}
	return list[0], nil
}

// matchHeader 建立 schema 列到文件列的映射，表头不合法时返回 *ImportError。
func matchHeader(s *schema, cells []string, allowUnknown bool, importErrs *ImportError) ([]int, error) {
	fileIndex := make(map[string]int, len(cells))
	for i, cell := range cells {
		h := strings.TrimSpace(cell)
		if h == "" {
			continue
		}
		if _, ok := fileIndex[h]; ok {
			importErrs.add(1, h, "表头重复")
			continue
		}
		fileIndex[h] = i
		if _, known := s.byHeader[h]; !known && !allowUnknown {
			importErrs.add(1, h, "未知列，请使用模板表头")
		}
	}
	if len(fileIndex) == 0 {
		return nil, fmt.Errorf("%w: 第一行缺少表头", ErrInvalidWorkbook)
	}

	fileCols := make([]int, len(s.columns))
	for i, col := range s.columns {
		idx, ok := fileIndex[col.Header]
		if !ok {
			fileCols[i] = -1
			if col.Required {
				importErrs.add(1, col.Header, "缺少必填列")
			}
			continue
		}
		fileCols[i] = idx
	}
	if importErrs.Total > 0 {
		return nil, importErrs
	}
	return fileCols, nil
}

// rowValue 返回可写的行结构体，T 为指针类型时先分配内存。
func rowValue[T any](item *T) reflect.Value {
	v := reflect.ValueOf(item).Elem()
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

// hasAnyValue 判断一行中是否有非空单元格，用于跳过完全空白的行。
func hasAnyValue(cells []string) bool {
	for _, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			return true
		}
	}
	return false
}

// humanSize 把字节数格式化成易读文本。
func humanSize(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d 字节", n)
}

// checkUnzipSize 在解析前读取 zip 目录，拒绝解压后超过上限的文件。
// 不是合法 zip 时直接放行，由 excelize 返回统一的解析错误。
func checkUnzipSize(raw []byte, limit int64) error {
	if limit <= 0 {
		return nil
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil
	}
	var total uint64
	for _, file := range zr.File {
		total += file.UncompressedSize64
		if total > uint64(limit) {
			return fmt.Errorf("%w: 解压后超过 %s", ErrFileTooLarge, humanSize(limit))
		}
	}
	return nil
}
