//go:build integration

package testutil_test

import (
	"strings"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// TestIntegrationModulePrefixPresent 是模块注册的防漏接网（ADR 0024）：装配处
// 忘了把模块 append 进注册器列表不会编译报错——签名不强制注册，这里对全部
// 10 个模块的路由命名空间逐一断言引擎路由表 ≥1 条，漏接在 CI Integration job
// 红掉而不是静默 serving 零路由。口径只到前缀在场：端点在受保护/公开组之间
// 迁移（路由仍在表中）与模块相对次序重排都不会让本测试红——组结构由
// per-module 集成测试与 ERP 路由树 golden 兜底，组归属 401 探测网另立
// follow-up #163。作为 C7 重构（router 注册器化）的零漂移网，它兜的也只是
// 「模块从路由表整体消失」这一类漂移，不兜组结构漂移。
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
