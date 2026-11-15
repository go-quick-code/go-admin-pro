//go:build integration

package testutil_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationModulePrefixPresent 是模块注册的防遗漏网（ADR 0024）：装配处
// 忘了把模块 append 进注册器列表不会编译报错——签名不强制注册，这里对全部
// 10 个模块的路由命名空间逐一断言引擎路由表 ≥1 条，漏接在 CI Integration job
// 红掉而不是静默 serving 零路由。本测试同时是 C7 重构（router 注册器化）的
// 零漂移网：三组结构与模块相对次序的意外变化会先在这里暴露。
func TestIntegrationModulePrefixPresent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	routes := env.App.Engine.Routes()
	apiPrefix := env.Config.App.APIPrefix
	// 前缀 = 各模块注册组在引擎路由表里的命名空间（APIPrefix 之下的首段）。
	// 九家直接以模块名开头；codegen 的后端路由挂在 /infra/codegen
	//（见 codegen.Handler.RegisterProtected——前端菜单的 /codegen 是前端
	// 路由，不在引擎路由表）。刻意用前缀匹配而不是 Contains：notify 的
	// /notify/message 组会让 Contains("/message") 被 notify 路由误满足，
	// message 模块漏接时这道网就是假的。
	prefixes := []string{
		apiPrefix + "/system",
		apiPrefix + "/infra",
		apiPrefix + "/infra/codegen",
		apiPrefix + "/crm",
		apiPrefix + "/oa",
		apiPrefix + "/bpm",
		apiPrefix + "/notify",
		apiPrefix + "/message",
		apiPrefix + "/erp",
		apiPrefix + "/pay",
	}
	for _, prefix := range prefixes {
		count := 0
		for _, route := range routes {
			if strings.HasPrefix(route.Path, prefix) {
				count++
			}
		}
		if count < 1 {
			t.Fatalf("引擎路由表缺少前缀 %s 的路由：对应模块漏接注册器（ADR 0024 前缀在场网）", prefix)
		}
	}
}
