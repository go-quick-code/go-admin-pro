// Package excel 提供结构体与 Excel 表格之间的导入、导出与模板生成能力。
//
// 字段通过 `excel` 标签声明表头映射：
//
//	type CustomerRow struct {
//		Name  string  `excel:"客户名称,required,width=24"`
//		Level int     `excel:"客户级别"`
//		Note  string  `excel:"-"`
//	}
//
// 标签格式为 `excel:"表头[,required][,width=20]"`；`excel:"-"` 表示不参与映射，
// 未声明标签的字段同样被忽略。匿名嵌入的非指针结构体会被展开。
package excel

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TagName 是结构体上声明 Excel 表头映射的标签名。
const TagName = "excel"

// Column 描述一个结构体字段与 Excel 表头之间的映射。
type Column struct {
	// Field 是 Go 结构体字段名。
	Field string
	// Header 是 Excel 表头文本。
	Header string
	// Required 表示导入时该列必填。
	Required bool
	// Width 是导出/模板中的列宽，0 表示使用默认宽度。
	Width float64
}

// schema 是结构体类型解析后的映射信息。
type schema struct {
	columns  []Column
	indexes  [][]int
	byField  map[string]int
	byHeader map[string]int
}

type schemaResult struct {
	schema *schema
	err    error
}

var schemaCache sync.Map // reflect.Type -> schemaResult

func schemaFor(t reflect.Type) (*schema, error) {
	if cached, ok := schemaCache.Load(t); ok {
		res := cached.(schemaResult)
		return res.schema, res.err
	}
	s, err := buildSchema(t)
	res, _ := schemaCache.LoadOrStore(t, schemaResult{schema: s, err: err})
	cached := res.(schemaResult)
	return cached.schema, cached.err
}

func buildSchema(t reflect.Type) (*schema, error) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s 不是结构体", ErrUnsupportedType, t)
	}
	s := &schema{
		byField:  make(map[string]int),
		byHeader: make(map[string]int),
	}
	if err := s.addFields(t, nil); err != nil {
		return nil, err
	}
	if len(s.columns) == 0 {
		return nil, fmt.Errorf("%w: %s 没有声明 excel 字段", ErrInvalidSchema, t)
	}
	return s, nil
}

func (s *schema) addFields(t reflect.Type, prefix []int) error {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get(TagName)
		if tag == "-" {
			continue
		}
		if tag == "" && sf.Anonymous {
			if sf.Type.Kind() == reflect.Ptr {
				return fmt.Errorf("%w: 字段 %s 是匿名指针，无法展开", ErrInvalidSchema, sf.Name)
			}
			if sf.Type.Kind() == reflect.Struct {
				if err := s.addFields(sf.Type, appendIndex(prefix, i)); err != nil {
					return err
				}
			}
			continue
		}
		if sf.PkgPath != "" { // 未导出字段不可写
			continue
		}
		if tag == "" {
			continue
		}

		col, err := parseColumnTag(sf, tag)
		if err != nil {
			return err
		}
		if _, ok := s.byHeader[col.Header]; ok {
			return fmt.Errorf("%w: 表头 %q 重复", ErrInvalidSchema, col.Header)
		}
		s.byField[col.Field] = len(s.columns)
		s.byHeader[col.Header] = len(s.columns)
		s.columns = append(s.columns, col)
		s.indexes = append(s.indexes, appendIndex(prefix, i))
	}
	return nil
}

func parseColumnTag(sf reflect.StructField, tag string) (Column, error) {
	parts := strings.Split(tag, ",")
	col := Column{
		Field:  sf.Name,
		Header: strings.TrimSpace(parts[0]),
	}
	if col.Header == "" {
		return Column{}, fmt.Errorf("%w: 字段 %s 的表头为空", ErrInvalidSchema, sf.Name)
	}
	if !supportedColumnType(sf.Type) {
		return Column{}, fmt.Errorf("%w: 字段 %s 的类型 %s 不支持导入/导出", ErrUnsupportedType, sf.Name, sf.Type)
	}
	for _, raw := range parts[1:] {
		opt := strings.TrimSpace(raw)
		switch {
		case opt == "":
			continue
		case opt == "required":
			col.Required = true
		case strings.HasPrefix(opt, "width="):
			width, err := strconv.ParseFloat(strings.TrimPrefix(opt, "width="), 64)
			if err != nil || width <= 0 {
				return Column{}, fmt.Errorf("%w: 字段 %s 的列宽 %q 非法", ErrInvalidSchema, sf.Name, opt)
			}
			col.Width = width
		default:
			return Column{}, fmt.Errorf("%w: 字段 %s 的标签选项 %q 未知", ErrInvalidSchema, sf.Name, opt)
		}
	}
	return col, nil
}

var textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()

// supportedColumnType 判断字段类型能否在 Excel 单元格与 Go 值之间转换。
func supportedColumnType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return true
	}
	if t.Implements(textUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func appendIndex(prefix []int, i int) []int {
	out := make([]int, len(prefix)+1)
	copy(out, prefix)
	out[len(prefix)] = i
	return out
}

// Columns 返回结构体 T 的列映射，顺序与字段声明顺序一致。
func Columns[T any]() ([]Column, error) {
	s, err := schemaFor(reflect.TypeOf((*T)(nil)).Elem())
	if err != nil {
		return nil, err
	}
	cols := make([]Column, len(s.columns))
	copy(cols, s.columns)
	return cols, nil
}
