// document_file.go 承载文档文件的上传/列表/下载/删除编排（issue #59，
// docs/oa-mvp-spec.md §3.8/§4.12）：
//
//   - 上传/删除需要目标目录管理权（目录 owner 或 oa:document:manage，
//     spec §4.12）；这两个入口不在路由上挂额外的按钮码，否则目录 owner
//     会被挡在自己的目录外（与 #58 的目录改名/删除一致）；
//   - 列表/下载复用 #58 的「最近祖先 ACL」读可见性谓词：不可读目录一律
//     404；下载按文件 ID 解析出所属目录后再校验目录读权限——文件 ID 不能
//     绕过 ACL，跨目录/跨租户文件同样 404（spec §4.12）；
//   - 文件本体存 infra_file（biz_type=oa_document、biz_id=目录 ID），
//     oa_document_file 只存归属与展示字段；删除是软删除（归属行与 infra
//     行都置 deleted_at，磁盘内容保留但读取入口关闭）；
//   - 下载地址走鉴权端点（带 API 前缀），不返回静态公开直链。
//
// 权限判定本身仍是纯函数（document_rule.go）：DocumentFolderCanManage、
// DocumentFolderVisible；本文件只做取数、落库与存储适配。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// DocumentFileBizType 是文档文件在 infra_file 中的业务类型标识；biz_id 取
// 目录 ID（同目录文件共享归属键，跨目录/跨租户由归属键 + 租户插件双重限定）。
const DocumentFileBizType = "oa_document"

// DocumentStoredFile 是一次上传落到 infra_file 后的展示字段（存储无关）。
type DocumentStoredFile struct {
	FileID int64
	Name   string
	Size   int64
	Type   string
}

// DocumentFileStore 是文档文件的存储依赖面（由 internal/app 装配 infra
// 适配器）。实现负责把 infra_file 的读写限定在 (bizType, bizID=目录 ID) 内，
// 不属于该目录/租户的文件一律按不存在处理。
type DocumentFileStore interface {
	// UploadFile 上传文件并写入业务归属，大小/扩展名限制复用 infra 口径。
	UploadFile(ctx context.Context, folderID int64, fh *multipart.FileHeader) (*DocumentStoredFile, error)
	// OpenFile 打开目录归属下的文件供鉴权下载；归属不符/磁盘缺失返回 404。
	OpenFile(ctx context.Context, folderID, fileID int64) (*dto.DocumentFileContent, error)
	// SoftDeleteFile 软删除 infra 侧文件行（只置 deleted_at，保留磁盘与行）：
	// 与 oa_document_file 的软删除配套，关闭 infra 列表/下载入口。
	SoftDeleteFile(ctx context.Context, folderID, fileID int64) error
	// DeleteFile 清理刚上传但落库失败的文件本体（失败回收，不是用户删除
	// 入口）。
	DeleteFile(ctx context.Context, folderID, fileID int64) error
}

// SetDocumentFileStore 注入文档文件存储实现（app 装配处调用）。
func (s *Service) SetDocumentFileStore(store DocumentFileStore) {
	s.documentFiles = store
}

// PageDocumentFiles 分页列出目录内文件（GET /oa/document/file/page）：
// 先按 #58 的读可见性谓词校验目录可读（不可见 404），再读归属行；
// 只返回未软删文件，新上传的在前。
func (s *Service) PageDocumentFiles(ctx context.Context, req *dto.DocumentFilePageReq) ([]dto.DocumentFileItem, int64, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, 0, err
	}
	if req == nil || req.FolderID <= 0 {
		return nil, 0, xerr.BadRequest("目录 ID 不能为空")
	}
	if _, err := s.documentFolderForRead(ctx, s.repo, req.FolderID, viewer); err != nil {
		return nil, 0, err
	}
	req.Normalize()
	rows, total, err := s.repo.PageDocumentFiles(ctx, req.FolderID, req.Offset(), req.PageSize)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return []dto.DocumentFileItem{}, 0, nil
	}
	uploaderIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		uploaderIDs = append(uploaderIDs, row.UploaderID)
	}
	names, err := s.sys.UserNicknames(ctx, uploaderIDs)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.DocumentFileItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.documentFileItemOf(row, names[row.UploaderID]))
	}
	return items, total, nil
}

