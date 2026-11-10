package api

import (
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/doctype"
)

// routeTreeHandler 构造 Handler（service 传 nil、requirePermission 传 no-op，
// 路由注册不触达业务依赖）。
func routeTreeHandler() *Handler {
	return New(nil, func(string) gin.HandlerFunc { return func(c *gin.Context) {} }, nil)
}

// registerAllRoutes 把 RegisterProtected 挂到空 gin 引擎上，返回路由树。
// 仅供本文件的路由形状断言使用，不发任何请求。
func registerAllRoutes(t *testing.T) gin.RoutesInfo {
	t.Helper()
	r := gin.New()
	routeTreeHandler().RegisterProtected(&r.RouterGroup)
	return r.Routes()
}

// docRouteActions 是单据组 7 条路由的注册序规格：(HTTP 方法, 路径后缀, 动作)。
// 顺序即 registerDocRoutes 的注册顺序（组内静态路由先于 /:id，后者已文档化为
// 契约），路径后缀拼在 /erp/<key> 之后（"" 即组根本身）。映射锁定按位比对
// 权限码时以本表为准。
var docRouteActions = [...]struct {
	method, path, action string
}{
	{"GET", "/page", doctype.ActionQuery},
	{"GET", "/export-excel", doctype.ActionExport},
	{"PUT", "/update-status", doctype.ActionApprove},
	{"GET", "/:id", doctype.ActionQuery},
	{"POST", "", doctype.ActionCreate},
	{"PUT", "", doctype.ActionUpdate},
	{"DELETE", "/:id", doctype.ActionDelete},
}

// TestDocSpecsCompleteness 断言 docSpec 闭包集合与注册表条目按同一 key 一一
// 对应（issue #125：完备性只在测试断言，不在启动期 panic）。缺规格会让
// TestDocRouteTree 的每组七条/总数断言失败，多规格在这里直接报出。
//
// 白盒说明：直接读未导出的 docSpecs() 是设计决定（doctype.go:15 与 ADR 0020
// 都把「完备性只在白盒测试断言」写为声明），不为迁就 CODING_STANDARDS
// 「测试写在公共 seam／测导出函数」条款而改导出。
func TestDocSpecsCompleteness(t *testing.T) {
	entryByKey := make(map[doctype.Key]doctype.Type, 12)
	for _, e := range doctype.All() {
		entryByKey[e.Key] = e
	}
	specs := routeTreeHandler().docSpecs()

	for key := range entryByKey {
		if _, ok := specs[key]; !ok {
			t.Errorf("注册表条目 %q 缺少 docSpec（该类单据将不会注册任何路由）", key)
		}
	}
	for key, spec := range specs {
		entry, ok := entryByKey[key]
		if !ok {
			t.Errorf("docSpec %q 没有对应的注册表条目（该规格永远不会注册）", key)
			continue
		}
		if spec.Type != entry {
			t.Errorf("docSpec %q 内嵌的注册表条目与 All() 中的条目不一致：%+v != %+v", key, spec.Type, entry)
		}
		for name, handler := range map[string]gin.HandlerFunc{
			"page": spec.page, "get": spec.get, "create": spec.create,
			"update": spec.update, "status": spec.status,
			"del": spec.del, "export": spec.export,
		} {
			if handler == nil {
				t.Errorf("docSpec %q 的 %s 闭包为 nil", key, name)
			}
		}
	}
}

