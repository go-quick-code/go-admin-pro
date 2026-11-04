package utils

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"strings"
	"unicode"
)

// RandomString 生成指定长度的随机小写字母数字串。
func RandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		b[i] = letters[idx.Int64()]
	}
	return string(b)
}

// UUID 生成 32 位无连字符随机 ID。
func UUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ContainsInt64 判断切片是否包含目标值。
func ContainsInt64(list []int64, target int64) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

// UniqueInt64 去重。
func UniqueInt64(list []int64) []int64 {
	seen := make(map[int64]struct{}, len(list))
	out := make([]int64, 0, len(list))
	for _, v := range list {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// CamelCase 把下划线/中划线/空格分隔的字符串转成大驼峰。
func CamelCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == ' ' || r == '.'
	})
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}

// LowerCamelCase 转小驼峰。
func LowerCamelCase(s string) string {
	c := CamelCase(s)
	if c == "" {
		return c
	}
	r := []rune(c)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// KebabCase 把驼峰转成中划线风格。
func KebabCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return strings.ReplaceAll(b.String(), "_", "-")
}
