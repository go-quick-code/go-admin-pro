// leave_file_store.go 是请假附件存储的装配适配器（issue #52）：
// oa service 只依赖 LeaveFileStore 接口，infra 侧只提供通用的
// 「按 biz_type/biz_id 上传/列表/删除」能力；本适配器把 infra_file 模型
// 映射成 oa 的附件视图，两个模块互不 import 对方的 service/repository。
// 与合同附件（issue #17）同款模式，#59 再抽共用组件。
package app

import (
	"context"
	"mime/multipart"

	inframodel "github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	infraservice "github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	oadto "github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	oaservice "github.com/go-quick-code/go-admin-pro/internal/module/oa/service"
)

// leaveFileStore 实现 oaservice.LeaveFileStore（鸭子类型，由 app.New 注入）。
type leaveFileStore struct {
	infra *infraservice.Service
}

func (s leaveFileStore) UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (oadto.LeaveFileResp, error) {
	f, err := s.infra.UploadFileForBiz(ctx, fh, bizType, bizID)
	if err != nil {
		return oadto.LeaveFileResp{}, err
	}
	return leaveFileResp(f), nil
}

func (s leaveFileStore) ListFiles(ctx context.Context, bizType string, bizID int64) ([]oadto.LeaveFileResp, error) {
	files, err := s.infra.ListFilesByBiz(ctx, bizType, bizID)
	if err != nil {
		return nil, err
	}
	out := make([]oadto.LeaveFileResp, 0, len(files))
	for i := range files {
		out = append(out, leaveFileResp(&files[i]))
	}
	return out, nil
}

func (s leaveFileStore) DeleteFile(ctx context.Context, bizType string, bizID, fileID int64) error {
	return s.infra.DeleteFileByBiz(ctx, bizType, bizID, fileID)
}

func (s leaveFileStore) OpenFile(ctx context.Context, bizType string, bizID, fileID int64) (*oaservice.LeaveFileContent, error) {
	f, fh, err := s.infra.OpenFileByBiz(ctx, bizType, bizID, fileID)
	if err != nil {
		return nil, err
	}
	return &oaservice.LeaveFileContent{
		Name:        f.Name,
		ContentType: f.Type,
		Size:        f.Size,
		Reader:      fh,
	}, nil
}

// leaveFileResp 把 infra_file 记录映射成请假附件视图（不暴露存储实现字段）。
func leaveFileResp(f *inframodel.File) oadto.LeaveFileResp {
	return oadto.LeaveFileResp{
		ID:         f.ID,
		Name:       f.Name,
		URL:        f.URL,
		Size:       f.Size,
		Type:       f.Type,
		CreateTime: f.CreatedAt,
	}
}
