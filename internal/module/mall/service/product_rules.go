package service

import (
	"fmt"
	"strings"
	"unicode/utf8"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// product_rules.go 是商品域基础三件字段级校验的纯函数（issue #176，
// docs/mall-mvp-spec.md §3.2/§4；照 member_rules.go 形状）：
//   - 分类：两级树父级合法性（父必须是根级）、同级同名判定、删除约束谓词
//     （有子级或被 SPU 引用禁删）；
//   - 品牌/属性名/属性值：名称必填 ≤64（DB varchar(64) 按 rune 计）；
//   - 状态枚举 0 启用 / 1 停用（数值对齐芋道 CommonStatusEnum）。
//
// 非法值一律 400，文案说清字段与期望格式（CODING_STANDARDS「错误与文案」）。
// name/parent_id 的租户内唯一冲突（409）由 service 层撞唯一键翻译，
// 不在本文件（纯函数无 IO）。

// ---------- 商品分类 ----------

// ValidateCategoryName 校验分类名称：必填、≤64 字（rune 口径，中文名不会被
// 字节长度误伤）。
func ValidateCategoryName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return xerr.BadRequest("分类名称不能为空")
	}
	if utf8.RuneCountInString(name) > 64 {
		return xerr.BadRequest("分类名称不能超过 64 字")
	}
	return nil
}

// ValidateCategoryStatus 校验分类状态枚举：0 启用 / 1 停用。
func ValidateCategoryStatus(status int8) error {
	if status != mallmodel.StatusEnable && status != mallmodel.StatusDisable {
		return xerr.BadRequest(fmt.Sprintf("分类状态 %d 不合法：期望 0 启用 / 1 停用", status))
	}
	return nil
}

// ValidateCategoryParent 校验分类父级合法性（两级树，spec §3.2「parent_id
// （0=根，两级树，叶子才可挂 SPU）」）：
//   - parentID 0 表示挂根，直接通过；
//   - 父分类必须存在（含软删过滤后的可见列表）；
//   - 父不能是自己；
//   - 父必须是根级（parent.parentID == 0）——两级树不允许三级及更深挂载；
//   - 更新场景（selfID != 0）把自己从根降级为子级时，自己不得有子分类——
//     否则其子分类会成第三级，破坏两级树不变量。
//
// 纯函数：调用方传入「当前租户的全部分类」。新建 selfID 传 0，更新传自身 ID。
func ValidateCategoryParent(list []mallmodel.ProductCategory, parentID, selfID int64) error {
	if parentID == mallmodel.ProductCategoryRootID {
		return nil
	}
	byID := make(map[int64]mallmodel.ProductCategory, len(list))
	for _, row := range list {
		byID[row.ID] = row
	}
	parent, ok := byID[parentID]
	if !ok {
		return xerr.BadRequest("上级分类不存在")
	}
	if parentID == selfID {
		return xerr.BadRequest("上级分类不能是自己")
	}
	// 两级树：父必须是根级。父本身挂在别的分类下，挂上去就是第三级。
	if parent.ParentID != mallmodel.ProductCategoryRootID {
		return xerr.BadRequest(fmt.Sprintf("上级分类 %q 已是子分类：商品分类只允许两级（父必须是根级）", parent.Name))
	}
	// 降级守卫：根分类挂了子分类就不可再挂到别的根下（子分类会成第三级）。
	if selfID != 0 {
		for _, row := range list {
			if row.ParentID == selfID {
				return xerr.BadRequest("分类下存在子分类，不能移动为子分类（商品分类只允许两级）")
			}
		}
	}
	return nil
}

// CategoryDeleteGuard 是分类删除约束的输入快照（引用计数由调用方查询，
// 纯函数只做判定）。
type CategoryDeleteGuard struct {
	// ChildrenCount 是子分类数（软删过滤后的可见子级）。
	ChildrenCount int64
	// SPUCount 是引用该分类的 SPU 数（T4 SPU 表落地后接线，接线前恒 0）。
	SPUCount int64
}

// ValidateCategoryDelete 校验分类删除约束（spec §4「删除约束——有子级或被
// SPU 引用不可删」）：返回 nil 表示可删，否则 409。
//   - 有子级：子分类会成为孤儿，先删/挪子级；
//   - 被 SPU 引用：SPU 挂着该分类（category_id），先调整 SPU 归属。
func ValidateCategoryDelete(guard CategoryDeleteGuard) error {
	if guard.ChildrenCount > 0 {
		return xerr.Conflict("分类下存在子分类，无法删除，请先处理子分类")
	}
	if guard.SPUCount > 0 {
		return xerr.Conflict("分类下存在商品（SPU）引用，无法删除，请先调整商品分类")
	}
	return nil
}

// ---------- 商品品牌 ----------

// ValidateBrandName 校验品牌名称：必填、≤64 字（rune 口径）。
func ValidateBrandName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return xerr.BadRequest("品牌名称不能为空")
	}
	if utf8.RuneCountInString(name) > 64 {
		return xerr.BadRequest("品牌名称不能超过 64 字")
	}
	return nil
}

// ValidateBrandStatus 校验品牌状态枚举：0 启用 / 1 停用。
func ValidateBrandStatus(status int8) error {
	if status != mallmodel.StatusEnable && status != mallmodel.StatusDisable {
		return xerr.BadRequest(fmt.Sprintf("品牌状态 %d 不合法：期望 0 启用 / 1 停用", status))
	}
	return nil
}

// ---------- 商品属性名/属性值 ----------

// ValidatePropertyName 校验属性名：必填、≤64 字（rune 口径）。
func ValidatePropertyName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return xerr.BadRequest("属性名不能为空")
	}
	if utf8.RuneCountInString(name) > 64 {
		return xerr.BadRequest("属性名不能超过 64 字")
	}
	return nil
}

// ValidatePropertyValueName 校验属性值：必填、≤64 字（rune 口径）。
func ValidatePropertyValueName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return xerr.BadRequest("属性值不能为空")
	}
	if utf8.RuneCountInString(name) > 64 {
		return xerr.BadRequest("属性值不能超过 64 字")
	}
	return nil
}
