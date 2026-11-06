// expense.go 是报销单的 HTTP 入口（issue #54/#55，docs/oa-mvp-spec.md
// §4.9/§5）：草稿创建/编辑/删除、提交（bpm 审批）、可见范围内的列表/详情
// （明细 + 附件）、附件的上传/下载/删除。
//
// 权限模型：
//   - 列表/详情/附件查看登录即可（服务层按 §4.6 可见范围收口，范围外 404）；
//   - 写动作逐码：oa:expense:create（创建草稿）、oa:expense:update（编辑/附件）、
//     oa:expense:delete（删除）、oa:expense:submit（提交审批）；
//   - 审批动作不在本组：通过/驳回/撤回统一在通用待办（/bpm/task/*、
//     /bpm/instance/:id/withdraw）由 bpm 收口，回调写回报销单状态。
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

// expenseFileBodyMaxBytes 是报销附件上传的请求体上限：20MB 文件 + 1MB
// multipart 开销，与 infra 上传口径一致（multipart 解析前限制）。
const expenseFileBodyMaxBytes = 20<<20 + (1 << 20)

// CreateExpenseClaim 创建报销草稿（POST /oa/expense/claim，oa:expense:create）：
// 表头金额由明细求和（请求里的 totalAmount 忽略）；无明细/零负金额/字典外
// 类别 400。
func (h *Handler) CreateExpenseClaim(c *gin.Context) {
	var req dto.ExpenseClaimSaveReq
	if !bindJSON(c, &req) {
		return
	}
	detail, err := h.svc.CreateExpenseClaim(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// UpdateExpenseClaim 更新草稿/已驳回报销单（PUT /oa/expense/claim，
// oa:expense:update）：明细行集整体替换，合计重算。
func (h *Handler) UpdateExpenseClaim(c *gin.Context) {
	var req dto.ExpenseClaimSaveReq
	if !bindJSON(c, &req) {
		return
	}
	detail, err := h.svc.UpdateExpenseClaim(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// DeleteExpenseClaim 删除草稿/已驳回报销单（DELETE /oa/expense/claim/:id，
// oa:expense:delete；明细级联软删，其他状态 409）。
func (h *Handler) DeleteExpenseClaim(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteExpenseClaim(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}

// PageExpenseClaims 报销单分页（GET /oa/expense/claim/page，登录即可）：
// status 可选过滤（0 草稿/1 待审/2 通过/3 驳回/4 撤回）；按 §4.6 可见范围
// 返回（manage → 全租户、部门负责人 → 子树、否则本人）。
func (h *Handler) PageExpenseClaims(c *gin.Context) {
	var req dto.ExpenseClaimPageReq
	if !bindQuery(c, &req) {
		return
	}
	items, total, err := h.svc.PageExpenseClaims(c.Request.Context(), &req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Page(c, items, total)
}

// GetExpenseClaimDetail 报销单详情（GET /oa/expense/claim/:id，登录即可）：
// 表头 + 明细行 + 附件 + bpm 审批时间线（#55 提交后）；范围外/跨租户 404。
func (h *Handler) GetExpenseClaimDetail(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.GetExpenseClaimDetail(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// SubmitExpenseClaim 提交报销单（POST /oa/expense/claim/:id/submit，
// oa:expense:submit，issue #55）：仅本人、仅草稿/已驳回（其余 409）；事务内
// 发起 oa_expense_approval 实例并置待审（部门负责人 → oa:expense:manage）；
// 审批人解析不到由 bpm 返回 400 且整笔回滚（单据保持草稿）。
func (h *Handler) SubmitExpenseClaim(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := h.svc.SubmitExpenseClaim(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, detail)
}

// ListExpenseClaimFiles 附件列表（GET /oa/expense/claim/:id/files，登录即可，
// 按 §4.6 可见范围）。
func (h *Handler) ListExpenseClaimFiles(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	files, err := h.svc.ListExpenseClaimFiles(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, files)
}

// UploadExpenseClaimFile 上传附件（POST /oa/expense/claim/:id/files，
// oa:expense:update；仅草稿/已驳回）。
func (h *Handler) UploadExpenseClaimFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, expenseFileBodyMaxBytes)
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
	file, err := h.svc.UploadExpenseClaimFile(c.Request.Context(), id, fh)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, file)
}

// DownloadExpenseClaimFile 鉴权下载附件（GET
// /oa/expense/claim/:id/files/:fileId/download，登录即可，按可见范围）。
func (h *Handler) DownloadExpenseClaimFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	content, err := h.svc.DownloadExpenseClaimFile(c.Request.Context(), id, fileID)
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

// DeleteExpenseClaimFile 删除附件（DELETE
// /oa/expense/claim/:id/files/:fileId，oa:expense:update；仅草稿/已驳回）。
func (h *Handler) DeleteExpenseClaimFile(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	fileID, ok := web.ParseID(c, "fileId")
	if !ok {
		response.BadRequest(c, "非法的文件 ID")
		return
	}
	if err := h.svc.DeleteExpenseClaimFile(c.Request.Context(), id, fileID); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, nil)
}
