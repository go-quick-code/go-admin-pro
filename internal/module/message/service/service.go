// Package service 是 message 模块的业务层：平台级渠道/模板管理、
// 模板渲染、发送与租户级发送日志。
//
// 规则来源：docs/platform-mvp-spec.md §4.3 / ADR 0010。
//   - 渠道/模板只有平台租户（ID=1）可读写；租户只能看自己的发送日志。
//   - 发送流程：模板启用 → 渠道启用 → 参数与声明完全一致 → 渲染 →
//     provider 发送（超时 10s）→ 写日志（失败也写）。
//   - provider 未知/发送失败返回 502，错误详情只落日志，不回显密钥。
package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/message/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ErrPlatformOnly 是渠道/模板越权访问的哨兵错误（非平台租户 403）。
var ErrPlatformOnly = errors.New("消息渠道与模板仅平台租户可管理")

// Service 汇聚 message 模块的业务能力。
type Service struct {
	repo     *repository.Repository
	registry *Registry
	// quotaGateway 是发送月度配额的限额来源（issue #75，spec §4.2.4），
	// 由 internal/app 装配期注入；未注入时（如单元测试直连 Service）不做配额校验。
	quotaGateway MessageQuotaGateway
	// location 是业务时区（ADR 0006），由 app 装配时 SetLocation 注入：
	// 月度配额的「当月」边界按它划分。nil 时退回 time.Local。
	location *time.Location
}

// New 创建 message Service，并注册内置 provider：log（开发用）、smtp
// （邮件，issue #73）与 aliyun_sms（阿里云短信，issue #74）。
func New(repo *repository.Repository) *Service {
	return &Service{
		repo:     repo,
		registry: NewRegistry(NewLogSender(), NewSMTPSender(), NewAliyunSMSSender()),
	}
}

// RegisterSender 注册一个 provider 实现（装配期或集成测试使用）。
func (s *Service) RegisterSender(sender Sender) {
	s.registry.Register(sender)
}

// requirePlatform 校验当前租户是平台租户（ID=1）。渠道/模板是平台级资源，
// 非平台租户即使持有 message:* 权限码也一律 403。
func (s *Service) requirePlatform(ctx context.Context) error {
	if tenant.ID(ctx) != tenant.DefaultID {
		return xerr.Wrap(ErrPlatformOnly, http.StatusForbidden, http.StatusForbidden, ErrPlatformOnly.Error())
	}
	return nil
}

// validateChannelType 校验渠道类型枚举（spec §3.2：sms / mail）。
func validateChannelType(channelType string) error {
	switch channelType {
	case model.ChannelTypeSMS, model.ChannelTypeMail:
		return nil
	default:
		return xerr.BadRequest("渠道类型不合法：期望 sms 或 mail")
	}
}

// validateStatus 校验启停状态枚举（0 启用 / 1 停用）。
func validateStatus(status int8) error {
	if status != model.StatusEnable && status != model.StatusDisable {
		return xerr.BadRequest("状态不合法：期望 0 启用 / 1 停用")
	}
	return nil
}

// badGateway 构造 provider 失败的 502 响应（中文提示，不含内部细节）。
func badGateway(msg string) error {
	return xerr.New(http.StatusBadGateway, http.StatusBadGateway, msg)
}

// truncateRunes 按字符（而非字节）截断，避免把 UTF-8 字符切坏。
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
