package doctype_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/doctype"
)

// TestAllCompleteness 断言注册表自身的完备性（issue #125）：键/菜单 ID/权限
// 前缀/Excel 文件名/sort 在 12 条目间唯一，字段非空。注册表与 docSpec、路由
// 树的一致性不在此处（分别在 api 的路由树单测与 seed 的 AST 比对里）。
func TestAllCompleteness(t *testing.T) {
	all := doctype.All()
	if len(all) != 12 {
		t.Fatalf("注册表应有 12 类单据，实际 %d", len(all))
	}
	seenKey := make(map[doctype.Key]doctype.Type, len(all))
	seenMenuID := make(map[int64]doctype.Key, len(all))
	seenPermPrefix := make(map[string]doctype.Key, len(all))
	seenExcelFile := make(map[string]doctype.Key, len(all))
	seenSort := make(map[int]doctype.Key, len(all))
	for _, e := range all {
		if e.Key == "" || e.Name == "" || e.Icon == "" || e.MenuID == 0 ||
			e.ButtonPrefix == "" || e.PermPrefix == "" || e.ExcelFile == "" || e.ExcelTitle == "" {
			t.Fatalf("条目 %q 存在空字段：%+v", e.Key, e)
		}
		if e.PermPrefix != "erp:"+string(e.Key) {
			t.Fatalf("条目 %q 权限前缀应为 erp:%s，实际 %s", e.Key, e.Key, e.PermPrefix)
		}
		if e.Sort <= 0 {
			t.Fatalf("条目 %q sort 应为正数，实际 %d", e.Key, e.Sort)
		}
		if prev, ok := seenKey[e.Key]; ok {
			t.Fatalf("条目键重复：%s 同时属于 %s 与 %s", e.Key, prev.Name, e.Name)
		}
		if prev, ok := seenMenuID[e.MenuID]; ok {
			t.Fatalf("菜单 ID 重复：%d 同时属于 %s 与 %s", e.MenuID, prev, e.Key)
		}
		if prev, ok := seenPermPrefix[e.PermPrefix]; ok {
			t.Fatalf("权限前缀重复：%s 同时属于 %s 与 %s", e.PermPrefix, prev, e.Key)
		}
		if prev, ok := seenExcelFile[e.ExcelFile]; ok {
			t.Fatalf("Excel 文件名重复：%s 同时属于 %s 与 %s", e.ExcelFile, prev, e.Key)
		}
		if prev, ok := seenSort[e.Sort]; ok {
			t.Fatalf("sort 重复：%d 同时属于 %s 与 %s（会让侧栏按 sort 排序时交错）", e.Sort, prev, e.Key)
		}
		seenKey[e.Key] = e
		seenMenuID[e.MenuID] = e.Key
		seenPermPrefix[e.PermPrefix] = e.Key
		seenExcelFile[e.ExcelFile] = e.Key
		seenSort[e.Sort] = e.Key
	}
}

// TestPermDerivation 断言权限码派生：<PermPrefix>:<action> 六码，不提供逐码
// 覆盖；六码互不相同。
func TestPermDerivation(t *testing.T) {
	actions := []string{
		doctype.ActionQuery, doctype.ActionCreate, doctype.ActionUpdate,
		doctype.ActionDelete, doctype.ActionApprove, doctype.ActionExport,
	}
	for _, e := range doctype.All() {
		seen := make(map[string]struct{}, len(actions))
		for _, action := range actions {
			perm := e.Perm(action)
			if want := e.PermPrefix + ":" + action; perm != want {
				t.Fatalf("%s 动作 %s 权限码应为 %s，实际 %s", e.Key, action, want, perm)
			}
			seen[perm] = struct{}{}
		}
		if len(seen) != len(actions) {
			t.Fatalf("%s 六个动作权限码出现重复：%v", e.Key, seen)
		}
	}
}

