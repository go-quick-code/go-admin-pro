package codegen_test

import (
	"go/format"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/codegen"
)

// TestGenerateEntityProductFileSet 断言产品实体（crm 模块、模块骨架已存在）生成的文件集。
func TestGenerateEntityProductFileSet(t *testing.T) {
	files, err := codegen.GenerateEntity(validProductSpec())
	if err != nil {
		t.Fatalf("GenerateEntity: %v", err)
	}
	want := []string{
		"INTEGRATION.md",
		"migrations/0000X_crm_product.tmpl",
		"seed/menus.append.go",
		"go/internal/module/crm/model/product.go",
		"go/internal/module/crm/dto/product.go",
		"go/internal/module/crm/repository/product.go",
		"go/internal/module/crm/service/product.go",
		"go/internal/module/crm/service/product_rules.go",
		"go/internal/module/crm/service/product_rules_test.go",
		"go/internal/module/crm/api/product.go",
		"go/internal/module/crm/product_scope_integration_test.go",
		"web/src/api/crm.append.ts",
		"web/src/views/crm/product/index.vue",
	}
	for _, name := range want {
		if _, ok := files[name]; !ok {
			t.Errorf("缺少产物 %s；实际文件集：%v", name, slices.Sorted(maps.Keys(files)))
		}
	}
	if len(files) != len(want) {
		t.Errorf("产物数量 %d，期望 %d；多出：%v", len(files), len(want),
			slices.Sorted(maps.Keys(files)))
	}
	// 所有 Go 产物必须能通过 gofmt 解析（语法合法性最低门槛）。
	for name, content := range files {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "append.go") {
			continue
		}
		if _, err := format.Source([]byte(content)); err != nil {
			t.Errorf("产物 %s 不是合法 Go 源码：%v", name, err)
		}
	}
}

// TestGenerateEntityProductKeyContent 断言产物内容落在既定样板形态上（customer 同款语义）。
func TestGenerateEntityProductKeyContent(t *testing.T) {
	files, err := codegen.GenerateEntity(validProductSpec())
	if err != nil {
		t.Fatalf("GenerateEntity: %v", err)
	}

	cases := []struct {
		file string
		want []string
	}{
		{"go/internal/module/crm/model/product.go", []string{
			"model.BaseEntity",
			`func (Product) TableName() string { return "crm_product" }`,
			"OwnerUserID int64", // owner 字段自动补齐
			"OwnerDeptID int64",
			"ProductStatusOnSale   int8 = 1",
			"money.Money",          // money 字段映射
			"OwnerUserName string", // 非持久化展示列
			"`gorm:\"-\" json:\"ownerUserName,omitempty\"`",
		}},
		{"go/internal/module/crm/dto/product.go", []string{
			"ProductCreateReq struct",
			"PageReq struct",
			"money.Money", // money 字段映射
			"*int8",       // 枚举用指针表达「未传」
		}},
		{"go/internal/module/crm/repository/product.go", []string{
			"OwnerDeptIDs    []int64",
			"SelfOwnerUserID int64",
			`q = q.Where("1 = 0")`, // fail-closed
			"owner_dept_id IN ?",
		}},
		{"go/internal/module/crm/service/product.go", []string{
			"s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))",
			"scope.Permits(row.OwnerDeptID, row.OwnerUserID)",
			"xerr.NotFound(\"产品不存在\")",
			"applyProductScope(",
			"func (s *Service) ListProducts(",
		}},
		{"go/internal/module/crm/api/product.go", []string{
			`h.requirePermission("crm:product:query")`,
			"func (h *Handler) RegisterProductRoutes(rg *gin.RouterGroup)",
		}},
		{"go/internal/module/crm/service/product_rules.go", []string{
			"func ValidateProductStatus(v int8) error",
			"产品状态",
		}},
		{"go/internal/module/crm/service/product_rules_test.go", []string{
			"package service_test",
			"func TestValidateProductStatus",
		}},
		{"go/internal/module/crm/product_scope_integration_test.go", []string{
			"//go:build integration",
			"TestIntegrationProductDataScopeMatrix",
			"TestIntegrationProductOutsideScope404",
			"testutil.NewIntegrationEnv",
		}},
		{"web/src/views/crm/product/index.vue", []string{
			`perm-prefix="crm:product"`,
			":api=\"api\"",
			"dictLabel(", // 枚举列用 formatter
			"#cell-ownerUserName",
			"ProTable",
		}},
		{"web/src/api/crm.append.ts", []string{
			"pageProducts",
			"'/crm/product/page'",
		}},
		{"seed/menus.append.go", []string{
			"menu(402, 400, \"产品管理\", \"product\", \"crm/product/index\", \"crm:product:query\", \"Goods\", 2)",
			"btn(4022, 402, \"产品新增\", \"crm:product:create\", 2)",
		}},
		{"migrations/0000X_crm_product.tmpl", []string{
			"CREATE TABLE `crm_product`",
			"+goose Up",
			"owner_user_id` bigint",
		}},
		{"INTEGRATION.md", []string{
			"tenant_tables.go",
			"crm_product",
			"RegisterProductRoutes",
			"#20",
		}},
	}
	for _, tc := range cases {
		content, ok := files[tc.file]
		if !ok {
			t.Fatalf("缺少产物 %s", tc.file)
		}
		for _, want := range tc.want {
			if !strings.Contains(content, want) {
				t.Errorf("%s 应包含 %q", tc.file, want)
			}
		}
	}
}

