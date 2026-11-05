package codegen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/utils"
)

// 本文件定义「owner-scoped 实体全栈脚手架」的元信息（EntitySpec）与校验/命名推导。
// 生成行为在 entity_generate.go，模板在 entity_templates.go。
//
// 元信息是唯一输入：不读数据库、不读文件系统，同一个 spec 永远生成同一批产物
// （golden 测试与 cmd/codegen CLI 都以此为前提）。

// EntitySpec 是生成 owner-scoped 实体脚手架的元信息（JSON 可直接反序列化，
// 供 HTTP 端点与 cmd/codegen CLI 复用）。
type EntitySpec struct {
	// Module 所属模块目录名（internal/module/<module>），小写下划线：crm。
	Module string `json:"module"`
	// Entity 实体名（snake_case）：product；Go 类型名按大驼峰推导 Product。
	Entity string `json:"entity"`
	// TableName 数据库表名：crm_product。
	TableName string `json:"tableName"`
	// Comment 实体中文名，用于页面标题、菜单与错误文案：产品。
	Comment string `json:"comment"`
	// PermPrefix 权限码前缀（<module>:<entity>），空则按模块与实体名推导：crm:product。
	PermPrefix string `json:"permPrefix"`
	// APIGroup 路由组（相对 /admin-api），空则 <module>/<entity>：crm/product。
	APIGroup string `json:"apiGroup"`
	// ModuleScaffold 模块骨架尚不存在时一并生成（Service/Repository/Handler 骨架、
	// api/helpers、api/router、各层 package doc）。模块已有骨架（如 crm）置 false，
	// 只生成 per-entity 文件。
	ModuleScaffold bool `json:"moduleScaffold"`
	// OwnerScoped 是否为 owner-scoped 实体：模型带 owner_user_id / owner_dept_id，
	// 列表套数据范围谓词、详情/改/删 scope.Permits（范围外 404）。
	OwnerScoped bool `json:"ownerScoped"`
	// ImportExport 预留 #20 Excel 导入导出接线位：生成的 service/api 带 TODO 注释，
	// INTEGRATION.md 列出接线清单，本工单不产出可用实现。
	ImportExport bool `json:"importExport"`
	// Sequence 业务编号（per-entity 选项）：非 nil 时走 crm_sequence 发号
	// （owner-scoped 实体默认不带编号）。
	Sequence *SequenceSpec `json:"sequence"`
	// Menu 菜单/权限码种子片段；nil 则不生成种子文件。
	Menu *MenuSpec `json:"menu"`
	// Fields 业务字段（不含 id/tenant_id/审计列；owner 列由 OwnerScoped 自动补）。
	Fields []FieldSpec `json:"fields"`
}

// FieldSpec 是单个业务字段的元信息。
type FieldSpec struct {
	// Name 列名（snake_case）：unit_price。
	Name string `json:"name"`
	// Type 逻辑类型，取值见 defaultFieldMappings：
	// string / text / int8 / int32 / int64 / money / datetime / bool。
	Type string `json:"type"`
	// Size varchar 长度（string 类型必填，生成 binding max 与 gorm size 标签）。
	Size int `json:"size"`
	// Comment 中文注释（页面列名、表单标签、迁移注释）。
	Comment string `json:"comment"`
	// Required 必填（binding required；money 类型前端提交 0.00 也会被拒）。
	Required bool `json:"required"`
	// Searchable 列表模糊筛选（仅 string/text；生成 LIKE 条件与搜索项）。
	Searchable bool `json:"searchable"`
	// Dict 系统字典类型名（如 crm_product_unit）：前端渲染 select，
	// 选项接系统字典（web 暂无字典 store，生成 TODO 占位）。与 Enum 互斥。
	Dict string `json:"dict"`
	// Enum Go 枚举常量 + service 校验 + 前端 select。与 Dict 互斥。
	Enum *EnumSpec `json:"enum"`
	// Default 整型默认值（gorm default 标签与前端 defaultForm）。
	Default *int `json:"default"`
	// FormType 覆盖前端表单组件（ProTable 类型：input/select/textarea/number/datetime）。
	FormType string `json:"formType"`
	// HideInList 不出现在列表列（详情/表单仍可编辑）。
	HideInList bool `json:"hideInList"`
}

// EnumSpec 定义整型枚举字段（模型常量 + Valid 函数 + service 400 校验）。
// 约定：枚举值连续（Valid 按 min/max 区间判断，与客户状态同款）。
type EnumSpec struct {
	// Type Go 常量中缀名：ProductStatus → ProductStatusOnSale。
	Type string `json:"type"`
	// Values 枚举值（Value 不得重复，Label 必填）。
	Values []EnumValue `json:"values"`
}

