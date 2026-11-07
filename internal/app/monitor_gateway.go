package app

import (
	"context"
	"time"

	infradto "github.com/go-quick-code/go-admin-pro/internal/module/infra/dto"
	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	messageservice "github.com/go-quick-code/go-admin-pro/internal/module/message/service"
	systemservice "github.com/go-quick-code/go-admin-pro/internal/module/system/service"
)

// monitorGateway 组合 system / message 的平台级统计能力（issue #84，
// spec §4.10）：对 infra 实现其 MonitorStatsGateway 窄接口——租户数/
// 用户数来自 system，消息发送结果来自 message。各模块只依赖各自定义的
// 窄接口，由 internal/app 在装配期组合注入，模块间不直接 import
// （同 quotaGateway 模式）。
type monitorGateway struct {
	sys     *systemservice.Service
	message *messageservice.Service
}

// TenantCount 实现 infra 侧 MonitorStatsGateway：全平台租户数（system）。
func (g monitorGateway) TenantCount(ctx context.Context) (int64, error) {
	return g.sys.TenantCount(ctx)
}

// UserCount 实现 infra 侧 MonitorStatsGateway：全平台用户数（system）。
func (g monitorGateway) UserCount(ctx context.Context) (int64, error) {
	return g.sys.UserCount(ctx)
}

// MessageSendStats 实现 infra 侧 MonitorStatsGateway：since 以来的消息
// 发送成功/失败数（message），映射为 infra 的中性响应 DTO。
func (g monitorGateway) MessageSendStats(ctx context.Context, since time.Time) (infradto.MonitorMessageInfo, error) {
	stats, err := g.message.SendStatsSince(ctx, since)
	if err != nil {
		return infradto.MonitorMessageInfo{}, err
	}
	return infradto.MonitorMessageInfo{
		SmsSuccess:  stats.SMSSuccess,
		SmsFailed:   stats.SMSFailed,
		MailSuccess: stats.MailSuccess,
		MailFailed:  stats.MailFailed,
	}, nil
}

var _ infraservice.MonitorStatsGateway = monitorGateway{}
