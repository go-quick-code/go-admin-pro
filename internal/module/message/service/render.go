package service

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// paramNameRe 是模板参数名/占位符名的合法形态（spec §4.3.3）。
var paramNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// placeholderRe 匹配 {{name}} 占位符；{{ name }} 等变体不被识别，
// 由 collectPlaceholders 在模板维护时拒绝（只支持 {{name}}）。
var placeholderRe = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

// mobileRe 是大陆手机号校验（spec §4.3.4）。
var mobileRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

// validateTemplateParams 校验模板声明的参数名：非空、符合标识符形态、不重复。
// 返回去重后的参数名（保持提交顺序）。
func validateTemplateParams(params []string) ([]string, error) {
	seen := make(map[string]struct{}, len(params))
	result := make([]string, 0, len(params))
	for _, name := range params {
		if !paramNameRe.MatchString(name) {
			return nil, xerr.BadRequest(fmt.Sprintf(
				"模板参数名不合法：%q（期望字母或下划线开头，仅含字母、数字、下划线）", name))
		}
		if _, ok := seen[name]; ok {
			return nil, xerr.BadRequest(fmt.Sprintf("模板参数重复：%s", name))
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

// collectPlaceholders 提取模板文本中的占位符名，并拒绝不支持的写法。
func collectPlaceholders(texts ...string) ([]string, error) {
	var names []string
	for _, text := range texts {
		for _, match := range placeholderRe.FindAllStringSubmatch(text, -1) {
			name := match[1]
			if !paramNameRe.MatchString(name) {
				return nil, xerr.BadRequest(fmt.Sprintf(
					"模板内容包含不支持的占位符：%s（仅支持 {{name}} 形式）", match[0]))
			}
			names = append(names, name)
		}
	}
	return names, nil
}

// validateTemplateContent 校验模板文本中的占位符全部在参数声明中：
// 渲染只替换声明过的参数，未声明占位会原样发给用户，必须在维护时拦截。
func validateTemplateContent(params []string, texts ...string) error {
	declared := make(map[string]struct{}, len(params))
	for _, name := range params {
		declared[name] = struct{}{}
	}
	names, err := collectPlaceholders(texts...)
	if err != nil {
		return err
	}
	var undeclared []string
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := declared[name]; ok {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		undeclared = append(undeclared, name)
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return xerr.BadRequest("模板内容使用了未声明的参数：" + strings.Join(undeclared, "、"))
	}
	return nil
}

// encodeTemplateParams 把参数声明序列化为 JSON 数组文本（varchar(1000)）。
func encodeTemplateParams(params []string) (string, error) {
	if params == nil {
		params = []string{}
	}
	data, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("序列化模板参数: %w", err)
	}
	if len(data) > 1000 {
		return "", xerr.BadRequest("模板参数过多：声明 JSON 不能超过 1000 字符")
	}
	return string(data), nil
}

// decodeTemplateParams 解析库内的参数声明 JSON。
func decodeTemplateParams(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	var params []string
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil, fmt.Errorf("解析模板参数声明: %w", err)
	}
	if params == nil {
		return []string{}, nil
	}
	return params, nil
}

// validateSendParams 校验发送参数与模板声明完全一致：缺失或多余都 400
// （spec §4.3.3）。参数值为空字符串是合法值，只比较键集合。
func validateSendParams(declared []string, provided map[string]string) error {
	declaredSet := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		declaredSet[name] = struct{}{}
	}
	providedSet := make(map[string]struct{}, len(provided))
	for name := range provided {
		providedSet[name] = struct{}{}
	}

	var missing, extra []string
	for _, name := range declared {
		if _, ok := providedSet[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range providedSet {
		if _, ok := declaredSet[name]; !ok {
			extra = append(extra, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return xerr.BadRequest("缺少模板参数：" + strings.Join(missing, "、"))
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return xerr.BadRequest("多余的模板参数：" + strings.Join(extra, "、"))
	}
	return nil
}

// renderTemplate 替换 {{name}} 占位。调用方已保证参数集合与声明一致，
// 这里仍对未解析的占位返回错误，避免把模板原文发给用户。
func renderTemplate(text string, params map[string]string) (string, error) {
	var missing []string
	seen := make(map[string]struct{})
	rendered := placeholderRe.ReplaceAllStringFunc(text, func(match string) string {
		name := placeholderRe.FindStringSubmatch(match)[1]
		value, ok := params[name]
		if !ok {
			if _, exists := seen[name]; !exists {
				seen[name] = struct{}{}
				missing = append(missing, name)
			}
			return match
		}
		return value
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", xerr.BadRequest("缺少模板参数：" + strings.Join(missing, "、"))
	}
	return rendered, nil
}

// validateRecipient 按渠道类型校验收件人：短信为大陆手机号，
// 邮件为基础 RFC 5322 地址（拒绝 "Name <addr>" 这类带显示名的写法）。
func validateRecipient(channelType, recipient string) error {
	switch channelType {
	case model.ChannelTypeSMS:
		if !mobileRe.MatchString(recipient) {
			return xerr.BadRequest("手机号格式不正确：期望 11 位大陆手机号（1[3-9] 开头）")
		}
		return nil
	case model.ChannelTypeMail:
		addr, err := mail.ParseAddress(recipient)
		if err != nil || addr.Name != "" || addr.Address != recipient {
			return xerr.BadRequest("邮箱格式不正确：期望形如 user@example.com")
		}
		return nil
	default:
		return xerr.BadRequest("渠道类型不合法：期望 sms 或 mail")
	}
}
