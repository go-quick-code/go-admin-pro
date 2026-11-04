package codegen

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"path"
	"strings"

	"github.com/lautung/go-admin-pro/internal/pkg/utils"
)

// GenerateReq 是代码生成参数。
type GenerateReq struct {
	TableName    string `json:"tableName" binding:"required"`
	ModuleName   string `json:"moduleName" binding:"required"`
	BusinessName string `json:"businessName" binding:"required"`
	ClassName    string `json:"className"`
	TableComment string `json:"tableComment"`
	Author       string `json:"author"`
}

type fieldTemplate struct {
	GoName     string
	JSONName   string
	ColumnName string
	GoType     string
	TSType     string
	Comment    string
	FormType   string
	ZeroValue  string
	IsNumber   bool
	IsTime     bool
	IsText     bool
	Searchable bool
}

type templateData struct {
	TableName    string
	TableComment string
	ClassName    string
	BusinessName string
	ModuleName   string
	VarName      string
	Author       string
	Fields       []fieldTemplate
	SearchFields []fieldTemplate
	HasTime      bool
}

// Generate 生成 Go + Vue 代码，返回 zip 字节与文件名。
func (g *Generator) Generate(ctx context.Context, req *GenerateReq) ([]byte, string, error) {
	if err := req.validate(); err != nil {
		return nil, "", err
	}
	table, err := g.GetTable(ctx, req.TableName)
	if err != nil {
		return nil, "", err
	}
	if req.TableComment != "" {
		table.Comment = req.TableComment
	}

	data := buildTemplateData(table, req)
	files := map[string]string{
		"go/internal/module/" + req.ModuleName + "/model/" + req.BusinessName + ".go":   mustRender(tmplModel, data),
		"go/internal/module/" + req.ModuleName + "/dto/" + req.BusinessName + ".go":     mustRender(tmplDTO, data),
		"go/internal/module/" + req.ModuleName + "/service/" + req.BusinessName + ".go": mustRender(tmplService, data),
		"go/internal/module/" + req.ModuleName + "/api/" + req.BusinessName + ".go":     mustRender(tmplHandler, data),
		"web/src/api/" + req.ModuleName + "/" + req.BusinessName + ".ts":                mustRender(tmplAPI, data),
		"web/src/views/" + req.ModuleName + "/" + req.BusinessName + "/index.vue":       mustRender(tmplVue, data),
		"README.md": mustRender(tmplReadme, data),
	}

	order := []string{
		"go/internal/module/" + req.ModuleName + "/model/" + req.BusinessName + ".go",
		"go/internal/module/" + req.ModuleName + "/dto/" + req.BusinessName + ".go",
		"go/internal/module/" + req.ModuleName + "/service/" + req.BusinessName + ".go",
		"go/internal/module/" + req.ModuleName + "/api/" + req.BusinessName + ".go",
		"web/src/api/" + req.ModuleName + "/" + req.BusinessName + ".ts",
		"web/src/views/" + req.ModuleName + "/" + req.BusinessName + "/index.vue",
		"README.md",
	}

	var buf bytes.Buffer
	zw := newZipWriter(&buf)
	for _, name := range order {
		content := files[name]
		if strings.HasSuffix(name, ".go") {
			formatted, err := format.Source([]byte(content))
			if err != nil {
				return nil, "", fmt.Errorf("生成 %s 失败：%w", name, err)
			}
			content = string(formatted)
		}
		if err := zw.add(name, []byte(content)); err != nil {
			return nil, "", err
		}
	}
	if err := zw.close(); err != nil {
		return nil, "", err
	}
	filename := fmt.Sprintf("%s-%s.zip", req.ModuleName, req.BusinessName)
	return buf.Bytes(), filename, nil
}

func (r *GenerateReq) validate() error {
	if !tableNameRe.MatchString(r.TableName) {
		return fmt.Errorf("非法的表名")
	}
	if !moduleRe.MatchString(r.ModuleName) {
		return fmt.Errorf("模块名只能包含小写字母、数字、下划线，且以字母开头")
	}
	if !businessRe.MatchString(r.BusinessName) {
		return fmt.Errorf("业务名只能包含字母、数字，且以字母开头")
	}
	if r.ClassName != "" && !businessRe.MatchString(r.ClassName) {
		return fmt.Errorf("类名只能包含字母、数字，且以字母开头")
	}
	return nil
}

