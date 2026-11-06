package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/jwt"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// MaskedSecret 是密钥脱敏占位符（spec §4.11.1）：查询一律替换为 ******，
// 更新时提交 ****** 表示保留原值。与消息渠道（#72）约定一致；
// 不复用 message 包常量，避免 system 反向依赖业务模块。
const MaskedSecret = "******"

// 社交登录骨架（issue #76，docs/platform-mvp-spec.md §4.4 / ADR 0011）：
// 只做第三方 OAuth2 客户端；未绑定账号返回 409 业务码 1901 并提示先登录再
// 绑定，不自动建号（绕过租户开户与配额流程，见 ADR 0011）。
//
// 本文件承载社交登录的业务编排：client CRUD（仅平台租户）、授权地址签发、
// 回调（登录签发/绑定完成）、绑定列表与解绑。provider 具体实现
//（微信/钉钉/企微）由 #77 经 social_provider.go 的接口缝注入。

// ErrSocialNotBound 是「三方账号未绑定本地用户」的业务错误：
// HTTP 409 + 业务码 1901（验收标准），不自动建号。
func ErrSocialNotBound() *xerr.Error {
	return xerr.New(http.StatusConflict, 1901, "该社交账号尚未绑定系统用户，请先用账号密码登录后在个人中心绑定")
}

// SocialService 汇聚社交登录能力；与 system.Service 共享仓储/JWT/配置但独立
// 实例，便于 #77 只扩展社交文件组而不碰既有服务。
type SocialService struct {
	repo *repository.Repository
	jwt  *jwt.Manager
	cfg  *config.Config

	states *socialStateStore

	socialMu        sync.RWMutex
	socialProviders map[string]SocialProvider
}

// NewSocialService 创建社交登录服务；state 有效期 10 分钟（spec §4.4.2）。
func NewSocialService(repo *repository.Repository, jwtMgr *jwt.Manager, cfg *config.Config) *SocialService {
	return &SocialService{
		repo:            repo,
		jwt:             jwtMgr,
		cfg:             cfg,
		states:          newSocialStateStore(socialStateTTL),
		socialProviders: make(map[string]SocialProvider),
	}
}

// SetStateTTL 调整 state 有效期（集成测试用；生产路径保持默认 10 分钟）。
func (s *SocialService) SetStateTTL(d time.Duration) { s.states.ttl = d }

// ---------- 客户端 CRUD（仅平台租户，路由层 RequirePlatformTenant 收口） ----------

