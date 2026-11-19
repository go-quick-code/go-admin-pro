package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// member_token.go 是 C 端会员令牌的签发与自校验（issue #178，T5；
// docs/mall-mvp-spec.md §2/§7 裁决 2）：
//
// 令牌形状（零新依赖，stdlib crypto/hmac + sha256 + base64 + encoding/json）：
//
//	token = base64url(json{mid:memberID, tid:tenantID, exp:unix秒})
//	        + "." + base64url(HMAC-SHA256(payload, mall.jwt-secret))
//
// 与 JWT 的 HS256 同构（payload.signature 两段式；刻意不用三段式 JWT 头：
// 头算法固定 HS256 无协商需求，两段式少一次 base64 解码与 alg 混淆面——
// JWT 历史上的 alg=none/RS-HS 混淆攻击即源于把 alg 交给令牌自述）。
//
// 校验口径（handler 内自校验，不动 router 三组结构——裁决 2）：
//   - 签名一致（hmac.Equal 恒时比较）+ 未过期 + 查 member_user
//    （tenant 一致 + status 启用）。停用会员的令牌在每次校验时经 DB 查询
//     失效——无撤销列表（改密不撤销在途令牌：MVP 无 token-version 列，
//     有效期 7 天兜底暴露面，状态停用覆盖主动封禁路径）。
//
// 密钥来自 config.Mall.JWTSecret（app.New 装配首检 fail-closed：为空拒启，
// 本文件再做防御——未注入密钥时签发/校验一律报错，不产出弱令牌）。
//
// 校验失败的应答统一「登录状态已失效，请重新登录」401：无令牌/伪造签名/
// 过期/停用/跨租户同答，不给攻击者区分存在性的探针（照登录失败统一文案
// 的不泄露口径）。

// MemberTokenTTLDefault 是会员令牌默认有效期：7 天（config mall.token-ttl
// 可覆盖，≤0 回落本值）。
const MemberTokenTTLDefault = 7 * 24 * time.Hour

// MemberTokenUnauthorizedMsg 是会员令牌校验失败的统一文案（401，不区分
// 失败原因——不泄露存在性）。api 层 memberAuth 缺令牌/防御路径复用同一
// 常量，保证「无令牌/伪造/过期/停用/跨租户」逐字同答。
const MemberTokenUnauthorizedMsg = "登录状态已失效，请重新登录"

// MemberClaims 是会员令牌 payload（JSON 字段名 mid/tid/exp 照规格 §2）。
type MemberClaims struct {
	MemberID  int64 `json:"mid"`
	TenantID  int64 `json:"tid"`
	ExpiresAt int64 `json:"exp"`
}

// SetMemberTokenAuth 注入会员令牌签名密钥与有效期（app 装配期一次调用，
// 形状照 paySvc.SetNotifyHMACKey）：密钥为空时照常注入——签发/校验在
// 首次使用时报错（app.New 已 fail-closed 拒启，这里只兜手工构造路径）。
func (s *Service) SetMemberTokenAuth(secret string, ttl time.Duration) {
	s.memberTokenSecret = []byte(strings.TrimSpace(secret))
	if ttl <= 0 {
		ttl = MemberTokenTTLDefault
	}
	s.memberTokenTTL = ttl
}

// memberTokenReady 报告签名材料是否就绪（防御路径：装配漏注入时报错而非
// 产出可预测令牌）。
func (s *Service) memberTokenReady() error {
	if len(s.memberTokenSecret) == 0 {
		return fmt.Errorf("会员令牌签名密钥未注入（mall.jwt_secret 为空）")
	}
	return nil
}

// IssueMemberToken 为会员签发 HMAC 令牌（登录/注册成功后调用）：
// payload = base64url(json{mid,tid,exp:now+ttl})，signature = base64url(
// HMAC-SHA256(payload, secret))。ttl 取装配注入值（缺省 7 天）。
func (s *Service) IssueMemberToken(row *model.MemberUser) (string, int64, error) {
	if err := s.memberTokenReady(); err != nil {
		return "", 0, err
	}
	ttl := s.memberTokenTTL
	if ttl <= 0 {
		ttl = MemberTokenTTLDefault
	}
	now := time.Now()
	claims := MemberClaims{
		MemberID:  row.ID,
		TenantID:  row.TenantID,
		ExpiresAt: now.Add(ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", 0, fmt.Errorf("序列化会员令牌 payload: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + s.signMemberPayload(encoded), int64(ttl / time.Second), nil
}

// signMemberPayload 计算 payload 段的 HMAC-SHA256 签名（base64url，无填充）。
func (s *Service) signMemberPayload(payload string) string {
	mac := hmac.New(sha256.New, s.memberTokenSecret)
	mac.Write([]byte(payload)) // hash.Write 不返回错误
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyMemberToken 校验令牌并回查会员行（tenant 一致 + status 启用）：
// 全部失败形态（无令牌/格式错/签名不符/过期/会员不存在/跨租户/停用）统一
// 401 同答，不泄露存在性。成功返回会员行（含 mobile——调用方构造
// authn.Principal.Username 用）。
//
// 查询租户上下文取令牌 payload 的 tid（WithTenantID 后走租户插件隔离）：
// 令牌即身份凭证，tid 是凭证自述的租户；member 行的 tenant_id 与 tid 再比
// 对一次（防御纵深：插件漏登记 member_user 时不静默放行跨租户 ID）。
func (s *Service) VerifyMemberToken(ctx context.Context, token string) (*model.MemberUser, error) {
	if err := s.memberTokenReady(); err != nil {
		return nil, xerr.Unauthorized(MemberTokenUnauthorizedMsg)
	}
	claims, err := s.parseMemberToken(token)
	if err != nil {
		return nil, xerr.Unauthorized(MemberTokenUnauthorizedMsg)
	}
	row, err := s.repo.FindMemberByID(tenant.WithTenantID(ctx, claims.TenantID), claims.MemberID)
	if err != nil {
		return nil, err
	}
	if row == nil || row.TenantID != claims.TenantID || row.Status != model.StatusEnable {
		return nil, xerr.Unauthorized(MemberTokenUnauthorizedMsg)
	}
	return row, nil
}

// parseMemberToken 解析并校验令牌签名与有效期（纯校验，不触库）：
// 两段式拆分 → 签名恒时比对（先解码再 hmac.Equal——等长性由原始字节比较
// 保证，避免对 base64 字符串直接比较引入的长度泄露）→ payload JSON →
// mid/tid 正数 + exp 未过期。
func (s *Service) parseMemberToken(token string) (*MemberClaims, error) {
	if err := s.memberTokenReady(); err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("会员令牌格式错误")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("会员令牌签名编码错误")
	}
	mac := hmac.New(sha256.New, s.memberTokenSecret)
	mac.Write([]byte(parts[0])) // hash.Write 不返回错误
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, fmt.Errorf("会员令牌签名不符")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("会员令牌 payload 编码错误")
	}
	var claims MemberClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("会员令牌 payload 解析错误")
	}
	if claims.MemberID <= 0 || claims.TenantID <= 0 {
		return nil, fmt.Errorf("会员令牌主体缺失")
	}
	if time.Now().Unix() >= claims.ExpiresAt {
		return nil, fmt.Errorf("会员令牌已过期")
	}
	return &claims, nil
}
