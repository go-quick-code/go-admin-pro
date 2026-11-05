package api

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-quick-code/go-admin-pro/internal/config"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/infra/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// PermissionMiddleware 由 system 模块提供权限中间件。
type PermissionMiddleware func(code string) gin.HandlerFunc

type Handler struct {
	svc  *service.Service
	cfg  *config.Config
	perm PermissionMiddleware
}

func New(svc *service.Service, cfg *config.Config, perm PermissionMiddleware) *Handler {
	return &Handler{svc: svc, cfg: cfg, perm: perm}
}

func bindJSON(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		response.BadRequest(c, "请求参数错误："+err.Error())
		return false
	}
	return true
}

func bindQuery(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindQuery(v); err != nil {
		response.BadRequest(c, "查询参数错误："+err.Error())
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	id, ok := web.ParseID(c, "id")
	if !ok {
		response.BadRequest(c, "非法的 ID")
		return 0, false
	}
	return id, true
}

// ---------- 文件 ----------

// UploadFile 上传文件。multipart 解析前先用 MaxBytesReader 限制整个请求体
// （20MB 文件 + 1MB 表单开销）：超限请求在解析阶段就被拒绝，不落临时文件、
// 不写 infra_file 行（PR #39 审查加固）。
func (h *Handler) UploadFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxUploadBodyBytes)
	fh, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			response.BadRequest(c, "文件大小不能超过 20MB")
			return
		}
		response.BadRequest(c, "请选择要上传的文件")
		return
	}
	file, err := h.svc.UploadFile(c.Request.Context(), fh)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, file)
}

// DownloadFile 鉴权下载文件（PR #39 残余风险 A）：公开 /files 静态目录关闭后，
// 这是上传文件的唯一读取入口，权限码 infra:file:query。文件按租户过滤，
// 跨租户/不存在/磁盘内容缺失统一 404；Content-Disposition 使用安全 ASCII
// 回退名 + RFC 5987 中文名，避免头注入与文件名乱码。
func (h *Handler) DownloadFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	f, fh, err := h.svc.OpenFile(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer fh.Close()

	// 以磁盘实际大小为准：记录里的 size 与文件内容不一致时不让 Content-Length 失真。
	size := f.Size
	if info, statErr := fh.Stat(); statErr == nil {
		size = info.Size()
	}
	c.Header("Content-Disposition", attachmentDisposition(f.Name, f.ID))
	c.DataFromReader(http.StatusOK, size, safeContentType(f.Name, f.Type), fh, nil)
}

// safeContentType 只接受可解析的存储 Content-Type（去掉参数，避免头注入），
// 缺失或非法时按扩展名推断，最后回退 application/octet-stream。
func safeContentType(name, stored string) string {
	if mediaType, _, err := mime.ParseMediaType(stored); err == nil && mediaType != "" {
		return mediaType
	}
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// attachmentDisposition 拼装下载响应头：ASCII 回退名只保留 ID 与安全扩展名，
// 原文件名走 RFC 5987 的 filename*。
func attachmentDisposition(name string, id int64) string {
	ext := strings.ToLower(filepath.Ext(name))
	var safeExt strings.Builder
	for _, r := range ext {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' {
			safeExt.WriteRune(r)
		}
	}
	return fmt.Sprintf(`attachment; filename="file-%d%s"; filename*=UTF-8''%s`,
		id, safeExt.String(), encodeRFC5987(name))
}

// encodeRFC5987 按 RFC 5987 attr-char 白名单做百分号编码：非 ASCII、控制字符、
// 引号与分号全部转义，确保文件名无法截断或伪造响应头。
func encodeRFC5987(s string) string {
	const attrChars = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.IndexByte(attrChars, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func (h *Handler) PageFiles(c *gin.Context) {
	var req dto.FilePageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListFiles(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) DeleteFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteFile(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 参数配置 ----------

func (h *Handler) PageConfigs(c *gin.Context) {
	var req dto.ConfigPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListConfigs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) GetConfig(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.GetConfig(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

func (h *Handler) CreateConfig(c *gin.Context) {
	var req dto.ConfigCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateConfig(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateConfig(c *gin.Context) {
	var req dto.ConfigUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateConfig(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteConfig(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteConfig(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

// ---------- 定时任务 ----------

func (h *Handler) PageJobs(c *gin.Context) {
	var req dto.JobPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListJobs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}

func (h *Handler) GetJob(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	job, err := h.svc.GetJob(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, job)
}

// ListJobHandlers 返回当前租户可用的处理器：系统级处理器只对平台租户展示
// （PR #39 审查 P0，客户端不能据此发现跨租户动作）。
func (h *Handler) ListJobHandlers(c *gin.Context) {
	response.OK(c, h.svc.ListJobHandlers(c.Request.Context()))
}

func (h *Handler) CreateJob(c *gin.Context) {
	var req dto.JobCreateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.CreateJob(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateJob(c *gin.Context) {
	var req dto.JobUpdateReq
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateJob(c.Request.Context(), &req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) UpdateJobStatus(c *gin.Context) {
	var req struct {
		ID     int64 `json:"id" binding:"required"`
		Status int8  `json:"status"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if err := h.svc.UpdateJobStatus(c.Request.Context(), req.ID, req.Status); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) DeleteJob(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteJob(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) RunJob(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.RunJob(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, true)
}

func (h *Handler) PageJobLogs(c *gin.Context) {
	var req dto.JobLogPageReq
	if !bindQuery(c, &req) {
		return
	}
	list, total, err := h.svc.ListJobLogs(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, list, total)
}
