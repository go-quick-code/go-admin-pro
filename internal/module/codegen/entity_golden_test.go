package codegen_test

import (
	"encoding/json"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/codegen"
)

// updateGolden 由 -update 打开：重新生成 demo_output（产品实体产物快照）。
var updateGolden = flag.Bool("update", false, "重新生成 codegen 的 demo_output golden 快照")

// loadProductSpec 从 testdata/entity/product.json 读产品元信息——
// spec fixture 是唯一真源：golden 快照、变体测试与 cmd/codegen 演示都用它。
func loadProductSpec(t *testing.T) *codegen.EntitySpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "entity", "product.json"))
	if err != nil {
		t.Fatalf("读取产品元信息 fixture: %v", err)
	}
	var spec codegen.EntitySpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("解析产品元信息 fixture: %v", err)
	}
	return &spec
}

// TestGenerateEntityGoldenProduct 产品实体产物的 golden 比对：
// testdata/demo_output 既是测试基准，也是留给项目所有者人工 review 的演示输出
// （与 `go run ./cmd/codegen` 的落盘结果逐字节一致）。
// 字段/开关变化后用 `go test ./internal/module/codegen -update` 重生快照并 review diff。
func TestGenerateEntityGoldenProduct(t *testing.T) {
	spec := loadProductSpec(t)
	files, err := codegen.GenerateEntity(spec)
	if err != nil {
		t.Fatalf("GenerateEntity: %v", err)
	}

	goldenDir := filepath.Join("testdata", "demo_output")
	if *updateGolden {
		if _, err := codegen.WriteFiles(goldenDir, files, true); err != nil {
			t.Fatalf("重写 golden 快照: %v", err)
		}
		t.Logf("golden 快照已更新：%s（%d 个文件）", goldenDir, len(files))
		return
	}

	// 目录按顶层产物组织：递归收集后与生成文件集逐文件比对。
	golden := map[string]string{}
	if err := collectFiles(goldenDir, "", golden); err != nil {
		t.Fatalf("收集 golden 文件: %v", err)
	}

	gotNames := slices.Sorted(maps.Keys(files))
	wantNames := slices.Sorted(maps.Keys(golden))
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("产物文件集与 golden 不一致：\n生成 %v\n基准 %v", gotNames, wantNames)
	}
	for _, name := range gotNames {
		if files[name] != golden[name] {
			t.Errorf("产物 %s 与 golden 不一致（字段变化后请执行 go test ./internal/module/codegen -update 并 review diff）", name)
		}
	}
}

// collectFiles 递归收集 golden 目录下的相对路径 → 内容。
func collectFiles(root, prefix string, out map[string]string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		rel := prefix + e.Name()
		p := filepath.Join(root, e.Name())
		if e.IsDir() {
			if err := collectFiles(p, rel+"/", out); err != nil {
				return err
			}
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
	}
	return nil
}

