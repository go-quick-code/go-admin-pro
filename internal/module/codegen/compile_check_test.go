package codegen_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/codegen"
)

// 本文件验证「生成的代码 go build/vet/test 通过」这一可信目标：
// 把仓库可编译子集（go.mod/go.sum/internal，跳过 testdata）复制进临时目录，
// 再把生成产物按目标路径叠加进去，跑真实 go 命令验证。
// 参照 codegen 既有校验思路（生成时 gofmt 解析），这里把门槛提到整包编译。

// TestGeneratedProductCompiles 产品脚手架叠加到 crm 模块后可编译、可 vet，
// 纯函数测试骨架可跑通，集成矩阵骨架在 integration 标签下可编译。
// issue #11 起产品实体已在 crm 真实落地：叠加前先清掉副本中该实体的既有文件
// （生成器语义是「重新生成覆盖同名产物」，见 INTEGRATION.md 限制一节），
// 否则样板与实现并存会导致类型/常量重复声明，编译检查失去意义。
func TestGeneratedProductCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过编译验证")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("找不到 go 命令，跳过编译验证")
	}
	spec := loadProductSpec(t)
	// 追加 datetime 字段：生成物必须走 Service.businessLocation（ADR 0006）的
	// location-aware 解析，编译检查覆盖该分支。
	spec.Fields = append(spec.Fields, codegen.FieldSpec{
		Name: "listed_at", Type: "datetime", Comment: "上架时间",
	})
	files, err := codegen.GenerateEntity(spec)
	if err != nil {
		t.Fatalf("GenerateEntity: %v", err)
	}

	tmp := copyModuleSubset(t)
	pruneOverlaidEntityFiles(t, tmp)
	if err := overlay(tmp, files); err != nil {
		t.Fatalf("叠加生成产物: %v", err)
	}

	runGo(t, tmp, "build", "./internal/...")
	runGo(t, tmp, "vet", "./internal/...")
	// 生成的集成矩阵骨架：integration 标签下必须可编译（运行需要数据库，不在此跑）。
	runGo(t, tmp, "vet", "-tags=integration", "./internal/module/crm/...")
	// 纯函数测试骨架（枚举校验/编号格式）真实执行。
	runGo(t, tmp, "test", "-count=1", "-run", "TestValidateProductStatus", "./internal/module/crm/service/")
}

// pruneOverlaidEntityFiles 从副本里删除 issue #11 落地的产品实体文件：
// 样板骨架与手写实现同属一个包，产品集成测试重复定义 crmProductMenuID 等常量，
// issue #20 的手写 Excel 文件与 issue #21 的演示数据种子又依赖产品实体独有的
// CategoryId/Code/Description，都会与「重新生成覆盖同名产物」的样板冲突。
// 清理后样板骨架在「生成物 + 模块底座」的干净组合上做编译验证——即
// INTEGRATION.md「重新生成会覆盖同名产物、手工修改需自行合并」语义在测试层的
// 等价物（seed 包通过 seedCRMDemoFunc 变量在缺失 crm_demo.go 时可正常编译）。
func pruneOverlaidEntityFiles(t *testing.T, tmp string) {
	t.Helper()
	for _, rel := range []string{
		"internal/module/crm/product_http_integration_test.go",
		"internal/module/crm/product_category_http_integration_test.go",
		"internal/module/crm/product_excel_http_integration_test.go",
		"internal/module/crm/service/product_excel.go",
		"internal/module/crm/api/product_excel.go",
		// PR #39 审查新增的字段级更新测试依赖真实产品字段（CategoryId/Code/
		// Description）与 UpdateProductFields；生成样板的产品模型/仓储不含这些，
		// 与上面同类冲突，随产品实体一起跳过编译验证。
		"internal/module/crm/repository/update_fields_test.go",
		"internal/seed/crm_demo.go",
	} {
		if err := os.Remove(filepath.Join(tmp, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			t.Fatalf("清理存量实体文件 %s: %v", rel, err)
		}
	}
}

// TestGeneratedFreshModuleCompiles 新模块骨架变体（ModuleScaffold=true、非 owner、
// 字典字段）整体生成后可编译可 vet——保证「生成一个全新模块」也是可信路径。
// 目标模块名沿用预留的 erp：issue #88 起 erp 已真实落地，叠加前剪除副本中的
// 真实 erp 模块（生成骨架即整个模块，等价于该测试最初「erp 尚不存在」的前置），
// 手写实现的编译质量由 make check 的常规路径覆盖。
func TestGeneratedFreshModuleCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过编译验证")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("找不到 go 命令，跳过编译验证")
	}
	spec := loadProductSpec(t)
	spec.Module = "erp"
	spec.TableName = "erp_product"
	spec.PermPrefix = "erp:product"
	spec.APIGroup = "erp/product"
	spec.ModuleScaffold = true
	spec.OwnerScoped = false
	spec.ImportExport = false
	spec.Menu = nil
	for i := range spec.Fields {
		if spec.Fields[i].Name == "unit" {
			spec.Fields[i].Dict = "erp_product_unit"
		}
		if spec.Fields[i].Name == "status" {
			spec.Fields[i].Enum = nil
			spec.Fields[i].Dict = "erp_product_status"
		}
	}
	// 追加 datetime 字段：模块骨架生成的 Service.location/businessLocation
	// 必须让 per-entity 解析助手可编译（ADR 0006）。
	spec.Fields = append(spec.Fields, codegen.FieldSpec{
		Name: "expire_at", Type: "datetime", Comment: "过期时间",
	})
	files, err := codegen.GenerateEntity(spec)
	if err != nil {
		t.Fatalf("GenerateEntity: %v", err)
	}

	tmp := copyModuleSubset(t)
	pruneRealErpModule(t, tmp)
	if err := overlay(tmp, files); err != nil {
		t.Fatalf("叠加生成产物: %v", err)
	}
	runGo(t, tmp, "build", "./internal/...")
	runGo(t, tmp, "vet", "./internal/...")
}

