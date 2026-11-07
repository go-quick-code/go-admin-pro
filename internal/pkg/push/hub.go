package push

import (
	"sync"
	"sync/atomic"
)

// subscriberBuffer 是单连接发送缓冲长度：缓冲满视为慢消费者，
// 后续事件丢弃并计数，绝不阻塞发布方（spec §4.5.1）。
const subscriberBuffer = 64

// MaxConnsPerStream 是单个 (tenant, user) 的连接数上限（#85 安全审计 NV2）：
// 容忍同账号多标签页（建议值 8），超过即拒绝新连接——没有上限时换票重连
// 可以无限堆积连接耗尽内存。检查与登记在同一把锁内完成（原子），并发建连
// 不会越过上限。
const MaxConnsPerStream = 8

// streamKey 是 hub 的订阅维度：(tenant_id, user_id)。跨租户/跨用户互不可见。
type streamKey struct {
	tenantID int64
	userID   int64
}

// Hub 是在线连接注册表：按 (tenant_id, user_id) 订阅，同一用户的全部连接
// 都收到事件。Publish 永不阻塞、永不报错——推送是 best-effort（ADR 0013），
// 丢事件由前端的 60 秒轮询兜底。Hub 方法并发安全。
type Hub struct {
	mu     sync.RWMutex
	subs   map[streamKey]map[*Subscriber]struct{}
	nextID atomic.Uint64
}

// NewHub 创建空 hub。
func NewHub() *Hub {
	return &Hub{subs: make(map[streamKey]map[*Subscriber]struct{})}
}

// Subscribe 注册一条连接并返回订阅句柄；该 (tenant, user) 在线连接数已达
// MaxConnsPerStream 时返回 (nil, false)，调用方（传输层 handler）应拒绝
// 建流（SSE 429 / WS 关闭，#85 安全审计 NV2）。调用方负责在断开时
// Unsubscribe（断开清理，spec §4.5.3）。
func (h *Hub) Subscribe(tenantID, userID int64) (*Subscriber, bool) {
	s := &Subscriber{
		id:     h.nextID.Add(1),
		tenant: tenantID,
		user:   userID,
		ch:     make(chan Event, subscriberBuffer),
		done:   make(chan struct{}),
		hub:    h,
		key:    streamKey{tenantID, userID},
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	k := s.key
	m, ok := h.subs[k]
	if !ok {
		m = make(map[*Subscriber]struct{})
		h.subs[k] = m
	}
	if len(m) >= MaxConnsPerStream {
		return nil, false
	}
	m[s] = struct{}{}
	return s, true
}

// Unsubscribe 移除连接并关闭其 Done 通道（幂等，可安全 defer 重复调用）。
func (h *Hub) Unsubscribe(s *Subscriber) {
	if s == nil {
		return
	}
	h.mu.Lock()
	if m, ok := h.subs[s.key]; ok {
		delete(m, s)
		if len(m) == 0 {
			delete(h.subs, s.key)
		}
	}
	h.mu.Unlock()
	s.closeOnce.Do(func() { close(s.done) })
}

// Subscribers 返回指定 (tenant, user) 当前的在线连接数（观测/测试用）。
func (h *Hub) Subscribers(tenantID, userID int64) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[streamKey{tenantID, userID}])
}

// Publish 向 (tenantID, userID) 的全部在线连接投递事件：
//   - 无订阅者直接返回（离线用户靠轮询，不缓存离线事件）；
//   - 单连接缓冲满丢弃该事件并计数（spec §4.5.1），不阻塞发布方。
func (h *Hub) Publish(tenantID, userID int64, ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs[streamKey{tenantID, userID}] {
		select {
		case s.ch <- ev:
		default:
			s.dropped.Add(1)
		}
	}
}

// Subscriber 是一条连接在 hub 中的订阅句柄。传输层从 Events 读事件、
// 在 Done 关闭（退订）后结束读循环；Dropped 供观测慢消费者。
type Subscriber struct {
	id        uint64
	tenant    int64
	user      int64
	ch        chan Event
	done      chan struct{}
	closeOnce sync.Once
	dropped   atomic.Uint64

	hub *Hub
	key streamKey
}

// Events 返回事件接收通道（缓冲 subscriberBuffer）。
func (s *Subscriber) Events() <-chan Event { return s.ch }

// Done 在退订（Unsubscribe）后关闭，传输层据此终止读循环。
func (s *Subscriber) Done() <-chan struct{} { return s.done }

// Dropped 返回本连接因缓冲满被丢弃的事件数。
func (s *Subscriber) Dropped() uint64 { return s.dropped.Load() }

// Close 退订本连接（等价 hub.Unsubscribe(s)）。
func (s *Subscriber) Close() { s.hub.Unsubscribe(s) }
