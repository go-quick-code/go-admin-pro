package service

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/utils"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// MaxUploadSize 是单个上传文件的字节上限（20MB）。
// HTTP 层需要在 multipart 解析前用 MaxUploadBodyBytes 限制请求体，
// 避免超大请求先落临时文件/占用内存（PR #39 审查加固）。
const MaxUploadSize = 20 << 20 // 20MB

// MaxUploadBodyBytes 是上传请求体上限：单文件上限 + 1MB multipart 开销余量。
// 表单字段本身也可能被塞入大段填充，所以限制的是整个请求体而不只是文件大小。
const MaxUploadBodyBytes = MaxUploadSize + (1 << 20)

var allowedExt = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {}, ".webp": {}, ".svg": {},
	".pdf": {}, ".doc": {}, ".docx": {}, ".xls": {}, ".xlsx": {}, ".ppt": {}, ".pptx": {},
	".txt": {}, ".md": {}, ".csv": {}, ".json": {}, ".zip": {}, ".rar": {}, ".7z": {},
	".mp4": {}, ".mp3": {},
}

// Service 汇聚 infra 模块能力。
type Service struct {
	repo      *repository.Repository
	cfg       *config.Config
	scheduler *JobScheduler
}

func New(repo *repository.Repository, cfg *config.Config, scheduler *JobScheduler) *Service {
	return &Service{repo: repo, cfg: cfg, scheduler: scheduler}
}

func (s *Service) Scheduler() *JobScheduler { return s.scheduler }

// ---------- 文件 ----------

func (s *Service) UploadFile(ctx context.Context, fh *multipart.FileHeader) (*model.File, error) {
	return s.uploadFile(ctx, fh, "", 0)
}

// UploadFileForBiz 上传文件并写入业务归属（biz_type + biz_id）。
// 大小/扩展名限制与 UploadFile 完全一致（同一段校验），调用方只需给出归属键。
func (s *Service) UploadFileForBiz(ctx context.Context, fh *multipart.FileHeader, bizType string, bizID int64) (*model.File, error) {
	if err := validateFileBiz(bizType, bizID); err != nil {
		return nil, err
	}
	return s.uploadFile(ctx, fh, bizType, bizID)
}

// ListFilesByBiz 按业务归属列出文件（新上传的在前）。
func (s *Service) ListFilesByBiz(ctx context.Context, bizType string, bizID int64) ([]model.File, error) {
	if err := validateFileBiz(bizType, bizID); err != nil {
		return nil, err
	}
	return s.repo.ListFilesByBiz(ctx, bizType, bizID)
}

// DeleteFileByBiz 删除业务归属下的文件：文件不属于该业务（或不存在、跨租户）时
// 返回 404，不误删他人附件；归属校验通过后删除磁盘文件与记录。
func (s *Service) DeleteFileByBiz(ctx context.Context, bizType string, bizID, fileID int64) error {
	if err := validateFileBiz(bizType, bizID); err != nil {
		return err
	}
	f, err := s.repo.FindFileByBiz(ctx, fileID, bizType, bizID)
	if err != nil {
		return err
	}
	if f == nil {
		return xerr.NotFound("文件不存在")
	}
	return s.deleteFile(ctx, f)
}

// OpenFileByBiz 打开业务归属文件供流式下载（PR #39 审查 P2）：文件不属于
// 该业务（或不存在、跨租户）返回 404；磁盘内容缺失同样按 404 处理，
// 不把内部存储路径暴露给调用方。调用方负责关闭返回的文件句柄。
func (s *Service) OpenFileByBiz(ctx context.Context, bizType string, bizID, fileID int64) (*model.File, *os.File, error) {
	if err := validateFileBiz(bizType, bizID); err != nil {
		return nil, nil, err
	}
	f, err := s.repo.FindFileByBiz(ctx, fileID, bizType, bizID)
	if err != nil {
		return nil, nil, err
	}
	if f == nil {
		return nil, nil, xerr.NotFound("文件不存在")
	}
	absPath := filepath.Join(s.cfg.Storage.Local.BasePath, filepath.FromSlash(f.Path))
	fh, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, xerr.NotFound("文件不存在")
		}
		return nil, nil, xerr.Internal(err)
	}
	return f, fh, nil
}

// deleteFile 删除磁盘文件与数据库记录（两个删除入口的公共实现）：
// 磁盘文件已不存在时视为删除成功，记录照常清理。
func (s *Service) deleteFile(ctx context.Context, f *model.File) error {
	absPath := filepath.Join(s.cfg.Storage.Local.BasePath, filepath.FromSlash(f.Path))
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return xerr.Internal(err)
	}
	return s.repo.DeleteFile(ctx, f.ID)
}

// validateFileBiz 校验通用业务归属键：类型非空且不超过列宽 32，ID 为正。
func validateFileBiz(bizType string, bizID int64) error {
	if bizType == "" {
		return xerr.BadRequest("业务类型不能为空")
	}
	if len(bizType) > 32 {
		return xerr.BadRequest("业务类型长度不能超过 32 个字符")
	}
	if bizID <= 0 {
		return xerr.BadRequest("业务 ID 必须大于 0")
	}
	return nil
}

