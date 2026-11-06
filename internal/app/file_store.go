// file_store.go 是 infra_file 的通用业务附件适配器（issue #64 code-review P2）：
// 公告/请假/报销/文档四个附件链路此前各有一份同形的「按 biz_type/biz_id
// 上传/列表/删除/打开」装配代码；这里收敛为一套实现。
//
// 模块边界不变：oa service 只依赖各自的 FileStore 接口，infra 侧只暴露通用的
// infra_file 能力，本文件属于 internal/app 装配层，不属于任一业务模块。
package app

import (
	"context"
	"mime/multipart"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	oadto "github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
)

// bizFileStore 实现公告/请假/报销三个 FileStore 接口（方法签名一致，返回的
// oa dto.FileResp/FileContent 是各业务 Resp 类型的别名）。bizType 由调用方
// 传入，各业务仍使用自己的归属键（oa_announcement/oa_leave/oa_expense）。
type bizFileStore struct {
	infra *infraservice.Service
}

func (s bizFileStore) UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (oadto.FileResp, error) {
	f, err := s.infra.UploadFileForBiz(ctx, fh, bizType, bizID)
	if err != nil {
		return oadto.FileResp{}, err
	}
	return fileRespOf(f), nil
}

func (s bizFileStore) ListFiles(ctx context.Context, bizType string, bizID int64) ([]oadto.FileResp, error) {
	files, err := s.infra.ListFilesByBiz(ctx, bizType, bizID)
	if err != nil {
		return nil, err
	}
	out := make([]oadto.FileResp, 0, len(files))
	for i := range files {
		out = append(out, fileRespOf(&files[i]))
	}
	return out, nil
}

func (s bizFileStore) DeleteFile(ctx context.Context, bizType string, bizID, fileID int64) error {
	return s.infra.DeleteFileByBiz(ctx, bizType, bizID, fileID)
}

func (s bizFileStore) OpenFile(ctx context.Context, bizType string, bizID, fileID int64) (*oadto.FileContent, error) {
	f, fh, err := s.infra.OpenFileByBiz(ctx, bizType, bizID, fileID)
	if err != nil {
		return nil, err
	}
	return &oadto.FileContent{
		Name:        f.Name,
		ContentType: f.Type,
		Size:        f.Size,
		Reader:      fh,
	}, nil
}

func (s bizFileStore) SoftDeleteFilesByBiz(ctx context.Context, bizType string, bizID int64) error {
	return s.infra.SoftDeleteFilesByBiz(ctx, bizType, bizID)
}

// fileRespOf 把 infra_file 记录映射成业务附件视图（不暴露存储实现字段）。
func fileRespOf(f *inframodel.File) oadto.FileResp {
	return oadto.FileResp{
		ID:         f.ID,
		Name:       f.Name,
		URL:        f.URL,
		Size:       f.Size,
		Type:       f.Type,
		CreateTime: f.CreatedAt,
	}
}

// documentFileStore 实现文档文件存储依赖面（issue #59）：归属键固定为
// (oa_document, 目录 ID)，方法不带 bizType；读写复用 bizFileStore。
type documentFileStore struct {
	files bizFileStore
}

func newDocumentFileStore(infra *infraservice.Service) documentFileStore {
	return documentFileStore{files: bizFileStore{infra: infra}}
}

func (s documentFileStore) UploadFile(ctx context.Context, folderID int64, fh *multipart.FileHeader) (*oaservice.DocumentStoredFile, error) {
	f, err := s.files.UploadFile(ctx, oaservice.DocumentFileBizType, folderID, fh)
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
	return s.files.OpenFile(ctx, oaservice.DocumentFileBizType, folderID, fileID)
}

func (s documentFileStore) SoftDeleteFile(ctx context.Context, folderID, fileID int64) error {
	return s.infra().SoftDeleteFileByBiz(ctx, oaservice.DocumentFileBizType, folderID, fileID)
}

func (s documentFileStore) DeleteFile(ctx context.Context, folderID, fileID int64) error {
	return s.infra().DeleteFileByBiz(ctx, oaservice.DocumentFileBizType, folderID, fileID)
}

func (s documentFileStore) infra() *infraservice.Service { return s.files.infra }
