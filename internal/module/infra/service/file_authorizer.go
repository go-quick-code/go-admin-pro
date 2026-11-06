package service

import (
	"context"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// FileAction 是通用 infra 文件端点上的业务动作：下载/分页列表按 Read 授权，
// 删除按 Delete 授权。业务授权器可以按动作区分「可见」与「可管理」。
type FileAction int

const (
	// FileActionRead 读取业务文件（GET /infra/file/:id/download 与分页列表）。
	FileActionRead FileAction = iota + 1
	// FileActionDelete 删除业务文件（DELETE /infra/file/:id）。
	FileActionDelete
)

// FileAuthorizer 判定当前登录主体能否对业务文件执行 action（PR #66 安全
// 审计 finding 1）。infra 平台层只认 biz_type/biz_id，业务语义由业务模块在
// app 装配期注册；未注册授权器的业务类型一律 fail-closed（按不存在处理）。
//
// bizID 是 infra_file.biz_id。bizID == 0 只出现在分页列表的「类型级管理
// 视角」请求（未指定 bizId）中，由授权器自行决定是否放行。
type FileAuthorizer func(ctx context.Context, bizID int64, action FileAction) error

// FileDeleteSyncer 在通用删除入口授权通过后、删除文件本体前同步业务侧归属
// 行（如 oa_document_file 软删），避免业务列表留下指向已删文件的悬空条目。
// 未注册时通用删除入口只删除 infra_file 本体（适用于没有独立归属行的业务）。
type FileDeleteSyncer func(ctx context.Context, bizID, fileID int64) error

// RegisterFileAuthorizer 注册业务文件授权器（app 装配期调用）：
// infra 不反向依赖业务模块，按 biz_type 维度的判定由业务方提供。
// 同一 bizType 重复注册以后者为准；空 bizType 或 nil fn 忽略。
func (s *Service) RegisterFileAuthorizer(bizType string, fn FileAuthorizer) {
	bizType = strings.TrimSpace(bizType)
	if bizType == "" || fn == nil {
		return
	}
	s.fileAuthMu.Lock()
	defer s.fileAuthMu.Unlock()
	if s.fileAuthorizers == nil {
		s.fileAuthorizers = make(map[string]FileAuthorizer)
	}
	s.fileAuthorizers[bizType] = fn
}

// RegisterFileDeleteSyncer 注册业务归属行同步器（app 装配期调用）：
// 通用删除入口在授权通过后调用它软删/清理业务侧归属行，再删除 infra_file。
// 空 bizType 或 nil fn 忽略。
func (s *Service) RegisterFileDeleteSyncer(bizType string, fn FileDeleteSyncer) {
	bizType = strings.TrimSpace(bizType)
	if bizType == "" || fn == nil {
		return
	}
	s.fileAuthMu.Lock()
	defer s.fileAuthMu.Unlock()
	if s.fileDeleteSyncers == nil {
		s.fileDeleteSyncers = make(map[string]FileDeleteSyncer)
	}
	s.fileDeleteSyncers[bizType] = fn
}

// fileAuthorizerOf 取业务类型的授权器（未注册返回 nil）。
func (s *Service) fileAuthorizerOf(bizType string) FileAuthorizer {
	s.fileAuthMu.RLock()
	defer s.fileAuthMu.RUnlock()
	return s.fileAuthorizers[bizType]
}

// fileDeleteSyncerOf 取业务类型的归属行同步器（未注册返回 nil）。
func (s *Service) fileDeleteSyncerOf(bizType string) FileDeleteSyncer {
	s.fileAuthMu.RLock()
	defer s.fileAuthMu.RUnlock()
	return s.fileDeleteSyncers[bizType]
}

// authorizeFile 对 biz_type != "" 的文件执行业务授权：
// 未注册授权器一律按不存在处理（fail-closed，不泄露存在性）。
func (s *Service) authorizeFile(ctx context.Context, f *model.File, action FileAction) error {
	if f.BizType == "" {
		return nil
	}
	authorizer := s.fileAuthorizerOf(f.BizType)
	if authorizer == nil {
		return xerr.NotFound("文件不存在")
	}
	return authorizer(ctx, f.BizID, action)
}

// AuthorizeFileList 校验「类型级」业务文件列表（GET /infra/file/page 显式
// 传 bizType 时）：未注册授权器返回 403；bizID 语义由授权器决定（0 表示
// 管理视角的全类型列表）。
func (s *Service) AuthorizeFileList(ctx context.Context, bizType string, bizID int64) error {
	authorizer := s.fileAuthorizerOf(bizType)
	if authorizer == nil {
		return xerr.Forbidden("无权查看该业务类型文件")
	}
	return authorizer(ctx, bizID, FileActionRead)
}
