package service

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
)

// product_spu_rules.go 是商品 SPU/SKU 字段级与结构级校验的纯函数（issue
// #177，docs/mall-mvp-spec.md §3.2/§4；照 product_rules.go/member_rules.go
// 形状）：
//   - SPU：名称必填 ≤128（rune 口径）、上下架状态枚举 0 下架 / 1 上架、
//     规格类型枚举 0 单规格 / 1 多规格、市场价格非负（Money 口径）；
//   - SKU：价格/划线价非负（Money）、库存非负整数（[0, MaxInt32]，DB 列
//     int 上限）、结构（单规格恰一空 properties SKU / 多规格至少一 SKU 且
//     每条至少一个规格项、规格项 (propertyId,valueId) 去重、主 SKU 至多一个
//     ——零个由服务端默认首条，见 service/product_spu.go）；
//   - 删除约束谓词：SPU「有订单引用禁删」（计数由 repo.CountOrdersBySPU
//     提供，T6 trade_order 落地前恒 0——照 T3 countSPUsByCategory 模式预留
//     接线点）、品牌「被 SPU 引用禁删」（T4 接线 T3 预留）。
//
// 规格引用的 propertyValueId 存在性与 value 归属属性（repo 查询，带 IO）不
// 在本文件——见 service/product_spu.go resolveSKUProperties。
//
// 非法值一律 400（删除约束 409），文案说清字段与期望格式（CODING_STANDARDS
// 「错误与文案」）。name 撞键 409 由 service 层翻译，不在本文件（纯函数无 IO）。

// PropertyValueRef 是 SKU 规格项的中性引用（rules 无 IO，调用方把 dto 拍平
// 传入；名称由 service 层按主数据反查写入快照，不进纯函数）。
type PropertyValueRef struct {
	PropertyID int64
	ValueID    int64
}

// SkuRuleInput 是 SKU 结构校验的中性输入（dto → rules 的拍平形状）。
type SkuRuleInput struct {
	Properties  []PropertyValueRef
	Price       money.Money
	MarketPrice money.Money
	Stock       int
	First       bool
}

// ValidateSPUName 校验 SPU 名称：必填、≤128 字（rune 口径，DB varchar(128)
// 按 rune 计——中文名不会被字节长度误伤）。
func ValidateSPUName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return xerr.BadRequest("商品名称不能为空")
	}
	if utf8.RuneCountInString(name) > 128 {
		return xerr.BadRequest("商品名称不能超过 128 字")
	}
	return nil
}

// ValidateSPUStatus 校验上下架状态枚举：0 下架 / 1 上架（数值对齐芋道
// ProductSpuStatusEnum；只有上架可被购买——购买边界随 T5）。
func ValidateSPUStatus(status int8) error {
	if status != mallmodel.ProductSPUStatusOff && status != mallmodel.ProductSPUStatusOn {
		return xerr.BadRequest(fmt.Sprintf("商品状态 %d 不合法：期望 0 下架 / 1 上架", status))
	}
	return nil
}

// ValidateSPUSpecType 校验规格类型枚举：0 单规格 / 1 多规格。
func ValidateSPUSpecType(specType int8) error {
	if specType != mallmodel.ProductSpecTypeSingle && specType != mallmodel.ProductSpecTypeMulti {
		return xerr.BadRequest(fmt.Sprintf("规格类型 %d 不合法：期望 0 单规格 / 1 多规格", specType))
	}
	return nil
}

// ValidateNonNegativeMoney 校验金额非负（Money 口径：decimal(12,2) 分值存储，
// 类型已保证精度与两位小数，这里只锁符号语义——负数 400）。label 进文案，
// 如「售价」「市场价格」。
func ValidateNonNegativeMoney(label string, m money.Money) error {
	if m.Cents() < 0 {
		return xerr.BadRequest(label + "不能为负数")
	}
	return nil
}

// ValidateSKUStock 校验库存非负整数：[0, math.MaxInt32]（DB 列 int 上限；
// T6 下单按行锁条件扣减，负库存无意义）。
func ValidateSKUStock(stock int) error {
	if stock < 0 {
		return xerr.BadRequest("SKU 库存不能为负数")
	}
	if stock > math.MaxInt32 {
		return xerr.BadRequest(fmt.Sprintf("SKU 库存 %d 超过上限 %d", stock, int64(math.MaxInt32)))
	}
	return nil
}