// ListSocialClients 客户端分页；client_secret 一律脱敏（spec §4.11.1）。
func (s *SocialService) ListSocialClients(ctx context.Context, req *dto.SocialClientPageReq) ([]model.SocialClient, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.SocialClientFilter{
		Code:     strings.TrimSpace(req.Code),
		Name:     strings.TrimSpace(req.Name),
		Provider: strings.TrimSpace(req.Provider),
		Status:   req.Status,
	}
	list, total, err := s.repo.ListSocialClients(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range list {
		list[i].ClientSecret = MaskedSecret
	}
	return list, total, nil
}

// CreateSocialClient 创建客户端；编码平台内唯一（409）。
func (s *SocialService) CreateSocialClient(ctx context.Context, req *dto.SocialClientCreateReq) error {
	code, provider, secret, err := s.validateSocialClient(req.Code, req.Name, req.Provider, req.ClientID, req.ClientSecret, req.RedirectURI, req.Status)
	if err != nil {
		return err
	}
	if err := s.ensureSocialClientCodeFree(ctx, code, 0); err != nil {
		return err
	}
	client := &model.SocialClient{
		Code:         code,
		Name:         strings.TrimSpace(req.Name),
		Provider:     provider,
		ClientID:     strings.TrimSpace(req.ClientID),
		ClientSecret: secret,
		AuthorizeURL: strings.TrimSpace(req.AuthorizeURL),
		TokenURL:     strings.TrimSpace(req.TokenURL),
		UserinfoURL:  strings.TrimSpace(req.UserinfoURL),
		Scopes:       strings.TrimSpace(req.Scopes),
		RedirectURI:  strings.TrimSpace(req.RedirectURI),
		Status:       req.Status,
		Remark:       strings.TrimSpace(req.Remark),
	}
	return s.repo.CreateSocialClient(ctx, client)
}

// UpdateSocialClient 修改客户端；clientSecret 提交 ****** 保留原值
// （spec §4.11.1，与消息渠道约定一致），空值同样视为不修改。
func (s *SocialService) UpdateSocialClient(ctx context.Context, req *dto.SocialClientUpdateReq) error {
	client, err := s.findSocialClient(ctx, req.ID)
	if err != nil {
		return err
	}
	secret := req.ClientSecret
	if secret == MaskedSecret || strings.TrimSpace(secret) == "" {
		secret = client.ClientSecret
	}
	code, provider, cleanSecret, err := s.validateSocialClient(req.Code, req.Name, req.Provider, req.ClientID, secret, req.RedirectURI, req.Status)
	if err != nil {
		return err
	}
	if err := s.ensureSocialClientCodeFree(ctx, code, req.ID); err != nil {
		return err
	}
	client.Code = code
	client.Name = strings.TrimSpace(req.Name)
	client.Provider = provider
	client.ClientID = strings.TrimSpace(req.ClientID)
	client.ClientSecret = cleanSecret
	client.AuthorizeURL = strings.TrimSpace(req.AuthorizeURL)
	client.TokenURL = strings.TrimSpace(req.TokenURL)
	client.UserinfoURL = strings.TrimSpace(req.UserinfoURL)
	client.Scopes = strings.TrimSpace(req.Scopes)
	client.RedirectURI = strings.TrimSpace(req.RedirectURI)
	client.Status = req.Status
	client.Remark = strings.TrimSpace(req.Remark)
	return s.repo.SaveSocialClient(ctx, client)
}

// DeleteSocialClient 删除客户端（软删）。已发起流程的 state 独立校验客户端
// 存在性，删除后回调会按「客户端未配置」400，不产生悬挂引用。
func (s *SocialService) DeleteSocialClient(ctx context.Context, id int64) error {
	if _, err := s.findSocialClient(ctx, id); err != nil {
		return err
	}
	return s.repo.DeleteSocialClient(ctx, id)
}

func (s *SocialService) findSocialClient(ctx context.Context, id int64) (*model.SocialClient, error) {
	client, err := s.repo.FindSocialClientByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, xerr.NotFound("社交客户端不存在")
	}
	return client, nil
}

// validateSocialClient 归一化并校验客户端字段。
// provider 值域不锁死枚举：#77 落地微信/钉钉/企微后再随 provider 注册表收紧。
func (s *SocialService) validateSocialClient(rawCode, rawName, rawProvider, rawClientID, rawSecret, rawRedirect string, status int8) (string, string, string, error) {
	code := strings.TrimSpace(rawCode)
	name := strings.TrimSpace(rawName)
	provider := strings.TrimSpace(rawProvider)
	clientID := strings.TrimSpace(rawClientID)
	secret := strings.TrimSpace(rawSecret)
	redirect := strings.TrimSpace(rawRedirect)
	switch {
	case code == "":
		return "", "", "", xerr.BadRequest("客户端编码不能为空")
	case name == "":
		return "", "", "", xerr.BadRequest("客户端名称不能为空")
	case provider == "":
		return "", "", "", xerr.BadRequest("社交平台不能为空")
	case clientID == "":
		return "", "", "", xerr.BadRequest("客户端 ID 不能为空")
	case secret == "":
		return "", "", "", xerr.BadRequest("客户端密钥不能为空")
	case redirect == "":
		return "", "", "", xerr.BadRequest("回调地址不能为空")
	}
	if err := validateStatus(status); err != nil {
		return "", "", "", err
	}
	return code, provider, secret, nil
}

