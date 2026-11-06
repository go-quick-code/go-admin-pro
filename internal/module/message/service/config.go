package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// MaskedSecret 是密钥字段查询时的占位值；更新时提交该值表示保留原值
// （spec §4.3.6 / §4.11）。
const MaskedSecret = "******"

// secretKeys 是需要脱敏的 provider 配置键（spec §4.3.6，大小写敏感）。
var secretKeys = []string{"password", "secret", "accessKeySecret"}

// parseConfigObject 把渠道配置解析为 JSON 对象；空串视为 {}。
// null、数组、标量等非对象形态一律拒绝（config 必须是 provider 配置对象）。
func parseConfigObject(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("渠道配置必须是 JSON 对象: %w", err)
	}
	if obj == nil {
		return nil, fmt.Errorf("渠道配置必须是 JSON 对象")
	}
	return obj, nil
}

// normalizeConfig 校验并压缩渠道配置 JSON；空串落库为 {}。
func normalizeConfig(raw string) (string, error) {
	obj, err := parseConfigObject(raw)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("序列化渠道配置: %w", err)
	}
	return string(data), nil
}

// maskConfig 返回脱敏后的渠道配置：password/secret/accessKeySecret 的非空
// 字符串值替换为 ******。存储内容异常（非 JSON 对象）时返回 {}，
// 宁可丢失展示也不回显可能的密钥（spec §4.11）。
func maskConfig(raw string) string {
	obj, err := parseConfigObject(raw)
	if err != nil {
		return "{}"
	}
	for _, key := range secretKeys {
		if value, ok := obj[key].(string); ok && value != "" {
			obj[key] = MaskedSecret
		}
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// mergeSecretConfig 处理更新请求的密钥占位：
//   - 提交值为 ****** 且原配置有该键：保留原值；
//   - 提交值为 ****** 但原配置没有该键：删除该键（不把占位符当密钥落库）；
//   - 其他值按提交值覆盖。
//
// 非密钥字段按提交值整体覆盖；返回压缩后的 JSON。
func mergeSecretConfig(oldRaw, newRaw string) (string, error) {
	oldObj, err := parseConfigObject(oldRaw)
	if err != nil {
		// 旧值异常时不阻断更新：按新值落库，旧密钥无法保留。
		oldObj = map[string]any{}
	}
	newObj, err := parseConfigObject(newRaw)
	if err != nil {
		return "", err
	}
	for _, key := range secretKeys {
		value, ok := newObj[key].(string)
		if !ok || value != MaskedSecret {
			continue
		}
		if oldValue, exists := oldObj[key]; exists {
			newObj[key] = oldValue
		} else {
			delete(newObj, key)
		}
	}
	data, err := json.Marshal(newObj)
	if err != nil {
		return "", fmt.Errorf("序列化渠道配置: %w", err)
	}
	return string(data), nil
}

// configValidationError 把解析错误翻译成用户可见的 400。
func configValidationError(err error) error {
	return xerr.BadRequest("渠道配置格式错误：" + err.Error())
}
