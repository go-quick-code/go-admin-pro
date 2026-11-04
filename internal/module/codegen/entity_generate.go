package codegen

import (
	"fmt"
	"go/format"
	"path"
	"sort"
	"strings"

	"github.com/lautung/go-admin-pro/internal/pkg/utils"
)

// GenerateEntity 按实体元信息生成「owner-scoped 实体全栈脚手架」，返回
// 相对产物路径 → 文件内容。纯函数：不读数据库与文件系统，同 spec 同产物。
//
// 产物覆盖：model / dto / repository / service / api 五层 + 路由注册点 +
// 菜单/权限码种子片段 + 测试骨架（纯函数 + 集成矩阵）+ 迁移模板 +
// web/src/api 追加片段 + ProTable 列表页 + INTEGRATION.md 装配说明。
// app.go / router.go / tenant_tables.go / seed.go 不自动改写，接线步骤在 INTEGRATION.md。
func GenerateEntity(spec *EntitySpec) (map[string]string, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	data := buildEntityData(spec)
	files := map[string]string{}
	add := func(name, tmpl string) {
		files[name] = mustRenderEntity(tmpl, data)
	}

	modDir := "go/internal/module/" + data.Module
	// ---------- 后端五层（per-entity 文件，方法挂在模块既有 Service/Repository/Handler 上） ----------
	add(modDir+"/model/"+data.Entity+".go", tmplEntityModel)
	add(modDir+"/dto/"+data.Entity+".go", tmplEntityDTO)
	add(modDir+"/repository/"+data.Entity+".go", tmplEntityRepository)
	add(modDir+"/service/"+data.Entity+".go", tmplEntityService)
	add(modDir+"/api/"+data.Entity+".go", tmplEntityAPI)
	// 集成测试骨架（//go:build integration，接线后随 make test-integration 生效）。
	add(modDir+"/"+data.Entity+"_scope_integration_test.go", tmplEntityIntegrationTest)
	if data.HasEnum {
		add(modDir+"/service/"+data.Entity+"_rules.go", tmplEntityRules)
		add(modDir+"/service/"+data.Entity+"_rules_test.go", tmplEntityRulesTest)
	}
	// ---------- 模块骨架（模块尚不存在时） ----------
	if data.ModuleScaffold {
		add(modDir+"/model/doc.go", tmplScaffoldModelDoc)
		add(modDir+"/dto/doc.go", tmplScaffoldDTODoc)
		add(modDir+"/repository/repository.go", tmplScaffoldRepository)
		add(modDir+"/service/service.go", tmplScaffoldService)
		add(modDir+"/api/router.go", tmplScaffoldRouter)
		add(modDir+"/api/helpers.go", tmplScaffoldHelpers)
	}
	// ---------- 前端 ----------
	add("web/src/api/"+data.Module+".append.ts", tmplEntityAPITS)
	add("web/src/views/"+data.Module+"/"+data.Entity+"/index.vue", tmplEntityVue)
	// ---------- 种子片段 / 迁移模板 / 装配说明 ----------
	if data.Menu != nil {
		add("seed/menus.append.go", tmplEntitySeed)
	}
	add("migrations/0000X_"+data.TableName+".tmpl", tmplEntityMigration)
	add("INTEGRATION.md", tmplEntityIntegration)

	// Go 产物统一过 gofmt：语法非法直接报错，保证产出可用（追加片段除外，
	// 它们是粘贴用的注释片段，不独立成包）。
	for name, content := range files {
		if !strings.HasSuffix(name, ".go") || strings.Contains(path.Base(name), ".append.") {
			continue
		}
		formatted, err := format.Source([]byte(content))
		if err != nil {
			return nil, fmt.Errorf("生成 %s 失败：%w", name, err)
		}
		files[name] = string(formatted)
	}
	return files, nil
}

