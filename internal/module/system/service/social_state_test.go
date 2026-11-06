package service

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// wantStateBadRequest 断言 state 流转错误是 HTTP 400 的业务错误。
func wantStateBadRequest(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望失败，实际成功", what)
	}
	e, ok := xerr.As(err)
	if !ok {
		t.Fatalf("%s: 期望业务错误，实际 %v", what, err)
	}
	if e.Status != http.StatusBadRequest {
		t.Fatalf("%s: 期望 HTTP 400，实际 %d（%s）", what, e.Status, e.Msg)
	}
}

// TestPKCECodeChallengeS256 用 RFC 7636 Appendix B 的官方测试向量锁定
// PKCE S256 挑战算法（spec §4.4.2：state + PKCE）。
func TestPKCECodeChallengeS256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := pkceCodeChallengeS256(verifier); got != want {
		t.Fatalf("code_challenge 不符 RFC 7636 向量：got %q want %q", got, want)
	}
}

// TestSocialStateIssueConsume 正常签发与消费：登录模式（userID=0）。
func TestSocialStateIssueConsume(t *testing.T) {
	store := newSocialStateStore(time.Minute)
	state, verifier, err := store.issue("mock", 1, 0)
	if err != nil {
		t.Fatalf("签发 state: %v", err)
	}
	if state == "" || verifier == "" {
		t.Fatalf("state/verifier 不能为空：state=%q verifier=%q", state, verifier)
	}
	// PKCE verifier 规范要求 43–128 字符的 base64url（RFC 7636 §4.1）。
	if len(verifier) != 43 || strings.ContainsAny(verifier, "+/=") {
		t.Fatalf("verifier 应为 43 字符 base64url：%q（len=%d）", verifier, len(verifier))
	}
	if state != strings.TrimSpace(state) || strings.ContainsAny(state, "+/=") {
		t.Fatalf("state 应为 base64url：%q", state)
	}

	rec, err := store.consume(state, "mock")
	if err != nil {
		t.Fatalf("消费 state: %v", err)
	}
	if rec.Provider != "mock" || rec.TenantID != 1 || rec.UserID != 0 {
		t.Fatalf("state 记录不符：provider=%s tenant=%d user=%d", rec.Provider, rec.TenantID, rec.UserID)
	}
	if rec.CodeVerifier != verifier {
		t.Fatalf("state 应携带 PKCE verifier：got %q want %q", rec.CodeVerifier, verifier)
	}
}

// TestSocialStateBindModeCarriesUser 绑定模式的 state 携带 user_id（spec §4.4.6）。
func TestSocialStateBindModeCarriesUser(t *testing.T) {
	store := newSocialStateStore(time.Minute)
	state, _, err := store.issue("mock", 3, 42)
	if err != nil {
		t.Fatalf("签发 state: %v", err)
	}
	rec, err := store.consume(state, "mock")
	if err != nil {
		t.Fatalf("消费 state: %v", err)
	}
	if rec.UserID != 42 || rec.TenantID != 3 {
		t.Fatalf("绑定模式 state 应携带 user_id=42 tenant=3：实际 user=%d tenant=%d", rec.UserID, rec.TenantID)
	}
}

// TestSocialStateConsumeIsOneTime 一次性消费：重放返回 400（验收标准）。
func TestSocialStateConsumeIsOneTime(t *testing.T) {
	store := newSocialStateStore(time.Minute)
	state, _, err := store.issue("mock", 1, 0)
	if err != nil {
		t.Fatalf("签发 state: %v", err)
	}
	if _, err := store.consume(state, "mock"); err != nil {
		t.Fatalf("首次消费应成功: %v", err)
	}
	wantStateBadRequest(t, "重放 state", func() error {
		_, err := store.consume(state, "mock")
		return err
	}())
}

// TestSocialStateProviderMismatch provider 与签发时不匹配 → 400（验收标准）。
func TestSocialStateProviderMismatch(t *testing.T) {
	store := newSocialStateStore(time.Minute)
	state, _, err := store.issue("mock", 1, 0)
	if err != nil {
		t.Fatalf("签发 state: %v", err)
	}
	wantStateBadRequest(t, "provider 不匹配", func() error {
		_, err := store.consume(state, "other")
		return err
	}())
}

// TestSocialStateExpired 过期 → 400（spec §4.4.2：10 分钟 TTL；测试用短 TTL）。
func TestSocialStateExpired(t *testing.T) {
	store := newSocialStateStore(5 * time.Millisecond)
	state, _, err := store.issue("mock", 1, 0)
	if err != nil {
		t.Fatalf("签发 state: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	wantStateBadRequest(t, "过期 state", func() error {
		_, err := store.consume(state, "mock")
		return err
	}())
}

// TestSocialStateUnknown 未知/伪造 state → 400。
func TestSocialStateUnknown(t *testing.T) {
	store := newSocialStateStore(time.Minute)
	wantStateBadRequest(t, "未知 state", func() error {
		_, err := store.consume("not-a-state", "mock")
		return err
	}())
}

// TestSocialStateCapacityEvictsOldest 容量护栏（#85 安全审计 NV1）：
// 签发达到容量上限后清最旧的未消费 state，最新 state 不受影响，
// 存储条目数不超上限（进程内内存有界）。
func TestSocialStateCapacityEvictsOldest(t *testing.T) {
	store := newSocialStateStore(time.Minute)
	store.capacity = 3

	first, _, err := store.issue("mock", 1, 0)
	if err != nil {
		t.Fatalf("签发第 1 条: %v", err)
	}
	if _, _, err := store.issue("mock", 1, 0); err != nil {
		t.Fatalf("签发第 2 条: %v", err)
	}
	if _, _, err := store.issue("mock", 1, 0); err != nil {
		t.Fatalf("签发第 3 条: %v", err)
	}

	// 第 4 条挤掉最旧的第 1 条。
	fourth, _, err := store.issue("mock", 1, 0)
	if err != nil {
		t.Fatalf("达上限后签发: %v", err)
	}
	if len(store.states) != 3 {
		t.Fatalf("容量护栏后条目数 = %d, want 3", len(store.states))
	}
	wantStateBadRequest(t, "最旧 state 被清", func() error {
		_, err := store.consume(first, "mock")
		return err
	}())
	if _, err := store.consume(fourth, "mock"); err != nil {
		t.Fatalf("最新 state 应可用: %v", err)
	}

	// 未配置容量（<=0）不淘汰：条目继续增长（与旧行为兼容）。
	store2 := newSocialStateStore(time.Minute)
	store2.capacity = 0
	for i := 0; i < 5; i++ {
		if _, _, err := store2.issue("mock", 1, 0); err != nil {
			t.Fatalf("未配置容量签发: %v", err)
		}
	}
	if len(store2.states) != 5 {
		t.Fatalf("未配置容量不应淘汰：条目数 = %d, want 5", len(store2.states))
	}
}
