package service

import (
	"testing"
	"time"
)

// dateKey/formatNo 是单号发放的纯函数（spec §4.9）：前缀 + yyyyMMdd（业务时区）
// + 至少 4 位序号（超 9999 自然扩位）。表驱动锁定三件事：业务时区跨日、
// 4 位下限补零、超 9999 扩位不截断。
func TestDateKey(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区: %v", err)
	}
	utc := time.UTC
	// 2026-11-12 16:30:00 UTC = 2026-11-13 00:30:00 上海：同一时刻两个时区日期键不同。
	crossing := time.Date(2026, 11, 12, 16, 30, 0, 0, utc)

	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"上海时区日期", crossing.In(sh), "20261113"},
		{"UTC 时区同一时刻日期不同", crossing.In(utc), "20261112"},
		{"新年首日", time.Date(2027, 1, 1, 0, 0, 1, 0, sh), "20270101"},
		{"月末进位", time.Date(2026, 2, 28, 23, 59, 59, 0, sh), "20260228"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dateKey(tt.now); got != tt.want {
				t.Fatalf("dateKey(%s) = %q，期望 %q", tt.now, got, tt.want)
			}
		})
	}
}

func TestFormatNo(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		dateKey string
		seq     int64
		want    string
	}{
		{"序号 1 补足 4 位", "ZF", "20261112", 1, "ZF202611120001"},
		{"序号 9999 恰 4 位", "TK", "20261112", 9999, "TK202611129999"},
		{"序号 10000 自然扩位为 5 位", "ZZ", "20261112", 10000, "ZZ2026111210000"},
		{"钱包流水前缀", "LS", "20270101", 42, "LS202701010042"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatNo(tt.prefix, tt.dateKey, tt.seq); got != tt.want {
				t.Fatalf("formatNo(%q,%q,%d) = %q，期望 %q", tt.prefix, tt.dateKey, tt.seq, got, tt.want)
			}
		})
	}
}