// entityFieldData 是模板视角的单字段：在 FieldSpec 之上预推导命名、类型与标签，
// 把分支逻辑留在 Go 侧，模板只做拼装。
type entityFieldData struct {
	Name       string // 列名 unit_price
	GoName     string // UnitPrice
	JSONName   string // unitPrice
	Comment    string
	Type       string // 逻辑类型（defaultFieldMappings 的 key）
	GoType     string
	TSType     string
	Size       int
	Required   bool
	Searchable bool
	Dict       string
	Enum       *EnumSpec
	FormType   string // select / input / textarea / number / datetime / switch
	TSDefault  string
	HideInList bool

	DefaultInt int  // Default 的解引用值
	HasDefault bool // Default 是否配置

	IsEnum     bool
	IsDict     bool
	IsMoney    bool
	IsTime     bool
	NeedImport bool // 模型/DTO 需要额外 import（money/time）

	ModelTag  string // 结构标签（gorm + json，不含反引号）
	DTOTag    string // 结构标签（json + binding）
	DTOGoType string // DTO 字段类型（枚举 *int8、datetime string）
	// ServiceAssign 是 service 里 Create/Update 对该字段的赋值右值
	// （枚举取局部变量、datetime 走 parse 助手、其余取请求字段）。
	ServiceAssign string

	// 枚举字段推导（非枚举字段为零值）。
	EnumConstPrefix   string // ProductStatus
	EnumMinConst      string // ProductStatusOnSale（值最小的常量全名）
	EnumMaxConst      string
	EnumOptionsExpr   string // 1 在售 / 2 下架
	EnumValidValues   string // 1, 2
	EnumInvalidValues string // 0, 3
	OptionsVar        string // 前端选项变量名：productStatusOptions
	MigrationType     string // tinyint / varchar(128) / decimal(12,2) ...
	MigrationDefault  string // 迁移列 DEFAULT 值（空则不生成 DEFAULT）
	TestLiteral       string // 集成测试占位值字面量
	IsLastField       bool   // 迁移列清单的最后一行（不加逗号）
}

// entityData 是实体模板的渲染数据。
type entityData struct {
	Derived
	GenMark string

	Fields       []entityFieldData
	ListFields   []entityFieldData // 进列表列的
	SearchFields []entityFieldData // searchable 文本字段（LIKE）
	SelectFields []entityFieldData // enum/dict 字段（搜索与表单 select）
	EnumFields   []entityFieldData
	DictFields   []entityFieldData

	RequiredFillFields []entityFieldData // 集成测试创建占位：必填且非名称字段
	NameFieldJSON      string            // 集成测试用「名称」字段的 JSON 名

	HasEnum bool
	HasDict bool

	NeedMoney    bool // 模型需 import money
	NeedTime     bool // 模型需 import time
	DTONeedMoney bool

	HasTime      bool // 业务字段含 datetime（service 生成 parse 助手、前端时间列）
	OwnerScoped  bool
	ImportExport bool
	HasSequence  bool
	SeqPrefix    string
	BizType      string
	NoGoName     string // ProductNo
	NoJSON       string // productNo
	NoColumn     string // product_no

	Menu           *MenuSpec
	HasMenu        bool
	MenuID         int64
	MenuIDsExpr    string // 集成测试角色绑定的 menuIds 字面量
	MenuShortName  string // 菜单名去掉「管理」后缀，用于按钮文案
	ModuleScaffold bool
}

