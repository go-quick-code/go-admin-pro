package seed

import (
	"fmt"
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
	sysmodel "github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// TestPermSetsConverge 是权限码收敛检查的全库泛化版（issue #148，ADR 0023；
// 先例：ERP S4 TestErpPermSetsConverge——已并入本测试，erp 成为配置表一员，
// 无特判）：用 go/parser 递归扫描 internal/module/ 全树非测试源码（testdata
// 目录跳过——生成器演示产物是 fixture 不是路由源；权限码天然跨目录，
// infra:codegen 的路由在 codegen 包），匹配三个中间件注入调用名
// requirePermission/RequirePermission/perm（同一 system 中间件经各模块注入
// 字段名不同）。第一参为 string 字面量直接收集；为标识符时做包内解引用——
// var/const 的 string 字面量赋值直接解析（oa roomManage 形状），已知派生
// 函数白名单按规则派生（service.ManagePermission("leave") →
// oa:<resource>:manage，oa manageLeave 形状）。误匹配方向安全：非目标前缀
// 的字符串不入集，多报暴露给人裁决、不静默漏。
//
// 路由侧集合（∪ per-前缀派生 hook）与 seed 构造函数产出的菜单/按钮行权限
// 集合（与真实 seed 同一事实源）按 9 个前缀双向比对：
//   - routeOnly（路由有、seed 无）= 授予路径缺失的 403 缺口，一律修复
//     不豁免（在 seed 补菜单/按钮授予行）；
//   - seedOnly（seed 有、路由无）= 待裁决差异，按带理由豁免清单逐条豁免
//     （唯一事实源 permSeedOnlyExemptions）；清单外任何差异报错，新增豁免
//     必须带理由，review 逐条质询。
//
// 扫描器防呆（S4 语义泛化）：任一前缀路由侧零发现即自判失效——目录移动、
// 调用名改名或 testdata 误扫不会伪装成全绿。纯静态（go/parser + 内存集合），
// 不依赖 DB，go test ./internal/seed/... 全库可跑。
func TestPermSetsConverge(t *testing.T) {
	routeByPrefix, unresolved := scanRoutePermCodes(t)
	if len(unresolved) > 0 {
		t.Fatalf("包内解引用失败（扫描口径出现未知形状，fail-closed——静默漏码会伪装成全绿）：%v", unresolved)
	}

	for _, spec := range permConvergenceSpecs {
		t.Run(spec.prefix, func(t *testing.T) {
			routeCodes := routeByPrefix[spec.prefix]
			if len(routeCodes) == 0 {
				t.Fatalf("扫描器防呆（S4 语义泛化）：%s 前缀路由侧零发现，扫描本身失效", spec.prefix)
			}

			seedCodes := make(map[string]struct{})
			for _, m := range spec.seedMenus() {
				if strings.HasPrefix(m.Permission, spec.prefix+":") {
					seedCodes[m.Permission] = struct{}{}
				}
			}
			if len(seedCodes) == 0 {
				t.Fatalf("seed 侧 %s 前缀零权限码：seed 来源配置失效", spec.prefix)
			}

			// full = 路由字面量 ∪ 包内解引用 ∪ 派生 hook（仅 erp 注册）。
			full := make(map[string]struct{}, len(routeCodes))
			for code := range routeCodes {
				full[code] = struct{}{}
			}
			hookCodes := 0
			if spec.derive != nil {
				for code := range spec.derive(t) {
					if _, ok := full[code]; !ok {
						hookCodes++
					}
					full[code] = struct{}{}
				}
			}
			t.Logf("route 守卫码 %d，hook 新增派生 %d，seed %d", len(routeCodes), hookCodes, len(seedCodes))

			var routeOnly []string
			for code := range full {
				if _, ok := seedCodes[code]; !ok {
					routeOnly = append(routeOnly, code)
				}
			}
			slices.Sort(routeOnly)
			if len(routeOnly) > 0 {
				t.Errorf("路由有、seed 无（授予角色也挡不住的 403 缺口，一律修复不豁免——在 seed 补菜单/按钮授予行）：%v", routeOnly)
			}

			exemptedByPrefix := make(map[string]struct{})
			for _, e := range permSeedOnlyExemptions {
				if strings.HasPrefix(e.code, spec.prefix+":") {
					exemptedByPrefix[e.code] = struct{}{}
				}
			}
			var seedOnly []string
			for code := range seedCodes {
				if _, ok := full[code]; ok {
					continue
				}
				if _, ok := exemptedByPrefix[code]; ok {
					continue
				}
				seedOnly = append(seedOnly, code)
			}
			slices.Sort(seedOnly)
			if len(seedOnly) > 0 {
				t.Errorf("seed 有、路由无且不在豁免清单（幽灵码候选：补豁免必须带理由逐条质询，或删码）：%v", seedOnly)
			}

			// 豁免时效性：seed 不再授予或路由已挂守卫的条目已过期——删过期
			// 豁免或修正理由，保持清单与模型一致（一条一行带理由的前提）。
			for _, e := range permSeedOnlyExemptions {
				if !strings.HasPrefix(e.code, spec.prefix+":") {
					continue
				}
				_, inSeed := seedCodes[e.code]
				_, inRoute := full[e.code]
				if !inSeed || inRoute {
					t.Errorf("豁免条目过期：%s（seed 授予=%t，路由守卫=%t）——删除过期豁免或修正理由", e.code, inSeed, inRoute)
				}
			}
		})
	}
}

// permConvergenceSpec 是 per-前缀配置行：前缀 → seed 集合来源 / 派生 hook。
// 路由扫描范围对全前缀统一（internal/module/ 递归），无需按前缀配目录；
// 新增前缀只需在此加一行（注册表类机制才需要派生 hook）。
type permConvergenceSpec struct {
	prefix string
	// seedMenus 是 seed 侧菜单/按钮行来源（与真实 seed 同一事实源），
	// 测试按前缀过滤其 Permission 字段。
	seedMenus func() []sysmodel.Menu
	// derive 是 per-前缀派生 hook：仅注册表类机制需要（ADR 0020 边界）。
	// 仅 erp 注册（doctype.All() 派生 72 码，机制同旧 S4）；其余模块零
	// hook——oa 计算码由包内解引用自动覆盖，无需 hardcode 资源名。
	derive func(t *testing.T) map[string]struct{}
}

var permConvergenceSpecs = []permConvergenceSpec{
	{prefix: "system", seedMenus: baseMenus},
	{prefix: "crm", seedMenus: baseMenus},
	{prefix: "erp", seedMenus: func() []sysmodel.Menu { return append(erpCoreMenus(), erpDocMenus()...) }, derive: deriveErpDocCodes},
	{prefix: "oa", seedMenus: baseMenus},
	{prefix: "pay", seedMenus: payMenus},
	{prefix: "infra", seedMenus: baseMenus},
	{prefix: "message", seedMenus: baseMenus},
	{prefix: "bpm", seedMenus: baseMenus},
	{prefix: "notify", seedMenus: baseMenus},
}

// permSeedOnlyExemption 是 seedOnly 差异的逐条裁决项：一条一行带理由，
// 测试文件是唯一事实源；判据是「码的消费方在哪」（前端菜单树 / 前端按钮 /
// 服务层可见范围 / 服务层组合校验 / 路由守卫），不是端点有没有列表页这类
// 表面特征（ADR 0023）。初始裁决 9 条（ADR 0023）；实施扫描另暴露 7 条
// 代码注释已写明 by-design 的既有差异（issue #148 实施裁决补入）。
type permSeedOnlyExemption struct {
	code     string
	category string // 菜单可见性码 / 前端操作码 / 服务层可见范围码 / 服务层组合校验码
	reason   string
}

var permSeedOnlyExemptions = []permSeedOnlyExemption{
	// —— 菜单可见性码：挂菜单行、仅控制前端菜单树过滤，可无对应端点。——
	{code: "system:area:query", category: "菜单可见性码", reason: "113 地区管理菜单行；树/子级端点登录即可（AreaCascader 全端复用，spec §4.8.1），查询码只过滤菜单树"},
	{code: "message:mail:query", category: "菜单可见性码", reason: "602 邮件管理菜单行；邮件页实为发送表单（实际调通道/模板接口），无 mail:query 守卫端点"},
	// —— 前端操作码：端点登录即可或走 BPM 审批，码用于前端按钮显隐/流程定义。——
	{code: "bpm:task:handle", category: "前端操作码", reason: "5121 任务办理按钮；办理动作以「当前办理人/发起人」身份收口（spec §5 不设权限码门槛），码只管前端显隐"},
	{code: "crm:contract:approve", category: "前端操作码", reason: "4105 合同审批；审批走 BPM 流程（crm_contract_approval 节点 2 规则码，候选池由 bpm 物化待办），无独立审批端点"},
	{code: "pay:demo-order:create", category: "前端操作码", reason: "8301 示例下单按钮；创建端点登录即可（demo 自助端点，本人校验在 service 层），码只管前端显隐"},
	{code: "pay:demo-order:refund", category: "前端操作码", reason: "8302 发起退款按钮；退款端点登录即可（自助端点，归属校验在 service 层），码只管前端显隐"},
	{code: "pay:demo-withdraw:create", category: "前端操作码", reason: "8311 申请提现按钮；创建端点登录即可（自助端点，user_account 服务端取登录主体），码只管前端显隐"},
	{code: "oa:document:upload", category: "前端操作码", reason: "5263 文件上传按钮；上传/删除只要求目录管理权（owner 或 oa:document:manage，service 层收口），码保留给前端按钮展示（#59）"},
	// —— 服务层可见范围码：不挂路由守卫，由服务层数据可见范围逻辑消费。——
	{code: "oa:expense:manage", category: "服务层可见范围码", reason: "5235 报销管理；VisibilityScopeOf(\"expense\") 全租户范围码（spec §4.6），服务层消费"},
	{code: "oa:attendance:manage", category: "服务层可见范围码", reason: "5332 全租户可见；VisibilityScopeOf(\"attendance\") 全租户范围码（spec §4.6），服务层消费"},
	{code: "oa:document:manage", category: "服务层可见范围码", reason: "5262 目录管理；管理员旁路 = 全租户可见 + 全目录可管理，服务层 UserHasPermission 消费（#58；owner 可能不持该码，路由不挂）"},
	// —— 服务层组合校验码：路由刻意不挂中间件，service 层做「本人/owner
	// 或 权限码」组合校验（持码者代管、本人不持码也可操作）。——
	{code: "oa:calendar:public", category: "服务层组合校验码", reason: "5244 公开日程；创建/可见性改为公开由 service 校验 403（calendar_rule），路由不挂码（#56）"},
	{code: "crm:customer:release", category: "服务层组合校验码", reason: "4030 客户释放；负责人本人或持码组合校验在 service 层（#13），负责人不持按钮码也可释放自己的客户"},
	{code: "crm:customer:transfer", category: "服务层组合校验码", reason: "4031 客户转移；负责人本人或持码组合校验在 service 层（#13，公海认领同口径），路由刻意不挂"},
	{code: "crm:follow-up:update", category: "服务层组合校验码", reason: "4042 跟进修改；创建人本人或持码组合校验在 service 层（#12，同释放/转移装配方式），路由不挂中间件"},
	{code: "crm:follow-up:delete", category: "服务层组合校验码", reason: "4043 跟进删除；创建人本人或持码组合校验在 service 层（#12，软删除），路由不挂中间件"},
}

// deriveErpDocCodes 是 erp 的 per-前缀派生 hook：单据类型注册表派生
// 12 类 × 6 码（菜单行 query + 五按钮动作），机制同旧 S4（ADR 0020）。
func deriveErpDocCodes(t *testing.T) map[string]struct{} {
	t.Helper()
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
	return docCodes
}

// ---------- 路由侧 AST 扫描 ----------

// permCallNames 是三个中间件注入调用名：同一 system 权限中间件经各模块
// 注入字段名不同（system 直调 RequirePermission、crm/erp/oa/bpm/notify
// requirePermission、pay/message/infra/codegen perm）。
var permCallNames = map[string]struct{}{
	"requirePermission": {},
	"RequirePermission": {},
	"perm":              {},
}

// oaServicePathSuffix 是 oa service 包 import 路径后缀：ManagePermission
// 派生函数白名单按 import 路径精确识别（「service」在各模块是同名惯用包名，
// 不能只看选择器限定符）。
const oaServicePathSuffix = "internal/module/oa/service"

// pkgSym 是单个包目录的包内符号表：包级 var/const 的字面量赋值与派生函数
// 赋值（解引用只在本包内做，跨包引用不在白名单口径内）。
type pkgSym struct {
	literals map[string]string // 名称 → string 字面量
	derived  map[string]string // 名称 → 白名单派生结果
}

// scanRoutePermCodes 两遍扫描 internal/module/ 全树非测试源码：
// 第一遍按目录建包符号表，第二遍收集三个调用名的首参权限码并解引用。
// 返回前缀 → 码集合，以及无法解引用的首参清单（fail-closed）。
func scanRoutePermCodes(t *testing.T) (map[string]map[string]struct{}, []string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件路径")
	}
	moduleRoot := filepath.Join(filepath.Dir(thisFile), "..", "module")

	fset := token.NewFileSet()
	filesByDir := map[string][]*ast.File{}
	symsByDir := map[string]*pkgSym{}
	err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata 是生成器演示产物（fixture），不是路由源（Go 工具链同款约定）。
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("解析 %s: %w", path, parseErr)
		}
		dir := filepath.Dir(path)
		filesByDir[dir] = append(filesByDir[dir], file)
		sym := symsByDir[dir]
		if sym == nil {
			sym = &pkgSym{literals: map[string]string{}, derived: map[string]string{}}
			symsByDir[dir] = sym
		}
		collectPackagePermSymbols(sym, file)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 %s: %v", moduleRoot, err)
	}

	codes := make(map[string]map[string]struct{}, len(permConvergenceSpecs))
	addCode := func(code string) {
		for _, spec := range permConvergenceSpecs {
			if strings.HasPrefix(code, spec.prefix+":") {
				set := codes[spec.prefix]
				if set == nil {
					set = make(map[string]struct{})
					codes[spec.prefix] = set
				}
				set[code] = struct{}{}
				return
			}
		}
	}

	var unresolved []string
	for dir, files := range filesByDir {
		sym := symsByDir[dir]
		rel, relErr := filepath.Rel(moduleRoot, dir)
		if relErr != nil {
			rel = dir
		}
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if _, ok := permCallNames[sel.Sel.Name]; !ok {
					return true
				}
				switch arg := call.Args[0].(type) {
				case *ast.BasicLit:
					if arg.Kind == token.STRING {
						addCode(strings.Trim(arg.Value, "`\""))
					}
				case *ast.Ident:
					if v, ok := sym.literals[arg.Name]; ok {
						addCode(v)
						return true
					}
					if v, ok := sym.derived[arg.Name]; ok {
						addCode(v)
						return true
					}
					unresolved = append(unresolved, fmt.Sprintf("%s: %s（%s 首参标识符在包内 var/const 无字面量或白名单派生赋值）", rel, arg.Name, sel.Sel.Name))
				case *ast.CallExpr:
					// erp doc.go 单据路由以 spec.Perm(doctype.ActionX) 计算码挂
					// 守卫——不以路由字面量形式出现，这正是 erp 派生 hook 存在的
					// 原因（机制同 S4：字面量 ∪ 注册表派生 72 码）。识别该白名单
					// 形状并交由 hook 覆盖；其余前缀的计算首参仍是未解形状。
					if isDoctypePermCall(file, arg) {
						return true
					}
					unresolved = append(unresolved, fmt.Sprintf("%s: %s 首参为未知函数调用形状（无白名单规则）", rel, sel.Sel.Name))
				default:
					unresolved = append(unresolved, fmt.Sprintf("%s: %s 首参既非 string 字面量也非标识符", rel, sel.Sel.Name))
				}
				return true
			})
		}
	}
	return codes, unresolved
}

