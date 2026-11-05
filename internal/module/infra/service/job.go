package service

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/infra/dto"
	"github.com/lautung/go-admin-pro/internal/module/infra/model"
	"github.com/lautung/go-admin-pro/internal/module/infra/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
	"github.com/robfig/cron/v3"
)

// HandlerFunc 是任务处理器签名。
type HandlerFunc func(ctx context.Context, param string) error

// PlatformTenantID 是平台租户 ID：种子租户 1（内置管理后台）。
// 系统级任务处理器（跨租户动作，如公海自动回收/日志清理）只允许该租户的管理员
// 创建、修改、启用与运行；普通租户即使持有 super_admin（"*"）也不能操作。
// 与 tenant.DefaultID 取同值，但语义不同：这里是「平台管理员」的准入边界，
// 不是缺省租户回退值（PR #39 审查 P0）。
const PlatformTenantID int64 = 1

// errSystemJobForbidden 是系统级任务越权操作的统一文案（HTTP 403）。
func errSystemJobForbidden() error {
	return xerr.Forbidden("系统级任务仅平台管理员可操作")
}

var cronParser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// JobScheduler 基于 robfig/cron 的定时任务调度器，支持多租户。
type JobScheduler struct {
	repo     *repository.Repository
	cron     *cron.Cron
	mu       sync.Mutex
	entries  map[int64]cron.EntryID
	handlers map[string]HandlerFunc
	// systemHandlers 记录系统级处理器（跨租户动作）：只有平台租户可注册任务、
	// 查看处理器与执行；调度/执行入口另有二次防线，防止修复前遗留的行被触发。
	systemHandlers map[string]struct{}
}

func NewJobScheduler(repo *repository.Repository) *JobScheduler {
	s := &JobScheduler{
		repo:           repo,
		cron:           cron.New(cron.WithSeconds()),
		entries:        make(map[int64]cron.EntryID),
		handlers:       make(map[string]HandlerFunc),
		systemHandlers: make(map[string]struct{}),
	}
	// 内置演示任务，便于验证定时任务链路。
	s.Register("demoJob", func(ctx context.Context, param string) error {
		slog.Info("demo job executed", "tenant", tenant.ID(ctx), "param", param)
		return nil
	})
	return s
}

// Register 注册普通任务处理器（任何租户可用）。
func (s *JobScheduler) Register(name string, fn HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[name] = fn
}

// RegisterSystem 注册系统级任务处理器：处理器会跨租户执行，只有平台租户
// （PlatformTenantID）的管理员可创建/修改/启用/运行，其他租户一律 403。
func (s *JobScheduler) RegisterSystem(name string, fn HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[name] = fn
	s.systemHandlers[name] = struct{}{}
}

// IsSystemHandler 判断处理器是否为系统级（跨租户）动作。
func (s *JobScheduler) IsSystemHandler(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.systemHandlers[name]
	return ok
}

// Handlers 返回已注册的处理器名称（全量，供平台租户与内部使用）。
func (s *JobScheduler) Handlers() []string {
	return s.handlersFor(0)
}

// HandlersForTenant 返回指定租户可见的处理器名称：非平台租户过滤掉系统级
// 处理器，避免普通租户管理员在「定时任务」页面看到不可用的跨租户动作。
func (s *JobScheduler) HandlersForTenant(tenantID int64) []string {
	return s.handlersFor(tenantID)
}

