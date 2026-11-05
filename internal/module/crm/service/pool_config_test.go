package service_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/service"
)

// 公海租户配置纯函数（issue #14，docs/crm-mvp-spec.md §4.2）：
// 本文件只测不连库的默认值与取值边界；配置读写的全链路（含权限码、租户隔离）
// 由集成测试验证。

// TestDefaultPoolConfigValues 默认值锁定（规格 §4.2：回收天数缺省 30、
// 认领上限缺省 50）：配置行缺失（新建租户尚未配置）时按默认值执行。
func TestDefaultPoolConfigValues(t *testing.T) {
	def := service.DefaultPoolConfigValues()
	if def.RecycleDays != 30 {
		t.Fatalf("回收天数默认应为 30，实际 %d", def.RecycleDays)
	}
	if def.ClaimLimit != 50 {
		t.Fatalf("认领上限默认应为 50，实际 %d", def.ClaimLimit)
	}
}

// TestValidatePoolConfig 配置取值边界：合法值放行；回收天数/认领上限
// 越界（0、负数、超上限）拒绝，错误文案中文且指明字段。
func TestValidatePoolConfig(t *testing.T) {
	if err := service.ValidatePoolConfig(30, 50); err != nil {
		t.Fatalf("合法默认值不应报错: %v", err)
	}
	if err := service.ValidatePoolConfig(1, 1); err != nil {
		t.Fatalf("最小合法值不应报错: %v", err)
	}
	if err := service.ValidatePoolConfig(3650, 100000); err != nil {
		t.Fatalf("最大合法值不应报错: %v", err)
	}
	for _, tc := range []struct {
		name        string
		recycleDays int
		claimLimit  int
		wantField   string
	}{
		{"回收天数为 0", 0, 50, "回收天数"},
		{"回收天数为负", -1, 50, "回收天数"},
		{"回收天数超上限", 3651, 50, "回收天数"},
		{"认领上限为 0", 30, 0, "认领上限"},
		{"认领上限为负", 30, -5, "认领上限"},
		{"认领上限超上限", 30, 100000 + 1, "认领上限"},
	} {
		err := service.ValidatePoolConfig(tc.recycleDays, tc.claimLimit)
		if err == nil {
			t.Fatalf("%s 应被拒绝", tc.name)
		}
		if msg := err.Error(); !strings.Contains(msg, tc.wantField) {
			t.Fatalf("%s 的错误文案应含 %q，实际 %q", tc.name, tc.wantField, msg)
		}
	}
}
