// Package service 是 pay 模块的业务层（issue #131 起步：应用与渠道域；
// #132 钱包域；#133 订单与收银台；#135 退款域）。
//
// 规则来源：docs/pay-mvp-spec.md §3.1/§3.2/§4.1/§4.2/§4.3/§4.4/§4.9/§4.10/§7。
//   - 渠道（pay_channel）是平台级资源，只有平台租户（ID=1）可读写；租户侧
//     只读渠道清单（无凭证）。凭证走三段式：明文落库、查询脱敏 ******、
//     更新占位符合并（照 message_channel 先例）。
//   - 应用是租户级资源；绑定全量替换，校验「码 ∈ 14 渠道码常量 ∧ 平台渠道
//     启用 ∧ 渠道已实现」（#133：注册表无 Client 的渠道绑定即 409 渠道未实现）。
//   - 应用删除：有渠道绑定须先解绑；订单/退款/转账引用禁删的检查经
//     AppRefChecker 扩展点注册（#133 订单引用 + #135 退款引用已注册）。
//   - 单号发放（spec §4.9）：pay_sequence 事务内发放，前缀 + 业务时区日期
//   - 至少 4 位序号；客户端提交的 no 一律不采纳。
//   - 订单（#133，spec §4.2）：下单幂等（撞幂等键返回原单）、金额服务端定、
//     支付成功入账函数三条路径共用（状态认领 0 行幂等返回）。
//   - 退款（#135，spec §4.4）：原路退回、部分退款累计 ≤ 实付（订单行锁内
//     裁决，全额置 20）、wallet 回加走唯一入账入口（biz=4，与支付扣减同锁
//     路径）、退款成功入账函数三条路径共用。
package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	paymodel "github.com/go-quick-code/go-admin-pro/internal/module/pay/model"
	payrepo "github.com/go-quick-code/go-admin-pro/internal/module/pay/repository"
	"github.com/go-quick-code/go-admin-pro/internal/module/pay/service/channel"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ErrPlatformOnly 是渠道越权访问的哨兵错误（非平台租户 403）。
var ErrPlatformOnly = errors.New("支付渠道仅平台租户可管理")

// Service 汇聚 pay 模块的业务能力。
type Service struct {
	repo *payrepo.Repository
	// registry 是渠道实现注册表（spec §4.1）：绑定校验与 submit 用 Known/Get
	// 判定渠道是否已实现（未注册 → 409 渠道未实现）。装配期 SetRegistry 注入。
	registry *channel.Registry
	// location 是业务时区（ADR 0006），由 app 装配时 SetLocation 注入：
	// 单号日期键一律按它划分。nil 时退回 time.Local。
	location *time.Location
	// appRefCheckers 是应用删除引用检查链（spec §4.10 扩展点）：#133 注册
	// 订单引用检查；#135/#136 的退款/转账检查经同一扩展点追加；未注册时只查
	// 渠道绑定（纯绑定关系须先解绑）。
	appRefCheckers AppRefChecker
	// notifyHMACKey 是服务端回调密钥（issue #134，configs pay.notify-hmac-key）：
	// 商户回调 POST 的 Pay-Signature 签名与 demo 回调端点验签共用；空值拒发/
	// 拒验（fail-closed）。装配期 SetNotifyHMACKey 注入。
	notifyHMACKey string
	// notifyHTTPClient 是回调 job 的出站 HTTP 客户端（10s 超时、1MB 上限，
	// 照 socialHTTP 先例）。New 时创建。
	notifyHTTPClient *notifyHTTP
	// tenants 是启用租户清单来源（#134 回调/关单 job 跨租户迭代，窄接口
	// 适配 system Service）。未注入时 job 报错拒跑（fail-closed）。
	tenants TenantLister
	// payloadEnrichers 是回调报文详情扩展（按任务类型；#134 内置 type=1，
	// #135/#136 经 SetNotifyPayloadEnricher 追加）。
	payloadEnrichers map[int8]NotifyPayloadEnricher
	// demoHandler 是 demo 商户回调业务联动扩展点（#137 注册；未注册时
	// demo 端点验签通过即回 code=0）。
	demoHandler DemoNotifyHandler
}

// SetRegistry 注入渠道实现注册表（装配期；未注入视为空表——所有渠道未实现）。
func (s *Service) SetRegistry(registry *channel.Registry) {
	s.registry = registry
}

// channelRegistry 返回注册表（nil 安全：空注册表语义）。
func (s *Service) channelRegistry() *channel.Registry {
	if s.registry == nil {
		return channel.NewRegistry()
	}
	return s.registry
}

// New 创建 pay Service：内置 type=1（支付）的回调报文详情 enricher；
// #135 退款 / #136 转账经 SetNotifyPayloadEnricher 追加各自的详情。
func New(repo *payrepo.Repository) *Service {
	s := &Service{repo: repo, notifyHTTPClient: newNotifyHTTP()}
	s.payloadEnrichers = map[int8]NotifyPayloadEnricher{
		paymodel.NotifyTypePayment: s.enrichPaymentPayload,
	}
	return s
}

// SetAppRefChecker 注册应用删除引用检查链（装配期或后续票追加；后注册覆盖先注册）。
func (s *Service) SetAppRefChecker(checker AppRefChecker) {
	s.appRefCheckers = checker
}

// SetLocation 注入业务时区（ADR 0006）：单号日期键的唯一口径。
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区；未注入时退回进程本地时区（测试直连场景）。
func (s *Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// businessNow 返回业务时区下的「现在」：单号日期键一律走这里，不直接用
// time.Now()（其 Location 是服务器本地时区，跨时区部署会算错日期键）。
func (s *Service) businessNow() time.Time {
	return time.Now().In(s.businessLocation())
}

// requirePlatform 校验当前租户是平台租户（ID=1）。渠道是平台级资源，
// 非平台租户即使持有 pay:channel:* 权限码也一律 403。
func (s *Service) requirePlatform(ctx context.Context) error {
	if tenant.ID(ctx) != tenant.DefaultID {
		return xerr.Wrap(ErrPlatformOnly, http.StatusForbidden, http.StatusForbidden, ErrPlatformOnly.Error())
	}
	return nil
}

// payTenantID 返回 context 中的当前租户 ID（序列发放等裸 SQL 的显式租户参数，
// 同 erpTenantID）。
func payTenantID(ctx context.Context) int64 {
	if p, ok := authn.From(ctx); ok {
		return p.TenantID
	}
	return 0
}
