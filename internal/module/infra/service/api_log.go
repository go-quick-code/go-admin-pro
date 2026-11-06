package service

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- API 访问日志异步写（issue #80，spec §4.6.5，ADR 0014）----------
//
// 写入走进程内有界队列 + 批量插入，把日志写入从请求路径移开：中间件在请求
// 完成后只做一次非阻塞入队，后台 goroutine 攒满 batchSize（默认 200）条或
// flushInterval（默认 1 秒）落库一次。队列满丢弃并计数（每 100 条告警一次），
// 绝不阻塞请求；写库失败记日志，绝不影响请求。代价（ADR 0014）：进程崩溃时
// 队列中未落库的日志会丢失；多实例各自缓冲，排序不保证。

const (
	// DefaultAPILogQueueSize 是访问日志队列容量（spec §4.6.5：4096）。
	DefaultAPILogQueueSize = 4096
	// DefaultAPILogBatchSize 是批量落库条数（spec §4.6.5：200 条或 1 秒）。
	DefaultAPILogBatchSize = 200
	// DefaultAPILogFlushInterval 是批量落库的时间间隔。
	DefaultAPILogFlushInterval = time.Second
	// APILogDropAlertEvery 是丢弃告警的间隔：每丢弃 100 条告警一次。
	APILogDropAlertEvery = 100
)

// APILogSink 是访问日志批量落库接缝：仓储满足它，测试用替身替换。
type APILogSink interface {
	BatchCreateAPILogs(ctx context.Context, logs []model.APILog) error
}

// APILogEnqueuer 是中间件对异步写的窄依赖：只暴露非阻塞入队
// （*APILogWriter 满足；测试可用收集替身替换）。
type APILogEnqueuer interface {
	Enqueue(entry model.APILog)
}

// APILogWriterConfig 允许测试收窄队列/批量/间隔；零值字段取默认值。
type APILogWriterConfig struct {
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
}

// APILogWriter 是访问日志的进程内异步写：有界队列 + 批量落库。
// 生命周期：New → Run（启动后台 goroutine）→ Flush（测试/关闭前排干）
// → Close（排干并退出）。Enqueue 永不阻塞、永不报错。
type APILogWriter struct {
	sink  APILogSink
	queue chan model.APILog
	batch int
	tick  time.Duration
	// flushCh 是 Flush 的握手通道：后台排干当前队列后应答。缓冲为 0，
	// 保证应答只在排干动作完成后送达。
	flushCh chan chan error
	// closeCh 关闭即通知后台排干退出；doneCh 在后台退出后关闭。
	closeCh chan struct{}
	doneCh  chan struct{}
	// dropped 累计因队列满而丢弃的条数（Dropped 供测试与监控读取）。
	dropped atomic.Uint64
}