// pruneRealErpModule 从副本中删除真实 erp 模块（issue #88 落地后的手写实现）：
// fresh-module 变体的语义是「生成一个全新模块」，生成骨架即整个模块，
// 真实实现与骨架并存会互相覆盖同名文件（repository.go/service.go/api.go 等）
// 导致编译检查失去意义。
//
// 装配外溢适配（issue #90 延续本测试的既有模式）：app.go 会对真实 erp Service
// 调用 SetWarehouseRefChecker/SetProductRefChecker/SetLocation（库存域引用
// 校验与业务时区装配），生成的全新骨架没有这些接缝——剪除模块的同时剔除
// 这些装配行，保持「全新模块自洽可编译」的检查语义。
func pruneRealErpModule(t *testing.T, tmp string) {
	t.Helper()
	dir := filepath.Join(tmp, "internal", "module", "erp")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("剪除真实 erp 模块: %v", err)
	}
	appPath := filepath.Join(tmp, "internal", "app", "app.go")
	raw, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("读取 app.go: %v", err)
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "erpSvc.Set") {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(appPath, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatalf("剔除 erpSvc.Set 装配行: %v", err)
	}
}

// copyModuleSubset 把仓库可编译子集复制进 t.TempDir()（跳过 testdata，
// go 工具链本就忽略它们，跳过是为了省拷贝）。
func copyModuleSubset(t *testing.T) string {
	t.Helper()
	root := moduleRoot(t)
	tmp := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmp, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// internal（业务代码）与 migrations（internal/pkg/database 的 embed 依赖）。
	for _, sub := range []string{"internal", "migrations"} {
		src := filepath.Join(root, sub)
		if err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return os.MkdirAll(filepath.Join(tmp, strings.TrimPrefix(p, root)), 0o755)
			}
			return copyFile(p, filepath.Join(tmp, strings.TrimPrefix(p, root)))
		}); err != nil {
			t.Fatalf("复制仓库子集 %s: %v", sub, err)
		}
	}
	return tmp
}

// overlay 把生成产物按相对路径写进临时模块（go/ 前缀对应仓库根）。
func overlay(tmp string, files map[string]string) error {
	for name, content := range files {
		rel := strings.TrimPrefix(name, "go/")
		target := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runGo 在 dir 下执行 go 子命令，非零退出即 Fatal（带完整 stderr）。
func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s：%v\n%s", strings.Join(args, " "), err, out)
	}
}

// moduleRoot 从当前目录向上找 go.mod。
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("向上找不到 go.mod")
		}
		dir = parent
	}
}

// copyFile 复制单个文件（保留普通文件权限）。
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}
