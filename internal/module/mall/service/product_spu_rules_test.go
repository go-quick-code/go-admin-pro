package service_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
)

// product_spu_rules_test.go 是 SPU/SKU 校验规则的纯函数表驱动测试（外部测试
// 包，照 member_rules_test 形状；issue #177，docs/mall-mvp-spec.md §3.2/§4）。
// 唯一冲突（name 撞键 409）、规格引用的属性值存在性（repo 查询）、删除守卫
// 的引用计数查询与租户隔离由 HTTP 集成测试覆盖；这里锁字段级与结构级校验
// 语义：非法值一律 400/409，文案说清字段与期望格式。
//
// 锁定的规则清单（票面验收）：
//   - 价格非负（Money 口径：decimal(12,2)，分值存储，负数 400）；
//   - 库存非负整数（负数 400，超 int 上限 400）；
//   - 多规格至少一 SKU；单规格恰一空 properties SKU；
//   - SKU 结构：规格项去重、主 SKU（first）至多一个（零个由服务端默认首条）；
//   - SPU 删除约束：有订单引用 409（计数由 repo 提供，T6 前恒 0）；
//   - 品牌删除约束：被 SPU 引用 409（T4 接线 T3 预留谓词）。

// TestValidateSPUName SPU 名称校验表：必填、≤128 字（rune 口径）。
func TestValidateSPUName(t *testing.T) {
	long := strings.Repeat("商", 128)
	if err := mallservice.ValidateSPUName(long); err != nil {
		t.Fatalf("128 字名称不应报错: %v", err)
	}
	cases := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{"空名称", "   ", "不能为空"},
		{"超长", strings.Repeat("商", 129), "128 字"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mallservice.ValidateSPUName(tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("报错 %v 应含 %q", err, tc.wantMsg)
			}
			e, ok := xerr.As(err)
			if !ok || e.Status != 400 {
				t.Fatalf("字段校验失败应为 400 业务错误，实际 %v", err)
			}
		})
	}
}

// TestValidateSPUStatusAndSpecType 上下架状态与规格类型枚举表。
func TestValidateSPUStatusAndSpecType(t *testing.T) {
	for _, status := range []int8{mallmodel.ProductSPUStatusOff, mallmodel.ProductSPUStatusOn} {
		if err := mallservice.ValidateSPUStatus(status); err != nil {
			t.Fatalf("合法状态 %d 不应报错: %v", status, err)
		}
	}
	for _, specType := range []int8{mallmodel.ProductSpecTypeSingle, mallmodel.ProductSpecTypeMulti} {
		if err := mallservice.ValidateSPUSpecType(specType); err != nil {
			t.Fatalf("合法规格类型 %d 不应报错: %v", specType, err)
		}
	}
	for _, tc := range []struct {
		name  string
		input int8
		want  string
	}{
		{"状态越界", 2, "商品状态"},
		{"规格类型越界", 3, "规格类型"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.name == "状态越界" {
				err = mallservice.ValidateSPUStatus(tc.input)
			} else {
				err = mallservice.ValidateSPUSpecType(tc.input)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("报错 %v 应含 %q", err, tc.want)
			}
			e, ok := xerr.As(err)
			if !ok || e.Status != 400 {
				t.Fatalf("应为 400 业务错误，实际 %v", err)
			}
		})
	}
}

// TestValidateNonNegativeMoney 金额 Money 口径：非负（负数 400）；Money 值
// 已由类型保证 decimal(12,2) 精度（分值存储，不经过 float64），纯函数只锁
// 符号语义。
func TestValidateNonNegativeMoney(t *testing.T) {
	for _, text := range []string{"0", "0.00", "12.34", "9999999999.99"} {
		m, err := money.ParseMoney(text)
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", text, err)
		}
		if err := mallservice.ValidateNonNegativeMoney("售价", m); err != nil {
			t.Fatalf("非负金额 %q 不应报错: %v", text, err)
		}
	}
	neg, err := money.ParseMoney("-0.01")
	if err != nil {
		t.Fatalf("ParseMoney(-0.01): %v", err)
	}
	err = mallservice.ValidateNonNegativeMoney("售价", neg)
	if err == nil || !strings.Contains(err.Error(), "售价不能为负数") {
		t.Fatalf("负数金额应报「售价不能为负数」，实际 %v", err)
	}
	if e, ok := xerr.As(err); !ok || e.Status != 400 {
		t.Fatalf("应为 400 业务错误，实际 %v", err)
	}
}

// TestValidateSKUStock 库存非负整数表。
func TestValidateSKUStock(t *testing.T) {
	for _, stock := range []int{0, 1, 2147483647} {
		if err := mallservice.ValidateSKUStock(stock); err != nil {
			t.Fatalf("合法库存 %d 不应报错: %v", stock, err)
		}
	}
	for _, tc := range []struct {
		name  string
		stock int
		want  string
	}{
		{"负库存", -1, "库存"},
		{"超 int32 上限", 2147483648, "库存"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mallservice.ValidateSKUStock(tc.stock)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("报错 %v 应含 %q", err, tc.want)
			}
			if e, ok := xerr.As(err); !ok || e.Status != 400 {
				t.Fatalf("应为 400 业务错误，实际 %v", err)
			}
		})
	}
}