func (s *SocialService) ensureSocialClientCodeFree(ctx context.Context, code string, excludeID int64) error {
	total, err := s.repo.CountSocialClientsByCode(ctx, code, excludeID)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("客户端编码已存在")
	}
	return nil
}

// ---------- 授权地址与回调 ----------

// AuthorizeURL 生成授权跳转地址（公开端点，登录模式）：
// 校验平台已配置启用客户端且 provider 已注册 → 签发 state + PKCE verifier
// （服务端保存 10 分钟、一次性）→ 返回 {url, state}（spec §4.4.2）。
func (s *SocialService) AuthorizeURL(ctx context.Context, req *dto.SocialAuthorizeReq) (*dto.SocialAuthorizeResp, error) {
	tenantID := req.TenantID
	if tenantID <= 0 {
		tenantID = tenant.DefaultID
	}
	provider := strings.TrimSpace(req.Provider)

	client, err := s.repo.FindEnabledSocialClientByProvider(ctx, provider)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, xerr.BadRequest("该社交平台未配置或已停用")
	}
	p := s.socialProvider(provider)
	if p == nil {
		return nil, xerr.BadRequest("暂不支持该社交平台")
	}

	state, verifier, err := s.states.issue(provider, tenantID, 0)
	if err != nil {
		return nil, err
	}
	url, err := p.AuthorizeURL(ctx, client, state, pkceCodeChallengeS256(verifier))
	if err != nil {
		return nil, err
	}
	return &dto.SocialAuthorizeResp{URL: url, State: state}, nil
}

// BindInitiate 发起绑定（受保护端点，登录即可）：为当前用户签发绑定模式的
// state（携带 user_id，spec §4.4.6），返回授权地址；回调完成写入绑定。
func (s *SocialService) BindInitiate(ctx context.Context, req *dto.SocialBindReq, userID, tenantID int64) (*dto.SocialAuthorizeResp, error) {
	provider := strings.TrimSpace(req.Provider)
	client, err := s.repo.FindEnabledSocialClientByProvider(ctx, provider)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, xerr.BadRequest("该社交平台未配置或已停用")
	}
	p := s.socialProvider(provider)
	if p == nil {
		return nil, xerr.BadRequest("暂不支持该社交平台")
	}

	state, verifier, err := s.states.issue(provider, tenantID, userID)
	if err != nil {
		return nil, err
	}
	url, err := p.AuthorizeURL(ctx, client, state, pkceCodeChallengeS256(verifier))
	if err != nil {
		return nil, err
	}
	return &dto.SocialAuthorizeResp{URL: url, State: state}, nil
}

// Callback 处理社交回调（公开端点）。流程（spec §4.4.3–§4.4.5）：
//  1. 一次性消费 state：无效/已使用/过期/provider 不匹配 → 400；
//  2. 校验平台已配置启用客户端、provider 已注册，换 token 拉三方身份
//     （失败统一 400，详情只落日志，不泄露内部错误，spec §4.11.3）；
//  3. 绑定模式（state 携带 user_id）：写入/幂等更新绑定；
//  4. 登录模式：已绑定 → 签发 JWT + 登录日志 login_type=3；
//     未绑定 → 409 业务码 1901，不自动建号。
func (s *SocialService) Callback(ctx context.Context, req *dto.SocialCallbackReq, ip, userAgent string) (*dto.SocialCallbackResp, error) {
	provider := strings.TrimSpace(req.Provider)
	st, err := s.states.consume(strings.TrimSpace(req.State), provider)
	if err != nil {
		return nil, err
	}

	// 回调按 state 登记的租户上下文查询绑定（登录页可带 tenantId，spec §4.4.2）。
	ctx = tenant.WithTenantID(ctx, st.TenantID)

	client, err := s.repo.FindEnabledSocialClientByProvider(ctx, st.Provider)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, xerr.BadRequest("该社交平台未配置或已停用")
	}
	p := s.socialProvider(st.Provider)
	if p == nil {
		return nil, xerr.BadRequest("暂不支持该社交平台")
	}

	identity, err := p.ExchangeIdentity(ctx, client, strings.TrimSpace(req.Code), st.CodeVerifier)
	if err != nil {
		slog.Error("social exchange identity failed", "provider", st.Provider, "err", err)
		return nil, xerr.BadRequest("社交登录失败，请稍后重试")
	}
	if identity == nil || identity.OpenID == "" {
		return nil, xerr.BadRequest("社交登录失败：未获取到三方身份")
	}
	identity.RawInfo = truncateRunes(identity.RawInfo, socialRawInfoMaxBytes)

	if st.UserID > 0 {
		return s.completeBind(ctx, st, identity)
	}
	return s.completeLogin(ctx, st, identity, ip, userAgent)
}

