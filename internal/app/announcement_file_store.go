// announcement_file_store.go 是公告附件存储的装配适配器（issue #48）：
// oa service 只依赖 AnnouncementFileStore 接口，infra 侧只提供通用的
// 「按 biz_type/biz_id 上传/列表/删除/软删」能力；本适配器把 infra_file
// 模型映射成 oa 的附件视图，两个模块互不 import 对方的 service/repository。
// 与合同附件（contract_file_store.go）同一模式。
package app

import (
	"context"
	"mime/multipart"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
)

// announcementFileStore 实现 oaservice.AnnouncementFileStore（鸭子类型，由 app.New 注入）。
type announcementFileStore struct {
	infra *infraservice.Service
}

func (s announcementFileStore) UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (dto.AnnouncementFileResp, error) {
	f, err := s.infra.UploadFileForBiz(ctx, fh, bizType, bizID)
	if err != nil {
		return dto.AnnouncementFileResp{}, err
	}
	return announcementFileResp(f), nil
}

func (s announcementFileStore) ListFiles(ctx context.Context, bizType string, bizID int64) ([]dto.AnnouncementFileResp, error) {
	files, err := s.infra.ListFilesByBiz(ctx, bizType, bizID)
	if err != nil {
		return nil, err
	}
	out := make([]dto.AnnouncementFileResp, 0, len(files))
	for i := range files {
		out = append(out, announcementFileResp(&files[i]))
	}
	return out, nil
}

func (s announcementFileStore) DeleteFile(ctx context.Context, bizType string, bizID int64, fileID int64) error {
	return s.infra.DeleteFileByBiz(ctx, bizType, bizID, fileID)
}

func (s announcementFileStore) OpenFile(ctx context.Context, bizType string, bizID int64, fileID int64) (*dto.AnnouncementFileContent, error) {
	f, fh, err := s.infra.OpenFileByBiz(ctx, bizType, bizID, fileID)
	if err != nil {
		return nil, err
	}
	return &dto.AnnouncementFileContent{
		Name:        f.Name,
		ContentType: f.Type,
		Size:        f.Size,
		Reader:      fh,
	}, nil
}

func (s announcementFileStore) SoftDeleteFilesByBiz(ctx context.Context, bizType string, bizID int64) error {
	return s.infra.SoftDeleteFilesByBiz(ctx, bizType, bizID)
}

// announcementFileResp 把 infra_file 记录映射成公告附件视图。
func announcementFileResp(f *model.File) dto.AnnouncementFileResp {
	return dto.AnnouncementFileResp{
		ID:         f.ID,
		Name:       f.Name,
		URL:        f.URL,
		Size:       f.Size,
		Type:       f.Type,
		CreateTime: f.CreatedAt,
	}
}