// TestButtonsDerivation 断言按钮派生（issue #125 user story 4）：五个按钮
// ID = MenuID*10+n（n=1..5 对应 create/update/delete/approve/export），sort
// 与 n 一致，名称 = 按钮前缀 + 动作后缀，权限码 = 派生动作码；按钮 ID 全仓
// 唯一且不与任何菜单 ID 冲突。
func TestButtonsDerivation(t *testing.T) {
	wantNames := [5]string{"新增", "修改", "删除", "审核", "导出"}
	wantActions := [5]string{"create", "update", "delete", "approve", "export"}

	menuIDs := make(map[int64]struct{}, 12)
	for _, e := range doctype.All() {
		menuIDs[e.MenuID] = struct{}{}
	}
	seenButtonIDs := make(map[int64]doctype.Key, 60)
	for _, e := range doctype.All() {
		buttons := e.Buttons()
		if len(buttons) != 5 {
			t.Fatalf("%s 应派生 5 个按钮，实际 %d", e.Key, len(buttons))
		}
		for i, b := range buttons {
			n := i + 1
			if b.ID != e.MenuID*10+int64(n) {
				t.Fatalf("%s 按钮 %d ID 应为 %d，实际 %d", e.Key, n, e.MenuID*10+int64(n), b.ID)
			}
			if b.Sort != n {
				t.Fatalf("%s 按钮 %d sort 应为 %d，实际 %d", e.Key, n, n, b.Sort)
			}
			if want := e.ButtonPrefix + wantNames[i]; b.Name != want {
				t.Fatalf("%s 按钮 %d 名称应为 %s，实际 %s", e.Key, n, want, b.Name)
			}
			if want := e.PermPrefix + ":" + wantActions[i]; b.Perm != want {
				t.Fatalf("%s 按钮 %d 权限码应为 %s，实际 %s", e.Key, n, want, b.Perm)
			}
			if _, ok := menuIDs[b.ID]; ok {
				t.Fatalf("%s 按钮 ID %d 与某条目的菜单 ID 冲突", e.Key, b.ID)
			}
			if prev, ok := seenButtonIDs[b.ID]; ok {
				t.Fatalf("按钮 ID 重复：%d 同时属于 %s 与 %s", b.ID, prev, e.Key)
			}
			seenButtonIDs[b.ID] = e.Key
		}
	}
}

// TestSeedMenuRowDerivation 断言菜单行派生：路径 = key、菜单行权限 =
// <PermPrefix>:query（spec §7：query 码挂菜单自身，页面入口）。
func TestSeedMenuRowDerivation(t *testing.T) {
	for _, e := range doctype.All() {
		if strings.ContainsAny(string(e.Key), "/ ") {
			t.Fatalf("条目键 %q 必须是单段路径（路由段 = 前端路径段）", e.Key)
		}
		if want := e.PermPrefix + ":" + doctype.ActionQuery; e.Perm(doctype.ActionQuery) != want {
			t.Fatalf("%s 菜单行权限应为 %s", e.Key, want)
		}
	}
}

// TestAllCoversEveryEntry 断言 All() 恰好覆盖全部 12 个导出条目各一次，
// 且按菜单 sort 升序（投影与注册顺序的可读性基线）。
func TestAllCoversEveryEntry(t *testing.T) {
	all := doctype.All()
	entries := []doctype.Type{
		doctype.StockIn, doctype.StockOut, doctype.StockMove, doctype.StockCheck,
		doctype.PurchaseOrder, doctype.PurchaseIn, doctype.PurchaseReturn,
		doctype.SaleOrder, doctype.SaleOut, doctype.SaleReturn,
		doctype.FinancePayment, doctype.FinanceReceipt,
	}
	for _, e := range entries {
		if !slices.Contains(all, e) {
			t.Fatalf("All() 缺少条目 %q", e.Key)
		}
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Sort >= all[i].Sort {
			t.Fatalf("All() 应按 sort 升序：%d(%s) 在 %d(%s) 之前", all[i-1].Sort, all[i-1].Key, all[i].Sort, all[i].Key)
		}
	}
}