// completeLogin 已绑定 → 签发 JWT + 登录日志 login_type=3；
// 未绑定 → 409/1901（不自动建号）。
func (s *SocialService) completeLogin(ctx context.Context, st *socialState, identity *SocialIdentity, ip, userAgent string) (*dto.SocialCallbackResp, error) {
	binding, err := s.repo.FindSocialUserBinding(ctx, st.Provider, identity.OpenID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, ErrSocialNotBound()
	}

	user, err := s.repo.FindUserByID(ctx, binding.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		// 绑定指向的本地用户已删除：按未绑定处理，提示先登录再绑定。
		return nil, ErrSocialNotBound()
	}
	if user.Status != model.StatusEnable {
		return nil, xerr.Conflict("账号已被禁用")
	}

	tenantID := tenant.ID(ctx)
	token, expireAt, err := s.jwt.Generate(user.ID, tenantID, user.Username)
	if err != nil {
		return nil, xerr.Internal(err)
	}

	now := time.Now()
	binding.LastLoginAt = &now
	binding.Nickname = firstNonEmpty(identity.Nickname, binding.Nickname)
	binding.Avatar = firstNonEmpty(identity.Avatar, binding.Avatar)
	if err := s.repo.SaveSocialUser(ctx, binding); err != nil {
		return nil, err
	}
	s.recordSocialLoginLog(ctx, user.ID, user.Username, ip, userAgent, true, "社交登录成功")

	return &dto.SocialCallbackResp{
		Mode:        "login",
		AccessToken: token,
		ExpiresIn:   int64(time.Until(expireAt).Seconds()),
		UserID:      user.ID,
		Username:    user.Username,
		TenantID:    tenantID,
		Provider:    st.Provider,
		OpenID:      identity.OpenID,
	}, nil
}

// completeBind 绑定模式回调：把三方身份绑定到 state 携带的本地用户。
//   - 同一 openid 已绑定其他用户 → 409（spec §4.4.6）；
//   - 同一用户重复绑定同一 openid → 幂等成功（更新资料，不新增行）；
//   - 绑定/解绑幂等：解绑（软删）后可重新绑定（唯一键含 deleted_at）。
func (s *SocialService) completeBind(ctx context.Context, st *socialState, identity *SocialIdentity) (*dto.SocialCallbackResp, error) {
	user, err := s.repo.FindUserByID(ctx, st.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, xerr.NotFound("用户不存在")
	}
	if user.Status != model.StatusEnable {
		return nil, xerr.Conflict("账号已被禁用")
	}

	binding, err := s.repo.FindSocialUserBinding(ctx, st.Provider, identity.OpenID)
	if err != nil {
		return nil, err
	}
	if binding != nil {
		if binding.UserID != st.UserID {
			return nil, xerr.Conflict("该社交账号已绑定其他用户")
		}
		// 幂等：更新三方资料与最近登录时间，不新增行。
		now := time.Now()
		binding.Nickname = firstNonEmpty(identity.Nickname, binding.Nickname)
		binding.Avatar = firstNonEmpty(identity.Avatar, binding.Avatar)
		binding.LastLoginAt = &now
		if err := s.repo.SaveSocialUser(ctx, binding); err != nil {
			return nil, err
		}
		return bindCallbackResp(st.Provider, identity), nil
	}

	now := time.Now()
	row := &model.SocialUser{
		TenantID:    tenant.ID(ctx),
		UserID:      st.UserID,
		Provider:    st.Provider,
		OpenID:      identity.OpenID,
		UnionID:     identity.UnionID,
		Nickname:    identity.Nickname,
		Avatar:      identity.Avatar,
		RawInfo:     identity.RawInfo,
		Status:      model.StatusEnable,
		LastLoginAt: &now,
	}
	if err := s.repo.CreateSocialUser(ctx, row); err != nil {
		return nil, err
	}
	return bindCallbackResp(st.Provider, identity), nil
}

