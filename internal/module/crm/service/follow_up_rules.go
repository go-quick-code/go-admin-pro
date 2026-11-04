package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// 跟进字段规则（issue #12）：
//   - 类型：芋道字典语义 1 电话 / 2 拜访 / 3 微信 / 4 邮件 / 5 其他；
//     可留空，服务层缺省「其他」，填了必须是合法枚举；
//   - 内容：必填（binding 层保证），长度上限 1000（binding 层保证）；
//   - 下次跟进时间：可留空，填了必须是可解析的日期时间。
//
// 非法值一律 400，文案说清哪个字段、期望什么格式（CODING_STANDARDS「错误与文案」）。

// followUpTimeLayouts 是下次跟进时间接受的格式：前端 value-format、纯日期与 RFC3339。
var followUpTimeLayouts = []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}

// ValidateFollowUpType 校验跟进类型：未传（nil）放行（服务层缺省「其他」），
// 传了必须是 1–5 枚举，非法值返回用户可读的 400 错误。
func ValidateFollowUpType(t *int8) error {
	if t == nil {
		return nil
	}
	if !model.ValidFollowUpType(*t) {
		return xerr.BadRequest(fmt.Sprintf("跟进类型 %d 不合法：期望 1 电话 / 2 拜访 / 3 微信 / 4 邮件 / 5 其他", *t))
	}
	return nil
}

// ValidateFollowUpNextTime 校验并解析下次跟进时间：空值放行（选填字段，返回 nil），
// 非空必须匹配已知格式，非法格式返回 400 并带回原始输入。
func ValidateFollowUpNextTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range followUpTimeLayouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, xerr.BadRequest(fmt.Sprintf("下次跟进时间 %q 格式不正确：应为 2026-01-02 15:04:05 形式", value))
}
