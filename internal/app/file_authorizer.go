// file_authorizer.go 是 infra 通用文件端点的业务授权装配（PR #66 安全审计
// finding 1：infra/file/download-delete-bypasses-business-acl）。
//
// 依赖方向不变：infra 平台层只提供按 biz_type 的授权注册表，不认识业务语义；
// OA 提供 AuthorizeFileAccess / SyncDocumentFileDelete；由本装配层（app）
// 把两者接起来，业务模块之间不互相 import。
package app

import (
	"context"

	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// registerOAFileAuthorizers 把 OA 四类业务附件注册到 infra 通用文件端点：
// 未注册的业务类型（含 CRM 合同附件）在通用下载/删除入口上一律 fail-closed。
func registerOAFileAuthorizers(infra *infraservice.Service, oa *oaservice.Service) {
	for _, bizType := range []string{
		oaservice.DocumentFileBizType,
		oaservice.LeaveFileBizType,
		oaservice.ExpenseFileBizType,
		oaservice.AnnouncementFileBizType,
	} {
		infra.RegisterFileAuthorizer(bizType, oaFileAuthorizer(oa, bizType))
	}
	// 文档文件有独立归属行 oa_document_file：通用删除入口先同步软删归属行，
	// 再删文件本体，保持「列表与内容一致」的既有文档删除语义。
	infra.RegisterFileDeleteSyncer(oaservice.DocumentFileBizType, oa.SyncDocumentFileDelete)
}

// oaFileAuthorizer 把 OA 的授权入口适配成 infra 的按 biz_type 授权器
// （两边的动作枚举属于不同模块，显式转换保持依赖方向 app → oa / infra）。
func oaFileAuthorizer(oa *oaservice.Service, bizType string) infraservice.FileAuthorizer {
	return func(ctx context.Context, bizID int64, action infraservice.FileAction) error {
		switch action {
		case infraservice.FileActionRead:
			return oa.AuthorizeFileAccess(ctx, bizType, bizID, oaservice.FileAccessRead)
		case infraservice.FileActionDelete:
			return oa.AuthorizeFileAccess(ctx, bizType, bizID, oaservice.FileAccessDelete)
		default:
			return xerr.NotFound("文件不存在")
		}
	}
}
