package service

import (
	"context"
	"testing"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/push"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
)

// fakePublisher 记录发布调用（单元测试替身）。
type fakePublisher struct {
	calls []pushCall
}

type pushCall struct {
	tenantID int64
	userID   int64
	ev       push.Event
}

func (f *fakePublisher) Publish(tenantID, userID int64, ev push.Event) {
	f.calls = append(f.calls, pushCall{tenantID, userID, ev})
}

// PublishInputs 把 Input 批次转换为统一事件、按 ctx 租户 + Input 用户发布：
// spec §4.5.4 的事件模型，spec §4.5.5 的「提交后显式 Publish」共用此转换。
func TestPublishInputsConvertsAndPublishes(t *testing.T) {
	fake := &fakePublisher{}
	s := New(nil)
	s.SetPushPublisher(fake)

	ctx := tenant.WithTenantID(context.Background(), 5)
	s.PublishInputs(ctx, []Input{
		{UserID: 7, Type: "bpm_todo", Title: "您有新的审批任务", Content: "合同审批",
			BizType: "bpm_task", BizID: 9, Route: "/bpm/task", RouteParams: `{"taskId":9}`},
		{UserID: 8, Type: "announcement", Title: "公告"},
	})

	if len(fake.calls) != 2 {
		t.Fatalf("发布次数 = %d, want 2", len(fake.calls))
	}
	c := fake.calls[0]
	if c.tenantID != 5 || c.userID != 7 {
		t.Fatalf("第一条 (%d,%d), want (5,7)", c.tenantID, c.userID)
	}
	if c.ev.Type != push.EventTypeNotification {
		t.Fatalf("type = %q, want %q", c.ev.Type, push.EventTypeNotification)
	}
	want := push.EventData{
		Title: "您有新的审批任务", Content: "合同审批", Type: "bpm_todo",
		BizType: "bpm_task", BizID: 9, Route: "/bpm/task", RouteParams: `{"taskId":9}`,
	}
	if c.ev.Data != want {
		t.Fatalf("data = %+v, want %+v", c.ev.Data, want)
	}
	if c.ev.Ts <= 0 {
		t.Fatalf("ts = %d, want 正数", c.ev.Ts)
	}
	if fake.calls[1].userID != 8 || fake.calls[1].tenantID != 5 {
		t.Fatalf("第二条 (%d,%d), want (5,8)", fake.calls[1].tenantID, fake.calls[1].userID)
	}
}

// 未装配发布器（push 关闭或单测基座）时静默跳过——推送是 best-effort。
func TestPublishInputsWithoutPublisher(t *testing.T) {
	s := New(nil)
	s.PublishInputs(tenant.WithTenantID(context.Background(), 1), []Input{{UserID: 7, Type: "bpm_todo", Title: "x"}})
}

// 空批次 no-op。
func TestPublishInputsEmpty(t *testing.T) {
	fake := &fakePublisher{}
	s := New(nil)
	s.SetPushPublisher(fake)
	s.PublishInputs(context.Background(), nil)
	if len(fake.calls) != 0 {
		t.Fatalf("空批次不应发布，实际 %d 次", len(fake.calls))
	}
}
