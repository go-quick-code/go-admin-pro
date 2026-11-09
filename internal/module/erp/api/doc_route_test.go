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

// TestDocSpecsCompleteness 断言 docSpec 闭包集合与注册表条目按同一 key 一一
// 对应（issue #125：完备性只在测试断言，不在启动期 panic）。缺规格会让
// TestDocRouteTree 的每组七条/总数断言失败，多规格在这里直接报出。
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
