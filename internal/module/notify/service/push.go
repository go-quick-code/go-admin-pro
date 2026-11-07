package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/push"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// PushPublisher 是 notify service 的实时推送接缝（issue #78，spec §4.5.5）：
// 通知落库提交后经它发布给在线用户。*push.Hub 满足该接口（internal/app 装配）。
// 推送是 best-effort：实现不得阻塞、不得返回错误——丢事件由前端 60 秒轮询
// 兜底（ADR 0013）。
type PushPublisher interface {
	Publish(tenantID, userID int64, ev push.Event)
}

// SetPushPublisher 注入推送发布器；未注入（push.enabled=false 或测试基座）
// 时 PublishInputs 静默跳过。
func (s *Service) SetPushPublisher(p PushPublisher) { s.push = p }

// PublishInputs 把一批通知输入转换为统一事件（spec §4.5.4）并按 ctx 租户
// 与 Input.UserID 发布给在线连接。
//
// 发布时机由调用方保证：Create 在自身事务提交成功后调用；CreateInTx 的
// 生产者（bpm 待办/结果、公告发布、会议提醒）在各自事务提交后显式调用
// （spec §4.5.5）。事务回滚不会走到 PublishInputs——已提交数据才有推送，
// 回滚产生的瞬时事件以前端刷新为准（best-effort，ADR 0013）。
func (s *Service) PublishInputs(ctx context.Context, inputs []Input) {
	if s.push == nil || len(inputs) == 0 {
		return
	}
	tenantID := tenant.ID(ctx)
	for i := range inputs {
		s.push.Publish(tenantID, inputs[i].UserID, push.NewNotification(EventDataOf(inputs[i])))
	}
}

// EventDataOf 把通知输入映射为推送事件载荷（与 notify_message 行同义）。
func EventDataOf(in Input) push.EventData {
	return push.EventData{
		Title:       in.Title,
		Content:     in.Content,
		Type:        in.Type,
		BizType:     in.BizType,
		BizID:       in.BizID,
		Route:       in.Route,
		RouteParams: in.RouteParams,
	}
}
