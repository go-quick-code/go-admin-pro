package service_test

import (
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// 联系人字段规则（issue #9，docs/crm-mvp-spec.md §3 联系人节）：
// 手机/邮箱可留空，填了就必须是合法格式（非法值 400，中文错误说清期望格式）；
// 性别对齐字典 sys_user_sex：0 未知 / 1 男 / 2 女。本文件只测不连库的校验逻辑。

func TestValidateContactMobile(t *testing.T) {
	valid := []string{"", "13800000001", "19912345678", "15912345678"}
	for _, m := range valid {
		if err := service.ValidateContactMobile(m); err != nil {
			t.Fatalf("手机号 %q 应合法，实际报错 %v", m, err)
		}
	}

	invalid := []string{
		"12345",                 // 太短且非 1 开头号段
		"12800000001",           // 12x 不是有效号段
		"1380000000",            // 10 位
		"138000000012",          // 12 位
		"138-0000-0001",         // 含分隔符
		"abcdefghijk",           // 非数字
		"138 0000 0001",         // 含空格
		strings.Repeat("1", 33), // 超出存储长度
	}
	for _, m := range invalid {
		err := service.ValidateContactMobile(m)
		if err == nil {
			t.Fatalf("手机号 %q 应被拒绝", m)
		}
		if msg := err.Error(); !strings.Contains(msg, "手机号") {
			t.Fatalf("错误文案应说明是手机号问题，实际 %q", msg)
		}
	}
}

func TestValidateContactEmail(t *testing.T) {
	valid := []string{"", "a@example.com", "user.name@example.cn", "user+tag@sub.example.org", "a_b-c@d.io"}
	for _, e := range valid {
		if err := service.ValidateContactEmail(e); err != nil {
			t.Fatalf("邮箱 %q 应合法，实际报错 %v", e, err)
		}
	}

	invalid := []string{
		"a@b",            // 缺少顶级域
		"@b.com",         // 缺少本地部分
		"a@",             // 缺少域名
		"a b@c.com",      // 含空格
		"a@@b.com",       // 双 @
		"a@b..com",       // 域名连续点
		"plain",          // 完全没有 @
		"你好@example.com", // 非 ASCII 字符
	}
	for _, e := range invalid {
		err := service.ValidateContactEmail(e)
		if err == nil {
			t.Fatalf("邮箱 %q 应被拒绝", e)
		}
		if msg := err.Error(); !strings.Contains(msg, "邮箱") {
			t.Fatalf("错误文案应说明是邮箱问题，实际 %q", msg)
		}
	}
}

func TestValidateContactGender(t *testing.T) {
	for _, g := range []int8{0, 1, 2} {
		if err := service.ValidateContactGender(g); err != nil {
			t.Fatalf("性别 %d 应合法，实际报错 %v", g, err)
		}
	}
	for _, g := range []int8{3, -1} {
		err := service.ValidateContactGender(g)
		if err == nil {
			t.Fatalf("性别 %d 应被拒绝", g)
		}
		if msg := err.Error(); !strings.Contains(msg, "性别") {
			t.Fatalf("错误文案应说明是性别问题，实际 %q", msg)
		}
	}
}

func TestValidContactGenderModel(t *testing.T) {
	// 模型层枚举与 service 校验共用同一真值来源，防止两处漂移。
	if !model.ValidContactGender(0) || !model.ValidContactGender(1) || !model.ValidContactGender(2) {
		t.Fatal("0 未知 / 1 男 / 2 女 应全部合法")
	}
	if model.ValidContactGender(3) || model.ValidContactGender(-1) {
		t.Fatal("3 与 -1 应非法")
	}
}
