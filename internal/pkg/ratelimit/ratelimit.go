// Package ratelimit 提供进程内固定窗口限速器（#85 安全审计 NV1/NV2 加固）：
// 公开端点按来源 IP、换票端点按登录用户等场景的轻量限速。
//
// 单实例语义：计数只在本进程内存有效，多实例部署或更精细的策略
// （如令牌桶、按路径分级）由网关/反代层（如 nginx limit_req）另配。
package ratelimit

import (
	"sync"
	"time"
)

// maxKeys 是限速器同时跟踪的 key 数上限：达到即先清理已过期的窗口。
// 防线定位是「误用与轻度滥用」，不能防住伪造海量 key 的定向攻击
// （那需要反代层限速），只保证进程内存不被吃穿。
const maxKeys = 65536

// windowCount 是一个 key 的当前窗口：起点 + 已放行次数。
type windowCount struct {
	start time.Time
	count int
}

// Limiter 是按 key 的固定窗口计数限速器：每个 key 在长度为 window 的
// 窗口内最多放行 limit 次，窗口翻新后计数归零。并发安全。
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	counts map[string]windowCount
}

// New 创建「每 window 最多 limit 次」的限速器；limit 或 window 非正时
// 退化为全放行（未配置限速是合法状态）。
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		limit:  limit,
		window: window,
		counts: make(map[string]windowCount),
	}
}

// Allow 报告 key 的本次请求是否放行。放行即计数（含窗口内第 limit 次），
// 超限返回 false 且不计数。
func (l *Limiter) Allow(key string) bool {
	if l.limit <= 0 || l.window <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.counts) >= maxKeys {
		l.pruneLocked(now)
	}
	w, ok := l.counts[key]
	if !ok || now.Sub(w.start) >= l.window {
		// 新窗口：起点取当前时刻，本次请求计入第 1 次。
		l.counts[key] = windowCount{start: now, count: 1}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	l.counts[key] = w
	return true
}

// pruneLocked 清理已过期（整个窗口已翻新）的 key，调用方持锁。
func (l *Limiter) pruneLocked(now time.Time) {
	for k, w := range l.counts {
		if now.Sub(w.start) >= l.window {
			delete(l.counts, k)
		}
	}
}
