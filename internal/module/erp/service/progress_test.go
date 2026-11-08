// progress_test.go 是采购/销售共用的订单进度分摊纯函数测试（issue #91/#92，
// docs/erp-mvp-spec.md §4.4/§4.5/§8.1 次缝：进度计算）。重点覆盖同产品多行：
// 聚合量必须按明细 ID 序分摊，而不是写给每一行（否则 Σ 进度虚高）。
package service

import (
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// purchaseItems 按显式 (产品, 数量) 对构造采购订单明细。
func purchaseItems(pairs ...[2]any) []model.PurchaseOrderItem {
	out := make([]model.PurchaseOrderItem, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, model.PurchaseOrderItem{
			ProductID: p[0].(int64),
			Count:     p[1].(qty.Qty),
		})
	}
	return out
}

// TestFillProgressByItemOrderPurchase 采购进度分摊：同产品跨行时按明细 ID 序
// 填满，Σ in_count/return_count 精确等于已审核聚合值（P0 修复回归测试）。
func TestFillProgressByItemOrderPurchase(t *testing.T) {
	cases := []struct {
		name    string
		items   []model.PurchaseOrderItem
		inSums  map[int64]qty.Qty
		retSums map[int64]qty.Qty
		wantIn  []string
		wantRet []string
	}{
		{
			name: "同产品两行按行序分摊",
			items: purchaseItems(
				[2]any{int64(100), q("4")},
				[2]any{int64(100), q("6")},
			),
			inSums:  prodSums([2]any{int64(100), q("7")}),
			retSums: prodSums([2]any{int64(100), q("1")}),
			wantIn:  []string{"4", "3"},
			wantRet: []string{"1", "0"},
		},
		{
			name: "不同产品各自填充",
			items: purchaseItems(
				[2]any{int64(100), q("4")},
				[2]any{int64(101), q("6")},
			),
			inSums:  prodSums([2]any{int64(100), q("2")}, [2]any{int64(101), q("5")}),
			retSums: map[int64]qty.Qty{},
			wantIn:  []string{"2", "5"},
			wantRet: []string{"0", "0"},
		},
		{
			name: "聚合超出明细总量截断",
			items: purchaseItems(
				[2]any{int64(100), q("4")},
				[2]any{int64(100), q("6")},
			),
			inSums:  prodSums([2]any{int64(100), q("20")}),
			retSums: prodSums([2]any{int64(100), q("3")}),
			wantIn:  []string{"4", "6"},
			wantRet: []string{"3", "0"},
		},
		{
			name:    "无进度",
			items:   purchaseItems([2]any{int64(100), q("4")}),
			inSums:  map[int64]qty.Qty{},
			retSums: map[int64]qty.Qty{},
			wantIn:  []string{"0"},
			wantRet: []string{"0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fillProgressByItemOrder(tc.items,
				func(it model.PurchaseOrderItem) int64 { return it.ProductID },
				func(it model.PurchaseOrderItem) qty.Qty { return it.Count },
				tc.inSums, tc.retSums,
				func(i int, inCount, returnCount qty.Qty) {
					tc.items[i].InCount = inCount
					tc.items[i].ReturnCount = returnCount
				})
			for i := range tc.items {
				if got := tc.items[i].InCount.String(); got != tc.wantIn[i]+".000" {
					t.Fatalf("行 %d inCount = %s，期望 %s.000", i, got, tc.wantIn[i])
				}
				if got := tc.items[i].ReturnCount.String(); got != tc.wantRet[i]+".000" {
					t.Fatalf("行 %d returnCount = %s，期望 %s.000", i, got, tc.wantRet[i])
				}
			}
		})
	}

	// 聚合 map 不被函数修改（纯函数约定）。
	sums := prodSums([2]any{int64(100), q("7")})
	items := purchaseItems([2]any{int64(100), q("4")}, [2]any{int64(100), q("6")})
	fillProgressByItemOrder(items,
		func(it model.PurchaseOrderItem) int64 { return it.ProductID },
		func(it model.PurchaseOrderItem) qty.Qty { return it.Count },
		sums, map[int64]qty.Qty{},
		func(i int, inCount, returnCount qty.Qty) {
			items[i].InCount = inCount
			items[i].ReturnCount = returnCount
		})
	if got := sums[int64(100)].String(); got != "7.000" {
		t.Fatalf("分摊不应修改调用方聚合 map：inSums[100] = %s，期望 7.000", got)
	}
}
