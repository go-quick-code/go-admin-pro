package app

import (
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/seed"
)

// missingJobBindings 是装配 fail-closed 的纯函数检查（ADR 0024 补遗）：
// 绑定齐全返回空名单；缺一条时名单恰好点名缺失的处理器，不夹带其余项。
func TestMissingJobBindings(t *testing.T) {
	specs := []seed.JobSpec{
		{HandlerName: "handlerA", System: true},
		{HandlerName: "handlerB"},
		{HandlerName: "handlerC"},
	}
	// 绑定齐全：返回空名单。
	bound := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		bound[spec.HandlerName] = struct{}{}
	}
	if missing := missingJobBindings(specs, bound); len(missing) != 0 {
		t.Fatalf("绑定齐全应返回空名单，实际 %v", missing)
	}

	// 缺一条：报哪条——名单只含缺失的处理器（保持规格次序）。
	delete(bound, "handlerB")
	missing := missingJobBindings(specs, bound)
	if len(missing) != 1 || missing[0] != "handlerB" {
		t.Fatalf("缺 handlerB 绑定应报 [handlerB]，实际 %v", missing)
	}
}
