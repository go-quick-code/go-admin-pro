package service_test

import (
	"strings"
	"testing"

	"github.com/lautung/go-admin-pro/internal/module/crm/service"
)

// 产品枚举校验（本文件由 codegen 生成（issue #10 样板）的测试骨架，跑在 service 纯函数外部测试包）。
// 非法枚举 400 且文案说清字段；可见性等需要数据库的行为由集成测试矩阵覆盖。

func TestValidateProductStatus(t *testing.T) {
	for _, v := range []int8{1, 2} {
		if err := service.ValidateProductStatus(v); err != nil {
			t.Fatalf("状态 %d 应合法，实际报错 %v", v, err)
		}
	}
	for _, v := range []int8{0, 3} {
		err := service.ValidateProductStatus(v)
		if err == nil {
			t.Fatalf("状态 %d 应被拒绝", v)
		}
		if msg := err.Error(); !strings.Contains(msg, "产品状态") {
			t.Fatalf("错误文案应说明产品状态，实际 %q", msg)
		}
	}
}
