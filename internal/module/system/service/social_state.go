package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// socialStateTTL 是社交授权 state 的有效期（spec §4.4.2：服务端保存 10 分钟）。
const socialStateTTL = 10 * time.Minute

// socialState 是一次社交授权流程的服务端一次性凭据：
//   - 登录模式：UserID == 0，回调按 (TenantID, Provider, OpenID) 查绑定并签发 JWT；
//   - 绑定模式：UserID > 0（spec §4.4.6：state 携带 user_id），回调把三方身份
//     绑定到该本地用户。
//
// 存储与推送票据（spec §4.5.2）同样只存进程内：单实例语义，多实例部署需要
// 外部存储，本轮不做（与 ADR 0011 一致，见回调端点注释）。
type socialState struct {
	Provider     string
	TenantID     int64
	UserID       int64
	CodeVerifier string
	ExpiresAt    time.Time
}

// socialStateStore 是 state 的服务端一次性存储：
// 签发 32 字节随机 state + PKCE code_verifier，消费时原子删除（一次性），
// 过期/重放/provider 不匹配一律 400（验收标准）。
type socialStateStore struct {
	mu     sync.Mutex
	ttl    time.Duration
	states map[string]socialState
}

func newSocialStateStore(ttl time.Duration) *socialStateStore {
	if ttl <= 0 {
		ttl = socialStateTTL
	}
	return &socialStateStore{ttl: ttl, states: make(map[string]socialState)}
}

// issue 生成并登记一条 state，返回 (state, code_verifier)。
func (s *socialStateStore) issue(provider string, tenantID, userID int64) (string, string, error) {
	state, err := randomBase64URL(32)
	if err != nil {
		return "", "", xerr.Internal(err)
	}
	verifier, err := randomBase64URL(32)
	if err != nil {
		return "", "", xerr.Internal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 顺带清扫过期项，防止长期运行下 map 无界增长（回调异常路径不保证消费）。
	now := time.Now()
	for k, v := range s.states {
		if now.After(v.ExpiresAt) {
			delete(s.states, k)
		}
	}
	s.states[state] = socialState{
		Provider:     provider,
		TenantID:     tenantID,
		UserID:       userID,
		CodeVerifier: verifier,
		ExpiresAt:    now.Add(s.ttl),
	}
	return state, verifier, nil
}

// consume 一次性消费 state：
//   - 不存在或已使用 → 400（不区分「无效」与「已使用」，避免泄露内部状态）；
//   - 已过期 → 400；
//   - provider 与签发时不一致 → 400（验收标准：provider 不匹配）。
//
// 消费（删除）先于 provider 匹配校验：拿到的 state 无论后续是否成功都作废。
func (s *socialStateStore) consume(state, provider string) (*socialState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.states[state]
	if !ok {
		return nil, xerr.BadRequest("state 无效或已使用")
	}
	delete(s.states, state)
	if time.Now().After(rec.ExpiresAt) {
		return nil, xerr.BadRequest("state 已过期，请重新发起登录")
	}
	if rec.Provider != provider {
		return nil, xerr.BadRequest("state 与社交平台不匹配")
	}
	return &rec, nil
}

// randomBase64URL 返回 n 字节随机数的 base64url 编码（无填充）。
func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// pkceCodeChallengeS256 计算 PKCE S256 挑战：BASE64URL(SHA256(verifier))。
// 算法对齐 RFC 7636 §4.2（Appendix B 向量见单测）。
func pkceCodeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
