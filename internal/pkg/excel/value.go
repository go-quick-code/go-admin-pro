package excel

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

var timeType = reflect.TypeOf(time.Time{})

var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"2006/1/2 15:04:05",
	"2006/01/02",
	"2006/1/2",
	time.RFC3339,
	"2006-01-02T15:04:05",
}

// setCellValue 把单元格文本写入可寻址的字段值，必要时分配指针。
func setCellValue(fv reflect.Value, text string) error {
	if fv.Kind() == reflect.Ptr {
		elem := reflect.New(fv.Type().Elem())
		if err := setCellValue(elem.Elem(), text); err != nil {
			return err
		}
		fv.Set(elem)
		return nil
	}

	if fv.Type() == timeType {
		t, err := parseTime(text)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(t))
		return nil
	}

	if fv.CanAddr() {
		if tu, ok := fv.Addr().Interface().(encoding.TextUnmarshaler); ok {
			if err := tu.UnmarshalText([]byte(text)); err != nil {
				return fmt.Errorf("值 %q 不合法：%v", text, err)
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
			return fmt.Errorf("值 %q 不是有效的整数", text)
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(text, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("值 %q 不是有效的非负整数", text)
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(text, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("值 %q 不是有效的数字", text)
		}
		fv.SetFloat(n)
	default:
		return fmt.Errorf("值 %q 无法写入类型 %s", text, fv.Type())
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
		return false, fmt.Errorf("值 %q 不是有效的布尔值（true/false、是/否）", text)
	}
	return b, nil
}

func parseTime(text string) (time.Time, error) {
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
			return t, nil
		}
	}
	// 未设置日期格式的单元格会以 Excel 序列号返回。
	if serial, err := strconv.ParseFloat(text, 64); err == nil && serial >= 20000 {
		if t, err := excelize.ExcelDateToTime(serial, false); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("值 %q 不是有效的日期（示例：2026-01-02）", text)
}
