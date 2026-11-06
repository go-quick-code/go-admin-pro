package push

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// 票据校验失败哨兵（HTTP 侧一律映射 401，不泄露具体原因——spec §4.11.3）。
var (
	// ErrTicketInvalid 票据不存在或格式非法。
	ErrTicketInvalid = errors.New("推送票据无效")
	// ErrTicketUsed 票据已被消费（一次性，spec §4.5.2）。
	ErrTicketUsed = errors.New("推送票据已使用")
	// ErrTicketExpired 票据已过 TTL。
	ErrTicketExpired = errors.New("推送票据已过期")
	// ErrTicketBinding 票据与请求身份（tenant/user）不匹配。
	ErrTicketBinding = errors.New("推送票据与当前身份不匹配")
)

// ticketBytes 是票据随机值的字节数（spec §4.5.2：32 字节）。
const ticketBytes = 32

// TicketIdentity 是票据绑定的身份。
type TicketIdentity struct {
	TenantID int64
	UserID   int64
}

type ticketEntry struct {
	identity  TicketIdentity
	expiresAt time.Time
	// consumed 标记票据已被消费：条目保留到 expiresAt 作为墓碑，
	// 使重放（一次性校验，spec §4.5.2）能精确报 ErrTicketUsed 而非 Invalid。
	consumed bool
}

// TicketManager 管理一次性推送票据：进程内存储（spec §4.5.2，单实例限制见
// ADR 0013），TTL 默认 30 秒，绑定签发时的 (tenant, user)，一次性消费。
//
// 浏览器 EventSource/WebSocket 不能带 Authorization 头，先用 Bearer 换票据、
// 再凭票据建立流，避免 JWT 进入 URL/日志（ADR 0013）。
type TicketManager struct {
	mu      sync.Mutex
	ttl     time.Duration
	tickets map[string]ticketEntry
	// now 可注入时钟（测试用）；生产为 time.Now。
	now func() time.Time
}

// NewTicketManager 创建票据管理器。ttl <= 0 时取 30 秒。
func NewTicketManager(ttl time.Duration) *TicketManager {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &TicketManager{
		ttl:     ttl,
		tickets: make(map[string]ticketEntry),
		now:     time.Now,
	}
}

// Issue 为 (tenantID, userID) 签发一张一次性票据，返回票据与其有效期。
// 顺带惰性清除已过期票据（含墓碑），进程内 map 不无界增长。
func (m *TicketManager) Issue(tenantID, userID int64) (string, time.Duration) {
	buf := make([]byte, ticketBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败是系统级故障：直接 panic（票据不可伪造是安全前提）。
		panic("push: 生成票据随机值失败: " + err.Error())
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.purgeLocked(now)
	m.tickets[token] = ticketEntry{
		identity:  TicketIdentity{TenantID: tenantID, UserID: userID},
		expiresAt: now.Add(m.ttl),
	}
	return token, m.ttl
}

// Consume 消费一张票据：一次性（无论后续校验是否通过都不可重放）、校验 TTL
// 与身份绑定。want* 传 0 表示调用方无第二身份可比对（浏览器 EventSource
// 场景）；非 0 时（如带 Bearer 的 HTTP 客户端）必须与绑定一致，否则拒绝
// ——跨租户/跨用户使用票据一律 401。
func (m *TicketManager) Consume(token string, wantTenantID, wantUserID int64) (*TicketIdentity, error) {
	if token == "" {
		return nil, ErrTicketInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.tickets[token]
	if !ok {
		return nil, ErrTicketInvalid
	}
	if entry.consumed {
		return nil, ErrTicketUsed
	}
	now := m.now()
	if now.After(entry.expiresAt) {
		delete(m.tickets, token)
		return nil, ErrTicketExpired
	}
	// 先标记消费再校验绑定：票据已出示即作废（泄露即失效）。
	entry.consumed = true
	m.tickets[token] = entry

	if wantTenantID != 0 && wantTenantID != entry.identity.TenantID ||
		wantUserID != 0 && wantUserID != entry.identity.UserID {
		return nil, ErrTicketBinding
	}
	id := entry.identity
	return &id, nil
}

// purgeLocked 清除已过期票据（含已消费墓碑），调用方持锁。O(n) 扫描在
// 票据签发频率与 TTL（30s）量级下开销可忽略，不引入后台 goroutine。
func (m *TicketManager) purgeLocked(now time.Time) {
	for token, entry := range m.tickets {
		if now.After(entry.expiresAt) {
			delete(m.tickets, token)
		}
	}
}
