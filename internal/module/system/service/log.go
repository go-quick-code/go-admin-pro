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
	return s.repo.ListLoginLogs(ctx, filter, page.Offset(), page.PageSize)
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