// TestGenerateEntityModuleScaffold 新模块（骨架不存在）额外生成骨架文件。
func TestGenerateEntityModuleScaffold(t *testing.T) {
	spec := validProductSpec()
	spec.Module = "erp"
	spec.TableName = "erp_product"
	spec.PermPrefix = "erp:product"
	spec.APIGroup = "erp/product"
	spec.ModuleScaffold = true
	spec.OwnerScoped = false // 非 owner 变体同轮验证
	spec.ImportExport = false
	spec.Menu = nil
	spec.Fields[4].Dict = "erp_product_unit"
	spec.Fields[4].Enum = nil

	files, err := codegen.GenerateEntity(spec)
	if err != nil {
		t.Fatalf("GenerateEntity: %v", err)
	}
	for _, name := range []string{
		"go/internal/module/erp/model/doc.go",
		"go/internal/module/erp/dto/doc.go",
		"go/internal/module/erp/repository/repository.go",
		"go/internal/module/erp/service/service.go",
		"go/internal/module/erp/api/router.go",
		"go/internal/module/erp/api/helpers.go",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("模块骨架缺少 %s", name)
		}
	}
	// 非 owner 变体：不生成数据范围谓词与 owner 展示列。
	if strings.Contains(files["go/internal/module/erp/service/product.go"], "DataScopeFilter") {
		t.Error("非 owner 实体不应出现数据范围过滤")
	}
	if strings.Contains(files["go/internal/module/erp/model/product.go"], "OwnerUserID") {
		t.Error("非 owner 实体模型不应有 owner 列")
	}
	// 新模块 service 骨架带 SystemGateway 与 ownerDeptSnapshot（后续 owner 实体复用）。
	scaffold := files["go/internal/module/erp/service/service.go"]
	for _, want := range []string{"SystemGateway interface", "ownerDeptSnapshot", "func New("} {
		if !strings.Contains(scaffold, want) {
			t.Errorf("service 骨架应包含 %q", want)
		}
	}
	// 无菜单输入则不生成种子片段。
	if _, ok := files["seed/menus.append.go"]; ok {
		t.Error("未提供菜单输入时不应生成种子片段")
	}
}

// TestGenerateEntitySequence 开启编号后生成发号链路（crm_sequence 事务发放）。
func TestGenerateEntitySequence(t *testing.T) {
	spec := validProductSpec()
	spec.Sequence = &codegen.SequenceSpec{Prefix: "CP", BizType: "product"}
	files, err := codegen.GenerateEntity(spec)
	if err != nil {
		t.Fatalf("GenerateEntity: %v", err)
	}
	model := files["go/internal/module/crm/model/product.go"]
	if !strings.Contains(model, "ProductNo string") {
		t.Error("开启编号后模型应有 ProductNo 字段")
	}
	svc := files["go/internal/module/crm/service/product.go"]
	for _, want := range []string{
		"FormatProductNo(dateKey, seq)",
		"s.repo.Transaction(ctx,",
		"tx.NextSequenceValue(ctx, currentTenantID(ctx), BizTypeProduct, dateKey)",
	} {
		if !strings.Contains(svc, want) {
			t.Errorf("service 应包含 %q", want)
		}
	}
	repo := files["go/internal/module/crm/repository/product.go"]
	if strings.Contains(repo, "func (r *Repository) NextSequenceValue") {
		t.Error("per-entity 产物不应重复定义 NextSequenceValue（crm 模块已有）")
	}
}