// handlersFor 是全量列表的实现：tenantID 为 PlatformTenantID（或 0，表示
// 内部调用）时返回全部，其余租户过滤系统级处理器。
func (s *JobScheduler) handlersFor(tenantID int64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.handlers))
	for name := range s.handlers {
		if tenantID != 0 && tenantID != PlatformTenantID {
			if _, system := s.systemHandlers[name]; system {
				continue
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *JobScheduler) handler(name string) (HandlerFunc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn, ok := s.handlers[name]
	return fn, ok
}

// Validate 校验 cron 表达式（6 段，含秒）。
func (s *JobScheduler) Validate(expr string) error {
	if _, err := cronParser.Parse(expr); err != nil {
		return xerr.BadRequest("cron 表达式不合法：" + err.Error())
	}
	return nil
}

// Start 加载所有启用任务并启动调度。
func (s *JobScheduler) Start(ctx context.Context) {
	jobs, err := s.repo.ListEnabledJobs(ctx)
	if err != nil {
		slog.Error("load jobs failed", "err", err)
		return
	}
	count := 0
	for _, job := range jobs {
		if err := s.Schedule(job); err != nil {
			slog.Error("schedule job failed", "jobId", job.ID, "name", job.Name, "err", err)
			continue
		}
		count++
	}
	s.cron.Start()
	slog.Info("job scheduler started", "scheduled", count, "total", len(jobs))
}

// Stop 停止调度器。
func (s *JobScheduler) Stop() {
	ctx := s.cron.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

// Schedule 注册任务。
func (s *JobScheduler) Schedule(job model.Job) error {
	if job.Status != 0 {
		return nil
	}
	// 二次防线：修复前遗留的「普通租户 + 系统级 handler」行不得进入调度，
	// 即使绕过创建/修改入口直接改库也不会被执行。
	if s.IsSystemHandler(job.HandlerName) && job.TenantID != PlatformTenantID {
		return errSystemJobForbidden()
	}
	if err := s.Validate(job.CronExpression); err != nil {
		return err
	}
	s.Remove(job.ID)
	entryID, err := s.cron.AddFunc(job.CronExpression, func() {
		s.Execute(job)
	})
	if err != nil {
		return xerr.BadRequest("cron 表达式不合法：" + err.Error())
	}
	s.mu.Lock()
	s.entries[job.ID] = entryID
	s.mu.Unlock()
	return nil
}

// Remove 取消任务调度。
func (s *JobScheduler) Remove(jobID int64) {
	s.mu.Lock()
	entryID, ok := s.entries[jobID]
	delete(s.entries, jobID)
	s.mu.Unlock()
	if ok {
		s.cron.Remove(entryID)
	}
}

// NextRun 返回任务下次执行时间。
func (s *JobScheduler) NextRun(jobID int64) *time.Time {
	s.mu.Lock()
	entryID, ok := s.entries[jobID]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	entry := s.cron.Entry(entryID)
	if entry.ID == 0 || entry.Next.IsZero() {
		return nil
	}
	next := entry.Next
	return &next
}

// Execute 执行任务并写入日志，失败按 retryCount 重试。
func (s *JobScheduler) Execute(job model.Job) {
	ctx := tenant.WithTenantID(context.Background(), job.TenantID)
	begin := time.Now()

	fn, ok := s.handler(job.HandlerName)
	attempts := job.RetryCount + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	if !ok {
		lastErr = fmt.Errorf("未注册的任务处理器：%s", job.HandlerName)
		attempts = 1
	} else if s.IsSystemHandler(job.HandlerName) && job.TenantID != PlatformTenantID {
		// 二次防线（PR #39 审查 P0）：系统级 handler 会以 tenant.Skip 跨租户
		// 执行，非平台租户的任务即使已落库也不得真正运行。
		ok = false
		lastErr = errSystemJobForbidden()
		attempts = 1
	}
	for i := 0; i < attempts && ok; i++ {
		lastErr = invoke(fn, ctx, job.HandlerParam)
		if lastErr == nil {
			break
		}
		slog.Warn("job attempt failed", "jobId", job.ID, "attempt", i+1, "err", lastErr)
	}

	end := time.Now()
	result := int8(0)
	message := "执行成功"
	if lastErr != nil {
		result = 1
		message = lastErr.Error()
	}
	logRow := &model.JobLog{
		TenantID:       job.TenantID,
		JobID:          job.ID,
		HandlerName:    job.HandlerName,
		HandlerParam:   job.HandlerParam,
		CronExpression: job.CronExpression,
		BeginTime:      begin,
		EndTime:        &end,
		Duration:       end.Sub(begin).Milliseconds(),
		Result:         result,
		Message:        message,
	}
	if err := s.repo.CreateJobLog(ctx, logRow); err != nil {
		slog.Error("write job log failed", "jobId", job.ID, "err", err)
	}
	if err := s.repo.UpdateJobColumns(ctx, job.ID, map[string]interface{}{"last_run_at": begin}); err != nil {
		slog.Error("update job last run failed", "jobId", job.ID, "err", err)
	}
}

func invoke(fn HandlerFunc, ctx context.Context, param string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("任务执行 panic：%v", r)
			slog.Error("job panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	return fn(ctx, param)
}

// ---------- 任务业务方法 ----------

func (s *Service) ListJobs(ctx context.Context, req *dto.JobPageReq) ([]model.Job, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.JobFilter{Name: req.Name, HandlerName: req.HandlerName, Status: req.Status}
	list, total, err := s.repo.ListJobs(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range list {
		list[i].NextRunAt = s.scheduler.NextRun(list[i].ID)
	}
	return list, total, nil
}

func (s *Service) GetJob(ctx context.Context, id int64) (*model.Job, error) {
	job, err := s.repo.FindJobByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, xerr.NotFound("定时任务不存在")
	}
	job.NextRunAt = s.scheduler.NextRun(job.ID)
	return job, nil
}

// ListJobHandlers 返回当前租户可注册的处理器名称：系统级处理器
// （跨租户动作）只对平台租户展示（PR #39 审查 P0）。
func (s *Service) ListJobHandlers(ctx context.Context) []string {
	return s.scheduler.HandlersForTenant(tenant.ID(ctx))
}

// ensureJobHandlerAllowed 校验当前租户能否操作该系统级处理器：
// 系统级处理器跨租户执行，只有平台租户管理员可创建/修改/启用/运行。
func (s *Service) ensureJobHandlerAllowed(ctx context.Context, handlerName string) error {
	if s.scheduler.IsSystemHandler(handlerName) && tenant.ID(ctx) != PlatformTenantID {
		return errSystemJobForbidden()
	}
	return nil
}

func (s *Service) CreateJob(ctx context.Context, req *dto.JobCreateReq) error {
	if err := s.scheduler.Validate(req.CronExpression); err != nil {
		return err
	}
	if _, ok := s.scheduler.handler(req.HandlerName); !ok {
		return xerr.BadRequest("未注册的任务处理器：" + req.HandlerName)
	}
	if err := s.ensureJobHandlerAllowed(ctx, req.HandlerName); err != nil {
		return err
	}
	if req.Status != 0 && req.Status != 1 {
		return xerr.BadRequest("非法的任务状态")
	}
	job := &model.Job{
		Name:           req.Name,
		HandlerName:    req.HandlerName,
		HandlerParam:   req.HandlerParam,
		CronExpression: req.CronExpression,
		RetryCount:     req.RetryCount,
		Status:         req.Status,
		Remark:         req.Remark,
	}
	if err := s.repo.CreateJob(ctx, job); err != nil {
		return err
	}
	if err := s.scheduler.Schedule(*job); err != nil {
		return err
	}
	return nil
}

func (s *Service) UpdateJob(ctx context.Context, req *dto.JobUpdateReq) error {
	job, err := s.repo.FindJobByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if job == nil {
		return xerr.NotFound("定时任务不存在")
	}
	if err := s.scheduler.Validate(req.CronExpression); err != nil {
		return err
	}
	if _, ok := s.scheduler.handler(req.HandlerName); !ok {
		return xerr.BadRequest("未注册的任务处理器：" + req.HandlerName)
	}
	// 同时校验原 handler 与新 handler：禁止普通租户把遗留的系统级任务
	// 改成别的名字后继续操作，也禁止把普通任务改成系统级 handler。
	if err := s.ensureJobHandlerAllowed(ctx, job.HandlerName); err != nil {
		return err
	}
	if err := s.ensureJobHandlerAllowed(ctx, req.HandlerName); err != nil {
		return err
	}
	job.Name = req.Name
	job.HandlerName = req.HandlerName
	job.HandlerParam = req.HandlerParam
	job.CronExpression = req.CronExpression
	job.RetryCount = req.RetryCount
	job.Status = req.Status
	job.Remark = req.Remark
	if err := s.repo.SaveJob(ctx, job); err != nil {
		return err
	}
	s.scheduler.Remove(job.ID)
	if job.Status == 0 {
		if err := s.scheduler.Schedule(*job); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) UpdateJobStatus(ctx context.Context, id int64, status int8) error {
	job, err := s.repo.FindJobByID(ctx, id)
	if err != nil {
		return err
	}
	if job == nil {
		return xerr.NotFound("定时任务不存在")
	}
	if status != 0 && status != 1 {
		return xerr.BadRequest("非法的任务状态")
	}
	if err := s.ensureJobHandlerAllowed(ctx, job.HandlerName); err != nil {
		return err
	}
	job.Status = status
	if err := s.repo.SaveJob(ctx, job); err != nil {
		return err
	}
	s.scheduler.Remove(job.ID)
	if status == 0 {
		return s.scheduler.Schedule(*job)
	}
	return nil
}

func (s *Service) DeleteJob(ctx context.Context, id int64) error {
	job, err := s.repo.FindJobByID(ctx, id)
	if err != nil {
		return err
	}
	if job == nil {
		return xerr.NotFound("定时任务不存在")
	}
	s.scheduler.Remove(id)
	return s.repo.DeleteJob(ctx, id)
}

// RunJob 立即执行一次任务（异步）。
func (s *Service) RunJob(ctx context.Context, id int64) error {
	job, err := s.repo.FindJobByID(ctx, id)
	if err != nil {
		return err
	}
	if job == nil {
		return xerr.NotFound("定时任务不存在")
	}
	if err := s.ensureJobHandlerAllowed(ctx, job.HandlerName); err != nil {
		return err
	}
	go s.scheduler.Execute(*job)
	return nil
}

func (s *Service) ListJobLogs(ctx context.Context, req *dto.JobLogPageReq) ([]dto.JobLogVO, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.JobLogFilter{JobID: req.JobID, HandlerName: req.HandlerName, Result: req.Result}
	list, total, err := s.repo.ListJobLogs(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	jobNames := make(map[int64]string)
	result := make([]dto.JobLogVO, 0, len(list))
	for _, item := range list {
		name, ok := jobNames[item.JobID]
		if !ok {
			if job, err := s.repo.FindJobByID(ctx, item.JobID); err == nil && job != nil {
				name = job.Name
			}
			jobNames[item.JobID] = name
		}
		result = append(result, dto.JobLogVO{
			ID:             item.ID,
			JobID:          item.JobID,
			JobName:        name,
			HandlerName:    item.HandlerName,
			HandlerParam:   item.HandlerParam,
			CronExpression: item.CronExpression,
			BeginTime:      item.BeginTime,
			EndTime:        item.EndTime,
			Duration:       item.Duration,
			Result:         item.Result,
			Message:        item.Message,
			CreateTime:     item.CreatedAt,
		})
	}
	return result, total, nil
}
