// sale_progress_test.go 是订单进度填充的纯函数表驱动测试（issue #92，
// docs/erp-mvp-spec.md §8.1 次缝：进度计算；§4.5 进度只计已审核，同产品跨行
// 按明细 ID 序依次占用，总量精确等于已审核聚合值）。
package service

import (
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

func q(s string) qty.Qty {
	v, err := qty.ParseQty(s)
	if err != nil {
		panic(err)
	}
	return v
}

// saleItems 按显式 (产品, 数量) 对构造订单明细（进度填充前 OutCount/ReturnCount 归零）。
func saleItems(pairs ...[2]any) []model.SaleOrderItem {
	out := make([]model.SaleOrderItem, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, model.SaleOrderItem{
			ProductID: p[0].(int64),
			Count:     p[1].(qty.Qty),
		})
	}
	return out
}

func prodSums(pairs ...[2]any) map[int64]qty.Qty {
	out := make(map[int64]qty.Qty, len(pairs))
	for _, p := range pairs {
		out[p[0].(int64)] = p[1].(qty.Qty)
	}
	return out
}

// TestFillOrderProgress 表驱动：单行全额 / 同产品跨行按行序填满 / 聚合超出
// 行数总和截断 / 无进度。
func TestFillOrderProgress(t *testing.T) {
	cases := []struct {
		name    string
		items   []model.SaleOrderItem
		outSums map[int64]qty.Qty
		retSums map[int64]qty.Qty
		wantOut []string
		wantRet []string
	}{
		{
			name:    "单行全额",
			items:   saleItems([2]any{int64(100), q("10")}),
			outSums: prodSums([2]any{int64(100), q("6")}),
			retSums: prodSums([2]any{int64(100), q("2")}),
			wantOut: []string{"6"},
			wantRet: []string{"2"},
		},
		{
			name: "同产品跨行按行序填满",
			items: saleItems(
				[2]any{int64(100), q("4")},
				[2]any{int64(100), q("6")},
			),
			outSums: prodSums([2]any{int64(100), q("7")}),
			retSums: map[int64]qty.Qty{},
			wantOut: []string{"4", "3"},
			wantRet: []string{"0", "0"},
		},
		{
			name: "聚合超出行数总和截断",
			items: saleItems(
				[2]any{int64(100), q("4")},
				[2]any{int64(100), q("6")},
			),
			outSums: prodSums([2]any{int64(100), q("20")}),
			retSums: prodSums([2]any{int64(100), q("1")}),
			wantOut: []string{"4", "6"},
			wantRet: []string{"1", "0"},
		},
		{
			name: "无进度",
			items: saleItems(
				[2]any{int64(100), q("4")},
				[2]any{int64(101), q("6")},
			),
			outSums: map[int64]qty.Qty{},
			retSums: map[int64]qty.Qty{},
			wantOut: []string{"0", "0"},
			wantRet: []string{"0", "0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fillOrderProgress(tc.items, tc.outSums, tc.retSums)
			for i := range tc.items {
				if got := tc.items[i].OutCount.String(); got != tc.wantOut[i]+".000" {
					t.Fatalf("行 %d outCount = %s，期望 %s.000", i, got, tc.wantOut[i])
				}
				if got := tc.items[i].ReturnCount.String(); got != tc.wantRet[i]+".000" {
					t.Fatalf("行 %d returnCount = %s，期望 %s.000", i, got, tc.wantRet[i])
				}
			}
		})
	}
}