// uploadFile 是上传的公共实现：校验 → 落磁盘 → 写记录（记录失败回滚磁盘文件）。
func (s *Service) uploadFile(ctx context.Context, fh *multipart.FileHeader, bizType string, bizID int64) (*model.File, error) {
	if fh.Size > MaxUploadSize {
		return nil, xerr.BadRequest("文件大小不能超过 20MB")
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if _, ok := allowedExt[ext]; !ok {
		return nil, xerr.BadRequest("不支持的文件类型：" + ext)
	}

	now := time.Now()
	relDir := filepath.Join(now.Format("2006"), now.Format("01"), now.Format("02"))
	storageName := utils.UUID() + ext
	relPath := filepath.Join(relDir, storageName)
	absPath := filepath.Join(s.cfg.Storage.Local.BasePath, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, xerr.Internal(err)
	}

	src, err := fh.Open()
	if err != nil {
		return nil, xerr.Internal(err)
	}
	defer src.Close()

	dst, err := os.Create(absPath)
	if err != nil {
		return nil, xerr.Internal(err)
	}
	defer dst.Close()

	written, err := io.Copy(dst, src)
	if err != nil {
		return nil, xerr.Internal(err)
	}

	// URL 列不再保存公开静态地址（PR #39 残余风险 A）：公开目录已关闭，
	// 文件的唯一读取入口是鉴权下载端点。新建记录留空，返回前按 ID 拼装
	// 下载地址；存量行里的旧公开地址由读取路径统一覆盖。
	f := &model.File{
		Name:     filepath.Base(fh.Filename),
		Path:     filepath.ToSlash(relPath),
		URL:      "",
		Size:     written,
		Type:     fh.Header.Get("Content-Type"),
		Provider: "local",
		BizType:  bizType,
		BizID:    bizID,
	}
	if err := s.repo.CreateFile(ctx, f); err != nil {
		_ = os.Remove(absPath)
		return nil, err
	}
	f.URL = s.FileDownloadURL(f.ID)
	return f, nil
}

func (s *Service) ListFiles(ctx context.Context, req *dto.FilePageReq) ([]model.File, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.FileFilter{Name: req.Name, Type: req.Type}
	list, total, err := s.repo.ListFiles(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	// 存量行的 URL 可能还是旧的公开静态地址：读取路径统一覆盖为鉴权下载地址。
	for i := range list {
		list[i].URL = s.FileDownloadURL(list[i].ID)
	}
	return list, total, nil
}

// OpenFile 打开文件供鉴权下载（PR #39 残余风险 A）：按 ID 查文件
// （租户插件保证同租户），磁盘内容缺失与跨租户一样按 404 处理，
// 不把内部存储路径暴露给调用方。调用方负责关闭返回的文件句柄。
func (s *Service) OpenFile(ctx context.Context, id int64) (*model.File, *os.File, error) {
	f, err := s.repo.FindFileByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if f == nil {
		return nil, nil, xerr.NotFound("文件不存在")
	}
	absPath := filepath.Join(s.cfg.Storage.Local.BasePath, filepath.FromSlash(f.Path))
	fh, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, xerr.NotFound("文件不存在")
		}
		return nil, nil, xerr.Internal(err)
	}
	return f, fh, nil
}

func (s *Service) DeleteFile(ctx context.Context, id int64) error {
	f, err := s.repo.FindFileByID(ctx, id)
	if err != nil {
		return err
	}
	if f == nil {
		return xerr.NotFound("文件不存在")
	}
	return s.deleteFile(ctx, f)
}

// FileDownloadURL 返回文件的鉴权下载地址（GET /infra/file/:id/download）。
// 公开静态目录已关闭，所有对外的文件 URL 都由这里统一拼装。
func (s *Service) FileDownloadURL(id int64) string {
	prefix := strings.TrimRight(s.cfg.App.APIPrefix, "/")
	if prefix == "" {
		prefix = "/admin-api"
	}
	return fmt.Sprintf("%s/infra/file/%d/download", prefix, id)
}

// ---------- 参数配置 ----------

func (s *Service) ListConfigs(ctx context.Context, req *dto.ConfigPageReq) ([]model.Config, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.ConfigFilter{Name: req.Name, Key: req.Key, Category: req.Category}
	return s.repo.ListConfigs(ctx, filter, page.Offset(), page.PageSize)
}

func (s *Service) GetConfig(ctx context.Context, id int64) (*model.Config, error) {
	c, err := s.repo.FindConfigByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, xerr.NotFound("参数配置不存在")
	}
	return c, nil
}

func (s *Service) CreateConfig(ctx context.Context, req *dto.ConfigCreateReq) error {
	existing, err := s.repo.FindConfigByKey(ctx, req.Key)
	if err != nil {
		return err
	}
	if existing != nil {
		return xerr.Conflict("参数键名已存在")
	}
	category := req.Category
	if category == "" {
		category = "default"
	}
	configType := req.Type
	if configType == "" {
		configType = "string"
	}
	c := &model.Config{
		Category: category,
		Name:     req.Name,
		Key:      req.Key,
		Value:    req.Value,
		Type:     configType,
		Visible:  req.Visible,
		Remark:   req.Remark,
	}
	return s.repo.CreateConfig(ctx, c)
}

func (s *Service) UpdateConfig(ctx context.Context, req *dto.ConfigUpdateReq) error {
	c, err := s.repo.FindConfigByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if c == nil {
		return xerr.NotFound("参数配置不存在")
	}
	if c.Key != req.Key {
		existing, err := s.repo.FindConfigByKey(ctx, req.Key)
		if err != nil {
			return err
		}
		if existing != nil {
			return xerr.Conflict("参数键名已存在")
		}
	}
	c.Category = req.Category
	c.Name = req.Name
	c.Key = req.Key
	c.Value = req.Value
	c.Type = req.Type
	c.Visible = req.Visible
	c.Remark = req.Remark
	return s.repo.SaveConfig(ctx, c)
}

func (s *Service) DeleteConfig(ctx context.Context, id int64) error {
	c, err := s.repo.FindConfigByID(ctx, id)
	if err != nil {
		return err
	}
	if c == nil {
		return xerr.NotFound("参数配置不存在")
	}
	return s.repo.DeleteConfig(ctx, id)
}