// EnumValue 是单个枚举值。
type EnumValue struct {
	// Name Go 常量后缀（大驼峰）：OnSale。
	Name string `json:"name"`
	// Value 枚举数值。
	Value int8 `json:"value"`
	// Label 前端显示文案：在售。
	Label string `json:"label"`
}

// SequenceSpec 是业务编号选项（crm_sequence 发号，KH202611050001 风格）。
type SequenceSpec struct {
	// Prefix 编号前缀：KH。
	Prefix string `json:"prefix"`
	// BizType crm_sequence 的 biz_type，空则取实体名。
	BizType string `json:"bizType"`
}

// MenuSpec 是菜单/权限码种子片段的输入（对齐 internal/seed/seed.go 的 menu/btn 行）。
type MenuSpec struct {
	// MenuID 菜单 ID（种子里手工分配，需与全库既有 ID 不冲突）。
	MenuID int64 `json:"menuId"`
	// ParentID 父目录 ID（如 CRM 目录 400）。
	ParentID int64 `json:"parentId"`
	// Name 菜单名：产品管理。
	Name string `json:"name"`
	// Path 菜单路径段：product。
	Path string `json:"path"`
	// Icon Element Plus 图标名：Goods。
	Icon string `json:"icon"`
	// Sort 排序。
	Sort int `json:"sort"`
	// ButtonIDs 四个按钮权限 ID，顺序固定：查询/新增/修改/删除。
	ButtonIDs []int64 `json:"buttonIds"`
}

var (
	entityModuleRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	entityNameRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	entityTableRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	entityFieldRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	entityEnumRe    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	entityPermSegRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	entityPathSegRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	entityGoIdentRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	entityLabelRe   = regexp.MustCompile(`\S`)
)

// reservedColumns 是实体字段不允许使用的列名（底座/租户/审计/owner 列由生成器自己补）。
var reservedColumns = map[string]bool{
	"id": true, "tenant_id": true,
	"created_at": true, "updated_at": true, "deleted_at": true,
	"creator": true, "updater": true,
	"owner_user_id": true, "owner_dept_id": true,
}

// Validate 校验实体元信息；错误文案面向使用者，说清哪个字段不合法（CODING_STANDARDS）。
func (s *EntitySpec) Validate() error {
	if !entityModuleRe.MatchString(s.Module) {
		return fmt.Errorf("模块名 %q 不合法：只能包含小写字母、数字、下划线，且以字母开头", s.Module)
	}
	if !entityNameRe.MatchString(s.Entity) {
		return fmt.Errorf("实体名 %q 不合法：只能包含小写字母、数字、下划线，且以字母开头", s.Entity)
	}
	if !entityTableRe.MatchString(s.TableName) {
		return fmt.Errorf("表名 %q 不合法：只能包含字母、数字、下划线", s.TableName)
	}
	if s.Comment == "" {
		return fmt.Errorf("实体中文名（comment）不能为空")
	}
	if err := validatePermPrefix(s.PermPrefix); err != nil {
		return err
	}
	if err := validateAPIGroup(s.APIGroup); err != nil {
		return err
	}
	if len(s.Fields) == 0 {
		return fmt.Errorf("至少需要一个业务字段")
	}
	seen := map[string]bool{}
	for i := range s.Fields {
		if err := validateField(&s.Fields[i]); err != nil {
			return err
		}
		if seen[s.Fields[i].Name] {
			return fmt.Errorf("字段 %q 重复定义", s.Fields[i].Name)
		}
		seen[s.Fields[i].Name] = true
	}
	if s.Sequence != nil {
		if s.Sequence.Prefix == "" {
			return fmt.Errorf("编号前缀（sequence.prefix）不能为空")
		}
		if !entityGoIdentRe.MatchString(s.Sequence.Prefix) {
			return fmt.Errorf("编号前缀 %q 不合法：只能包含字母与数字，且以字母开头", s.Sequence.Prefix)
		}
		if s.Sequence.BizType != "" && !entityNameRe.MatchString(s.Sequence.BizType) {
			return fmt.Errorf("编号业务类型 %q 不合法：只能包含小写字母、数字、下划线", s.Sequence.BizType)
		}
	}
	if s.Menu != nil {
		if err := validateMenu(s.Menu); err != nil {
			return err
		}
	}
	return nil
}

func validatePermPrefix(perm string) error {
	if perm == "" {
		return nil
	}
	for _, seg := range strings.Split(perm, ":") {
		if !entityPermSegRe.MatchString(seg) {
			return fmt.Errorf("权限码前缀 %q 不合法：各段只能包含小写字母、数字、下划线，以冒号分隔", perm)
		}
	}
	return nil
}