// TestValidateSPUSKUs SKU 结构校验表：单规格恰一空 properties SKU、多规格
// 至少一 SKU 且每条至少一个规格项、规格项去重、主 SKU（first）至多一个。
func TestValidateSPUSKUs(t *testing.T) {
	red := mallservice.PropertyValueRef{PropertyID: 1, ValueID: 11}
	blue := mallservice.PropertyValueRef{PropertyID: 1, ValueID: 12}
	std16g := mallservice.SkuRuleInput{Properties: []mallservice.PropertyValueRef{red}, First: true}
	std32g := mallservice.SkuRuleInput{Properties: []mallservice.PropertyValueRef{blue}}

	// 合法形态：单规格恰一空 SKU；多规格 ≥1 条非空 SKU。
	if err := mallservice.ValidateSPUSKUs(mallmodel.ProductSpecTypeSingle,
		[]mallservice.SkuRuleInput{{}}); err != nil {
		t.Fatalf("单规格空 SKU 不应报错: %v", err)
	}
	if err := mallservice.ValidateSPUSKUs(mallmodel.ProductSpecTypeMulti,
		[]mallservice.SkuRuleInput{std16g, std32g}); err != nil {
		t.Fatalf("多规格双 SKU 不应报错: %v", err)
	}

	cases := []struct {
		name     string
		specType int8
		skus     []mallservice.SkuRuleInput
		wantMsg  string
	}{
		{"单规格缺 SKU", mallmodel.ProductSpecTypeSingle, nil, "单规格"},
		{"单规格多条 SKU", mallmodel.ProductSpecTypeSingle,
			[]mallservice.SkuRuleInput{{}, {}}, "单规格"},
		{"单规格带规格项", mallmodel.ProductSpecTypeSingle,
			[]mallservice.SkuRuleInput{{Properties: []mallservice.PropertyValueRef{red}}}, "单规格"},
		{"多规格缺 SKU", mallmodel.ProductSpecTypeMulti, nil, "至少一条"},
		{"多规格空规格项", mallmodel.ProductSpecTypeMulti,
			[]mallservice.SkuRuleInput{{}}, "规格项"},
		{"多规格单条也合法——不报错才对", mallmodel.ProductSpecTypeMulti,
			[]mallservice.SkuRuleInput{std16g}, ""},
		{"规格项重复", mallmodel.ProductSpecTypeMulti,
			[]mallservice.SkuRuleInput{{Properties: []mallservice.PropertyValueRef{red, red}}}, "重复"},
		{"主 SKU 多个", mallmodel.ProductSpecTypeMulti,
			[]mallservice.SkuRuleInput{{Properties: []mallservice.PropertyValueRef{red}, First: true},
				{Properties: []mallservice.PropertyValueRef{blue}, First: true}}, "主 SKU"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mallservice.ValidateSPUSKUs(tc.specType, tc.skus)
			if tc.name == "多规格单条也合法——不报错才对" {
				if err != nil {
					t.Fatalf("多规格单条 SKU 不应报错: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("报错 %v 应含 %q", err, tc.wantMsg)
			}
			if e, ok := xerr.As(err); !ok || e.Status != 400 {
				t.Fatalf("应为 400 业务错误，实际 %v", err)
			}
		})
	}
}

// TestValidateSPUDelete SPU 删除约束：有订单引用 409（引用计数由 repo 查询
// 提供——T6 trade_order 落地前恒 0，谓词调用点照 T3 countSPUsByCategory
// 模式预留）；无引用放行。
func TestValidateSPUDelete(t *testing.T) {
	if err := mallservice.ValidateSPUDelete(mallservice.SPUDeleteGuard{}); err != nil {
		t.Fatalf("无订单引用不应报错: %v", err)
	}
	err := mallservice.ValidateSPUDelete(mallservice.SPUDeleteGuard{OrderCount: 1})
	if err == nil || !strings.Contains(err.Error(), "订单") {
		t.Fatalf("有订单引用应报 409 含「订单」，实际 %v", err)
	}
	if e, ok := xerr.As(err); !ok || e.Status != 409 {
		t.Fatalf("删除约束应为 409 业务错误，实际 %v", err)
	}
}

// TestValidateBrandDelete 品牌删除约束（T4 接线 T3 预留谓词）：被 SPU 引用
// 409，无引用放行。
func TestValidateBrandDelete(t *testing.T) {
	if err := mallservice.ValidateBrandDelete(mallservice.BrandDeleteGuard{}); err != nil {
		t.Fatalf("无 SPU 引用不应报错: %v", err)
	}
	err := mallservice.ValidateBrandDelete(mallservice.BrandDeleteGuard{SPUCount: 2})
	if err == nil || !strings.Contains(err.Error(), "商品") {
		t.Fatalf("被 SPU 引用应报 409 含「商品」，实际 %v", err)
	}
	if e, ok := xerr.As(err); !ok || e.Status != 409 {
		t.Fatalf("删除约束应为 409 业务错误，实际 %v", err)
	}
}
