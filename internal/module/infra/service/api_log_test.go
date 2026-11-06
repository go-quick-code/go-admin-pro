package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
)

// fakeAPILogSink 记录批量写入调用，供断言。
type fakeAPILogSink struct {
	mu     sync.Mutex
	calls  [][]model.APILog
	err    error // 非 nil 时批量写入失败
	signal chan struct{}
}

func (f *fakeAPILogSink) BatchCreateAPILogs(ctx context.Context, logs []model.APILog) error {
	f.mu.Lock()
	f.calls = append(f.calls, logs)
	f.mu.Unlock()
	if f.signal != nil {
		f.signal <- struct{}{}
	}
	return f.err
}

func (f *fakeAPILogSink) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += len(c)
	}
	return n
}

// newTestWriter 构造小容量的测试 writer 并启动后台 goroutine。
func newTestWriter(t *testing.T, sink APILogSink, cfg APILogWriterConfig) *APILogWriter {
	t.Helper()
	w := NewAPILogWriter(sink, cfg)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = w.Close(ctx)
	})
	return w
}

// Flush 后测试可见：Enqueue 的条目在 Flush(ctx) 返回时必须已全部落库。
func TestAPILogWriterFlushPersistsEnqueuedEntries(t *testing.T) {
	sink := &fakeAPILogSink{}
	w := newTestWriter(t, sink, APILogWriterConfig{QueueSize: 64, BatchSize: 200, FlushInterval: time.Hour})
	w.Run()

	for i := 0; i < 5; i++ {
		w.Enqueue(model.APILog{RequestPath: "/x", TenantID: 1})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := sink.total(); got != 5 {
		t.Fatalf("Flush 后落库 %d 条，期望 5", got)
	}
}

// 批量 200 条落库：攒满 batch 即写，不等 1 秒。
func TestAPILogWriterBatchesBySize(t *testing.T) {
	sink := &fakeAPILogSink{}
	w := newTestWriter(t, sink, APILogWriterConfig{QueueSize: 64, BatchSize: 3, FlushInterval: time.Hour})
	w.Run()

	for i := 0; i < 3; i++ {
		w.Enqueue(model.APILog{RequestPath: "/x", TenantID: 1})
	}
	// 攒满一批由后台 goroutine 异步落库，轮询等待（不等 Flush）。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sink.total() < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.total(); got != 3 {
		t.Fatalf("攒满批应落库 3 条，实际 %d", got)
	}
}

// 1 秒（测试缩到毫秒级）超时落库：未攒满批也要定时刷出。
func TestAPILogWriterBatchesByInterval(t *testing.T) {
	sink := &fakeAPILogSink{}
	w := newTestWriter(t, sink, APILogWriterConfig{QueueSize: 64, BatchSize: 100, FlushInterval: 20 * time.Millisecond})
	w.Run()

	w.Enqueue(model.APILog{RequestPath: "/x", TenantID: 1})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sink.total() < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.total(); got != 1 {
		t.Fatalf("超时应刷出 1 条，实际 %d", got)
	}
}

// 队列满：不阻塞请求、丢弃计数，每 100 条告警一次（告警走 slog，这里断言计数）。
// 不启动后台循环，保证丢弃计数的确定性；「入队即可被 Flush 落库」由
// TestAPILogWriterFlushPersistsEnqueuedEntries 覆盖。
func TestAPILogWriterDropsWhenQueueFull(t *testing.T) {
	sink := &fakeAPILogSink{}
	w := NewAPILogWriter(sink, APILogWriterConfig{QueueSize: 2, BatchSize: 200, FlushInterval: time.Hour})

	for i := 0; i < 250; i++ {
		w.Enqueue(model.APILog{RequestPath: "/x", TenantID: 1})
	}
	if got := w.Dropped(); got != 248 {
		t.Fatalf("队列容量 2 时入队 250 条应丢弃 248 条，实际 %d", got)
	}
}

// 写日志失败绝不影响请求：sink 报错时 Flush 返回错误但不 panic，
// 后续写入（修复后）仍能落库。
func TestAPILogWriterSurvivesSinkFailure(t *testing.T) {
	sink := &fakeAPILogSink{err: errors.New("db down")}
	w := newTestWriter(t, sink, APILogWriterConfig{QueueSize: 64, BatchSize: 200, FlushInterval: time.Hour})
	w.Run()

	w.Enqueue(model.APILog{RequestPath: "/x", TenantID: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// 失败也要等到 Flush 返回（失败不吞）：这里只断言不 panic、可继续工作。
	if err := w.Flush(ctx); err == nil {
		t.Fatal("sink 失败时 Flush 应返回错误")
	}

	// 修复后继续写。
	sink.mu.Lock()
	sink.err = nil
	sink.calls = nil // 清掉失败调用的记录，只统计修复后的写入
	sink.mu.Unlock()
	w.Enqueue(model.APILog{RequestPath: "/y", TenantID: 1})
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("修复后 Flush: %v", err)
	}
	if got := sink.total(); got != 1 {
		t.Fatalf("修复后应落库 1 条，实际 %d", got)
	}
}

// Close：排干队列后退出，Close 返回后无遗留条目。
func TestAPILogWriterCloseDrainsQueue(t *testing.T) {
	sink := &fakeAPILogSink{}
	w := NewAPILogWriter(sink, APILogWriterConfig{QueueSize: 64, BatchSize: 200, FlushInterval: time.Hour})
	w.Run()
	for i := 0; i < 7; i++ {
		w.Enqueue(model.APILog{RequestPath: "/x", TenantID: 1})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := sink.total(); got != 7 {
		t.Fatalf("Close 应排干队列 7 条，实际 %d", got)
	}
}
