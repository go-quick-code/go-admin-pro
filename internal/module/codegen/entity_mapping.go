package codegen

import (
	"sort"
)

// 本文件是「逻辑类型 → Go 类型 / 前端类型 / 表单组件」的字段映射表。
//
// 配置位说明：新增支持的字段类型只需在此登记一行（含 Go import 与前端组件），
// 生成器其余部分（校验、模板）自动感知；单个字段可在 FieldSpec.FormType 上
// 覆盖表单组件。金额类型 money 固定映射到 internal/pkg/money.Money（int64 分，
// JSON 十进制字符串，decimal(12,2) 列），金额换算不经过 float64（CODING_STANDARDS）。

// fieldMapping 是一个逻辑类型的生成映射。
type fieldMapping struct {
	// GoType Go 字段类型；跨包类型写限定名（money.Money），Import 给出包路径。
	GoType string
	// Import 该类型需要的额外 import（Go 源文件路径），空则无需额外导入。
	Import string
	// TSType 前端 TS 类型。
	TSType string
	// FormType ProTable formItems 缺省组件类型（枚举/字典字段自动改为 select）。
	FormType string
	// TSDefault 前端 defaultForm 缺省值字面量（JS 表达式）。
	TSDefault string
}

// moneyImport 是 money 值类型的包路径。
const moneyImport = "github.com/go-quick-code/go-admin-pro/internal/pkg/money"

// defaultFieldMappings 是逻辑类型映射表；key 即 FieldSpec.Type 的合法取值。
var defaultFieldMappings = map[string]fieldMapping{
	"string":   {GoType: "string", TSType: "string", FormType: "input", TSDefault: "''"},
	"text":     {GoType: "string", TSType: "string", FormType: "textarea", TSDefault: "''"},
	"int8":     {GoType: "int8", TSType: "number", FormType: "number", TSDefault: "0"},
	"int32":    {GoType: "int32", TSType: "number", FormType: "number", TSDefault: "0"},
	"int64":    {GoType: "int64", TSType: "number", FormType: "number", TSDefault: "0"},
	"money":    {GoType: "money.Money", Import: moneyImport, TSType: "string", FormType: "input", TSDefault: "''"},
	"datetime": {GoType: "*time.Time", Import: "time", TSType: "string", FormType: "datetime", TSDefault: "''"},
	"bool":     {GoType: "bool", TSType: "boolean", FormType: "switch", TSDefault: "false"},
}

// mappingKeys 返回映射表所有逻辑类型名（升序），用于错误文案与 README。
func mappingKeys() []string {
	keys := make([]string, 0, len(defaultFieldMappings))
	for k := range defaultFieldMappings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