func buildTemplateData(table *TableInfo, req *GenerateReq) templateData {
	className := req.ClassName
	if className == "" {
		className = utils.CamelCase(req.BusinessName)
	}
	data := templateData{
		TableName:    table.Name,
		TableComment: table.Comment,
		ClassName:    className,
		BusinessName: req.BusinessName,
		ModuleName:   req.ModuleName,
		VarName:      utils.LowerCamelCase(className),
		Author:       req.Author,
	}
	if data.TableComment == "" {
		data.TableComment = table.Name
	}

	for _, col := range table.Columns {
		if isSystemColumn(col.Name) {
			continue
		}
		field := fieldTemplate{
			GoName:     utils.CamelCase(col.Name),
			JSONName:   utils.LowerCamelCase(col.Name),
			ColumnName: col.Name,
			Comment:    col.Comment,
			GoType:     goType(col),
		}
		if field.Comment == "" {
			field.Comment = col.Name
		}
		field.TSType = tsType(field.GoType)
		field.IsNumber = isNumberType(field.GoType)
		field.IsTime = field.GoType == "*time.Time"
		field.IsText = isTextType(col.DataType)
		field.FormType = formType(field)
		switch {
		case field.IsNumber:
			field.ZeroValue = "0"
		default:
			field.ZeroValue = "''"
		}
		if field.IsTime {
			field.ZeroValue = "''"
		}
		field.Searchable = isSearchable(col, field)
		data.Fields = append(data.Fields, field)
	}

	for _, f := range data.Fields {
		if f.IsTime {
			data.HasTime = true
			break
		}
	}
	for _, f := range data.Fields {
		if f.Searchable {
			data.SearchFields = append(data.SearchFields, f)
			if len(data.SearchFields) >= 3 {
				break
			}
		}
	}
	if len(data.SearchFields) == 0 {
		for _, f := range data.Fields {
			if f.GoType == "string" && !f.IsText {
				data.SearchFields = append(data.SearchFields, f)
				break
			}
		}
	}
	return data
}

func isSystemColumn(name string) bool {
	switch name {
	case "id", "tenant_id", "created_at", "updated_at", "deleted_at":
		return true
	}
	return false
}

func goType(col ColumnInfo) string {
	switch strings.ToLower(col.DataType) {
	case "bigint":
		return "int64"
	case "int", "integer", "mediumint", "smallint":
		return "int32"
	case "tinyint":
		return "int8"
	case "float":
		return "float32"
	case "double", "decimal", "numeric":
		return "float64"
	case "datetime", "timestamp", "date", "time":
		return "*time.Time"
	case "json":
		return "string"
	default:
		return "string"
	}
}

func tsType(goType string) string {
	switch goType {
	case "int64", "int32", "int8", "float32", "float64":
		return "number"
	default:
		return "string"
	}
}

func isNumberType(goType string) bool {
	switch goType {
	case "int64", "int32", "int8", "float32", "float64":
		return true
	}
	return false
}

func isTextType(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "text", "longtext", "mediumtext", "tinytext", "json":
		return true
	}
	return false
}

func formType(f fieldTemplate) string {
	switch {
	case f.IsTime:
		return "datetime"
	case f.IsText:
		return "textarea"
	case f.IsNumber:
		return "number"
	default:
		return "input"
	}
}

func isSearchable(col ColumnInfo, f fieldTemplate) bool {
	if f.IsTime || f.IsNumber || f.IsText {
		return false
	}
	name := strings.ToLower(col.Name)
	for _, key := range []string{"name", "title", "code", "mobile", "username"} {
		if strings.Contains(name, key) {
			return true
		}
	}
	return false
}

// 生成文件路径仅用于 README 展示。
func modulePath(module string) string {
	return path.Join("internal/module", module)
}