func validateAPIGroup(group string) error {
	if group == "" {
		return nil
	}
	if strings.HasPrefix(group, "/") || strings.HasSuffix(group, "/") {
		return fmt.Errorf("路由组 %q 不合法：不以斜杠开头或结尾", group)
	}
	for _, seg := range strings.Split(group, "/") {
		if !entityPathSegRe.MatchString(seg) {
			return fmt.Errorf("路由组 %q 不合法：各段只能包含小写字母、数字、下划线、中划线", group)
		}
	}
	return nil
}

func validateField(f *FieldSpec) error {
	if !entityFieldRe.MatchString(f.Name) {
		return fmt.Errorf("字段名 %q 不合法：只能包含小写字母、数字、下划线，且以字母开头", f.Name)
	}
	if reservedColumns[f.Name] {
		return fmt.Errorf("字段名 %q 是保留列（底座/租户/审计/owner 列由生成器自动补）", f.Name)
	}
	if _, ok := defaultFieldMappings[f.Type]; !ok {
		return fmt.Errorf("字段 %q 的类型 %q 不在字段映射表内（支持：%s）",
			f.Name, f.Type, strings.Join(mappingKeys(), " / "))
	}
	if f.Comment == "" {
		return fmt.Errorf("字段 %q 的中文注释（comment）不能为空", f.Name)
	}
	if f.Type == "string" && f.Size <= 0 {
		return fmt.Errorf("字段 %q 是 string 类型，必须指定 size", f.Name)
	}
	if f.Searchable && f.Type != "string" && f.Type != "text" {
		return fmt.Errorf("字段 %q 的 searchable 只支持 string/text 类型", f.Name)
	}
	if f.Dict != "" && f.Enum != nil {
		return fmt.Errorf("字段 %q 的字典（dict）与枚举（enum）互斥", f.Name)
	}
	if f.Enum != nil {
		if err := validateEnum(f.Enum); err != nil {
			return fmt.Errorf("字段 %q 的枚举不合法：%w", f.Name, err)
		}
	}
	return nil
}

func validateEnum(e *EnumSpec) error {
	if !entityEnumRe.MatchString(e.Type) {
		return fmt.Errorf("枚举类型名 %q 不合法：应为大驼峰 Go 标识符（如 ProductStatus）", e.Type)
	}
	if len(e.Values) == 0 {
		return fmt.Errorf("枚举至少需要一个值")
	}
	seen := map[int8]bool{}
	for _, v := range e.Values {
		if !entityGoIdentRe.MatchString(v.Name) {
			return fmt.Errorf("枚举常量名 %q 不合法：应为 Go 标识符", v.Name)
		}
		if seen[v.Value] {
			return fmt.Errorf("枚举值 %d 重复", v.Value)
		}
		seen[v.Value] = true
		if !entityLabelRe.MatchString(v.Label) {
			return fmt.Errorf("枚举常量 %s 的显示文案（label）不能为空", v.Name)
		}
	}
	return nil
}

func validateMenu(m *MenuSpec) error {
	if m.MenuID <= 0 || m.ParentID <= 0 {
		return fmt.Errorf("菜单 ID 与父目录 ID 必须为正数")
	}
	if m.Name == "" || m.Path == "" {
		return fmt.Errorf("菜单名与路径不能为空")
	}
	if len(m.ButtonIDs) != 4 {
		return fmt.Errorf("菜单需要 4 个按钮权限 ID（查询/新增/修改/删除），实际 %d 个", len(m.ButtonIDs))
	}
	ids := map[int64]bool{m.MenuID: true}
	for _, id := range m.ButtonIDs {
		if id <= 0 {
			return fmt.Errorf("按钮权限 ID 必须为正数")
		}
		if ids[id] {
			return fmt.Errorf("菜单 ID %d 重复", id)
		}
		ids[id] = true
	}
	return nil
}

// Derived 是从 EntitySpec 推导出的命名与路径信息（GenerateEntity 内部使用，
// 导出供 CLI 打印与测试断言）。
type Derived struct {
	Module     string
	Entity     string
	ClassName  string // Product
	VarName    string // product
	TableName  string
	Comment    string
	PermPrefix string // crm:product（空则按 module:entity 推导）
	RouteGroup string // crm/product（空则按 module/entity 推导）
	ViewDir    string // crm/product/index（web/src/views 下的组件路径）
}

// Derived 推导命名信息；调用前应先 Validate。
func (s *EntitySpec) Derived() Derived {
	perm := s.PermPrefix
	if perm == "" {
		perm = s.Module + ":" + s.Entity
	}
	route := s.APIGroup
	if route == "" {
		route = s.Module + "/" + s.Entity
	}
	return Derived{
		Module:     s.Module,
		Entity:     s.Entity,
		ClassName:  utils.CamelCase(s.Entity),
		VarName:    utils.LowerCamelCase(s.Entity),
		TableName:  s.TableName,
		Comment:    s.Comment,
		PermPrefix: perm,
		RouteGroup: route,
		ViewDir:    s.Module + "/" + s.Entity + "/index",
	}
}
