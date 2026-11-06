package service

import (
	"context"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/quota"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// MessageQuotaGateway 是 message 发送月度配额的窄接口：限额由租户套餐决定
// （system 模块解释，ADR 0012），由 internal/app 装配期注入适配器；message
// 只依赖本接口与 internal/pkg/quota 的中性类型，不反向依赖 system。
type MessageQuotaGateway interface {
	// MessageLimits 返回租户的套餐限额；Unlimited 或 Max*Monthly<=0 表示该项不限。
	MessageLimits(ctx context.Context, tenantID int64) (quota.Limits, error)
}

// SetQuotaGateway 注入发送月度配额限额来源（internal/app 装配调用）。
// 未注入时（如单元测试直连 Service）发送不做配额校验。
func (s *Service) SetQuotaGateway(gw MessageQuotaGateway) {
	s.quotaGateway = gw
}

// SetLocation 注入应用业务时区（ADR 0006：唯一来源是 config.Location()，
// 由 internal/app 装配时调用）。月度配额的「当月」边界按它划分。nil 忽略，
// 保留当前值。
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区：app 装配后是配置的 app.timezone；
// 未注入（如直接构造 Service 的测试）时退回 time.Local（与 oa/crm 同口径）。
func (s *Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// quotaClock 是月度配额统计「现在」的来源。用 var 而非直接 time.Now()：
// 仅集成测试注入固定时钟验证月边界（用例内复原），生产代码不写此变量。
var quotaClock = time.Now

// SetQuotaClock 是配额统计时钟的写缝：注入固定时钟并返回原值，供集成测试
// 在用例内替换与复原（同 SetSendTimeout 的定位），生产代码不应调用。
func SetQuotaClock(f func() time.Time) func() time.Time {
	prev := quotaClock
	quotaClock = f
	return prev
}

// businessNow 返回业务时区下的「现在」：月度窗口划分统一走这里，
// 不直接用 time.Now()（其 Location 是服务器本地时区，与 app.timezone
// 可能不同，跨月零点会划错月份——ADR 0006）。
func (s *Service) businessNow() time.Time {
	return quotaClock().In(s.businessLocation())
}

// MonthlyUsage 返回租户当月指定渠道的发送次数（spec §4.2.4 口径）：统计
// message_log 中 [当月 1 日 00:00, 下月 1 日 00:00)（按应用时区）内该渠道的
// 全部行数，成功与失败都算——provider 失败也写日志行，自然计入用量。
// tenantID 显式传参：发送校验传当前租户，app 装配的配额提供者可查任意租户。
// channelType 非法返回 400。
func (s *Service) MonthlyUsage(ctx context.Context, tenantID int64, channelType string) (int64, error) {
	if err := validateChannelType(channelType); err != nil {
		return 0, err
	}
	start, end := monthWindow(s.businessNow(), s.businessLocation())
	return s.repo.CountLogsInMonth(ctx, tenantID, channelType, start, end)
}

// checkSendQuota 在写任何日志行之前校验当月配额（spec §4.2.4/§4.2.6）：
// 当月该渠道行数 >= Max*Monthly 时返回 409「本月短信/邮件额度已用尽」，
// 调用方不得再写 message_log。平台租户与未绑定套餐（Unlimited）、
// Max*Monthly<=0 不限；配额网关未注入（直连 Service 的测试）跳过校验。
// 校验放在渲染之后、provider 调用之前（spec §4.3.2 的流程顺序），
// 与渠道测试发送共用 deliver，因此渠道测试发送同样受配额约束。
func (s *Service) checkSendQuota(ctx context.Context, channelType string) error {
	if s.quotaGateway == nil {
		return nil
	}
	tenantID := tenant.ID(ctx)
	limits, err := s.quotaGateway.MessageLimits(ctx, tenantID)
	if err != nil {
		return err
	}
	max := limits.MaxSMSMonthly
	if channelType == model.ChannelTypeMail {
		max = limits.MaxMailMonthly
	}
	if limits.Unlimited || max <= 0 {
		return nil
	}
	used, err := s.MonthlyUsage(ctx, tenantID, channelType)
	if err != nil {
		return err
	}
	if used >= max {
		return xerr.Conflict(quotaExhaustedMessage(channelType, used, max))
	}
	return nil
}

// quotaExhaustedMessage 是月度配额超限的 409 文案（spec §4.2.4：
// 「本月短信/邮件额度已用尽」），按渠道细分并附上用量与上限。
func quotaExhaustedMessage(channelType string, used, max int64) string {
	name := "短信"
	if channelType == model.ChannelTypeMail {
		name = "邮件"
	}
	return fmt.Sprintf("本月%s额度已用尽（当月已用 %d 条，套餐上限 %d 条）", name, used, max)
}

// monthWindow 返回 now 所在「当月」的 [start, end) 半开区间，按 loc 的
// 日历月划分（issue #75，spec §4.2.4：月度配额按应用时区统计当月行数）。
//
// 同一时刻在不同时区可能属于不同月份（如北京 11 月 1 日 02:00 在 UTC 仍是
// 10 月 31 日 18:00），因此边界必须由调用方传入应用业务时区（ADR 0006，
// 唯一来源 config.Location()），不得使用 time.Local。
//
// start 落在当月 1 日 00:00:00.000（含），end 为下月 1 日 00:00:00.000
// （不含）：与 message_log.created_at 的 datetime(3) 精度对齐，月末最后一
// 毫秒计入当月，跨月零点即重置。start 是每月 1 日，AddDate(0,1,0) 不会
// 触发月末归一化陷阱。
func monthWindow(now time.Time, loc *time.Location) (time.Time, time.Time) {
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 1, 0)
}