// TestGenerateEntityVariations 字段与开关变化 → 产物相应变化（golden 之外的快速断言）。
func TestGenerateEntityVariations(t *testing.T) {
	t.Run("owner 开关关闭：不生成数据范围与 owner 列，集成骨架换 CRUD 冒烟", func(t *testing.T) {
		spec := loadProductSpec(t)
		spec.OwnerScoped = false
		spec.Menu = nil
		files, err := codegen.GenerateEntity(spec)
		if err != nil {
			t.Fatalf("GenerateEntity: %v", err)
		}
		model := files["go/internal/module/crm/model/product.go"]
		if strings.Contains(model, "OwnerUserID") || strings.Contains(model, "OwnerDeptID") {
			t.Error("非 owner 实体模型不应有 owner 列")
		}
		svc := files["go/internal/module/crm/service/product.go"]
		if strings.Contains(svc, "DataScopeFilter") || strings.Contains(svc, "ownerDeptSnapshot") {
			t.Error("非 owner 实体 service 不应引用数据范围/负责人快照")
		}
		it := files["go/internal/module/crm/product_scope_integration_test.go"]
		if !strings.Contains(it, "TestIntegrationProductCRUDSmoke") {
			t.Error("非 owner 实体的集成骨架应为 CRUD 冒烟")
		}
	})

	t.Run("编号开关：生成 crm_sequence 发号链路与编号测试", func(t *testing.T) {
		spec := loadProductSpec(t)
		spec.Sequence = &codegen.SequenceSpec{Prefix: "CP"}
		files, err := codegen.GenerateEntity(spec)
		if err != nil {
			t.Fatalf("GenerateEntity: %v", err)
		}
		model := files["go/internal/module/crm/model/product.go"]
		if !strings.Contains(model, "ProductNo string") {
			t.Error("开启编号后模型应有 ProductNo")
		}
		svc := files["go/internal/module/crm/service/product.go"]
		for _, want := range []string{"BizTypeProduct", "FormatProductNo", "s.repo.Transaction(ctx,", "tx.NextSequenceValue"} {
			if !strings.Contains(svc, want) {
				t.Errorf("开启编号后 service 应包含 %q", want)
			}
		}
		rules := files["go/internal/module/crm/service/product_rules_test.go"]
		if !strings.Contains(rules, "TestFormatProductNo") {
			t.Error("开启编号后纯函数测试应覆盖编号格式")
		}
		mig := files["migrations/0000X_crm_product.tmpl"]
		if !strings.Contains(mig, "UNIQUE KEY `uk_crm_product_tenant_no`") {
			t.Error("开启编号后迁移模板应有租户内唯一编号键")
		}
	})

	t.Run("导入导出开关关闭：不生成 #20 接线位", func(t *testing.T) {
		spec := loadProductSpec(t)
		spec.ImportExport = false
		files, err := codegen.GenerateEntity(spec)
		if err != nil {
			t.Fatalf("GenerateEntity: %v", err)
		}
		for name, content := range files {
			if strings.Contains(content, "TODO(#20)") {
				t.Errorf("%s 不应包含 #20 接线位", name)
			}
		}
	})

	t.Run("datetime 字段：service parse 助手 + 前端时间列 + 迁移 datetime(3)", func(t *testing.T) {
		spec := loadProductSpec(t)
		spec.Fields = append(spec.Fields, codegen.FieldSpec{
			Name: "listed_at", Type: "datetime", Comment: "上架时间",
		})
		files, err := codegen.GenerateEntity(spec)
		if err != nil {
			t.Fatalf("GenerateEntity: %v", err)
		}
		svc := files["go/internal/module/crm/service/product.go"]
		for _, want := range []string{
			"s.parseProductTime(req.ListedAt)",
			"func (s *Service) parseProductTime(value string) *time.Time",
			"s.businessLocation()",
		} {
			if !strings.Contains(svc, want) {
				t.Errorf("datetime 字段应生成 %q", want)
			}
		}
		dto := files["go/internal/module/crm/dto/product.go"]
		if !strings.Contains(dto, "ListedAt") || strings.Contains(dto, "*time.Time") {
			t.Error("datetime 字段 DTO 用字符串提交（YYYY-MM-DD HH:mm:ss），不用 *time.Time")
		}
		vue := files["web/src/views/crm/product/index.vue"]
		if !strings.Contains(vue, "formatTime(row.listedAt)") {
			t.Error("datetime 列应用 formatTime 渲染")
		}
		mig := files["migrations/0000X_crm_product.tmpl"]
		if !strings.Contains(mig, "`listed_at` datetime(3) DEFAULT NULL") {
			t.Error("datetime 列迁移应为 datetime(3) 可空")
		}
	})

	t.Run("bool 字段：switch 表单与 tinyint 迁移", func(t *testing.T) {
		spec := loadProductSpec(t)
		spec.Fields = append(spec.Fields, codegen.FieldSpec{
			Name: "shelf_recommended", Type: "bool", Comment: "是否推荐",
		})
		files, err := codegen.GenerateEntity(spec)
		if err != nil {
			t.Fatalf("GenerateEntity: %v", err)
		}
		vue := files["web/src/views/crm/product/index.vue"]
		if !strings.Contains(vue, "type: 'switch'") {
			t.Error("bool 字段表单应为 switch")
		}
		mig := files["migrations/0000X_crm_product.tmpl"]
		if !strings.Contains(mig, "`shelf_recommended` tinyint NOT NULL DEFAULT '0'") {
			t.Error("bool 列迁移应为 tinyint NOT NULL DEFAULT 0")
		}
	})

	t.Run("searchable 非 text 类型直接 400", func(t *testing.T) {
		spec := loadProductSpec(t)
		for i := range spec.Fields {
			if spec.Fields[i].Name == "price" {
				spec.Fields[i].Searchable = true
			}
		}
		err := spec.Validate()
		if err == nil || !strings.Contains(err.Error(), "searchable") {
			t.Fatalf("money 字段开 searchable 应报错，实际 %v", err)
		}
	})
}