// collectPackagePermSymbols 收集文件内包级 var/const 的 string 字面量赋值与
// 白名单派生函数赋值（oa roomManage = "oa:room:manage" 与
// manageLeave = service.ManagePermission("leave") 两种形状）。
func collectPackagePermSymbols(sym *pkgSym, file *ast.File) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || (gen.Tok != token.VAR && gen.Tok != token.CONST) {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				switch v := vs.Values[i].(type) {
				case *ast.BasicLit:
					if v.Kind == token.STRING {
						sym.literals[name.Name] = strings.Trim(v.Value, "`\"")
					}
				case *ast.CallExpr:
					if code, ok := deriveManagePermission(file, v); ok {
						sym.derived[name.Name] = code
					}
				}
			}
		}
	}
}

// deriveManagePermission 是派生函数白名单唯一条目：oa service 的
// ManagePermission(<resource>) → oa:<resource>:manage（ADR 0023）。
// 按 import 路径识别 oa/service，避免与其他模块的同名 service 包误配。
func deriveManagePermission(file *ast.File, call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "ManagePermission" || len(call.Args) != 1 {
		return "", false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || !fileImportsPath(file, pkgIdent.Name, oaServicePathSuffix) {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return "oa:" + strings.Trim(lit.Value, "`\"") + ":manage", true
}

// fileImportsPath 判断文件是否以 localName 为限定符 import 了路径以
// pathSuffix 结尾的包。
func fileImportsPath(file *ast.File, localName, pathSuffix string) bool {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasSuffix(path, pathSuffix) {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == localName {
			return true
		}
	}
	return false
}

// erpDoctypePathSuffix 是 erp doctype 注册表包的 import 路径后缀。
const erpDoctypePathSuffix = "internal/module/erp/doctype"

// isDoctypePermCall 识别 erp 注册表计算码守卫形状：<recv>.Perm(doctype.ActionX)
// （文件需 import erp/doctype）。该形状的计算值由 erp 派生 hook（doctype.All()
// 全注册表派生）并入路由侧集合，调用点本身无需静态求值。
func isDoctypePermCall(file *ast.File, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Perm" || len(call.Args) != 1 {
		return false
	}
	action, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := action.X.(*ast.Ident)
	if !ok || !fileImportsPath(file, pkgIdent.Name, erpDoctypePathSuffix) {
		return false
	}
	return strings.HasPrefix(action.Sel.Name, "Action")
}
