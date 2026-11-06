package ratelimit_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/ratelimit"
)

// TestLimiterAllowsUpToLimit 窗口内前 limit 次放行，之后拒绝。
func TestLimiterAllowsUpToLimit(t *testing.T) {
	l := ratelimit.New(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("窗口内第 %d 次应放行", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("超限请求应被拒绝")
	}
}

// TestLimiterKeysIndependent key 之间互不影响（按来源 IP/用户限速的前提）。
func TestLimiterKeysIndependent(t *testing.T) {
	l := ratelimit.New(1, time.Minute)
	if !l.Allow("a") || l.Allow("a") {
		t.Fatal("key a 第一次应放行、第二次应拒绝")
	}
	if !l.Allow("b") {
		t.Fatal("key b 不应受 key a 计数影响")
	}
}

// TestLimiterWindowReset 窗口翻新后计数归零：用短窗口 + 睡眠验证
// （不注入时钟，按外部包口径只走导出 API）。
func TestLimiterWindowReset(t *testing.T) {
	l := ratelimit.New(1, 40*time.Millisecond)
	if !l.Allow("k") || l.Allow("k") {
		t.Fatal("窗口内第一次应放行、第二次应拒绝")
	}
	time.Sleep(60 * time.Millisecond) // 越过窗口
	if !l.Allow("k") {
		t.Fatal("窗口翻新后应重新放行")
	}
}

// TestLimiterUnconfigured 全放行退化：limit/window 非正未配置限速是合法状态。
func TestLimiterUnconfigured(t *testing.T) {
	for _, l := range []*ratelimit.Limiter{
		ratelimit.New(0, time.Minute),
		ratelimit.New(-1, time.Minute),
		ratelimit.New(10, 0),
	} {
		for i := 0; i < 100; i++ {
			if !l.Allow("k") {
				t.Fatal("未配置限速应全放行")
			}
		}
	}
}

// TestLimiterConcurrent 并发安全：-race 下多 goroutine 对同一 key 计数，
// 放行总数恰好等于 limit（不超卖）。
func TestLimiterConcurrent(t *testing.T) {
	l := ratelimit.New(16, time.Minute)
	var wg sync.WaitGroup
	var allowed atomic.Int64
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("k") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 16 {
		t.Fatalf("并发下放行 %d 次，期望恰好 16", got)
	}
}
