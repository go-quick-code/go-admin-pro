package push

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

// 票据签发：32 字节随机值、base64url 无填充（43 字符）、TTL 原样返回、
// 绑定签发时的 tenant/user。
func TestTicketIssueAndConsume(t *testing.T) {
	m := NewTicketManager(30 * time.Second)
	token, expiresIn := m.Issue(1, 7)
	if expiresIn != 30*time.Second {
		t.Fatalf("expiresIn = %v, want 30s", expiresIn)
	}
	if len(token) != 43 {
		t.Fatalf("token 长度 = %d, want 43（32 字节 base64url 无填充）", len(token))
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		t.Fatalf("token 不是合法 base64url: %v", err)
	}
	id, err := m.Consume(token, 1, 7)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if id.TenantID != 1 || id.UserID != 7 {
		t.Fatalf("identity = (%d,%d), want (1,7)", id.TenantID, id.UserID)
	}
}

// 一次性：第二次消费报 ErrTicketUsed（无论首次是否成功）。
func TestTicketOneTime(t *testing.T) {
	m := NewTicketManager(30 * time.Second)
	token, _ := m.Issue(1, 7)
	if _, err := m.Consume(token, 1, 7); err != nil {
		t.Fatalf("首次 Consume: %v", err)
	}
	if _, err := m.Consume(token, 1, 7); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("重放 err = %v, want ErrTicketUsed", err)
	}
}

// 过期：超过 TTL 后消费报 ErrTicketExpired。
func TestTicketExpired(t *testing.T) {
	m := NewTicketManager(30 * time.Second)
	now := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return now }
	token, _ := m.Issue(1, 7)

	now = now.Add(31 * time.Second)
	if _, err := m.Consume(token, 1, 7); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("过期 err = %v, want ErrTicketExpired", err)
	}
}

// 过期票据在 TTL 边界内仍然有效。
func TestTicketWithinTTL(t *testing.T) {
	m := NewTicketManager(30 * time.Second)
	now := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return now }
	token, _ := m.Issue(1, 7)
	now = now.Add(29 * time.Second)
	if _, err := m.Consume(token, 1, 7); err != nil {
		t.Fatalf("TTL 边界内 Consume: %v", err)
	}
}

// 绑定校验：跨租户/跨用户使用一律拒绝（消费仍然生效——票据已泄露即作废）。
func TestTicketBindingMismatch(t *testing.T) {
	m := NewTicketManager(30 * time.Second)
	token, _ := m.Issue(1, 7)

	if _, err := m.Consume(token, 2, 7); !errors.Is(err, ErrTicketBinding) {
		t.Fatalf("跨租户 err = %v, want ErrTicketBinding", err)
	}
	// 校验失败的票据同样被消费（一次性）：再用正确身份也拿不回来。
	if _, err := m.Consume(token, 1, 7); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("绑定失败后重放 err = %v, want ErrTicketUsed", err)
	}
}

// 未知/畸形票据。
func TestTicketUnknown(t *testing.T) {
	m := NewTicketManager(30 * time.Second)
	if _, err := m.Consume("no-such-ticket", 1, 7); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("未知票据 err = %v, want ErrTicketInvalid", err)
	}
}

// 惰性清理：签发时顺带清除已过期票据，进程内 map 不无界增长。
func TestTicketLazyPurgeOnIssue(t *testing.T) {
	m := NewTicketManager(30 * time.Second)
	now := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return now }
	m.Issue(1, 7)
	m.Issue(1, 8)
	if got := len(m.tickets); got != 2 {
		t.Fatalf("清理前票据数 = %d, want 2", got)
	}
	now = now.Add(31 * time.Second)
	m.Issue(2, 9)
	if got := len(m.tickets); got != 1 {
		t.Fatalf("清理后票据数 = %d, want 1", got)
	}
}
