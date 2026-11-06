package service

import (
	"context"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

func (s *Service) ListLoginLogs(ctx context.Context, req *dto.LoginLogPageReq) ([]model.LoginLog, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.LoginLogFilter{Username: req.Username, LoginIP: req.LoginIP, Result: req.Result}
	list, total, err := s.repo.ListLoginLogs(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	s.fillLoginLocations(list)
	return list, total, nil
}

// fillLoginLocations 为分页结果补归属地（issue #82，spec §4.8.5）：
// 解析器由 internal/app 注入（iploc + 10000 条 LRU）；未注入（未配置
// ip-location.xdb-path）或单个 IP 定位失败时对应行为空串，
// 不影响分页本身（iploc.Resolver 契约：永远返回串、不返回错误）。
func (s *Service) fillLoginLocations(list []model.LoginLog) {
	if s.ipResolver == nil {
		return
	}
	for i := range list {
		if list[i].LoginIP == "" {
			continue
		}
		list[i].LoginLocation = s.ipResolver.Resolve(list[i].LoginIP)
	}
}

func (s *Service) ClearLoginLogs(ctx context.Context) error {
	return s.repo.ClearLoginLogs(ctx)
}

func (s *Service) ListOperateLogs(ctx context.Context, req *dto.OperateLogPageReq) ([]model.OperateLog, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.OperateLogFilter{Username: req.Username, Module: req.Module, Status: req.Status}
	return s.repo.ListOperateLogs(ctx, filter, page.Offset(), page.PageSize)
}

func (s *Service) DeleteOperateLogs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.repo.DeleteOperateLogs(ctx, ids)
}

func (s *Service) ClearOperateLogs(ctx context.Context) error {
	return s.repo.ClearOperateLogs(ctx)
}

// RecordOperateLog 写入操作日志。
func (s *Service) RecordOperateLog(ctx context.Context, log *model.OperateLog) {
	if err := s.repo.CreateOperateLog(ctx, log); err != nil {
		s.logError("write operate log", err)
	}
}

// CleanLogsBefore 清理指定天数之前的日志。
func (s *Service) CleanLogsBefore(ctx context.Context, days int) error {
	if days <= 0 {
		days = 30
	}
	return s.repo.CleanLogsBefore(ctx, time.Now().AddDate(0, 0, -days))
}
