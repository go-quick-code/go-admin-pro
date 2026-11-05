package service_test

import (
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
)

// 客户编号规则（docs/crm-mvp-spec.md §3、issue #8）：
// KH + yyyyMMdd + 4 位序号，租户内唯一；序号由 crm_sequence 原子发放。
// 本文件只测不连库的格式化与校验逻辑；发放与唯一性由集成测试验证。

func TestFormatCustomerNo(t *testing.T) {
	cases := []struct {
		name    string
		dateKey string
		seq     int64
		want    string
	}{
		{name: "首号", dateKey: "20261105", seq: 1, want: "KH202611050001"},
		{name: "序号补零到 4 位", dateKey: "20261105", seq: 42, want: "KH202611050042"},
		{name: "序号 9999", dateKey: "20261231", seq: 9999, want: "KH202612319999"},
		// 超过 9999 时不截断：唯一性优先于固定位数，自然扩位。
		{name: "序号超过 9999 自然扩位", dateKey: "20261105", seq: 10000, want: "KH2026110510000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.FormatCustomerNo(tc.dateKey, tc.seq); got != tc.want {
				t.Fatalf("FormatCustomerNo(%q, %d) = %q，期望 %q", tc.dateKey, tc.seq, got, tc.want)
			}
		})
	}
}

func TestFormatCustomerNoShape(t *testing.T) {
	// 与规格的字面约束对照：前缀 KH、日期 8 位、序号至少 4 位。
	no := service.FormatCustomerNo("20260229", 7)
	if !strings.HasPrefix(no, "KH") {
		t.Fatalf("编号 %q 缺少 KH 前缀", no)
	}
	if !strings.HasPrefix(no[2:], "20260229") {
		t.Fatalf("编号 %q 前缀后应为 yyyyMMdd 日期键", no)
	}
	if seq := no[10:]; len(seq) < 4 || seq != "0007" {
		t.Fatalf("编号 %q 的序号段 %q 应为 4 位补零序号", no, seq)
	}
}

func TestDateKey(t *testing.T) {
	// 日期键按调用方 Location 解释（ADR 0006：无时区时间按调用方时区）。
	loc := time.FixedZone("UTC+8", 8*3600)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "北京时间上午", at: time.Date(2026, 11, 5, 8, 0, 0, 0, loc), want: "20261105"},
		{name: "零点即当天", at: time.Date(2026, 1, 1, 0, 0, 0, 0, loc), want: "20260101"},
		{name: "月份与日期补零", at: time.Date(2026, 2, 9, 23, 59, 59, 0, loc), want: "20260209"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.DateKey(tc.at); got != tc.want {
				t.Fatalf("DateKey = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestValidateCustomerStatus(t *testing.T) {
	for _, s := range []int8{1, 2, 3} {
		if err := service.ValidateCustomerStatus(s); err != nil {
			t.Fatalf("状态 %d 应合法，实际报错 %v", s, err)
		}
	}
	for _, s := range []int8{0, 4, -1} {
		err := service.ValidateCustomerStatus(s)
		if err == nil {
			t.Fatalf("状态 %d 应被拒绝", s)
		}
		if msg := err.Error(); !strings.Contains(msg, "客户状态") {
			t.Fatalf("错误文案应说明客户状态，实际 %q", msg)
		}
	}
}

func TestValidCustomerStatusModel(t *testing.T) {
	// 模型层枚举与 service 校验共用同一真值来源，防止两处漂移。
	if !model.ValidCustomerStatus(1) || !model.ValidCustomerStatus(2) || !model.ValidCustomerStatus(3) {
		t.Fatal("1 潜在 / 2 活跃 / 3 流失 应全部合法")
	}
	if model.ValidCustomerStatus(0) || model.ValidCustomerStatus(4) {
		t.Fatal("0 与 4 应非法")
	}
}