// Unbind 解绑（受保护端点，权限 system:social-user:delete）：
// 只能解绑当前租户的绑定关系，不存在/跨租户一律 404（spec §4.4.7）。
// 解绑是软删，同一 openid 可重新绑定（幂等语义：解绑后重新绑定不冲突）。
func (s *SocialService) Unbind(ctx context.Context, id int64) error {
	binding, err := s.repo.FindSocialUserByID(ctx, id)
	if err != nil {
		return err
	}
	if binding == nil {
		return xerr.NotFound("绑定关系不存在")
	}
	return s.repo.DeleteSocialUser(ctx, id)
}

// ListSocialUsers 绑定用户分页（当前租户，租户条件由插件追加），
// 回填本地用户名便于管理页展示。
func (s *SocialService) ListSocialUsers(ctx context.Context, req *dto.SocialUserPageReq) ([]dto.SocialUserResp, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.SocialUserFilter{Provider: strings.TrimSpace(req.Provider), UserID: req.UserID}
	list, total, err := s.repo.ListSocialUsers(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int64, 0, len(list))
	for _, row := range list {
		ids = append(ids, row.UserID)
	}
	users, err := s.repo.ListUsersByIDs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	names := make(map[int64]string, len(users))
	for _, u := range users {
		names[u.ID] = u.Username
	}
	out := make([]dto.SocialUserResp, 0, len(list))
	for _, row := range list {
		createTime := row.CreatedAt
		out = append(out, dto.SocialUserResp{
			ID:          row.ID,
			UserID:      row.UserID,
			Username:    names[row.UserID],
			Provider:    row.Provider,
			OpenID:      row.OpenID,
			UnionID:     row.UnionID,
			Nickname:    row.Nickname,
			Avatar:      row.Avatar,
			Status:      row.Status,
			LastLoginAt: row.LastLoginAt,
			CreateTime:  &createTime,
		})
	}
	return out, total, nil
}

// recordSocialLoginLog 写社交登录日志（login_type=3，验收标准），
// 失败不影响主流程（与账号密码登录一致）。
func (s *SocialService) recordSocialLoginLog(ctx context.Context, userID int64, username, ip, userAgent string, success bool, message string) {
	result := int8(0)
	if !success {
		result = 1
	}
	log := &model.LoginLog{
		TenantID:  tenant.ID(ctx),
		UserID:    userID,
		Username:  username,
		LoginIP:   ip,
		UserAgent: truncateRunes(userAgent, 500),
		LoginType: 3, // 1 账号密码 2 短信 3 社交
		Result:    result,
		Message:   message,
		LoginTime: time.Now(),
	}
	if err := s.repo.CreateLoginLog(ctx, log); err != nil {
		slog.Error("write social login log", "err", err)
	}
}

func bindCallbackResp(provider string, identity *SocialIdentity) *dto.SocialCallbackResp {
	return &dto.SocialCallbackResp{
		Mode:     "bind",
		Provider: provider,
		OpenID:   identity.OpenID,
	}
}

// socialRawInfoMaxBytes 是三方原始信息的落库上限（spec §4.4.9：截断 4096 字节）。
const socialRawInfoMaxBytes = 4096

// truncateRunes 按字节上限截断且不截断 UTF-8 rune（raw_info 是 text 列，
// 半个字符会导致写库失败）。
func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
