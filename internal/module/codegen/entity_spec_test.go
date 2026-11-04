package codegen_test

import (
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/codegen"
)

// validProductSpec 返回一份合法的产品实体元信息（与 testdata/entity/product.json 同构），
// 供各测试复用；用例内按需改字段构造非法输入。
func validProductSpec() *codegen.EntitySpec {
	return &codegen.EntitySpec{
		Module:       "crm",
		Entity:       "product",
		TableName:    "crm_product",
		Comment:      "产品",
		OwnerScoped:  true,
		ImportExport: true,
		Menu: &codegen.MenuSpec{
			MenuID:    402,
			ParentID:  400,
			Name:      "产品管理",
			Path:      "product",
			Icon:      "Goods",
			Sort:      2,
			ButtonIDs: []int64{4021, 4022, 4023, 4024},
		},
		Fields: []codegen.FieldSpec{
			{Name: "name", Type: "string", Size: 128, Comment: "产品名称", Required: true, Searchable: true},
			{Name: "category", Type: "string", Size: 64, Comment: "产品分类"},
			{Name: "unit", Type: "string", Size: 32, Comment: "单位", Dict: "crm_product_unit"},
			{Name: "price", Type: "money", Comment: "价格（元）", Required: true},
			{Name: "status", Type: "int8", Comment: "状态", Default: intPtr(1), Enum: &codegen.EnumSpec{
				Type: "ProductStatus",
				Values: []codegen.EnumValue{
					{Name: "OnSale", Value: 1, Label: "在售"},
					{Name: "OffShelf", Value: 2, Label: "下架"},
				},
			}},
			{Name: "remark", Type: "text", Size: 500, Comment: "备注"},
		},
	}
}

func intPtr(v int) *int { return &v }

func TestEntitySpecValidate(t *testing.T) {
	t.Run("合法的产品元信息", func(t *testing.T) {
		if err := validProductSpec().Validate(); err != nil {
			t.Fatalf("合法元信息不应报错，实际 %v", err)
		}
	})

	t.Run("非法输入一律拒绝并给出中文原因", func(t *testing.T) {
		cases := []struct {
			name    string
			mutate  func(s *codegen.EntitySpec)
			wantMsg string
		}{
			{"缺模块名", func(s *codegen.EntitySpec) { s.Module = "" }, "模块名"},
			{"模块名大写", func(s *codegen.EntitySpec) { s.Module = "CRM" }, "模块名"},
			{"缺实体名", func(s *codegen.EntitySpec) { s.Entity = "" }, "实体名"},
			{"实体名大驼峰", func(s *codegen.EntitySpec) { s.Entity = "Product" }, "实体名"},
			{"表名不匹配", func(s *codegen.EntitySpec) { s.TableName = "crm-product" }, "表名"},
			{"无字段", func(s *codegen.EntitySpec) { s.Fields = nil }, "字段"},
			{"字段名保留列", func(s *codegen.EntitySpec) {
				s.Fields[0].Name = "tenant_id"
			}, "保留"},
			{"未知字段类型", func(s *codegen.EntitySpec) { s.Fields[0].Type = "varchar" }, "类型"},
			{"string 缺 size", func(s *codegen.EntitySpec) { s.Fields[0].Size = 0 }, "size"},
			{"枚举字段重复值", func(s *codegen.EntitySpec) {
				s.Fields[4].Enum.Values[1].Value = 1
			}, "枚举"},
			{"枚举值缺 Label", func(s *codegen.EntitySpec) {
				s.Fields[4].Enum.Values[0].Label = ""
			}, "枚举"},
			{"字典与枚举互斥", func(s *codegen.EntitySpec) {
				s.Fields[4].Dict = "crm_product_status"
			}, "字典"},
			{"searchable 只支持文本", func(s *codegen.EntitySpec) {
				s.Fields[3].Searchable = true
			}, "searchable"},
			{"权限码前缀含大写", func(s *codegen.EntitySpec) { s.PermPrefix = "crm:Product" }, "权限码"},
			{"菜单缺按钮 ID", func(s *codegen.EntitySpec) {
				s.Menu.ButtonIDs = s.Menu.ButtonIDs[:3]
			}, "按钮"},
			{"菜单 ID 重复", func(s *codegen.EntitySpec) {
				s.Menu.ButtonIDs[3] = 402
			}, "菜单"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				spec := validProductSpec()
				tc.mutate(spec)
				err := spec.Validate()
				if err == nil {
					t.Fatalf("期望报错（含 %q），实际通过", tc.wantMsg)
				}
				if !strings.Contains(err.Error(), tc.wantMsg) {
					t.Fatalf("错误文案应包含 %q，实际 %q", tc.wantMsg, err.Error())
				}
			})
		}
	})
}

func TestEntitySpecDerived(t *testing.T) {
	spec := validProductSpec()
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	d := spec.Derived()
	cases := map[string]string{
		"ClassName": d.ClassName,  // 期望 Product
		"VarName":   d.VarName,    // 期望 product
		"Perm":      d.PermPrefix, // 期望 crm:product
		"Route":     d.RouteGroup, // 期望 crm/product
		"ViewDir":   d.ViewDir,    // 期望 crm/product/index
	}
	wants := map[string]string{
		"ClassName": "Product",
		"VarName":   "product",
		"Perm":      "crm:product",
		"Route":     "crm/product",
		"ViewDir":   "crm/product/index",
	}
	for name, got := range cases {
		if got != wants[name] {
			t.Fatalf("%s = %q，期望 %q", name, got, wants[name])
		}
	}

	// 显式权限码与路由组优先于缺省推导。
	spec.PermPrefix = "crm:sku"
	spec.APIGroup = "crm/sku"
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	d = spec.Derived()
	if d.PermPrefix != "crm:sku" || d.RouteGroup != "crm/sku" {
		t.Fatalf("显式配置未生效：perm=%q route=%q", d.PermPrefix, d.RouteGroup)
	}

	// 下划线实体名推大驼峰：sale_lead → SaleLead。
	spec = validProductSpec()
	spec.Entity = "sale_lead"
	spec.TableName = "crm_sale_lead"
	spec.APIGroup = "crm/sale-lead"
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := spec.Derived().ClassName; got != "SaleLead" {
		t.Fatalf("sale_lead 的 ClassName = %q，期望 SaleLead", got)
	}
}
