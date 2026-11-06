// Package text 提供跨模块的文本处理小工具（issue #64 code-review P2：
// CRM 合同审批与 OA 请假/报销等处的 truncateRunes 同形实现去重）。
package text

// TruncateRunes 按字符截断到 max 个字符（列宽按字符计，避免切坏多字节
// 字符）。max <= 0 返回空串；不足 max 时原样返回。
func TruncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
