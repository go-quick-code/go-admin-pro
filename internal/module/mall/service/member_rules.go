package service

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	mallmodel "github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// member_rules.go 是会员域字段级校验的纯函数（issue #174，
// docs/mall-mvp-spec.md §3.1/§4；照 crm contact_rules.go 形状）：
//   - 手机号必填且为 11 位大陆手机号（C 端注册/登录身份，T2 复用同一规则）；
//   - 密码选填（管理端创建可不设，C 端注册自设；更新时空值 = 不改密码），
//     填了须 6–64 位——bcrypt 哈希不收明文，明文只在校验后立即散列；
//   - 状态枚举 0 启用 / 1 停用（数值对齐芋道 CommonStatusEnum）。
//
// 非法值一律 400，文案说清字段与期望格式（CODING_STANDARDS「错误与文案」）。
// mobile/name/level 的租户内唯一冲突（409）由 service 层撞唯一键翻译，
// 不在本文件（纯函数无 IO）。

// memberMobileRe 是大陆手机号：1 开头、第二位 3-9、共 11 位数字（与
// crm contactMobileRe / message mobileRe 同款）。
var memberMobileRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

// ValidateMemberMobile 校验会员手机号：必填且匹配大陆手机号格式。
func ValidateMemberMobile(mobile string) error {
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return xerr.BadRequest("手机号不能为空")
	}
	if !memberMobileRe.MatchString(mobile) {
		return xerr.BadRequest(fmt.Sprintf("手机号 %q 格式不正确：应为 11 位大陆手机号（1 开头，如 13800000001）", mobile))
	}
	return nil
}

// ValidateMemberPassword 校验会员密码：选填（空/纯空白 = 不设密码或不改），
// 非空须 6–64 位。
func ValidateMemberPassword(password string) error {
	trimmed := strings.TrimSpace(password)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) < 6 {
		return xerr.BadRequest("密码至少 6 位")
	}
	if len(trimmed) > 64 {
		return xerr.BadRequest("密码不能超过 64 位")
	}
	return nil
}

// ValidateMemberStatus 校验会员状态枚举：0 启用 / 1 停用。
func ValidateMemberStatus(status int8) error {
	if status != mallmodel.StatusEnable && status != mallmodel.StatusDisable {
		return xerr.BadRequest(fmt.Sprintf("会员状态 %d 不合法：期望 0 启用 / 1 停用", status))
	}
	return nil
}

// ValidateLevelName 校验等级名称：必填、≤64 字（DB varchar(64) 按 utf8mb4
// 字符计——长度用 rune 数口径，中文名不会被字节长度误伤）。
func ValidateLevelName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return xerr.BadRequest("等级名称不能为空")
	}
	if utf8.RuneCountInString(name) > 64 {
		return xerr.BadRequest("等级名称不能超过 64 字")
	}
	return nil
}

// ValidateLevelValue 校验等级值（排序，租户内唯一）：≥1 的整数。
func ValidateLevelValue(level int) error {
	if level < 1 {
		return xerr.BadRequest(fmt.Sprintf("等级值 %d 不合法：应为 ≥1 的整数（小值低级）", level))
	}
	return nil
}

// ValidateLevelExperience 校验经验阈值：≥0（纯维护列，MVP 无经验值体系）。
func ValidateLevelExperience(experience int) error {
	if experience < 0 {
		return xerr.BadRequest(fmt.Sprintf("经验阈值 %d 不合法：应为 ≥0 的整数", experience))
	}
	return nil
}

// ValidateAddressInput 校验收货地址输入（C 端新增/更新共用，规格 §3.1
// member_address 列宽口径）：四项省市县+详址非空且不超列宽，联系人/手机号
// 同宽；手机号复用会员 mobile 规则（同一 C 端身份标识，口径不分叉）。
func ValidateAddressInput(receiverName, receiverMobile, province, city, district, detail string) error {
	if len([]rune(receiverName)) == 0 || len([]rune(receiverName)) > 64 {
		return xerr.BadRequest("联系人姓名必填且不超过 64 字")
	}
	if err := ValidateMemberMobile(receiverMobile); err != nil {
		return err
	}
	for _, v := range []struct{ label, val string }{
		{"省份", province}, {"城市", city}, {"区县", district},
	} {
		if len([]rune(v.val)) == 0 || len([]rune(v.val)) > 64 {
			return xerr.BadRequest(v.label + "必填且不超过 64 字")
		}
	}
	if len([]rune(detail)) == 0 || len([]rune(detail)) > 255 {
		return xerr.BadRequest("详细地址必填且不超过 255 字")
	}
	return nil
}
