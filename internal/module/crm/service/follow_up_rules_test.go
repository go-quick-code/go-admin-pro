package service_test

import (
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// 跟进字段规则（issue #12，docs/crm-mvp-spec.md §3 客户节/§4.2）：
// 类型对齐芋道字典语义（1 电话 / 2 拜访 / 3 微信 / 4 邮件 / 5 其他），
// 可留空（服务层缺省「其他」），填了必须是合法枚举；
// 下次跟进时间可留空，填了必须是可解析的日期时间（非法值 400，中文错误说清格式）。
// 本文件只测不连库的校验逻辑。

func TestValidFollowUpType(t *testing.T) {
	for g := int8(1); g <= model.FollowUpTypeOther; g++ {
		if !model.ValidFollowUpType(g) {
			t.Fatalf("类型 %d 应合法（1–5）", g)
		}
	}
	for _, g := range []int8{0, 6, -1, 100} {
		if model.ValidFollowUpType(g) {
			t.Fatalf("类型 %d 不应合法（合法值 1–5）", g)
		}
	}
}

func TestValidateFollowUpType(t *testing.T) {
	// 未传（nil）：放行，由服务层缺省「其他」。
	if err := service.ValidateFollowUpType(nil); err != nil {
		t.Fatalf("类型未传应放行，实际报错 %v", err)
	}
	for _, v := range []int8{1, 2, 3, 4, 5} {
		g := v
		if err := service.ValidateFollowUpType(&g); err != nil {
			t.Fatalf("类型 %d 应合法，实际报错 %v", v, err)
		}
	}
	for _, v := range []int8{0, 6, -1} {
		g := v
		err := service.ValidateFollowUpType(&g)
		if err == nil {
			t.Fatalf("类型 %d 应返回 400 错误", v)
		}
		if err.Error() == "" {
			t.Fatalf("类型 %d 的错误信息应为中文可读文案", v)
		}
	}
}

func TestValidateFollowUpNextTime(t *testing.T) {
	// 留空放行（选填字段）；空白串按留空处理。
	for _, s := range []string{"", "   "} {
		next, err := service.ValidateFollowUpNextTime(s)
		if err != nil || next != nil {
			t.Fatalf("%q 应放行且返回 nil，实际 %v %v", s, next, err)
		}
	}

	// 合法格式：前端 value-format（YYYY-MM-DD HH:mm:ss）、纯日期与 RFC3339。
	for _, s := range []string{"2026-01-02 15:04:05", "2026-01-02", "2026-01-02T15:04:05+08:00"} {
		next, err := service.ValidateFollowUpNextTime(s)
		if err != nil {
			t.Fatalf("下次跟进时间 %q 应合法，实际报错 %v", s, err)
		}
		if next == nil {
			t.Fatalf("下次跟进时间 %q 应解析出时间", s)
		}
	}

	// 日期部分回读一致（本地时区）。
	next, err := service.ValidateFollowUpNextTime("2026-01-02 15:04:05")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := next.Format("2006-01-02 15:04:05"); got != "2026-01-02 15:04:05" {
		t.Fatalf("回读时间 %s，期望 2026-01-02 15:04:05", got)
	}

	// 非法格式：400，中文错误信息。
	for _, s := range []string{"明天", "2026/01/02", "2026-13-01 00:00:00", "20260102"} {
		_, err := service.ValidateFollowUpNextTime(s)
		if err == nil {
			t.Fatalf("下次跟进时间 %q 应返回 400 错误", s)
		}
	}

	// 错误信息里应带回原始输入，帮助用户定位。
	_, err = service.ValidateFollowUpNextTime("明天")
	if err != nil && !strings.Contains(err.Error(), "明天") {
		t.Fatalf("错误信息应包含原始输入 %q，实际 %q", "明天", err.Error())
	}
}
