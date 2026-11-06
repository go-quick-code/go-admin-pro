// document_file_store.go 是文档文件存储的装配适配器（issue #59）：
// oa service 只依赖 DocumentFileStore 接口，infra 侧只提供通用的
// 「按 biz_type/biz_id 上传/打开/删除」能力。归属键固定为
// (oa_document, 目录 ID)，因此跨目录/跨租户的文件 ID 在存储层也命中不了。
// 与公告/请假/报销附件同款模式（本工单同时抽出前端共用上传组件）。
package app

import (
	"context"
	"mime/multipart"

	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	oadto "github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
)

// documentFileStore 实现 oaservice.DocumentFileStore（鸭子类型，由 app.New 注入）。
type documentFileStore struct {
	infra *infraservice.Service
}

func (s documentFileStore) UploadFile(ctx context.Context, folderID int64, fh *multipart.FileHeader) (*oaservice.DocumentStoredFile, error) {
	f, err := s.infra.UploadFileForBiz(ctx, fh, oaservice.DocumentFileBizType, folderID)
	if err != nil {
		return nil, err
	}
	return &oaservice.DocumentStoredFile{
		FileID: f.ID,
		Name:   f.Name,
		Size:   f.Size,
		Type:   f.Type,
	}, nil
}

func (s documentFileStore) OpenFile(ctx context.Context, folderID, fileID int64) (*oadto.DocumentFileContent, error) {
	f, fh, err := s.infra.OpenFileByBiz(ctx, oaservice.DocumentFileBizType, folderID, fileID)
	if err != nil {
		return nil, err
	}
	return &oadto.DocumentFileContent{
		Name:        f.Name,
		ContentType: f.Type,
		Size:        f.Size,
		Reader:      fh,
	}, nil
}

func (s documentFileStore) SoftDeleteFile(ctx context.Context, folderID, fileID int64) error {
	return s.infra.SoftDeleteFileByBiz(ctx, oaservice.DocumentFileBizType, folderID, fileID)
}

func (s documentFileStore) DeleteFile(ctx context.Context, folderID, fileID int64) error {
	return s.infra.DeleteFileByBiz(ctx, oaservice.DocumentFileBizType, folderID, fileID)
}
