package service

import (
	"fmt"
	"regexp"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// 联系人字段规则（issue #9）：
//   - 手机：11 位大陆手机号，1[3-9] 号段；可留空，填了必须是合法格式；
//   - 邮箱：本地部分@域名.顶级域；可留空，填了必须是合法格式；
//   - 性别：0 未知 / 1 男 / 2 女（对齐字典 sys_user_sex）。
//
// 非法值一律 400，文案说清哪个字段、期望什么格式（CODING_STANDARDS「错误与文案」）。

// contactMobileRe 是大陆手机号：1 开头、第二位 3-9、共 11 位数字。
var contactMobileRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

// contactEmailRe 是邮箱：本地部分（不含空白与 @）@ 域名标签（不以点开头结尾、无连续点）. 至少 2 字母的顶级域。
var contactEmailRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@(?:[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?\.)+[A-Za-z]{2,}$`)

// ValidateContactMobile 校验联系人手机号：空值放行（选填），非空必须匹配大陆手机号格式。
func ValidateContactMobile(mobile string) error {
	if mobile == "" || contactMobileRe.MatchString(mobile) {
		return nil
	}
	return xerr.BadRequest(fmt.Sprintf("手机号 %q 格式不正确：应为 11 位大陆手机号（1 开头，如 13800000001）", mobile))
}

// ValidateContactEmail 校验联系人邮箱：空值放行（选填），非空必须匹配邮箱格式。
func ValidateContactEmail(email string) error {
	if email == "" || contactEmailRe.MatchString(email) {
		return nil
	}
	return xerr.BadRequest(fmt.Sprintf("邮箱 %q 格式不正确：应为 name@example.com 形式", email))
}

// ValidateContactGender 校验联系人性别枚举，非法值返回用户可读的 400 错误。
func ValidateContactGender(gender int8) error {
	if !model.ValidContactGender(gender) {
		return xerr.BadRequest(fmt.Sprintf("性别 %d 不合法：期望 0 未知 / 1 男 / 2 女", gender))
	}
	return nil
}
