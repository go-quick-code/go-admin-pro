package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
)

// ErrUnknownProvider 是 provider 未注册的哨兵错误：发送时 fail-closed，
// 绝不静默成功（spec §4.3 / ADR 0010）。
var ErrUnknownProvider = errors.New("未注册的消息 provider")

// SendRequest 是 provider 的发送入参。Content/Title 已由 service 渲染完成，
// provider 只负责投递，不再解析模板。
type SendRequest struct {
	Channel      model.Channel
	TemplateCode string
	Recipient    string
	Title        string
	Content      string
	Params       map[string]string
}

// SendResult 是 provider 的发送结果。
type SendResult struct {
	ProviderMessageID string
}

// Sender 是消息 provider 的接缝：新增 smtp/aliyun_sms 等实现时只需在
// internal/app 或模块 New 中注册，不修改发送流程。
type Sender interface {
	// Provider 返回注册键（与 message_channel.provider 对应）。
	Provider() string
	// Send 投递一条消息；失败返回错误，调用方负责写失败日志并返回 502。
	Send(ctx context.Context, req SendRequest) (SendResult, error)
}

// Registry 是按 provider 编码索引的发送器注册表，读写并发安全
// （注册发生在装配期，查询发生在请求期）。
type Registry struct {
	mu      sync.RWMutex
	senders map[string]Sender
}

// NewRegistry 创建注册表并注册初始 sender。
func NewRegistry(senders ...Sender) *Registry {
	r := &Registry{senders: make(map[string]Sender, len(senders))}
	for _, sender := range senders {
		r.Register(sender)
	}
	return r
}

// Register 注册/替换一个 sender（同名后注册者生效）。
func (r *Registry) Register(sender Sender) {
	if sender == nil || sender.Provider() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.senders[sender.Provider()] = sender
}

// Lookup 按 provider 编码查找 sender；未注册返回 ErrUnknownProvider。
func (r *Registry) Lookup(provider string) (Sender, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sender, ok := r.senders[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}
	return sender, nil
}

// LogSender 是内置的 log provider（开发/演示用）：只写 slog，不真正投递。
// 生产环境应注册 smtp / aliyun_sms 等实现（后续工单）。
type LogSender struct {
	now func() time.Time
}

// NewLogSender 创建 log provider。
func NewLogSender() *LogSender {
	return &LogSender{now: time.Now}
}

// Provider 返回注册键。
func (s *LogSender) Provider() string { return model.ProviderLog }

// Send 只记录一条结构化日志并返回可追踪的 provider 消息 ID。
// 不记录 Content：正文可能包含验证码等敏感信息。
func (s *LogSender) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	slog.Info("message log provider send",
		"channelType", req.Channel.ChannelType,
		"channelId", req.Channel.ID,
		"templateCode", req.TemplateCode,
		"recipient", req.Recipient,
	)
	return SendResult{ProviderMessageID: fmt.Sprintf("log-%d", s.now().UnixNano())}, nil
}
