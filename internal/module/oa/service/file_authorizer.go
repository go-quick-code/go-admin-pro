// file_authorizer.go 是 infra 通用文件端点的 OA 业务授权实现（PR #66 安全
// 审计 finding 1：infra/file/download-delete-bypasses-business-acl）。
//
// infra 平台层不反向依赖业务模块：它只暴露按 biz_type 的授权注册表（见
// internal/module/infra/service/file_authorizer.go），OA 在 internal/app 装配
// 期把本文件的 AuthorizeFileAccess 注册给 infra。授权语义与各业务自己的
// 附件端点保持一致：
//
//   - oa_document（biz_id = 目录 ID）：Read 复用目录读权限（最近祖先 ACL），
//     Delete 复用目录管理权（owner 或 oa:document:manage）；类型级列表
//     （bizID=0）只放行 oa:document:manage；
//   - oa_leave / oa_expense（biz_id = 申请/报销单 ID）：Read 复用可见范围
//     （manage/部门负责人子树/本人），Delete 放行申请人本人或对应 manage 码；
//     类型级列表只放行 manage 码；
//   - oa_announcement（biz_id = 公告 ID）：Read 复用公告可见性，Delete 要求
//     oa:announcement:update 且仅草稿（与业务附件删除同口径）；类型级列表
//     放行 oa:announcement:query（管理查询码）。
//
// 未授权一律按不存在处理（404，不泄露业务对象存在性）；类型级列表无权限
// 返回 403。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// FileAccessAction 是通用 infra 文件端点上的 OA 动作（与 infra 的
// FileAction 同义；由 app 装配层转换，避免 oa 反向依赖 infra）。
type FileAccessAction int

const (
	// FileAccessRead 下载/分页列表。
	FileAccessRead FileAccessAction = iota + 1
	// FileAccessDelete 删除文件本体。
	FileAccessDelete
)

// AuthorizeFileAccess 是 OA 业务文件的通用端点授权入口（app 装配期注册到
// infra 的按 biz_type 授权表）。bizID 为 infra_file.biz_id；bizID == 0 只出现
// 在分页列表的「类型级管理视角」请求中。未注册的业务类型一律 404。
func (s *Service) AuthorizeFileAccess(ctx context.Context, bizType string, bizID int64, action FileAccessAction) error {
	switch bizType {
	case DocumentFileBizType:
		return s.authorizeDocumentFileAccess(ctx, bizID, action)
	case LeaveFileBizType:
		return s.authorizeLeaveFileAccess(ctx, bizID, action)
	case ExpenseFileBizType:
		return s.authorizeExpenseFileAccess(ctx, bizID, action)
	case AnnouncementFileBizType:
		return s.authorizeAnnouncementFileAccess(ctx, bizID, action)
	default:
		return xerr.NotFound("文件不存在")
	}
}

// SyncDocumentFileDelete 在 infra 通用删除入口删除文件本体前同步软删
// oa_document_file 归属行：保持文档删除语义（归属行与 infra 行都软删），
// 业务列表不会留下「列表有、内容 404」的悬空条目。归属行不存在时幂等返回。
func (s *Service) SyncDocumentFileDelete(ctx context.Context, folderID, infraFileID int64) error {
	if folderID <= 0 || infraFileID <= 0 {
		return nil
	}
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return err
	}
	row, err := s.repo.FindDocumentFileByFileID(ctx, folderID, infraFileID)
	if err != nil {
		return err
	}
	if row == nil {
		return nil
	}
	return s.deleteDocumentFileInFolder(ctx, folderID, row.ID, viewer)
}

// authorizeDocumentFileAccess 文档文件：Read = 目录读权限，Delete = 目录管理权。
func (s *Service) authorizeDocumentFileAccess(ctx context.Context, folderID int64, action FileAccessAction) error {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return err
	}
	switch action {
	case FileAccessRead:
		if folderID <= 0 {
			// 类型级列表 = 管理视角：需要 oa:document:manage（viewer.Manage）。
			if !viewer.Manage {
				return xerr.Forbidden("需要文档管理权限")
			}
			return nil
		}
		_, err := s.documentFolderForRead(ctx, s.repo, folderID, viewer)
		return err
	case FileAccessDelete:
		if folderID <= 0 {
			return xerr.NotFound("文件不存在")
		}
		_, err := s.documentFolderForManage(ctx, s.repo, folderID, viewer)
		return err
	default:
		return xerr.NotFound("文件不存在")
	}
}