// TestDocRouteTree 是 S2 路由树单测（issue #125）：注册后断言每个单据组恰好
// 7 条、方法+路径与注册表派生的期望一一对应，且全包恰好 134 条（50 条非
// 注册表段 + 12×7 条单据段）。
//
// 口径分工：S2 锁形状与逐条映射（映射锁定见 TestDocRoutePermMapping），
// 134/50 总数在这里硬编码是稳定契约；S4（internal/seed/erp_perm_sets_test.go）
// 锁集合相等，不硬编码计数防漂移。
func TestDocRouteTree(t *testing.T) {
	routes := registerAllRoutes(t)
	if len(routes) != 134 {
		got := make([]string, 0, len(routes))
		for _, r := range routes {
			got = append(got, r.Method+" "+r.Path)
		}
		slices.Sort(got)
		t.Fatalf("erp 包应注册 134 条路由，实际 %d：%v", len(routes), got)
	}

	// 按注册表 key 收集每个单据组的路由（/erp/<key> 及其子路径）；
	// 剩余的归入非注册表段兜底计数。
	docRoutes := make(map[doctype.Key]map[string]struct{}, 12)
	for _, e := range doctype.All() {
		docRoutes[e.Key] = map[string]struct{}{}
	}
	otherRoutes := 0
	for _, r := range routes {
		matched := false
		for _, e := range doctype.All() {
			base := "/erp/" + string(e.Key)
			if r.Path == base || strings.HasPrefix(r.Path, base+"/") {
				docRoutes[e.Key][r.Method+" "+r.Path] = struct{}{}
				matched = true
				break
			}
		}
		if !matched {
			otherRoutes++
		}
	}

	for _, e := range doctype.All() {
		want := map[string]struct{}{
			"GET /erp/" + string(e.Key) + "/page":          {},
			"GET /erp/" + string(e.Key) + "/export-excel":  {},
			"PUT /erp/" + string(e.Key) + "/update-status": {},
			"GET /erp/" + string(e.Key) + "/:id":           {},
			"POST /erp/" + string(e.Key):                   {},
			"PUT /erp/" + string(e.Key):                    {},
			"DELETE /erp/" + string(e.Key) + "/:id":        {},
		}
		got := docRoutes[e.Key]
		if len(got) != len(want) {
			t.Fatalf("单据组 %s 应恰好 7 条路由，实际 %d：%v", e.Key, len(got), got)
		}
		for m := range want {
			if _, ok := got[m]; !ok {
				t.Errorf("单据组 %s 缺少路由 %s", e.Key, m)
			}
		}
		for m := range got {
			if _, ok := want[m]; !ok {
				t.Errorf("单据组 %s 出现非注册表路由 %s", e.Key, m)
			}
		}
	}
	if otherRoutes != 50 {
		t.Fatalf("非单据段（主数据/库存只读/统计）应恰好 50 条路由，实际 %d", otherRoutes)
	}
}

// TestDocRoutePermMapping 是 S2 的「路由→动作码」映射锁定（issue #125 双轴
// 审查补强）：TestDocRouteTree 只断言方法+路径，若 registerDocRoutes 把
// update-status 误挂 query 码，形状断言抓不到。这里把 requirePermission 的
// 工厂从 no-op 换成记录型——收到的权限码按调用顺序 append 进 codes，走同一
// 注册循环 registerDocRoutes 单独登记 doc 段，与 docRouteActions + 注册表
// 派生的 12×7 个 (方法, 路径, 权限码) 三元组逐位比对。
//
// 按位比对是刻意的：两码互换不改变码的多重集（S4 的集合相等也抓不到），
// 只有位置敏感才锁得住错挂。工厂调用序与路由注册序一致（同一表达式中先
// 求值中间件再挂路由）；注册顺序本身是 registerDocRoutes 已文档化的契约
// （组内静态路由先于 /:id），docRouteActions 即按此序声明。
func TestDocRoutePermMapping(t *testing.T) {
	var codes []string
	h := New(nil, func(code string) gin.HandlerFunc {
		codes = append(codes, code)
		return func(c *gin.Context) {}
	}, nil)
	docOnly := gin.New()
	h.registerDocRoutes(&docOnly.RouterGroup)

	wantCount := len(doctype.All()) * len(docRouteActions)
	if got := len(docOnly.Routes()); got != wantCount {
		t.Fatalf("doc 段应注册 %d 条路由，实际 %d", wantCount, got)
	}
	if len(codes) != wantCount {
		t.Fatalf("每条 doc 路由恰好调用一次 requirePermission：路由 %d 条，工厂实收 %d 个码", wantCount, len(codes))
	}

	// 三元组的路由半边：doc 段路由集合（方法+路径）须与表逐条派生一致。
	wantRoutes := make([]string, 0, wantCount)
	for _, e := range doctype.All() {
		for _, ep := range docRouteActions {
			wantRoutes = append(wantRoutes, ep.method+" /erp/"+string(e.Key)+ep.path)
		}
	}
	gotRoutes := make([]string, 0, len(docOnly.Routes()))
	for _, r := range docOnly.Routes() {
		gotRoutes = append(gotRoutes, r.Method+" "+r.Path)
	}
	slices.Sort(wantRoutes)
	slices.Sort(gotRoutes)
	if !slices.Equal(wantRoutes, gotRoutes) {
		t.Fatalf("doc 段路由集合与注册表派生不一致：\nwant %v\ngot  %v", wantRoutes, gotRoutes)
	}

	// 三元组的权限码半边：按注册序逐位比对。
	for i, e := range doctype.All() {
		for j, ep := range docRouteActions {
			want := e.Perm(ep.action)
			if got := codes[i*len(docRouteActions)+j]; got != want {
				t.Errorf("单据组 %s 路由 %s %s 应挂权限码 %s，实际 %s",
					e.Key, ep.method, "/erp/"+string(e.Key)+ep.path, want, got)
			}
		}
	}
}
