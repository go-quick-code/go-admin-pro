package service

import (
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock_move_check_rules_test.go 是库存调拨/盘点的纯函数表驱动测试（issue #93，
// spec §8.1 次 seam）：盘点差异 = 实际 − 账面、明细校验（调拨数量 > 0、盘点
// 实际数 ≥ 0）、多仓库加锁键排序（仓库 ID 升序 + 产品 ID 升序，去重）。

// TestCheckDiff 盘点差异（spec §4.6：差异 = actual_count − 审核时账面数；
// 差正盘盈、差负盘亏、零账实一致）。
func TestCheckDiff(t *testing.T) {
	cases := []struct {
		name    string
		actual  string
		book    string
		want    string
		wantErr bool
	}{
		{name: "盘盈", actual: "12.500", book: "10.000", want: "2.500"},
		{name: "盘亏", actual: "8.000", book: "10.000", want: "-2.000"},
		{name: "账实一致", actual: "10.000", book: "10.000", want: "0.000"},
		{name: "零账面全盘盈", actual: "3.000", book: "0.000", want: "3.000"},
		{name: "全额盘亏", actual: "0.000", book: "7.500", want: "-7.500"},
		{name: "小数毫单位", actual: "0.125", book: "0.002", want: "0.123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diff, err := checkDiff(mustQty(t, c.actual), mustQty(t, c.book))
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望错误，实际 diff=%s", diff)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkDiff: %v", err)
			}
			if diff.String() != c.want {
				t.Fatalf("差异 = %s，期望 %s（%s − %s）", diff, c.want, c.actual, c.book)
			}
		})
	}
}

// TestBuildMoveItemDrafts 调拨明细校验：数量 > 0、产品必填、至少一条。
func TestBuildMoveItemDrafts(t *testing.T) {
	t.Run("合法", func(t *testing.T) {
		drafts, err := buildMoveItemDrafts([]dto.StockMoveItemPayload{
			{ProductId: 1, Count: mustQty(t, "1.500")},
			{ProductId: 2, Count: mustQty(t, "2")},
		})
		if err != nil {
			t.Fatalf("buildMoveItemDrafts: %v", err)
		}
		if len(drafts) != 2 || drafts[0].ProductID != 1 || drafts[0].Count.String() != "1.500" {
			t.Fatalf("drafts = %+v", drafts)
		}
	})
	for _, c := range []struct {
		name  string
		items []dto.StockMoveItemPayload
	}{
		{name: "空明细", items: nil},
		{name: "缺少产品", items: []dto.StockMoveItemPayload{{ProductId: 0, Count: mustQty(t, "1")}}},
		{name: "数量为零", items: []dto.StockMoveItemPayload{{ProductId: 1, Count: mustQty(t, "0")}}},
		{name: "数量为负", items: []dto.StockMoveItemPayload{{ProductId: 1, Count: mustQty(t, "-1")}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildMoveItemDrafts(c.items); err == nil {
				t.Fatal("期望校验错误")
			}
		})
	}
}

// TestBuildCheckItemDrafts 盘点明细校验：实际数 ≥ 0、产品必填、至少一条；
// base_count 取创建时快照（无余额行按 0）。
func TestBuildCheckItemDrafts(t *testing.T) {
	base := map[int64]qty.Qty{1: mustQty(t, "10.000")}
	t.Run("合法含零实际数", func(t *testing.T) {
		drafts, err := buildCheckItemDrafts([]dto.StockCheckItemPayload{
			{ProductId: 1, ActualCount: mustQty(t, "8.500")},
			{ProductId: 2, ActualCount: mustQty(t, "0")},
		}, base)
		if err != nil {
			t.Fatalf("buildCheckItemDrafts: %v", err)
		}
		if len(drafts) != 2 {
			t.Fatalf("drafts 长度 = %d", len(drafts))
		}
		if drafts[0].BaseCount.String() != "10.000" {
			t.Fatalf("产品 1 base_count = %s，期望 10.000", drafts[0].BaseCount)
		}
		if drafts[1].BaseCount.String() != "0.000" {
			t.Fatalf("产品 2 无余额行 base_count = %s，期望 0.000", drafts[1].BaseCount)
		}
		if drafts[1].ActualCount.String() != "0.000" {
			t.Fatalf("实际清点数 0 应合法，实际 %s", drafts[1].ActualCount)
		}
	})
	for _, c := range []struct {
		name  string
		items []dto.StockCheckItemPayload
	}{
		{name: "空明细", items: nil},
		{name: "缺少产品", items: []dto.StockCheckItemPayload{{ProductId: 0, ActualCount: mustQty(t, "1")}}},
		{name: "实际数为负", items: []dto.StockCheckItemPayload{{ProductId: 1, ActualCount: mustQty(t, "-0.001")}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := buildCheckItemDrafts(c.items, base); err == nil {
				t.Fatal("期望校验错误")
			}
		})
	}
}

// TestSortStockLockKeys 多仓库调拨的加锁顺序（spec §4.2：按仓库 ID 升序）：
// 去重后按（仓库 ID, 产品 ID）升序，交叉调拨不会以相反顺序取锁。
func TestSortStockLockKeys(t *testing.T) {
	keys := []stockLockKey{
		{WarehouseID: 2, ProductID: 5},
		{WarehouseID: 1, ProductID: 9},
		{WarehouseID: 2, ProductID: 1},
		{WarehouseID: 1, ProductID: 9}, // 重复键
		{WarehouseID: 1, ProductID: 2},
	}
	sorted := sortStockLockKeys(keys)
	want := []stockLockKey{
		{WarehouseID: 1, ProductID: 2},
		{WarehouseID: 1, ProductID: 9},
		{WarehouseID: 2, ProductID: 1},
		{WarehouseID: 2, ProductID: 5},
	}
	if len(sorted) != len(want) {
		t.Fatalf("排序后长度 = %d，期望 %d（去重）", len(sorted), len(want))
	}
	for i := range want {
		if sorted[i] != want[i] {
			t.Fatalf("sorted[%d] = %+v，期望 %+v", i, sorted[i], want[i])
		}
	}
}
