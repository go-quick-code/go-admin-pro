package seed

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

// testDemoOrder 是撞序号单测专用的 order 值：避开 DemoOrder 常量段（10–50），
// 与 demo 文件 init 的真实注册互不干扰。
const testDemoOrder = 987654

func testDemoSeedA(*gorm.DB) error { return nil }

func testDemoSeedB(*gorm.DB) error { return nil }

// TestRegisterDemoDuplicateOrderPanics 撞序号是复制粘贴接线 bug，不是可替换
// seam（bpm/pay registry 的重复注册覆盖是测试替身故意替换的语义，与之无关，
// ADR 0024）——RegisterDemo 必须 fail-closed 立即 panic，消息点名两个撞号方
// 并含 order 值，让接线错误在每个 import seed 的构建（含 make check）里立即
// 爆炸，而不是静默吞掉一个模块的演示数据。用测试专用 order 注册两次触发，
// 测完恢复列表避免污染其他测试。
func TestRegisterDemoDuplicateOrderPanics(t *testing.T) {
	saved := demoSeeds
	t.Cleanup(func() { demoSeeds = saved })

	RegisterDemo(testDemoOrder, testDemoSeedA)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("重复 order 注册未 panic：撞序号被静默吞掉")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic 值不是 string：%v", r)
		}
		if !strings.Contains(msg, "testDemoSeedA") || !strings.Contains(msg, "testDemoSeedB") {
			t.Fatalf("panic 消息未点名两个撞号方：%q", msg)
		}
		if !strings.Contains(msg, "987654") {
			t.Fatalf("panic 消息未含 order 值：%q", msg)
		}
	}()
	RegisterDemo(testDemoOrder, testDemoSeedB)
}

// TestDemoOrderOAAfterCRM 顺序契约的最小结构化表达（spec #160）：OA 演示数据
// 依赖 CRM 演示组织（sales1/sales2/manager），必须排在 CRM 之后；其余演示
// 种子顺序无关。契约只写在 DemoOrder 常量注释里，本断言防未来重排。
func TestDemoOrderOAAfterCRM(t *testing.T) {
	if DemoOrderOA <= DemoOrderCRM {
		t.Fatalf("DemoOrderOA=%d 必须大于 DemoOrderCRM=%d（OA 依赖 CRM 演示组织）", DemoOrderOA, DemoOrderCRM)
	}
}