// UploadDocumentFile 上传文件到目标目录（POST /oa/document/file/upload，
// folderId 由 multipart 表单字段给出）：需要目录管理权。文件先落
// infra_file，再在事务内锁目录二次校验管理权并写 oa_document_file；
// 落库失败时回收文件本体，避免孤儿。
func (s *Service) UploadDocumentFile(ctx context.Context, folderID int64, fh *multipart.FileHeader) (*dto.DocumentFileItem, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	if fh == nil {
		return nil, xerr.BadRequest("请选择要上传的文件")
	}
	// 落盘前先做一次只读管理权校验：无权限请求不产生磁盘写入。
	if _, err := s.documentFolderForManage(ctx, s.repo, folderID, viewer); err != nil {
		return nil, err
	}
	store, err := s.documentFileStore()
	if err != nil {
		return nil, err
	}
	stored, err := store.UploadFile(ctx, folderID, fh)
	if err != nil {
		return nil, err
	}
	name, err := ValidateDocumentFileName(stored.Name)
	if err != nil {
		_ = store.DeleteFile(ctx, folderID, stored.FileID)
		return nil, err
	}
	row := &model.DocumentFile{
		FolderID:   folderID,
		FileID:     stored.FileID,
		Name:       name,
		Size:       stored.Size,
		Type:       stored.Type,
		UploaderID: viewer.UserID,
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 行锁目录：与目录删除串行化，避免往刚删除的目录里写孤儿文件。
		if _, err := s.documentFolderForManageLocked(ctx, tx, folderID, viewer); err != nil {
			return err
		}
		return tx.CreateDocumentFile(ctx, row)
	})
	if err != nil {
		// 归属行没落成，文件本体不应留下（磁盘回收失败不覆盖主错误）。
		_ = store.DeleteFile(ctx, folderID, stored.FileID)
		return nil, err
	}
	item := s.documentFileItemOf(*row, "")
	if names, err := s.sys.UserNicknames(ctx, []int64{row.UploaderID}); err == nil {
		item.UploaderName = names[row.UploaderID]
	}
	return &item, nil
}

// DownloadDocumentFile 鉴权下载（GET /oa/document/file/:id/download）：
//  1. 按文件 ID 读归属行（未软删、跨租户 404）；
//  2. 解析出文件所属目录，再按 #58 最近祖先 ACL 谓词校验目录读权限——
//     直接走文件 ID 不能绕过 ACL（spec §4.12）；
//  3. 文件本体按 (biz_type=oa_document, biz_id=目录 ID) 打开，磁盘缺失 404。
func (s *Service) DownloadDocumentFile(ctx context.Context, fileID int64) (*dto.DocumentFileContent, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.repo.FindDocumentFileByID(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("文件不存在")
	}
	if _, err := s.documentFolderForRead(ctx, s.repo, row.FolderID, viewer); err != nil {
		return nil, err
	}
	store, err := s.documentFileStore()
	if err != nil {
		return nil, err
	}
	content, err := store.OpenFile(ctx, row.FolderID, row.FileID)
	if err != nil {
		return nil, err
	}
	if content == nil || content.Reader == nil {
		return nil, xerr.NotFound("文件不存在")
	}
	if content.Name == "" {
		content.Name = row.Name
	}
	if content.ContentType == "" {
		content.ContentType = row.Type
	}
	if content.ContentType == "" {
		content.ContentType = "application/octet-stream"
	}
	if content.Size <= 0 {
		content.Size = row.Size
	}
	return content, nil
}

