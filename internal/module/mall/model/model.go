// Package model 是 mall 模块的实体定义（issue #174，docs/mall-mvp-spec.md §3.1）。
//
// mall 五张 member_* 表全部是租户级数据（登记 TenantTables，ADR 0019）：
// 查询/写入由租户插件自动追加 tenant_id 条件，跨租户业务 ID 一律不可见。
// 本包 T1 只落会员域实体：member_user/member_level/member_address 的读写
// 随 #174 管理端；member_point_config/member_point_record 表结构一次建全，
// 业务逻辑（唯一入账入口）随 T2 填充——T1 无任何积分写入路径。
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