// NewAPILogWriter 创建 writer；cfg 零值字段取 spec 默认（4096/200/1s）。
func NewAPILogWriter(sink APILogSink, cfg APILogWriterConfig) *APILogWriter {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultAPILogQueueSize
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultAPILogBatchSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultAPILogFlushInterval
	}
	return &APILogWriter{
		sink:    sink,
		queue:   make(chan model.APILog, cfg.QueueSize),
		batch:   cfg.BatchSize,
		tick:    cfg.FlushInterval,
		flushCh: make(chan chan error),
		closeCh: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Run 启动后台落库 goroutine，进程生命周期内只调用一次。
func (w *APILogWriter) Run() {
	go w.loop()
}

// Enqueue 非阻塞入队：队列满时丢弃、计数并每 100 条告警一次。
// 中间件在请求完成后调用，任何情况下不得阻塞或报错（spec §4.6.5/§4.6.6）。
func (w *APILogWriter) Enqueue(entry model.APILog) {
	select {
	case w.queue <- entry:
	default:
		n := w.dropped.Add(1)
		if n%APILogDropAlertEvery == 0 {
			slog.Warn("api 访问日志队列已满，丢弃写入", "dropped", n)
		}
	}
}

// Dropped 返回累计丢弃条数（测试与监控用）。
func (w *APILogWriter) Dropped() uint64 { return w.dropped.Load() }

// Flush 排干当前队列并落库后返回：供集成测试在断言前同步、优雅关闭前排干。
// 本轮落库失败时返回该次错误（失败同样不影响已完成的请求）。
func (w *APILogWriter) Flush(ctx context.Context) error {
	ack := make(chan error, 1)
	select {
	case w.flushCh <- ack:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-ack:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close 通知后台排干队列并退出，等待退出完成（供优雅关闭）。
// 排干期间的落库失败只记日志——Close 的职责是尽力排干，不阻塞退出。
func (w *APILogWriter) Close(ctx context.Context) error {
	select {
	case <-w.closeCh:
	default:
		close(w.closeCh)
	}
	select {
	case <-w.doneCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// loop 是后台落库循环：攒满 batch 落库、超时落库、Flush 排干、Close 收尾。
func (w *APILogWriter) loop() {
	defer close(w.doneCh)
	batch := make([]model.APILog, 0, w.batch)
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()

	// writeErr 记录本轮（Flush 周期内）落库失败，作为 Flush 的应答值。
	var writeErr error
	// write 落库当前攒批并清空；失败记日志（不重试、不 panic，进程内队列的
	// 日志允许丢失，ADR 0014），并保存到 writeErr 供 Flush 应答。
	write := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := w.sink.BatchCreateAPILogs(ctx, batch)
		cancel()
		if err != nil {
			slog.Error("api 访问日志批量落库失败", "count", len(batch), "err", err)
			writeErr = err
		}
		batch = make([]model.APILog, 0, w.batch)
	}
	// drain 非阻塞地把队列里现存的条目搬进攒批，攒满即落库。
	drain := func() {
		for {
			select {
			case e := <-w.queue:
				batch = append(batch, e)
				if len(batch) >= w.batch {
					write()
				}
			default:
				return
			}
		}
	}

	for {
		select {
		case e, ok := <-w.queue:
			if !ok {
				write()
				return
			}
			batch = append(batch, e)
			if len(batch) >= w.batch {
				write()
			}
		case <-ticker.C:
			write()
		case ack := <-w.flushCh:
			writeErr = nil
			drain()
			write()
			ack <- writeErr
		case <-w.closeCh:
			// 收尾：排干残余后退出。此后 Enqueue 的写入不再保证落库
			//（优雅关闭链路里 HTTP server 已先停止，不会再有新请求）。
			drain()
			write()
			return
		}
	}
}

// ---------- 访问日志查询/删除/清理（issue #80，spec §5.5/§4.6.7）----------

const apilogTimeLayout = "2006-01-02 15:04:05"

// ListAPILogs 按筛选分页查询当前租户的访问日志。
func (s *Service) ListAPILogs(ctx context.Context, req *dto.APILogPageReq) ([]model.APILog, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.APILogFilter{
		Path:     req.Path,
		Status:   req.Status,
		Username: req.Username,
		UserID:   req.UserID,
	}
	// 时间段按业务时区解析（ADR 0006）；解析失败视为参数错误，不静默忽略。
	if req.BeginTime != "" {
		t, err := time.ParseInLocation(apilogTimeLayout, req.BeginTime, s.cfg.Location())
		if err != nil {
			return nil, 0, xerr.BadRequest("beginTime 格式应为 YYYY-MM-DD HH:mm:ss")
		}
		filter.BeginTime = &t
	}
	if req.EndTime != "" {
		t, err := time.ParseInLocation(apilogTimeLayout, req.EndTime, s.cfg.Location())
		if err != nil {
			return nil, 0, xerr.BadRequest("endTime 格式应为 YYYY-MM-DD HH:mm:ss")
		}
		filter.EndTime = &t
	}
	return s.repo.ListAPILogs(ctx, filter, page.Offset(), page.PageSize)
}

// DeleteAPILogs 按 IDs 硬删除访问日志。删除前校验全部 ID 都属于当前租户：
// 有任一 ID 跨租户/不存在即 404 且不删（fail-closed，不泄露哪个 ID 越权，
// spec §4.6「跨租户业务 ID 一律 404」的批量语义）。
func (s *Service) DeleteAPILogs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return xerr.BadRequest("请选择要删除的日志")
	}
	n, err := s.repo.CountAPILogsByIDs(ctx, ids)
	if err != nil {
		return err
	}
	if n != int64(len(ids)) {
		return xerr.NotFound("日志不存在或无权访问")
	}
	return s.repo.DeleteAPILogs(ctx, ids)
}

// ClearAPILogs 清空当前租户的全部访问日志（硬删除，租户插件限定）。
func (s *Service) ClearAPILogs(ctx context.Context) error {
	return s.repo.ClearAPILogs(ctx)
}

// CleanAccessLogsBefore 清理指定天数之前的访问日志，供 cleanAccessLogs 系统
// 级定时任务调用（每日 03:00，spec §4.6.7）。days 非正时取配置保留期
// （access-log.retention-days，默认 30）；定时任务也可传 handler_param 覆盖。
func (s *Service) CleanAccessLogsBefore(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		days = s.cfg.AccessLog.RetentionDays
	}
	if days <= 0 {
		days = 30
	}
	return s.repo.CleanAPILogsBefore(ctx, time.Now().AddDate(0, 0, -days))
}
