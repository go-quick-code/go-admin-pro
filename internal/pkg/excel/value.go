package excel

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

var timeType = reflect.TypeOf(time.Time{})

var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006-1-2 15:04:05",
	"2006-1-2 15:04",
	"2006-1-2",
	"2006/01/02 15:04:05",
	"2006/01/02 15:04",
	"2006/1/2 15:04:05",
	"2006/1/2 15:04",
	"2006/01/02",
	"2006/1/2",
	time.RFC3339,
	"2006-01-02T15:04:05",
}

// setCellValue 把单元格文本写入可寻址的字段值，必要时分配指针。
func setCellValue(fv reflect.Value, text string, location *time.Location, date1904 bool) error {
	if fv.Kind() == reflect.Ptr {
		elem := reflect.New(fv.Type().Elem())
		if err := setCellValue(elem.Elem(), text, location, date1904); err != nil {
			return err
		}
		fv.Set(elem)
		return nil
	}

	if fv.Type() == timeType {
		t, err := parseTime(text, location, date1904)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(t))
		return nil
	}

	if fv.CanAddr() {
		if tu, ok := fv.Addr().Interface().(encoding.TextUnmarshaler); ok {
			if err := tu.UnmarshalText([]byte(text)); err != nil {
				return fmt.Errorf("值 %q 不合法：%v", truncateEchoValue(text), err)
			}
			return nil
		}
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(text)
	case reflect.Bool:
		b, err := parseBool(text)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(text, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("值 %q 不是有效的整数", truncateEchoValue(text))
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(text, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("值 %q 不是有效的非负整数", truncateEchoValue(text))
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(text, fv.Type().Bits())
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("值 %q 不是有效的数字", truncateEchoValue(text))
		}
		fv.SetFloat(n)
	default:
		return fmt.Errorf("值 %q 无法写入类型 %s", truncateEchoValue(text), fv.Type())
	}
	return nil
}

func parseBool(text string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "是", "yes", "y":
		return true, nil
	case "否", "no", "n":
		return false, nil
	}
	b, err := strconv.ParseBool(text)
	if err != nil {
		return false, fmt.Errorf("值 %q 不是有效的布尔值（true/false、是/否）", truncateEchoValue(text))
	}
	return b, nil
}

// Excel 日期序列号上限：1900 日期系统中 9999-12-31 的序列号是 2958465。
const (
	maxSerial1900 = 2958465.0
	// 1904 日期系统整体比 1900 系统早 1462 天，序列号 0 是 1904-01-01。
	maxSerial1904 = maxSerial1900 - 1462
)

// dateRangeError 统一构造序列号超出 Excel 日期范围的错误。
func dateRangeError(text string) error {
	return fmt.Errorf("值 %q 超出 Excel 日期范围（示例：2026-01-02）", truncateEchoValue(text))
}

func parseTime(text string, location *time.Location, date1904 bool) (time.Time, error) {
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, text, location); err == nil {
			return t, nil
		}
	}
	// 未设置日期格式的单元格会以 Excel 序列号返回。
	if serial, err := strconv.ParseFloat(text, 64); err == nil {
		maxSerial := maxSerial1900
		minSerial := 1.0
		if date1904 {
			maxSerial = maxSerial1904
			minSerial = 0
		}
		if math.IsNaN(serial) || math.IsInf(serial, 0) || serial < minSerial || serial > maxSerial {
			return time.Time{}, dateRangeError(text)
		}
		if t, err := excelize.ExcelDateToTime(serial, date1904); err == nil {
			// 兜底拒绝转换后仍超出 9999 年的结果，避免数据库无法存储的年份。
			if t.Year() > 9999 {
				return time.Time{}, dateRangeError(text)
			}
			return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), location), nil
		}
	}
	return time.Time{}, fmt.Errorf("值 %q 不是有效的日期（示例：2026-01-02）", truncateEchoValue(text))
}

// maxEchoRunes 是错误消息中回显单元格原始文本的最大 rune 数。
const maxEchoRunes = 32

// truncateEchoValue 把错误消息中回显的单元格文本截断到 maxEchoRunes 个 rune，
// 超出时追加省略号。超长单元格只影响错误文案，不影响行列定位与错误类别。
func truncateEchoValue(text string) string {
	if utf8.RuneCountInString(text) <= maxEchoRunes {
		return text
	}
	return string([]rune(text)[:maxEchoRunes]) + "…"
}
