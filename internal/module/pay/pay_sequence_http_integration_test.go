//go:build integration

// pay_sequence 序列发放集成测试（issue #131，spec §4.9/§8.2）：并发发放
// 不重号、格式 ^前缀+8 位日期+至少 4 位序号$、租户隔离（按租户独立发放）、
// 四前缀口径。pay_sequence 尚无消费它的业务端点（订单/退款随 #133/#134
// 落地），本票经 service 公共 seam（App.Pay）直发，落真实 MySQL。
package pay_test

import (
	"context"
	"regexp"
	"sync"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/testutil"
)

var noPattern = regexp.MustCompile(`^(ZF|TK|ZZ|LS)\d{8}\d{4,}$`)

// nextNo 在指定租户上下文中发一个单号（goroutine 里不能触碰 testing.T，
// 错误带回主 goroutine 断言）。
func nextNo(svc *service.Service, tenantID int64, kind string) (string, error) {
	ctx := authn.With(context.Background(), authn.Principal{TenantID: tenantID, UserID: 1})
	switch kind {
	case "order":
		return svc.NextOrderNo(ctx)
	case "refund":
		return svc.NextRefundNo(ctx)
	case "transfer":
		return svc.NextTransferNo(ctx)
	default:
		return svc.NextWalletTxNo(ctx)
	}
}

// assertNo 断言单号格式与前缀 + 业务时区日期键。
func assertNo(t *testing.T, no, prefix string, env *testutil.IntegrationEnv) {
	t.Helper()
	if !noPattern.MatchString(no) {
		t.Fatalf("单号 %q 不匹配 ^(ZF|TK|ZZ|LS)\\d{8}\\d{4,}$", no)
	}
	want := prefix + env.DateKey()
	if len(no) < len(want) || no[:len(want)] != want {
		t.Fatalf("单号 %q 应以 %s（业务时区日期键）开头", no, want)
	}
}

// TestIntegrationPaySequenceConcurrent 并发发放：30 个请求同租户同日抢号，
// 单号全不重复且格式合规（事务内 INSERT ... ON DUPLICATE KEY UPDATE 后回读，
// 行锁串行化）。
func TestIntegrationPaySequenceConcurrent(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	svc := env.App.Pay

	const n = 30
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = nextNo(svc, 1, "order")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发发放 #%d: %v", i, err)
		}
	}
	seen := make(map[string]struct{}, n)
	for _, no := range results {
		assertNo(t, no, "ZF", env)
		if _, dup := seen[no]; dup {
			t.Fatalf("并发发放重复单号 %q", no)
		}
		seen[no] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("应发放 %d 个互异单号，实际 %d", n, len(seen))
	}
}

// TestIntegrationPaySequencePrefixAndTenant 四前缀口径 + 租户隔离：
// ZF/TK/ZZ/LS 各自成序列；新租户从 0001 起发（与租户 1 互不影响）。
func TestIntegrationPaySequencePrefixAndTenant(t *testing.T) {
	env := testutil.NewIntegrationEnv(t)
	svc := env.App.Pay

	// 租户 1 先发一个 ZF（后续并发测试另起环境，序列互不干扰）。
	zf, err := nextNo(svc, 1, "order")
	if err != nil {
		t.Fatalf("发放支付单号: %v", err)
	}
	assertNo(t, zf, "ZF", env)

	for _, tc := range []struct{ kind, prefix string }{
		{"refund", "TK"}, {"transfer", "ZZ"}, {"wallet", "LS"},
	} {
		no, err := nextNo(svc, 1, tc.kind)
		if err != nil {
			t.Fatalf("发放 %s 单号: %v", tc.kind, err)
		}
		assertNo(t, no, tc.prefix, env)
	}

	// 新租户独立发放：首个单号序号段应为 0001（该租户当日尚未发过号）。
	tenant2 := createTenantByAPI(t, env, env.Login(t, "admin", "123456"), "序列隔离租户")
	no2, err := nextNo(svc, tenant2, "order")
	if err != nil {
		t.Fatalf("租户 2 发放单号: %v", err)
	}
	want := "ZF" + env.DateKey() + "0001"
	if no2 != want {
		t.Fatalf("租户 2 首个单号应 %s（按租户独立发放），实际 %s", want, no2)
	}
}
