// Package model 是 mall 模块的实体定义（issue #174 起步，docs/mall-mvp-spec.md
// §3）。
//
// mall 全部业务表（member_/product_/trade_/mall_sequence）都是租户级数据
// （登记 TenantTables，ADR 0019）：查询/写入由租户插件自动追加 tenant_id
// 条件，跨租户业务 ID 一律不可见。本包按域落实体：会员域随 #174/#175，
// 商品域随 #176/#177，交易域（trade_cart/trade_order/trade_order_item）
// 随 #179（T6）。
package model

import (
	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// BaseEntity 是统一实体底座，定义见 internal/pkg/model。
type BaseEntity = basemodel.BaseEntity

// 启用状态：0 启用 / 1 停用，与 system/pay 保持一致（数值对齐芋道
// CommonStatusEnum）。
const (
	StatusEnable  int8 = 0
	StatusDisable int8 = 1
)

// MemberPointBizType 是积分流水业务类型（biz_type，spec §3.1）：三类枚举。
const (
	// MemberPointBizOrderPay 支付成功按实付入账（正数，T4 支付联动写入）。
	MemberPointBizOrderPay = "order_pay"
	// MemberPointBizOrderCancel 取消/关单回扣（负数，T3 落地）。
	MemberPointBizOrderCancel = "order_cancel"
	// MemberPointBizAdminAdjust 管理员调整（正负皆可，T2 唯一入账入口）。
	MemberPointBizAdminAdjust = "admin_adjust"
)

// MemberPointBizTypes 是全部合法 biz_type 集合（T2 入账入口与查询过滤共用）。
var MemberPointBizTypes = []string{
	MemberPointBizOrderPay, MemberPointBizOrderCancel, MemberPointBizAdminAdjust,
}

// IsMemberPointBizType 报告 bizType 是否为合法的积分流水业务类型。
func IsMemberPointBizType(bizType string) bool {
	for _, t := range MemberPointBizTypes {
		if t == bizType {
			return true
		}
	}
	return false
}
