//go:build integration

package testutil_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

// protectedProbes 是组归属 401 探测的选点表（#163，PR #162 安全审查 P3-1）：
// 每个受保护命名空间取一条稳定代表端点。选点原则：401 在 Auth 中间件触发、
// 先于任何 handler——未认证请求必 401 且不触库，任意受保护端点都是合法探针；
// 统一选各模块最稳的 GET 分页/列表端点，不挑随业务演进的新增/详情端点。
// 刻意手写路径而不是从 engine.Routes() 动态挑每家第一条：system/notify/pay
// 的命名空间里混有公开组路由（登录/推送流/回调），动态挑点可能选中公开
// 路由造成假红；手写清单同时把「探针漂移」显式化——端点改名时存在性核对
// 先红，提示同步更新选点。与前缀在场测试（routes_test.go）正交：那张网兜
// 「模块整体漏接」（路由表 ≥1 条），本网兜「代表端点意外暴露到公开组」
// （无 token 必 401）——ADR 0024 的 RegisterPublic 是运行时 type assert 探测，
// 未来任何模块误加公开端点波及探针所在命名空间时在此红掉。与 ADR 0023
// 权限码扫描正交，比已拒绝的全量路由 golden 便宜得多。
var protectedProbes = []struct {
	namespace string
	method    string
	path      string
}{
	{"system", http.MethodGet, "/system/user/page"},
	{"infra", http.MethodGet, "/infra/file/page"},
	{"codegen", http.MethodGet, "/infra/codegen/tables"},
	{"crm", http.MethodGet, "/crm/customer/page"},
	{"oa", http.MethodGet, "/oa/attendance/result/page"},
	{"bpm", http.MethodGet, "/bpm/definition/page"},
	{"notify", http.MethodGet, "/notify/message/page"},
	{"message", http.MethodGet, "/message/channel/page"},
	{"erp", http.MethodGet, "/erp/product/page"},
	{"pay", http.MethodGet, "/pay/app/page"},
}

// TestIntegrationProtectedNamespaceRequiresAuth 对 10 个受保护命名空间逐一
// 断言：先核对探针端点仍在引擎路由表（漂移时给出可读的失败信息），再无
// token 请求，断言 HTTP 401 + 业务码 401（与 smoke_test 同口径）。
func TestIntegrationProtectedNamespaceRequiresAuth(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	routes := env.App.Engine.Routes()
	apiPrefix := env.Config.App.APIPrefix
	for _, probe := range protectedProbes {
		full := apiPrefix + probe.path
		if !routeRegistered(routes, probe.method, full) {
			t.Fatalf("探针端点漂移：%s %s 已不在引擎路由表——端点改名/迁移后请同步 protectedProbes 选点（#163）", probe.method, full)
		}
		status, api := env.Request(t, probe.method, probe.path, "", nil)
		if status != http.StatusUnauthorized || api.Code != 401 {
			t.Fatalf("无 token 访问 %s（%s 命名空间）：HTTP %d code %d msg %q，期望 HTTP 401 code 401——受保护端点不得落入公开组（#163）", full, probe.namespace, status, api.Code, api.Msg)
		}
	}
}

// TestIntegrationPushTicketStreamAuth 断言换票端点的组归属语义（#163）：
// POST /notify/push/ticket 挂换票流组（仅 Auth，RegisterStream 是 ADR 0024
// 的运行时 type assert 探测——接收者/命名漂移会静默丢失注册）。无 token 必
// 401：端点被挪进公开组或 RegisterStream 静默漏探测时红掉；带 token 非 401：
// 登录用户不得被 Auth 拒绝。换票全行为（票据发放/一次性消费/跨租户）由
// notify 包集成测试覆盖，本网只守组归属。
func TestIntegrationPushTicketStreamAuth(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)

	const ticketPath = "/notify/push/ticket"
	full := env.Config.App.APIPrefix + ticketPath
	if !routeRegistered(env.App.Engine.Routes(), http.MethodPost, full) {
		t.Fatalf("探针端点漂移：POST %s 已不在引擎路由表——RegisterStream 漏探测或端点迁移后请同步本测试（#163）", full)
	}

	status, api := env.Request(t, http.MethodPost, ticketPath, "", nil)
	if status != http.StatusUnauthorized || api.Code != 401 {
		t.Fatalf("无 token 换票：HTTP %d code %d msg %q，期望 HTTP 401 code 401——换票流组必须挂 Auth（#163）", status, api.Code, api.Msg)
	}

	token := env.Login(t, "admin", "123456")
	status, api = env.Request(t, http.MethodPost, ticketPath, token, nil)
	if status == http.StatusUnauthorized {
		t.Fatalf("带 token 换票：HTTP %d code %d msg %q，登录用户不得被 Auth 拒绝（#163）", status, api.Code, api.Msg)
	}
}

// routeRegistered 判定 method+path 是否已注册进引擎路由表。
func routeRegistered(routes gin.RoutesInfo, method, fullPath string) bool {
	for _, r := range routes {
		if r.Method == method && r.Path == fullPath {
			return true
		}
	}
	return false
}
