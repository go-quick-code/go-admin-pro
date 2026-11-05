// contract_file.go 是合同附件能力（issue #17，docs/crm-mvp-spec.md §6 合同详情
// 「附件」/§9「附件复用 infra_file」）。
//
// 存储复用 infra_file：合同附件写入 biz_type='crm_contract'、biz_id=合同 ID，
// infra 侧只提供通用的「按业务归属上传/列表/删除」能力，不认识合同语义。
// crm service 通过 ContractFileStore 依赖接口访问存储（与 SystemGateway /
// ApprovalService 同一注入风格），由 internal/app 装配 infra 适配器，
// 避免 crm 模块反向依赖 infra service。
//
// 权限跟随合同：路由挂 crm:contract:query（查看）与 crm:contract:update
// （上传/删除），不要求 infra:file:* 权限码；合同不可见（数据范围外/跨租户）
// 时与其他合同操作一致返回 404，不泄露存在性。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strings"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ContractFileBizType 是合同附件在 infra_file 中的业务类型标识。
const ContractFileBizType = "crm_contract"

// defaultAPIPrefix 是未注入 HTTP 前缀时的兜底（与 configs/config.yaml 默认值一致）。
const defaultAPIPrefix = "/admin-api"

// ContractFileContent 是合同附件的下载内容（已通过合同可见性与文件归属校验）。
// Reader 由调用方负责关闭（API 层流式写出后关闭）。
type ContractFileContent struct {
	Name        string
	ContentType string
	Size        int64
	Reader      io.ReadCloser
}

// ContractFileStore 是合同附件的存储依赖面（由 internal/app 装配 infra 适配器）。
// 实现负责按 (bizType, bizID) 做归属过滤：不属于该业务的文件一律按不存在处理。
type ContractFileStore interface {
	// UploadFile 上传文件并写入业务归属，大小/扩展名限制由实现复用 infra 口径。
	UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (dto.ContractFileResp, error)
	// ListFiles 列出业务归属下的文件（新上传的在前）。
	ListFiles(ctx context.Context, bizType string, bizID int64) ([]dto.ContractFileResp, error)
	// DeleteFile 删除业务归属下的文件；文件不属于该业务时返回 404。
	DeleteFile(ctx context.Context, bizType string, bizID int64, fileID int64) error
	// OpenFile 打开业务归属下的文件供鉴权下载；不属于该业务时返回 404。
	OpenFile(ctx context.Context, bizType string, bizID int64, fileID int64) (*ContractFileContent, error)
}

// SetContractFileStore 注入合同附件存储实现（app 装配处调用）。
func (s *Service) SetContractFileStore(store ContractFileStore) {
	s.files = store
}

// SetAPIPrefix 注入 HTTP API 前缀，附件 URL 使用该前缀拼装鉴权下载地址。
func (s *Service) SetAPIPrefix(prefix string) {
	if prefix != "" {
		s.apiPrefix = prefix
	}
}

// contractFileDownloadURL 返回合同附件的鉴权下载地址（GET，挂 crm:contract:query）：
// 不再暴露 /files/ 公开静态 URL，避免未登录/无权限者拿到直链（PR #39 审查 P2）。
func (s *Service) contractFileDownloadURL(contractID, fileID int64) string {
	prefix := s.apiPrefix
	if prefix == "" {
		prefix = defaultAPIPrefix
	}
	return fmt.Sprintf("%s/crm/contract/%d/files/%d/download", strings.TrimRight(prefix, "/"), contractID, fileID)
}

// ListContractFiles 列出合同附件：合同不存在或数据范围外 404。
// 返回的 URL 统一改写成鉴权下载地址（存储层只给出归属元数据）。
func (s *Service) ListContractFiles(ctx context.Context, contractID int64) ([]dto.ContractFileResp, error) {
	if _, err := s.findVisibleContract(ctx, contractID); err != nil {
		return nil, err
	}
	store, err := s.contractFileStore()
	if err != nil {
		return nil, err
	}
	files, err := store.ListFiles(ctx, ContractFileBizType, contractID)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].URL = s.contractFileDownloadURL(contractID, files[i].ID)
	}
	return files, nil
}

// UploadContractFile 上传合同附件：合同须可见且非作废（作废合同是失效凭据，
// 不再接受新材料）；大小/扩展名由存储实现按 infra 的 20MB + 白名单口径校验。
func (s *Service) UploadContractFile(ctx context.Context, contractID int64, fh *multipart.FileHeader) (*dto.ContractFileResp, error) {
	c, err := s.findVisibleContract(ctx, contractID)
	if err != nil {
		return nil, err
	}
	if err := ValidateContractFileUpload(c); err != nil {
		return nil, err
	}
	if fh == nil {
		return nil, xerr.BadRequest("请选择要上传的文件")
	}
	store, err := s.contractFileStore()
	if err != nil {
		return nil, err
	}
	file, err := store.UploadFile(ctx, ContractFileBizType, contractID, fh)
	if err != nil {
		return nil, err
	}
	file.URL = s.contractFileDownloadURL(contractID, file.ID)
	return &file, nil
}

// DownloadContractFile 打开合同附件供鉴权下载：合同须可见（数据范围外/跨租户
// 404），文件必须属于该合同（跨合同 fileId 由存储层按归属过滤返回 404）。
// 返回内容由 API 层以 Content-Disposition: attachment 流式写出。
func (s *Service) DownloadContractFile(ctx context.Context, contractID, fileID int64) (*ContractFileContent, error) {
	if _, err := s.findVisibleContract(ctx, contractID); err != nil {
		return nil, err
	}
	store, err := s.contractFileStore()
	if err != nil {
		return nil, err
	}
	content, err := store.OpenFile(ctx, ContractFileBizType, contractID, fileID)
	if err != nil {
		return nil, err
	}
	if content == nil || content.Reader == nil {
		return nil, xerr.NotFound("文件不存在")
	}
	if content.ContentType == "" {
		content.ContentType = "application/octet-stream"
	}
	return content, nil
}

// DeleteContractFile 删除合同附件：合同须可见；文件必须属于该合同
// （跨合同 fileId 由存储实现按归属过滤返回 404），归属通过后删除文件与记录。
func (s *Service) DeleteContractFile(ctx context.Context, contractID, fileID int64) error {
	if _, err := s.findVisibleContract(ctx, contractID); err != nil {
		return err
	}
	store, err := s.contractFileStore()
	if err != nil {
		return err
	}
	return store.DeleteFile(ctx, ContractFileBizType, contractID, fileID)
}

// ValidateContractFileUpload 校验合同是否允许上传附件：作废合同（终态、
// 签约凭据失效）拒绝；草稿/待审/已签允许补充签约资料。纯函数，便于单测。
func ValidateContractFileUpload(c *model.Contract) error {
	if c == nil {
		return xerr.NotFound("合同不存在")
	}
	if c.Status == model.ContractStatusVoid {
		return xerr.BadRequest("已作废合同不可上传附件")
	}
	return nil
}

// contractFileStore 返回已装配的存储实现；未装配时显式报错，不静默丢附件。
func (s *Service) contractFileStore() (ContractFileStore, error) {
	if s.files == nil {
		return nil, xerr.Internal(errors.New("合同附件存储未装配"))
	}
	return s.files, nil
}
