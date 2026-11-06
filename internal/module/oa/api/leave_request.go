// leave_request.go 是请假申请的 HTTP 入口（issue #52/#53，
// docs/oa-mvp-spec.md §4.8/§5）：草稿创建/编辑/删除、提交、撤回、取消、
// 可见范围内的列表/详情（审批时间线）与附件。
//
// 权限模型：
//   - 列表/详情/附件查看登录即可（服务层按 §4.6 可见范围收口，范围外 404）；
//   - 写动作逐码：oa:leave:create（创建草稿）、oa:leave:update（编辑/附件）、
//     oa:leave:delete（删除）、oa:leave:submit（提交审批）、
//     oa:leave:withdraw（撤回，button 5295）、oa:leave:cancel（取消，button 5296）；
//   - 审批动作走通用待办（/bpm/task/*），不在此重复造审批 UI/接口。
package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// leaveFileBodyMaxBytes 是请假附件上传的请求体上限：20MB 文件 + 1MB
// multipart 开销，与 infra 上传口径一致（multipart 解析前限制）。
const leaveFileBodyMaxBytes = 20<<20 + (1 << 20)

// CreateLeaveRequest 创建请假草稿（POST /oa/leave/request，oa:leave:create）：
// 时长按工作日历计算；同日 PM→AM、起止颠倒、跨年 400。
func (h *Handler) CreateLeaveRequest(c *gin.Context) {
	var req dto.LeaveRequestSaveReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.CreateLeaveRequest(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// UpdateLeaveRequest 更新草稿/已驳回申请（PUT /oa/leave/request，oa:leave:update）。
func (h *Handler) UpdateLeaveRequest(c *gin.Context) {
	var req dto.LeaveRequestSaveReq
	if !bindJSON(c, &req) {
		return
	}
	item, err := h.svc.UpdateLeaveRequest(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// DeleteLeaveRequest 删除草稿/已驳回申请（DELETE /oa/leave/request/:id，
// oa:leave:delete；其他状态 409）。
func (h *Handler) DeleteLeaveRequest(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteLeaveRequest(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// SubmitLeaveRequest 提交审批（POST /oa/leave/request/:id/submit，
// oa:leave:submit）：事务内创建 oa_leave_approval 实例并置待审。
func (h *Handler) SubmitLeaveRequest(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.SubmitLeaveRequest(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// PageLeaveRequests 请假分页（GET /oa/leave/request/page，登录即可）：
// status 可选过滤（0 草稿/1 待审/2 通过/3 驳回/4 撤回/5 已取消）；按
// §4.6 可见范围返回（manage → 全租户、部门负责人 → 子树、否则本人）。
func (h *Handler) PageLeaveRequests(c *gin.Context) {
	var req dto.LeaveRequestPageReq
	if !bindQuery(c, &req) {
		return
	}
	items, total, err := h.svc.PageLeaveRequests(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, items, total)
}

// GetLeaveRequestDetail 请假详情（GET /oa/leave/request/:id，登录即可）：
// 申请行 + 附件 + bpm 审批时间线；按 §4.6 可见范围读取，范围外/跨租户 404。
func (h *Handler) GetLeaveRequestDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetLeaveRequestDetail(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// WithdrawLeaveRequest 撤回待审申请（POST /oa/leave/request/:id/withdraw，
// oa:leave:withdraw，button 5295）：仅发起人、首个审批动作前；状态迁移由
// bpm 撤回入口 + OnWithdrawn 完成，额度不变。
func (h *Handler) WithdrawLeaveRequest(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.WithdrawLeaveRequest(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// CancelLeaveRequest 取消已通过申请（POST /oa/leave/request/:id/cancel，
// oa:leave:cancel，button 5296）：仅发起人、开始日期前；事务内返还额度。
func (h *Handler) CancelLeaveRequest(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.svc.CancelLeaveRequest(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, item)
}

// ListLeaveRequestFiles 附件列表（GET /oa/leave/request/:id/files，登录即可，
// 按 §4.6 可见范围）。
func (h *Handler) ListLeaveRequestFiles(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	files, err := h.svc.ListLeaveRequestFiles(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, files)
}

// UploadLeaveRequestFile 上传附件（POST /oa/leave/request/:id/files，
// oa:leave:update；仅草稿/已驳回）。
func (h *Handler) UploadLeaveRequestFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, leaveFileBodyMaxBytes)
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
	file, err := h.svc.UploadLeaveRequestFile(c.Request.Context(), id, fh)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, file)
}

// DownloadLeaveRequestFile 鉴权下载附件（GET
// /oa/leave/request/:id/files/:fileId/download，登录即可，仅本人申请）。
func (h *Handler) DownloadLeaveRequestFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	content, err := h.svc.DownloadLeaveRequestFile(c.Request.Context(), id, fileID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer content.Reader.Close()
	asciiName := fmt.Sprintf("file-%d%s", fileID, strings.ToLower(filepath.Ext(content.Name)))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		asciiName, url.PathEscape(content.Name)))
	c.DataFromReader(http.StatusOK, content.Size, content.ContentType, content.Reader, nil)
}

// DeleteLeaveRequestFile 删除附件（DELETE /oa/leave/request/:id/files/:fileId，
// oa:leave:update；仅草稿/已驳回）。
func (h *Handler) DeleteLeaveRequestFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	if err := h.svc.DeleteLeaveRequestFile(c.Request.Context(), id, fileID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}
