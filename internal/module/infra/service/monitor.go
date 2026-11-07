package service

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ---------- 监控面板（issue #84，spec §4.10/§5.8）----------
//
// 平台级只读聚合：运行期指标（runtime/database/sql）+ 本模块数据
//（infra_api_log / infra_job）+ 跨模块统计。跨模块部分只依赖本文件声明的
// MonitorStatsGateway 窄接口，由 internal/app 装配期注入适配器，infra 不
// 直接读 system/message 表（ADR 模块边界，同 QuotaGateway 模式）。
// 不做 Prometheus/pprof/链路追踪（spec §4.10.3）。

// ErrMonitorPlatformOnly 是监控面板越权访问的哨兵错误（非平台租户 403）。
var ErrMonitorPlatformOnly = errors.New("监控面板仅平台租户可访问")

// monitorDefaultHours / monitorMaxHours 是 requests 聚合窗口的默认值与上限
// （spec §4.10.2：最近 1–168 小时）。
const (
	monitorDefaultHours = 24
	monitorMaxHours     = 168
)

// monitorTimeLayout 是监控面板对外的时间格式（业务时区，ADR 0006）。
const monitorTimeLayout = "2006-01-02 15:04:05"

// monitorStartedAt 是进程启动时间：包初始化时刻（进程内最先执行的代码之一），
// 与实际 main 起点相差毫秒级，对「运行时长」的展示精度足够。
var monitorStartedAt = time.Now()

// monitorVersion 是应用版本：嵌入 VCS 修订号时取短哈希，否则回退 dev。
var monitorVersion = resolveMonitorVersion()

func resolveMonitorVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return "dev"
}

// MonitorStatsGateway 是监控面板跨模块统计的窄接口：租户/用户数来自
// system，消息发送结果来自 message，由 internal/app 装配期注入适配器。
// infra 只依赖本接口，不反向依赖 system/message（ADR 模块边界）。
type MonitorStatsGateway interface {
	// TenantCount 返回租户总数（未删除，跨全部租户）。
	TenantCount(ctx context.Context) (int64, error)
	// UserCount 返回用户总数（未删除，跨全部租户）。
	UserCount(ctx context.Context) (int64, error)
	// MessageSendStats 返回 since 以来的消息发送成功/失败数（分渠道）。
	MessageSendStats(ctx context.Context, since time.Time) (dto.MonitorMessageInfo, error)
}

// SetMonitorStatsGateway 注入跨模块统计来源（internal/app 装配调用）。
// 未注入时 overview 的租户/用户/消息统计为 0（单测直连 Service 不报错）。
func (s *Service) SetMonitorStatsGateway(gw MonitorStatsGateway) {
	s.monitorStats = gw
}

// requireMonitorPlatform 校验当前租户是平台租户。监控面板暴露全平台的
// 运行指标与规模统计，是平台级能力：非平台租户即使持有
// infra:monitor:query 权限码也一律 403（spec §4.10.3，同数据库文档模式）。
func requireMonitorPlatform(ctx context.Context) error {
	if tenant.ID(ctx) != tenant.DefaultID {
		return xerr.Wrap(ErrMonitorPlatformOnly, http.StatusForbidden, http.StatusForbidden, ErrMonitorPlatformOnly.Error())
	}
	return nil
}

// monitorWindow 把 hours 收敛到 [1,168] 并计算聚合窗口 [start,end)：
// start 对齐整点（now-hours 的落点可能带分钟，直接用会切出半截桶），
// end 取当前时刻。loc 是业务时区（ADR 0006）。
func monitorWindow(hours int, now time.Time, loc *time.Location) (start, end time.Time, normalized int) {
	if hours <= 0 {
		hours = monitorDefaultHours
	}
	if hours > monitorMaxHours {
		hours = monitorMaxHours
	}
	end = now.In(loc)
	raw := end.Add(-time.Duration(hours) * time.Hour)
	start = time.Date(raw.Year(), raw.Month(), raw.Day(), raw.Hour(), 0, 0, 0, loc)
	return start, end, hours
}

// monitorHourBucket 返回时刻所在整点桶的标签（YYYY-MM-DD HH:00）。
func monitorHourBucket(at time.Time, loc *time.Location) string {
	t := at.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Format("2006-01-02 15:04")
}

