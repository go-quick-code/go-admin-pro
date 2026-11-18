package service_test

import (
	"strings"
	"testing"

	mallservice "github.com/go-quick-code/go-admin-pro/internal/module/mall/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// member_rules_test.go 是会员/等级校验规则的纯函数表驱动测试（外部测试包，
// 照 crm contact_rules_test 形状；issue #174，docs/mall-mvp-spec.md §3.1/§4）。
// 唯一冲突（mobile/name/level 撞键 409）与租户隔离由 HTTP 集成测试覆盖，
// 这里只锁字段级校验语义：非法值一律 400，文案说清字段与期望格式。

// TestValidateMemberMobile 会员手机号校验表：必填 + 11 位大陆手机号。
func TestValidateMemberMobile(t *testing.T) {
	for _, mobile := range []string{"13800000001", "19999999999", "15912345678"} {
		if err := mallservice.ValidateMemberMobile(mobile); err != nil {
			t.Fatalf("合法手机号 %s 不应报错: %v", mobile, err)
		}
	}
	cases := []struct {
		name    string
		mobile  string
		wantMsg string
	}{
		{"空手机号", "  ", "不能为空"},
		{"缺数字", "1380000000", "格式不正确"},
		{"非 1 开头", "93800000001", "格式不正确"},
		{"第二位越界", "12800000001", "格式不正确"},
		{"含字母", "1380000000a", "格式不正确"},
		{"超长", "138000000011", "格式不正确"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mallservice.ValidateMemberMobile(tc.mobile)
			if err == nil {
				t.Fatalf("期望报错含 %q，实际 nil", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("报错 %q 应含 %q", err.Error(), tc.wantMsg)
			}
			e, ok := xerr.As(err)
			if !ok || e.Status != 400 {
				t.Fatalf("字段校验失败应为 400 业务错误，实际 %v", err)
			}
		})
	}
}

// TestValidateMemberPassword 会员密码校验表：选填（管理端可不设密码，C 端
// 注册自设），填了须 6–64 位；空串与纯空白放行（更新语义 = 不改密码）。
func TestValidateMemberPassword(t *testing.T) {
	for _, password := range []string{"", "   ", "123456", strings.Repeat("p", 64)} {
		if err := mallservice.ValidateMemberPassword(password); err != nil {
			t.Fatalf("合法密码 %q 不应报错: %v", password, err)
		}
	}
	cases := []struct {
		name     string
		password string
		wantMsg  string
	}{
		{"过短", "12345", "至少 6 位"},
		{"超长", strings.Repeat("p", 65), "64 位"},
		{"纯空白带内容边界", "  12  ", "至少 6 位"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mallservice.ValidateMemberPassword(tc.password)
			if err == nil {
				t.Fatalf("期望报错含 %q，实际 nil", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("报错 %q 应含 %q", err.Error(), tc.wantMsg)
			}
			e, ok := xerr.As(err)
			if !ok || e.Status != 400 {
				t.Fatalf("字段校验失败应为 400 业务错误，实际 %v", err)
			}
		})
	}
}

// TestValidateMemberStatus 会员状态枚举：0 启用 / 1 停用。
func TestValidateMemberStatus(t *testing.T) {
	for _, status := range []int8{0, 1} {
		if err := mallservice.ValidateMemberStatus(status); err != nil {
			t.Fatalf("合法状态 %d 不应报错: %v", status, err)
		}
	}
	err := mallservice.ValidateMemberStatus(2)
	if err == nil || !strings.Contains(err.Error(), "不合法") {
		t.Fatalf("状态 2 应报「不合法」，实际 %v", err)
	}
}

// TestValidateLevelName 等级名称校验表：必填 + ≤64 字。
func TestValidateLevelName(t *testing.T) {
	for _, name := range []string{"普通会员", strings.Repeat("级", 64)} {
		if err := mallservice.ValidateLevelName(name); err != nil {
			t.Fatalf("合法等级名 %q 不应报错: %v", name, err)
		}
	}
	cases := []struct {
		name    string
		level   string
		wantMsg string
	}{
		{"空名称", "  ", "不能为空"},
		{"超长", strings.Repeat("级", 65), "64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mallservice.ValidateLevelName(tc.level)
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("等级名 %q 期望报错含 %q，实际 %v", tc.level, tc.wantMsg, err)
			}
		})
	}
}

// TestValidateLevelValue 等级值校验：≥1 的整数（排序值，小值低级）。
func TestValidateLevelValue(t *testing.T) {
	for _, level := range []int{1, 10, 99} {
		if err := mallservice.ValidateLevelValue(level); err != nil {
			t.Fatalf("合法等级值 %d 不应报错: %v", level, err)
		}
	}
	if err := mallservice.ValidateLevelValue(0); err == nil || !strings.Contains(err.Error(), "等级值") {
		t.Fatalf("等级值 0 应报「等级值」，实际 %v", err)
	}
}

// TestValidateLevelExperience 经验阈值校验：≥0（纯维护列）。
func TestValidateLevelExperience(t *testing.T) {
	for _, exp := range []int{0, 100} {
		if err := mallservice.ValidateLevelExperience(exp); err != nil {
			t.Fatalf("合法经验阈值 %d 不应报错: %v", exp, err)
		}
	}
	if err := mallservice.ValidateLevelExperience(-1); err == nil || !strings.Contains(err.Error(), "经验阈值") {
		t.Fatalf("经验阈值 -1 应报「经验阈值」，实际 %v", err)
	}
}
