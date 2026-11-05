package service_test

import (
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
)

// TestDateParsersUseBusinessLocation 日期解析必须用调用方传入的业务时区
// （ADR 0006），而不是隐式的 time.Local：用固定非本地时区解析，结果的
// Location 与墙上时间都应保持业务时区口径。
func TestDateParsersUseBusinessLocation(t *testing.T) {
	loc := time.FixedZone("UTC+05:30", 5*3600+30*60)
	if loc == time.Local {
		t.Skip("测试时区与本地时区相同，无法区分解析口径")
	}

	cases := []struct {
		name  string
		input string
		parse func(string, *time.Location) (*time.Time, error)
	}{
		{"合同日期", "2026-01-02 03:04:05", service.ParseContractDate},
		{"商机预计成交日期", "2026-01-02", service.ParseExpectedCloseDate},
		{"回款到期日", "2026-01-02 15:04:05", func(v string, l *time.Location) (*time.Time, error) {
			return service.ParseReceivableDate("到期日", v, l)
		}},
		{"跟进下次时间", "2026-01-02 15:04:05", service.ValidateFollowUpNextTime},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.parse(tc.input, loc)
			if err != nil || got == nil {
				t.Fatalf("%q 应可解析，实际 %v %v", tc.input, got, err)
			}
			if got.Location() != loc {
				t.Fatalf("解析结果 Location = %v，期望传入的业务时区 %v", got.Location(), loc)
			}
			if want := tc.input[:10]; got.Format("2006-01-02") != want {
				t.Fatalf("墙上日期 = %s，期望 %s（不得随时区偏移漂移）", got.Format("2006-01-02"), want)
			}
		})
	}
}

// TestDateParsersRejectInvalidInputWithLocation 非法输入仍返回 400，
// 与业务时区无关（回归：签名改造不能吞掉格式校验）。
func TestDateParsersRejectInvalidInputWithLocation(t *testing.T) {
	loc := time.FixedZone("UTC-03:00", -3*3600)
	if _, err := service.ParseContractDate("明年", loc); err == nil {
		t.Fatal("合同日期非法格式应返回错误")
	}
	if _, err := service.ParseExpectedCloseDate("2026/01/02", loc); err == nil {
		t.Fatal("预计成交日期非法格式应返回错误")
	}
	if _, err := service.ParseReceivableDate("到期日", "20260102", loc); err == nil {
		t.Fatal("回款日期非法格式应返回错误")
	}
	if _, err := service.ValidateFollowUpNextTime("明天", loc); err == nil {
		t.Fatal("下次跟进时间非法格式应返回错误")
	}
}