// MonitorOverview 返回监控面板总览（spec §4.10.1）：
//   - app：应用名/环境/版本/Go 版本/启动时间/运行时长；
//   - runtime：goroutine 数/堆内存/GC 次数（runtime.MemStats）；
//   - dbPool：连接池 open/in-use/idle/wait 与配置上限；
//   - jobs：调度器任务总数/启用数（infra_job 全租户）与注册 handler 数；
//   - tenants：租户数/用户数（窄接口）；
//   - traffic：近 24h 访问量/错误率/平均与最大耗时（infra_api_log 全租户）；
//   - messages：近 24h 消息发送成功/失败数（窄接口）。
func (s *Service) MonitorOverview(ctx context.Context) (*dto.MonitorOverviewResp, error) {
	if err := requireMonitorPlatform(ctx); err != nil {
		return nil, err
	}
	loc := s.cfg.Location()
	now := time.Now().In(loc)

	resp := &dto.MonitorOverviewResp{
		App: dto.MonitorAppInfo{
			Name:          s.cfg.App.Name,
			Env:           s.cfg.App.Env,
			Version:       monitorVersion,
			GoVersion:     runtime.Version(),
			StartTime:     monitorStartedAt.In(loc).Format(monitorTimeLayout),
			UptimeSeconds: int64(now.Sub(monitorStartedAt) / time.Second),
		},
		Runtime: monitorRuntimeInfo(),
	}

	// DB 连接池（database/sql Stats）；取不到底层句柄时保持零值，不让
	// 单项指标失败拖垮整个 overview。
	if sqlDB, err := s.repo.DB().DB(); err == nil {
		stats := sqlDB.Stats()
		resp.DBPool = dto.MonitorDBPoolInfo{
			MaxOpen:    s.cfg.Database.MaxOpenConns,
			Open:       stats.OpenConnections,
			InUse:      stats.InUse,
			Idle:       stats.Idle,
			WaitCount:  stats.WaitCount,
			WaitMillis: stats.WaitDuration.Milliseconds(),
		}
	}

	// 调度器：任务总数/启用数查 infra_job（平台视角全租户），handler 数取
	// 本进程注册表（平台租户可见全量，含系统级）。
	total, enabled, err := s.repo.CountJobs(ctx)
	if err != nil {
		return nil, err
	}
	resp.Jobs = dto.MonitorJobInfo{
		Total:        total,
		Enabled:      enabled,
		HandlerCount: len(s.scheduler.Handlers()),
	}

	// 跨模块统计：未注入网关时保持零值（不报错）。
	if s.monitorStats != nil {
		if resp.Tenants.Count, err = s.monitorStats.TenantCount(ctx); err != nil {
			return nil, err
		}
		if resp.Tenants.Users, err = s.monitorStats.UserCount(ctx); err != nil {
			return nil, err
		}
		if resp.Messages, err = s.monitorStats.MessageSendStats(ctx, now.Add(-24*time.Hour)); err != nil {
			return nil, err
		}
	}

	// 近 24h 流量（infra_api_log 全租户）：错误率 = (4xx+5xx)/总数，保留
	// 1 位小数；总耗时与计数一次查询取齐。
	start, end, _ := monitorWindow(monitorDefaultHours, now, loc)
	reqTotal, clientErr, serverErr, avgMs, maxMs, err := s.repo.SummarizeAPILogs(ctx, start, end)
	if err != nil {
		return nil, err
	}
	resp.Traffic = dto.MonitorTrafficInfo{
		Total: reqTotal,
		AvgMs: avgMs,
		MaxMs: maxMs,
	}
	if reqTotal > 0 {
		resp.Traffic.ErrorRate = float64(clientErr+serverErr) * 100 / float64(reqTotal)
		resp.Traffic.ErrorRate = float64(int(resp.Traffic.ErrorRate*10+0.5)) / 10
	}
	return resp, nil
}

// MonitorRequests 按小时分桶聚合近 hours 小时的访问日志（spec §4.10.2）：
// 请求数、2xx/3xx/4xx/5xx、平均/最大耗时。桶按整点对齐（业务时区），
// 只返回有数据的小时、按时间升序；空数据返回空数组不报错。
func (s *Service) MonitorRequests(ctx context.Context, hours int) (*dto.MonitorRequestsResp, error) {
	if err := requireMonitorPlatform(ctx); err != nil {
		return nil, err
	}
	loc := s.cfg.Location()
	start, end, normalized := monitorWindow(hours, time.Now(), loc)
	rows, err := s.repo.AggregateAPILogsByHour(ctx, start, end)
	if err != nil {
		return nil, err
	}
	resp := &dto.MonitorRequestsResp{
		Hours:   normalized,
		Buckets: make([]dto.MonitorRequestBucket, 0, len(rows)),
	}
	for _, row := range rows {
		resp.Buckets = append(resp.Buckets, dto.MonitorRequestBucket{
			// Hour 取 DATE_FORMAT 的 YYYY-MM-DD HH:00:00，截掉秒位即桶标签。
			Hour:  row.Hour[:16],
			Total: row.Total,
			S2xx:  row.S2xx,
			S3xx:  row.S3xx,
			S4xx:  row.S4xx,
			S5xx:  row.S5xx,
			AvgMs: int64(row.AvgMs + 0.5),
			MaxMs: row.MaxMs,
		})
	}
	return resp, nil
}

// monitorRuntimeInfo 采集 Go 运行期指标（spec §4.10.1）。
func monitorRuntimeInfo() dto.MonitorRuntimeInfo {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return dto.MonitorRuntimeInfo{
		Goroutines: runtime.NumGoroutine(),
		HeapBytes:  int64(ms.HeapAlloc),
		SysBytes:   int64(ms.Sys),
		GCCount:    int64(ms.NumGC),
	}
}