// buildEntityData 把 EntitySpec 推导成模板数据（假定 spec 已通过 Validate）。
func buildEntityData(spec *EntitySpec) *entityData {
	data := &entityData{
		Derived:        spec.Derived(),
		GenMark:        entityGenMark,
		OwnerScoped:    spec.OwnerScoped,
		ImportExport:   spec.ImportExport,
		HasSequence:    spec.Sequence != nil,
		ModuleScaffold: spec.ModuleScaffold,
		Menu:           spec.Menu,
	}
	if spec.Sequence != nil {
		data.SeqPrefix = spec.Sequence.Prefix
		data.BizType = spec.Sequence.BizType
		if data.BizType == "" {
			data.BizType = spec.Entity
		}
		data.NoGoName = data.ClassName + "No"
		data.NoJSON = utils.LowerCamelCase(data.ClassName) + "No"
		data.NoColumn = data.Entity + "_no"
	}
	if data.Menu != nil {
		data.HasMenu = true
		data.MenuID = data.Menu.MenuID
		data.MenuShortName = strings.TrimSuffix(data.Menu.Name, "管理")
		ids := append([]int64{data.Menu.MenuID}, data.Menu.ButtonIDs...)
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, fmt.Sprintf("%d", id))
		}
		data.MenuIDsExpr = strings.Join(parts, ", ")
	}

	for _, f := range spec.Fields {
		fd := buildEntityField(f)
		data.Fields = append(data.Fields, fd)
		data.HasEnum = data.HasEnum || fd.IsEnum
		data.HasDict = data.HasDict || fd.IsDict
		data.HasTime = data.HasTime || fd.IsTime
		data.NeedMoney = data.NeedMoney || fd.IsMoney
		data.NeedTime = data.NeedTime || fd.IsTime
		data.DTONeedMoney = data.DTONeedMoney || fd.IsMoney
	}

	// 先做二轮推导（会回写 Fields 内元素），再重建各分类切片——切片必须持有
	// 推导后的副本（OptionsVar / ServiceAssign 等），否则模板拿到零值。
	data.postProcess()
	data.rebuildFieldViews()
	return data
}

// rebuildFieldViews 从推导完成的 Fields 重建各分类切片。
func (d *entityData) rebuildFieldViews() {
	for _, fd := range d.Fields {
		switch {
		case fd.IsEnum:
			d.EnumFields = append(d.EnumFields, fd)
			d.SelectFields = append(d.SelectFields, fd)
		case fd.IsDict:
			d.DictFields = append(d.DictFields, fd)
			d.SelectFields = append(d.SelectFields, fd)
		}
		if fd.Searchable {
			d.SearchFields = append(d.SearchFields, fd)
		}
		if !fd.HideInList {
			d.ListFields = append(d.ListFields, fd)
		}
		if fd.Required && fd.JSONName != d.NameFieldJSON {
			d.RequiredFillFields = append(d.RequiredFillFields, fd)
		}
	}
}