// DeleteDocumentFile 删除文件（DELETE /oa/document/file/:id）：需要文件所属
// 目录的管理权（owner 或 oa:document:manage）；先按文件 ID 解析目录，再在
// 事务内锁目录、校验归属并软删。
func (s *Service) DeleteDocumentFile(ctx context.Context, fileID int64) error {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return err
	}
	row, err := s.repo.FindDocumentFileByID(ctx, fileID)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.NotFound("文件不存在")
	}
	return s.deleteDocumentFileInFolder(ctx, row.FolderID, fileID, viewer)
}

// deleteDocumentFileInFolder 是删除的写路径：锁目录校验管理权后软删归属行
// （deleted_at），并配套软删 infra 文件行（磁盘内容保留，但 infra 列表/下载
// 入口关闭）。infra 侧软删失败不回滚文档归属行（磁盘与行都还在），只记
// 警告——文档下载入口已经 404，不影响安全边界。
func (s *Service) deleteDocumentFileInFolder(ctx context.Context, folderID, fileID int64, viewer DocumentViewer) error {
	store, err := s.documentFileStore()
	if err != nil {
		return err
	}
	var infraFileID int64
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if _, err := s.documentFolderForManageLocked(ctx, tx, folderID, viewer); err != nil {
			return err
		}
		row, err := tx.FindDocumentFile(ctx, folderID, fileID)
		if err != nil {
			return err
		}
		if row == nil {
			return xerr.NotFound("文件不存在")
		}
		deleted, err := tx.SoftDeleteDocumentFile(ctx, folderID, fileID)
		if err != nil {
			return err
		}
		if !deleted {
			// 并发重复删除：第一次已删成功，本次按不存在处理。
			return xerr.NotFound("文件不存在")
		}
		infraFileID = row.FileID
		return nil
	})
	if err != nil {
		return err
	}
	if err := store.SoftDeleteFile(ctx, folderID, infraFileID); err != nil {
		slog.Warn("关闭文档文件 infra 入口失败（文档归属行已软删）",
			"folderId", folderID, "fileId", infraFileID, "err", err)
	}
	return nil
}

// ---------- 内部辅助 ----------

// documentFolderForRead 读取目录并校验读权限（#58 单一可见性谓词）：
// 不可见目录（含跨租户、无 ACL 顶层）统一 404，不泄露存在性。
func (s *Service) documentFolderForRead(ctx context.Context, repo *repository.Repository, id int64, viewer DocumentViewer) (*repository.DocumentFolderListRow, error) {
	if id <= 0 {
		return nil, xerr.NotFound("目录不存在")
	}
	row, err := repo.FindVisibleDocumentFolder(ctx, id, documentVisibilityFilterOf(viewer))
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("目录不存在")
	}
	return row, nil
}

// documentFileStore 返回已装配的存储实现；未装配显式报错，不静默丢文件。
func (s *Service) documentFileStore() (DocumentFileStore, error) {
	if s.documentFiles == nil {
		return nil, xerr.Internal(errors.New("文档文件存储未装配"))
	}
	return s.documentFiles, nil
}

// documentFileItemOf 组装文件视图（时间按业务时区墙钟格式化，URL 指向
// 鉴权下载端点）。
func (s *Service) documentFileItemOf(row model.DocumentFile, uploaderName string) dto.DocumentFileItem {
	item := dto.DocumentFileItem{
		ID:           row.ID,
		FolderID:     row.FolderID,
		FileID:       row.FileID,
		Name:         row.Name,
		Size:         row.Size,
		Type:         row.Type,
		UploaderID:   row.UploaderID,
		UploaderName: uploaderName,
		URL:          s.documentFileURL(row.ID),
	}
	if !row.CreatedAt.IsZero() {
		item.CreateTime = row.CreatedAt.In(s.businessLocation()).Format(timeLayout)
	}
	return item
}

// documentFileURL 返回文件的鉴权下载地址（带 API 前缀，不暴露静态公开直链）。
func (s *Service) documentFileURL(fileID int64) string {
	prefix := s.apiPrefix
	if prefix == "" {
		prefix = defaultAPIPrefix
	}
	return fmt.Sprintf("%s/oa/document/file/%d/download",
		strings.TrimRight(prefix, "/"), fileID)
}
