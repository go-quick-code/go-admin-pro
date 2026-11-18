package service

import (
	"fmt"
	"strings"
	"unicode/utf8"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// point_rules.go 是积分域字段级校验的纯函数（issue #175，
// docs/mall-mvp-spec.md §3.1/§3.4；照 member_rules.go 形状）：
//   - biz_type ∈ 三类枚举（order_pay/order_cancel/admin_adjust）；
//   - 变动积分非零（正入负出，0 无意义一律 400）；
//   - point_per_yuan ≥ 0（0 = 支付不入账，合法配置）。
//
// 余额不足（409）与会员不存在（404）不是纯函数可判——由 service 层在
// 行锁内/存在性查询后翻译。

// ValidatePointBizType 校验积分流水业务类型 ∈ 三类枚举（spec §3.1）。
func ValidatePointBizType(bizType string) error {
	if !mallmodel.IsMemberPointBizType(bizType) {
		return xerr.BadRequest(fmt.Sprintf("积分流水业务类型 %q 不合法：期望 order_pay / order_cancel / admin_adjust", bizType))
	}
	return nil
}

// ValidateAdjustPoint 校验变动积分：非零（正入负出，0 拒绝——调整弹窗与
// 入账入口共用同一规则，spec §4「point 可正可负，0 拒绝」）。
func ValidateAdjustPoint(point int) error {
	if point == 0 {
		return xerr.BadRequest("变动积分不能为 0：正数入账、负数扣减")
	}
	return nil
}

// ValidatePointRemark 校验调整备注：选填，≤500 字（DB varchar(500) 按
// utf8mb4 字符计，rune 口径同 ValidateLevelName）。
func ValidatePointRemark(remark string) error {
	if utf8.RuneCountInString(strings.TrimSpace(remark)) > 500 {
		return xerr.BadRequest("备注不能超过 500 字")
	}
	return nil
}

// ValidatePointPerYuan 校验积分配置：≥ 0（0 = 支付成功不入账，合法；
// 负数无意义一律 400，spec §3.1「point_per_yuan int NOT NULL DEFAULT 1」）。
func ValidatePointPerYuan(pointPerYuan int) error {
	if pointPerYuan < 0 {
		return xerr.BadRequest(fmt.Sprintf("point_per_yuan %d 不合法：应为 ≥0 的整数（0 = 支付不入账）", pointPerYuan))
	}
	return nil
}