// postProcess 做需要全量字段信息的二轮推导：名称字段、测试占位、前端选项变量、
// service 赋值右值、迁移列收尾。
func (d *entityData) postProcess() {
	// 名称字段：优先必填 string，其次任意 string / text。
	nameIdx := -1
	for i, f := range d.Fields {
		if f.Required && f.Type == "string" {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		for i, f := range d.Fields {
			if f.Type == "string" || f.Type == "text" {
				nameIdx = i
				break
			}
		}
	}
	if nameIdx >= 0 {
		d.NameFieldJSON = d.Fields[nameIdx].JSONName
	} else {
		d.NameFieldJSON = "name"
	}

	for i := range d.Fields {
		fd := &d.Fields[i]
		fd.IsLastField = i == len(d.Fields)-1
		fd.OptionsVar = d.VarName + fd.GoName + "Options"
		fd.ServiceAssign = d.serviceAssign(*fd)
	}
}

// serviceAssign 计算 service 层 Create/Update 的字段赋值右值。
func (d *entityData) serviceAssign(f entityFieldData) string {
	switch {
	case f.IsEnum:
		return f.GoName
	case f.IsTime:
		return fmt.Sprintf("parse%sTime(req.%s)", d.ClassName, f.GoName)
	default:
		return "req." + f.GoName
	}
}

// buildEntityField 推导单字段的命名、类型映射与标签（假定字段已通过 Validate）。
func buildEntityField(f FieldSpec) entityFieldData {
	mapping := defaultFieldMappings[f.Type]
	fd := entityFieldData{
		Name:       f.Name,
		GoName:     utils.CamelCase(f.Name),
		JSONName:   utils.LowerCamelCase(f.Name),
		Comment:    f.Comment,
		Type:       f.Type,
		GoType:     mapping.GoType,
		TSType:     mapping.TSType,
		Size:       f.Size,
		Required:   f.Required,
		Searchable: f.Searchable,
		Dict:       f.Dict,
		Enum:       f.Enum,
		FormType:   f.FormType,
		TSDefault:  mapping.TSDefault,
		HideInList: f.HideInList,
		IsEnum:     f.Enum != nil,
		IsDict:     f.Dict != "",
		IsMoney:    f.Type == "money",
		IsTime:     f.Type == "datetime",
		NeedImport: mapping.Import != "",
		HasDefault: f.Default != nil,
	}
	if f.Default != nil {
		fd.DefaultInt = *f.Default
	}
	if fd.FormType == "" {
		fd.FormType = mapping.FormType
	}
	if fd.IsEnum || fd.IsDict {
		// 枚举/字典字段统一渲染为 select（选项内联或接字典）。
		fd.FormType = "select"
	}
	if fd.HasDefault {
		fd.TSDefault = fmt.Sprintf("%d", fd.DefaultInt)
	} else if fd.IsEnum && len(f.Enum.Values) > 0 {
		// 枚举缺省取最小枚举值（模型 gorm default 同源）。
		fd.TSDefault = fmt.Sprintf("%d", minEnumValue(f.Enum))
	}
	fd.MigrationType = migrationType(fd)
	fd.MigrationDefault = migrationDefault(fd)
	fd.TestLiteral = testLiteral(fd)
	fd.EnumConstPrefix, fd.EnumMinConst, fd.EnumMaxConst, fd.EnumOptionsExpr,
		fd.EnumValidValues, fd.EnumInvalidValues = enumInfo(f.Enum)
	fd.ModelTag = modelTag(fd)
	fd.DTOGoType = dtoGoType(fd)
	fd.DTOTag = dtoTag(fd)
	return fd
}

// modelTag 生成模型字段结构标签（gorm + json，不含反引号；与 crm 手写样板同风格）。
func modelTag(f entityFieldData) string {
	tag := ""
	if g := buildGormTag(f); g != "" {
		tag = `gorm:"` + g + `" `
	}
	return tag + `json:"` + f.JSONName + `"`
}

// buildGormTag 生成 gorm 标签内容（不含 gorm:"" 外壳与 json 部分）。
func buildGormTag(f entityFieldData) string {
	notNull := ""
	if f.Required {
		notNull = ";not null"
	}
	switch {
	case f.IsMoney:
		return `type:decimal(12,2)` + notNull
	case f.IsTime:
		return "" // 可空时间列与底座同款：无类型标签
	case f.IsEnum || f.GoType == "int8":
		def := 0
		if f.HasDefault {
			def = f.DefaultInt
		} else if f.IsEnum && len(f.Enum.Values) > 0 {
			def = int(minEnumValue(f.Enum))
		}
		return fmt.Sprintf("not null;default:%d", def)
	case f.GoType == "bool":
		return "not null;default:0"
	case f.GoType == "int64" || f.GoType == "int32":
		return "index"
	case f.Size > 0:
		return fmt.Sprintf("size:%d%s", f.Size, notNull)
	default:
		return ""
	}
}

// dtoGoType 计算 DTO 字段类型：枚举用指针表达「未传」，datetime 用字符串
// （前端 datetime picker 的 value-format 与 parse 助手约定一致）。
func dtoGoType(f entityFieldData) string {
	switch {
	case f.IsEnum:
		return "*int8"
	case f.IsTime:
		return "string"
	default:
		return f.GoType
	}
}

// dtoTag 生成 DTO 字段结构标签（json + binding；枚举不设 binding）。
func dtoTag(f entityFieldData) string {
	tag := `json:"` + f.JSONName + `"`
	if binding := fieldBinding(f); binding != "" && !f.IsEnum {
		tag += ` binding:"` + binding + `"`
	}
	return tag
}

// binding 内容：required + 长度上限（string/text）。
func fieldBinding(f entityFieldData) string {
	var parts []string
	if f.Required {
		parts = append(parts, "required")
	}
	if (f.Type == "string" || f.Type == "text") && f.Size > 0 {
		parts = append(parts, fmt.Sprintf("max=%d", f.Size))
	}
	return strings.Join(parts, ",")
}

// migrationType 计算迁移列类型（模板里拼 DDL 用）。
func migrationType(f entityFieldData) string {
	switch {
	case f.Type == "string":
		return fmt.Sprintf("varchar(%d)", f.Size)
	case f.Type == "text":
		if f.Size > 0 {
			return fmt.Sprintf("varchar(%d)", f.Size)
		}
		return "text"
	case f.Type == "int8":
		return "tinyint"
	case f.Type == "int32":
		return "int"
	case f.Type == "int64":
		return "bigint"
	case f.IsMoney:
		return "decimal(12,2)"
	case f.IsTime:
		return "datetime(3)"
	default:
		return "tinyint" // bool
	}
}

// migrationDefault 计算迁移列 DEFAULT 值（空串表示不生成 DEFAULT）。
func migrationDefault(f entityFieldData) string {
	switch {
	case f.IsEnum:
		if f.HasDefault {
			return fmt.Sprintf("%d", f.DefaultInt)
		}
		return fmt.Sprintf("%d", minEnumValue(f.Enum))
	case f.GoType == "int8" || f.GoType == "int32" || f.GoType == "int64":
		return fmt.Sprintf("%d", f.DefaultInt)
	case f.GoType == "bool":
		return "0"
	default:
		return ""
	}
}

// testLiteral 计算集成测试创建占位值。
func testLiteral(f entityFieldData) string {
	switch {
	case f.IsMoney:
		return `"9.90"`
	case f.IsTime:
		return `"2026-01-01 00:00:00"`
	case f.GoType == "bool":
		return "true"
	case f.GoType == "int8" || f.GoType == "int32" || f.GoType == "int64":
		return "1"
	default:
		return `"占位"`
	}
}

// enumInfo 计算枚举字段的常量名区间、选项文案与合法/非法值清单。
func enumInfo(e *EnumSpec) (prefix, minConst, maxConst, options, valid, invalid string) {
	if e == nil || len(e.Values) == 0 {
		return "", "", "", "", "", ""
	}
	prefix = e.Type
	sorted := make([]EnumValue, len(e.Values))
	copy(sorted, e.Values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Value < sorted[j].Value })
	minConst = prefix + sorted[0].Name
	maxConst = prefix + sorted[len(sorted)-1].Name

	optParts := make([]string, 0, len(sorted))
	validParts := make([]string, 0, len(sorted))
	for _, v := range sorted {
		optParts = append(optParts, fmt.Sprintf("%d %s", v.Value, v.Label))
		validParts = append(validParts, fmt.Sprintf("%d", v.Value))
	}
	options = strings.Join(optParts, " / ")
	valid = strings.Join(validParts, ", ")
	invalid = fmt.Sprintf("%d, %d", int(sorted[0].Value)-1, int(sorted[len(sorted)-1].Value)+1)
	return prefix, minConst, maxConst, options, valid, invalid
}

// minEnumValue 返回枚举最小值（模型/前端缺省同源）。
func minEnumValue(e *EnumSpec) int8 {
	minV := e.Values[0].Value
	for _, v := range e.Values {
		if v.Value < minV {
			minV = v.Value
		}
	}
	return minV
}
