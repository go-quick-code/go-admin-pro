package app

import (
	"context"
	"testing"

	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	"github.com/go-quick-code/go-admin-pro/internal/seed"
)

// missingJobBindings 是装配 fail-closed 的纯函数检查（ADR 0024 补遗）：
// 绑定齐全（键在且值非 nil）返回空名单；缺一条时名单恰好点名缺失的处理器，
// 不夹带其余项；键在但值为 nil 同样报缺失（nil 执行体 cron 首触发才 panic，
// 须在装配期拦截）。
func TestMissingJobBindings(t *testing.T) {
	specs := []seed.JobSpec{
		{HandlerName: "handlerA", System: true},
		{HandlerName: "handlerB"},
		{HandlerName: "handlerC"},
	}
	// 绑定齐全：返回空名单。
	bound := make(map[string]infraservice.HandlerFunc, len(specs))
	for _, spec := range specs {
		bound[spec.HandlerName] = func(ctx context.Context, param string) error { return nil }
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

	// 键在但值为 nil：同样报缺失（手写 X: nil 的绑定表条目不算绑定齐全）。
	bound["handlerB"] = nil
	missing = missingJobBindings(specs, bound)
	if len(missing) != 1 || missing[0] != "handlerB" {
		t.Fatalf("handlerB 绑定值为 nil 应报 [handlerB]，实际 %v", missing)
	}
}