// ValidateSPUSKUs 校验 SPU 的 SKU 集合结构（spec §3.2「spec_type（单规格/
// 多规格）」+ SKU properties 形状）：
//   - 单规格：恰一条 SKU 且 properties 为空（价格/库存挂唯一 SKU）；
//   - 多规格：至少一条 SKU，每条至少一个规格项，同一 SKU 内 (propertyId,
//     valueId) 不得重复（重复即同规格变体重复报价）；
//   - 主 SKU（first）至多一个——零个合法，服务端默认首条为主 SKU（前端
//     不选也能落库，展示口径稳定）。
//
// 纯函数：调用方传入拍平后的 SKU 输入；规格项引用的属性值存在性/归属由
// service 层查库校验（404/400），不在此。
func ValidateSPUSKUs(specType int8, skus []SkuRuleInput) error {
	if err := ValidateSPUSpecType(specType); err != nil {
		return err
	}
	switch specType {
	case mallmodel.ProductSpecTypeSingle:
		if len(skus) != 1 {
			return xerr.BadRequest(fmt.Sprintf("单规格商品必须且只能有一条 SKU，实际 %d 条", len(skus)))
		}
		if len(skus[0].Properties) != 0 {
			return xerr.BadRequest("单规格 SKU 不能携带规格项（properties 须为空）")
		}
	case mallmodel.ProductSpecTypeMulti:
		if len(skus) == 0 {
			return xerr.BadRequest("多规格商品至少一条 SKU")
		}
		for i, sku := range skus {
			if len(sku.Properties) == 0 {
				return xerr.BadRequest(fmt.Sprintf("第 %d 条 SKU 未设置规格项：多规格商品每条 SKU 至少一个规格项", i+1))
			}
			seen := make(map[PropertyValueRef]struct{}, len(sku.Properties))
			for _, ref := range sku.Properties {
				if _, dup := seen[ref]; dup {
					return xerr.BadRequest(fmt.Sprintf("第 %d 条 SKU 规格项重复（propertyId=%d valueId=%d）", i+1, ref.PropertyID, ref.ValueID))
				}
				seen[ref] = struct{}{}
			}
		}
	}
	firstCount := 0
	for _, sku := range skus {
		if sku.First {
			firstCount++
		}
	}
	if firstCount > 1 {
		return xerr.BadRequest(fmt.Sprintf("主 SKU（first）只能有一个，实际 %d 个", firstCount))
	}
	return nil
}

// SPUDeleteGuard 是 SPU 删除约束的输入快照（引用计数由调用方查询，纯函数
// 只做判定）。
type SPUDeleteGuard struct {
	// OrderCount 是引用该 SPU 的订单数（repo.CountOrdersBySPU，T6 trade_order
	// 落地前恒 0——照 T3 countSPUsByCategory 模式预留接线点）。
	OrderCount int64
}

// ValidateSPUDelete 校验 SPU 删除约束（spec §4「删除约束：有订单引用不可删」）：
// 返回 nil 表示可删，否则 409。被订单引用的 SPU 禁删（订单项成交快照之外，
// 售后/对账要按 spu_id 回查主数据）。SKU 不拦删除：删除事务级联软删全部
// SKU（SKU 只经 SPU 创建/全量替换产生，无独立删除端点；不变量要求每 SPU
// 至少一条 SKU，拦「有 SKU」会让 SPU 永不可删——级联口径见 repo.DeleteSKUsBySPU）。
func ValidateSPUDelete(guard SPUDeleteGuard) error {
	if guard.OrderCount > 0 {
		return xerr.Conflict("商品已被订单引用，无法删除，请先处理相关订单")
	}
	return nil
}

// BrandDeleteGuard 是品牌删除约束的输入快照（引用计数由调用方查询）。
type BrandDeleteGuard struct {
	// SPUCount 是引用该品牌的 SPU 数（repo.CountSPUsByBrand，软删 SPU 不算）。
	SPUCount int64
}

// ValidateBrandDelete 校验品牌删除约束（T4 接线 T3 预留谓词，spec §3.2
// product_spu.brand_id 引用）：被 SPU 引用禁删（409）——品牌是 SPU 的展示
// 归属，先调整商品品牌再删。
func ValidateBrandDelete(guard BrandDeleteGuard) error {
	if guard.SPUCount > 0 {
		return xerr.Conflict("品牌下存在商品（SPU）引用，无法删除，请先调整商品品牌")
	}
	return nil
}
