package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// config.go 是渠道凭证三段式（明文落库 / 查询脱敏 / 更新占位符合并），
// 机制复制 message_channel 先例（docs/pay-mvp-spec.md §3.1，ADR 0021 显式
// 接受 v1 不加密；全局加密另立工单时三处一起收口）。

// MaskedSecret 是密钥字段查询时的占位值；更新时提交该值表示保留原值。
const MaskedSecret = "******"

// secretKeys 是需要脱敏的渠道配置键（pay 渠道凭证为渠道各自的多态结构，
// 键名不固定；沿用 message 先例的密钥键 + 渠道常见密钥命名，大小写敏感）。
var secretKeys = []string{"password", "secret", "secretKey", "apiKey", "appSecret", "privateKey", "accessKeySecret"}

// parseConfigObject 把渠道配置解析为 JSON 对象；空串视为 {}。
// null、数组、标量等非对象形态一律拒绝（config 必须是渠道配置对象）。
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

// maskConfig 返回脱敏后的渠道配置：密钥键的非空字符串值替换为 ******。
// 存储内容异常（非 JSON 对象）时返回 {}，宁可丢失展示也不回显可能的密钥。
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
