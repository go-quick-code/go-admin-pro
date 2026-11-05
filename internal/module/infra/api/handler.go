package api

import (
	"github.com/gin-gonic/gin"
	"github.com/lautung/go-admin-pro/internal/config"
	"github.com/lautung/go-admin-pro/internal/module/infra/dto"
	"github.com/lautung/go-admin-pro/internal/module/infra/service"
	"github.com/lautung/go-admin-pro/internal/pkg/response"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
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

func (h *Handler) UploadFile(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
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
