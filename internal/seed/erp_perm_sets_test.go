package seed

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/doctype"
)

// TestErpPermSetsConverge 是 S4 权限双向比对（issue #125，ADR 0020）：用
// go/parser 扫描 erp/api 包非测试源码里的 requirePermission("erp:...") 字面量，
// 与单据类型注册表派生的 72 个动作码取并集，和 seed 产出的 ERP 权限集合
// 双向比对——「路由有、seed 无」会让非管理员在运行时拿到 403 而 seed 看似
// 正常，「seed 有、路由无」是永远授不到的幽灵码，两个方向都要报。
//
// 不硬编码路由数（134）或 distinct 码数（111）：断言的是集合相等——
// 单据码重构后不再出现在字面量里，由注册表派生补齐比对。
func TestErpPermSetsConverge(t *testing.T) {
	routeCodes := scanErpRoutePermLiterals(t)
	if len(routeCodes) == 0 {
		t.Fatal("AST 扫描未发现任何 requirePermission(\"erp:...\") 字面量，扫描本身失效")
	}

	// 注册表派生：12 类 × 6 码（菜单行 query + 五按钮动作）。
	docCodes := make(map[string]struct{}, 12*6)
	for _, e := range doctype.All() {
		docCodes[e.Perm(doctype.ActionQuery)] = struct{}{}
		for _, b := range e.Buttons() {
			docCodes[b.Perm] = struct{}{}
		}
	}
	if len(docCodes) != 72 {
		t.Fatalf("注册表应派生 72 个 distinct 动作码，实际 %d", len(docCodes))
	}

	// seed 侧：ERP 段（核心字面量 + 单据投影）全部权限码。
	seedPerms := make(map[string]struct{}, 111)
	for _, m := range append(erpCoreMenus(), erpDocMenus()...) {
		if strings.HasPrefix(m.Permission, "erp:") {
			seedPerms[m.Permission] = struct{}{}
		}
	}

	full := make(map[string]struct{}, len(routeCodes)+len(docCodes))
	for code := range routeCodes {
		full[code] = struct{}{}
	}
	for code := range docCodes {
		full[code] = struct{}{}
	}

	var routeOnly, seedOnly []string
	for code := range full {
		if _, ok := seedPerms[code]; !ok {
			routeOnly = append(routeOnly, code)
		}
	}
	for code := range seedPerms {
		if _, ok := full[code]; !ok {
			seedOnly = append(seedOnly, code)
		}
	}
	slices.Sort(routeOnly)
	slices.Sort(seedOnly)
	if len(routeOnly) > 0 {
		t.Errorf("路由有、seed 无（授予角色也挡不住 403）：%v", routeOnly)
	}
	if len(seedOnly) > 0 {
		t.Errorf("seed 有、路由无（幽灵码）：%v", seedOnly)
	}
}

// scanErpRoutePermLiterals 扫描 erp/api 包非测试源码，收集 requirePermission
// 的 "erp:" 前缀字符串字面量。用 runtime.Coler 定位包目录，不依赖测试 cwd。
func scanErpRoutePermLiterals(t *testing.T) map[string]struct{} {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件路径")
	}
	apiDir := filepath.Join(filepath.Dir(thisFile), "..", "module", "erp", "api")
	entries, err := os.ReadDir(apiDir)
	if err != nil {
		t.Fatalf("读取 api 包目录: %v", err)
	}

	codes := make(map[string]struct{}, 111)
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(apiDir, name), nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "requirePermission" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			code := strings.Trim(lit.Value, "`\"")
			if strings.HasPrefix(code, "erp:") {
				codes[code] = struct{}{}
			}
			return true
		})
	}
	return codes
}
