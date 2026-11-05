// contract_file_store.go 是合同附件存储的装配适配器（issue #17）：
// crm service 只依赖 ContractFileStore 接口，infra 侧只提供通用的
// 「按 biz_type/biz_id 上传/列表/删除」能力；本适配器把 infra_file 模型
// 映射成 crm 的附件视图，两个模块互不 import 对方的 service/repository。
package app

import (
	"context"
	"mime/multipart"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	crmservice "github.com/lautung/go-admin-pro/internal/module/crm/service"
	inframodel "github.com/lautung/go-admin-pro/internal/module/infra/model"
	infraservice "github.com/lautung/go-admin-pro/internal/module/infra/service"
)

// contractFileStore 实现 crmservice.ContractFileStore（鸭子类型，由 app.New 注入）。
type contractFileStore struct {
	infra *infraservice.Service
}

func (s contractFileStore) UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (dto.ContractFileResp, error) {
	f, err := s.infra.UploadFileForBiz(ctx, fh, bizType, bizID)
	if err != nil {
		return dto.ContractFileResp{}, err
	}
	return contractFileResp(f), nil
}

func (s contractFileStore) ListFiles(ctx context.Context, bizType string, bizID int64) ([]dto.ContractFileResp, error) {
	files, err := s.infra.ListFilesByBiz(ctx, bizType, bizID)
	if err != nil {
		return nil, err
	}
	out := make([]dto.ContractFileResp, 0, len(files))
	for i := range files {
		out = append(out, contractFileResp(&files[i]))
	}
	return out, nil
}

func (s contractFileStore) DeleteFile(ctx context.Context, bizType string, bizID int64, fileID int64) error {
	return s.infra.DeleteFileByBiz(ctx, bizType, bizID, fileID)
}

// OpenFile 实现 crmservice.ContractFileStore：按业务归属打开文件供鉴权下载。
// 归属/租户不符或磁盘内容缺失时，infra 侧返回 404。
func (s contractFileStore) OpenFile(ctx context.Context, bizType string, bizID int64, fileID int64) (*crmservice.ContractFileContent, error) {
	f, fh, err := s.infra.OpenFileByBiz(ctx, bizType, bizID, fileID)
	if err != nil {
		return nil, err
	}
	return &crmservice.ContractFileContent{
		Name:        f.Name,
		ContentType: f.Type,
		Size:        f.Size,
		Reader:      fh,
	}, nil
}

// contractFileResp 把 infra_file 记录映射成合同附件视图（不暴露存储实现字段）。
func contractFileResp(f *inframodel.File) dto.ContractFileResp {
	return dto.ContractFileResp{
		ID:         f.ID,
		Name:       f.Name,
		URL:        f.URL,
		Size:       f.Size,
		Type:       f.Type,
		CreateTime: f.CreatedAt,
	}
}
