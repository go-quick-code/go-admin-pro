package service

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 通知字段长度上限（与 00015_notify_message 迁移的 varchar 定义一一对应，
// 按字符数计，与 MySQL varchar(n) 语义一致）。
const (
	maxTitleLen       = 128
	maxContentLen     = 500
	maxBizTypeLen     = 64
	maxRouteLen       = 255
	maxRouteParamsLen = 512
)

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// ValidateInput 校验一条通知投递输入（生产者 seam 的契约，表驱动单测锁定）：
//   - UserID > 0（通知按接收人物化）；
//   - Type 必须是 spec §4.13 枚举之一（bpm_todo/bpm_result/announcement/meeting_reminder）；
//   - Title 必填，≤128 字符；
//   - Content/BizType/Route 非必填，超长拒绝；
//   - RouteParams 非空时必须是合法的 JSON 对象字符串且 ≤512 字符
//     （前端以 JSON.parse 后作为 router.push 的 query 使用）。
func ValidateInput(in *Input) error {
	if in == nil {
		return xerr.BadRequest("通知输入不能为空")
	}
	if in.UserID <= 0 {
		return xerr.BadRequest("通知接收人 userId 必填")
	}
	if !model.ValidType(in.Type) {
		return xerr.BadRequest("通知类型非法：" + in.Type)
	}
	if strings.TrimSpace(in.Title) == "" {
		return xerr.BadRequest("通知标题必填")
	}
	if runeLen(in.Title) > maxTitleLen {
		return xerr.BadRequest("通知标题超长（最多 128 字符）")
	}
	if runeLen(in.Content) > maxContentLen {
		return xerr.BadRequest("通知内容超长（最多 500 字符）")
	}
	if runeLen(in.BizType) > maxBizTypeLen {
		return xerr.BadRequest("通知业务类型超长（最多 64 字符）")
	}
	if runeLen(in.Route) > maxRouteLen {
		return xerr.BadRequest("通知跳转路由超长（最多 255 字符）")
	}
	if in.RouteParams == "" {
		return nil
	}
	if runeLen(in.RouteParams) > maxRouteParamsLen {
		return xerr.BadRequest("通知跳转参数超长（最多 512 字符）")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(in.RouteParams), &obj); err != nil || obj == nil {
		return xerr.BadRequest("通知跳转参数必须是 JSON 对象字符串")
	}
	return nil
}