// authorizeLeaveFileAccess 请假附件：Read = 可见范围，Delete = 申请人或
// oa:leave:manage；类型级列表只放行 oa:leave:manage。
func (s *Service) authorizeLeaveFileAccess(ctx context.Context, requestID int64, action FileAccessAction) error {
	switch action {
	case FileAccessRead:
		if requestID <= 0 {
			return s.requireFileListPermission(ctx, ManagePermission("leave"))
		}
		_, err := s.findVisibleLeaveRequest(ctx, requestID)
		return err
	case FileAccessDelete:
		if requestID <= 0 {
			return xerr.NotFound("文件不存在")
		}
		row, err := s.repo.FindLeaveRequest(ctx, requestID)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("文件不存在")
		}
		return s.authorizeOwnerOrManage(ctx, row.UserID, ManagePermission("leave"))
	default:
		return xerr.NotFound("文件不存在")
	}
}

// authorizeExpenseFileAccess 报销附件：Read = 可见范围，Delete = 申请人或
// oa:expense:manage；类型级列表只放行 oa:expense:manage。
func (s *Service) authorizeExpenseFileAccess(ctx context.Context, claimID int64, action FileAccessAction) error {
	switch action {
	case FileAccessRead:
		if claimID <= 0 {
			return s.requireFileListPermission(ctx, ManagePermission("expense"))
		}
		_, err := s.findVisibleExpenseClaim(ctx, claimID)
		return err
	case FileAccessDelete:
		if claimID <= 0 {
			return xerr.NotFound("文件不存在")
		}
		row, err := s.repo.FindExpenseClaim(ctx, claimID)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("文件不存在")
		}
		return s.authorizeOwnerOrManage(ctx, row.UserID, ManagePermission("expense"))
	default:
		return xerr.NotFound("文件不存在")
	}
}

// authorizeAnnouncementFileAccess 公告附件：Read = 公告可见性；Delete =
// oa:announcement:update 且仅草稿；类型级列表放行 oa:announcement:query。
func (s *Service) authorizeAnnouncementFileAccess(ctx context.Context, announcementID int64, action FileAccessAction) error {
	switch action {
	case FileAccessRead:
		if announcementID <= 0 {
			return s.requireFileListPermission(ctx, AnnouncementPermissionQuery)
		}
		_, _, err := s.findAccessibleAnnouncement(ctx, announcementID)
		return err
	case FileAccessDelete:
		if announcementID <= 0 {
			return xerr.NotFound("文件不存在")
		}
		allowed, err := s.hasPermission(ctx, AnnouncementPermissionUpdate)
		if err != nil {
			return err
		}
		if !allowed {
			return xerr.NotFound("文件不存在")
		}
		row, err := s.repo.FindAnnouncement(ctx, announcementID)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("文件不存在")
		}
		if !CanEditAnnouncement(row.Status) {
			return xerr.Conflict("公告已发布或已撤回，不能修改附件")
		}
		return nil
	default:
		return xerr.NotFound("文件不存在")
	}
}

// authorizeOwnerOrManage 写动作的归属判定：本人或持管理码放行；其余一律
// 404（不泄露业务对象存在性，与业务端点的归属校验同口径）。
func (s *Service) authorizeOwnerOrManage(ctx context.Context, ownerID int64, manageCode string) error {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return xerr.Unauthorized("")
	}
	if ownerID == userID {
		return nil
	}
	allowed, err := s.hasPermission(ctx, manageCode)
	if err != nil {
		return err
	}
	if !allowed {
		return xerr.NotFound("文件不存在")
	}
	return nil
}

// requireFileListPermission 类型级列表（管理视角）的权限判定：无权限 403。
func (s *Service) requireFileListPermission(ctx context.Context, code string) error {
	allowed, err := s.hasPermission(ctx, code)
	if err != nil {
		return err
	}
	if !allowed {
		return xerr.Forbidden("无权查看该业务类型文件")
	}
	return nil
}

// hasPermission 判断当前登录用户是否持权限码（未登录返回错误）。
func (s *Service) hasPermission(ctx context.Context, code string) (bool, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return false, xerr.Unauthorized("")
	}
	return s.sys.UserHasPermission(ctx, userID, code)
}
