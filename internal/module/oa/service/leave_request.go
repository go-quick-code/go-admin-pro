// leave_request.go 承接请假申请的完整流程（issue #52，docs/oa-mvp-spec.md
// §4.8）：草稿创建/编辑/删除、提交（事务内 StartInstanceInTx 生成 bpm 实例）、
// 可见范围内的列表/详情（审批时间线）、附件（infra_file，biz_type='oa_leave'）
// 与 bpm 回调（通过扣额度、驳回/撤回只改状态）。
//
// issue #53 扩展：
//   - 撤回：仅发起人、仅首个审批动作前；状态迁移由 bpm.WithdrawInstance 与
//     OnWithdrawn 完成，业务侧不重复写状态；
//   - 取消：仅发起人、已通过且开始日期未到（业务时区）；事务内返还 used；
//   - 列表/详情/附件读取按 §4.6 可见范围（manage → 部门负责人子树 → 本人），
//     范围外与跨租户一律 404。
//
// 一致性约定（ADR 0008）：
//   - 提交时「落申请状态 + 发起审批」在同一事务内完成（StartInstanceInTx）；
//   - 审批回调在 bpm 的事务内执行，业务写一律使用回调入参 tx（失败整笔回滚）；
//   - 额度扣减只在 OnApproved 发生；驳回/撤回不动额度；取消返还（spec §4.8）。
//
// 时长与重叠判定全部复用纯函数：槽位来自 #50 ResolveWorkdaySlots，
// 区间/方向/重叠来自 leave_application_rule.go。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"time"

	bpmdto "github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// LeaveApprovalBusinessType 是请假审批的 bpm 业务类型（spec §4.8 默认流程
// oa_leave_approval：部门负责人 → oa:leave:manage）。
const LeaveApprovalBusinessType = "oa_leave_approval"

// LeaveFileBizType 是请假附件在 infra_file 中的业务类型标识（spec §3.5）。
const LeaveFileBizType = "oa_leave"

// MaxLeaveRequestReasonLen 是请假事由长度上限（oa_leave_request.reason 列宽）。
const MaxLeaveRequestReasonLen = 500

// MaxLeaveRejectReasonLen 是驳回原因落库上限（oa_leave_request.reject_reason
// 为 varchar(200)；bpm 意见允许 500 字，超长按 200 字截断保留可读前缀）。
const MaxLeaveRejectReasonLen = 200

// defaultLeaveAPIPrefix 是未注入 HTTP 前缀时的兜底（与默认配置一致）。
const defaultLeaveAPIPrefix = "/admin-api"

// LeaveFileContent 是请假附件的下载内容（已通过归属校验）。
// Reader 由调用方负责关闭（API 层流式写出后关闭）。
type LeaveFileContent struct {
	Name        string
	ContentType string
	Size        int64
	Reader      io.ReadCloser
}

// LeaveFileStore 是请假附件的存储依赖面（由 internal/app 装配 infra 适配器，
// 与 CRM 合同附件同款模式）。实现负责按 (bizType, bizID) 做归属过滤。
type LeaveFileStore interface {
	UploadFile(ctx context.Context, bizType string, bizID int64, fh *multipart.FileHeader) (dto.LeaveFileResp, error)
	ListFiles(ctx context.Context, bizType string, bizID int64) ([]dto.LeaveFileResp, error)
	DeleteFile(ctx context.Context, bizType string, bizID int64, fileID int64) error
	OpenFile(ctx context.Context, bizType string, bizID int64, fileID int64) (*LeaveFileContent, error)
}

// SetBPM 注入审批引擎（app 装配处调用；OA → bpm 单向依赖）。
func (s *Service) SetBPM(engine *bpmservice.Service) { s.bpm = engine }

// SetLeaveFileStore 注入请假附件存储实现（app 装配处调用）。
func (s *Service) SetLeaveFileStore(store LeaveFileStore) { s.files = store }

// SetAPIPrefix 注入 HTTP API 前缀（附件鉴权下载地址拼装用）。
func (s *Service) SetAPIPrefix(prefix string) {
	if prefix != "" {
		s.apiPrefix = prefix
	}
}

// ---------- 草稿 ----------

// CreateLeaveRequest 创建请假草稿：类型须启用；日期与半天边界立即校验
// （同日 PM→AM、起止颠倒、跨年 400），时长按工作日历快照；草稿允许零工作日，
// 提交时再拒绝（spec §4.8「总槽位为 0 时拒绝提交」）。
func (s *Service) CreateLeaveRequest(ctx context.Context, req *dto.LeaveRequestSaveReq) (*dto.LeaveRequestItem, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	typ, err := s.leaveTypeForRequest(ctx, s.repo, req.LeaveTypeID)
	if err != nil {
		return nil, err
	}
	reason, err := validateLeaveReason(req.Reason)
	if err != nil {
		return nil, err
	}
	start, end, err := parseLeaveDates(req.StartDate, req.EndDate)
	if err != nil {
		return nil, err
	}
	if err := ValidateLeaveSpan(start, end, req.StartHalf, req.EndHalf); err != nil {
		return nil, err
	}
	slots, err := s.resolveLeaveSlots(ctx, s.repo, start, end, req.StartHalf, req.EndHalf)
	if err != nil {
		return nil, err
	}

	row := &model.LeaveRequest{
		UserID:       userID,
		LeaveTypeID:  typ.ID,
		StartDate:    start,
		StartHalf:    req.StartHalf,
		EndDate:      end,
		EndHalf:      req.EndHalf,
		DurationDays: LeaveDaysOfSlots(slots),
		Reason:       reason,
		Status:       model.LeaveRequestStatusDraft,
	}
	if err := s.repo.CreateLeaveRequest(ctx, row); err != nil {
		return nil, err
	}
	item := leaveRequestItemOf(*row, typ)
	return &item, nil
}

// UpdateLeaveRequest 更新草稿（或已驳回申请重新编辑）：仅本人，仅草稿/已驳回
// 可改；字段与区间校验同创建，时长重新快照。状态保持不变（重提交由 Submit 负责）。
func (s *Service) UpdateLeaveRequest(ctx context.Context, req *dto.LeaveRequestSaveReq) (*dto.LeaveRequestItem, error) {
	if req.ID <= 0 {
		return nil, xerr.BadRequest("请假申请 ID 不能为空")
	}
	row, err := s.findOwnedLeaveRequest(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if !isEditableLeaveRequest(row.Status) {
		return nil, xerr.Conflict("仅草稿或已驳回的请假申请可编辑")
	}
	typ, err := s.leaveTypeForRequest(ctx, s.repo, req.LeaveTypeID)
	if err != nil {
		return nil, err
	}
	reason, err := validateLeaveReason(req.Reason)
	if err != nil {
		return nil, err
	}
	start, end, err := parseLeaveDates(req.StartDate, req.EndDate)
	if err != nil {
		return nil, err
	}
	if err := ValidateLeaveSpan(start, end, req.StartHalf, req.EndHalf); err != nil {
		return nil, err
	}
	slots, err := s.resolveLeaveSlots(ctx, s.repo, start, end, req.StartHalf, req.EndHalf)
	if err != nil {
		return nil, err
	}

	row.LeaveTypeID = typ.ID
	row.StartDate = start
	row.StartHalf = req.StartHalf
	row.EndDate = end
	row.EndHalf = req.EndHalf
	row.DurationDays = LeaveDaysOfSlots(slots)
	row.Reason = reason
	if err := s.repo.SaveLeaveRequest(ctx, row); err != nil {
		return nil, err
	}
	item := leaveRequestItemOf(*row, typ)
	return &item, nil
}

// DeleteLeaveRequest 删除申请：仅本人，仅草稿/已驳回（spec §4.14）。
func (s *Service) DeleteLeaveRequest(ctx context.Context, id int64) error {
	row, err := s.findOwnedLeaveRequest(ctx, id)
	if err != nil {
		return err
	}
	if !isEditableLeaveRequest(row.Status) {
		return xerr.Conflict("仅草稿或已驳回的请假申请可删除")
	}
	deleted, err := s.repo.DeleteLeaveRequest(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return xerr.NotFound("请假申请不存在")
	}
	return nil
}

// SubmitLeaveRequest 提交请假申请（spec §4.8）：
//  1. 事务内行锁读申请（仅草稿/已驳回），校验类型启用与区间/半天边界；
//  2. 按工作日历计算槽位（空 → 400）与时长快照；
//  3. 同用户待审/已通过申请按槽位判重（重叠 → 409，错误带冲突单日期）；
//  4. 占用额度的类型检查可用额度（quota + adjustment - used，不足 → 400）；
//  5. 事务内 StartInstanceInTx 发起 oa_leave_approval（部门负责人 → 管理码），
//     同时把状态置待审并写入实例 ID——业务与审批同生共死。
func (s *Service) SubmitLeaveRequest(ctx context.Context, id int64) (*dto.LeaveRequestItem, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	var submitted *model.LeaveRequest
	var submittedType *model.LeaveType
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindLeaveRequestForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil || row.UserID != userID {
			return xerr.NotFound("请假申请不存在")
		}
		if row.Status != model.LeaveRequestStatusDraft && row.Status != model.LeaveRequestStatusRejected {
			return xerr.Conflict("当前状态不可提交（仅草稿或已驳回可提交）")
		}
		typ, err := s.leaveTypeForRequest(ctx, tx, row.LeaveTypeID)
		if err != nil {
			return err
		}
		start, end := row.StartDate, row.EndDate
		if err := ValidateLeaveSpan(start, end, row.StartHalf, row.EndHalf); err != nil {
			return err
		}
		slots, err := s.resolveLeaveSlots(ctx, tx, start, end, row.StartHalf, row.EndHalf)
		if err != nil {
			return err
		}
		if err := ValidateLeaveSlots(slots); err != nil {
			return err
		}
		duration := LeaveDaysOfSlots(slots)

		active, err := tx.ListActiveLeaveRequestsInRange(ctx, userID, DateKey(start), DateKey(end), row.ID)
		if err != nil {
			return err
		}
		rows, err := tx.ListWorkdaysByRange(ctx, DateKey(start), DateKey(end))
		if err != nil {
			return err
		}
		explicit := explicitDaysOf(rows)
		for i := range active {
			other := active[i]
			otherSlots := ResolveWorkdaySlots(other.StartDate, other.EndDate, other.StartHalf, other.EndHalf, explicit)
			if SlotsOverlap(slots, otherSlots) {
				return xerr.Conflict(fmt.Sprintf(
					"与已有请假申请（%s 至 %s）的时段重叠，不能重复提交",
					DateKey(other.StartDate), DateKey(other.EndDate),
				))
			}
		}

		if typ.BalanceManaged {
			available := money.FromCents(0)
			balance, err := tx.FindBalanceForUpdate(ctx, userID, typ.ID, start.Year())
			if err != nil {
				return err
			}
			if balance != nil {
				available = AvailableDays(balance.Quota, balance.Adjustment, balance.Used)
			}
			if err := CheckLeaveBalance(typ.Name, available, duration); err != nil {
				return err
			}
		}

		definitionID, err := s.publishedLeaveDefinitionID(ctx)
		if err != nil {
			return err
		}
		inst, err := s.bpm.StartInstanceInTx(ctx, tx.DB(), bpmservice.StartInstanceInput{
			DefinitionID: definitionID,
			BusinessID:   row.ID,
			Title:        leaveApprovalTitle(*row, typ, duration),
			Summary:      row.Reason,
		})
		if err != nil {
			return err
		}

		row.Status = model.LeaveRequestStatusPending
		row.DurationDays = duration
		row.RejectReason = ""
		row.ApprovalInstanceID = &inst.ID
		if err := tx.SaveLeaveRequest(ctx, row); err != nil {
			return err
		}
		submitted, submittedType = row, typ
		return nil
	})
	if err != nil {
		return nil, err
	}
	item := leaveRequestItemOf(*submitted, submittedType)
	return &item, nil
}

// ---------- 撤回 / 取消（issue #53） ----------

// WithdrawLeaveRequest 发起人撤回待审申请（spec §4.8/§5）：
//   - 仅本人可撤回（非本人/跨租户一律 404，不泄露存在性）；
//   - 业务状态必须是待审；「首个审批动作前」由 bpm.WithdrawInstance 在实例
//     事务内权威判定（已通过/驳回过则 409），业务侧不重复实现审批事实；
//   - 状态迁移与 OnWithdrawn 回调由 bpm 完成，额度不变；本方法只做入口校验
//     与回读最新申请行，不写业务状态。
func (s *Service) WithdrawLeaveRequest(ctx context.Context, id int64) (*dto.LeaveRequestItem, error) {
	if CurrentUserID(ctx) <= 0 {
		return nil, xerr.Unauthorized("")
	}
	row, err := s.findOwnedLeaveRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := ValidateLeaveAction(LeaveActionWithdraw, row.Status, time.Time{}, time.Time{}, nil); err != nil {
		return nil, err
	}
	if row.ApprovalInstanceID == nil {
		return nil, xerr.Conflict("请假申请未关联审批实例，不能撤回")
	}
	if s.bpm == nil {
		return nil, xerr.Internal(errors.New("oa: 审批引擎未装配"))
	}
	if _, err := s.bpm.WithdrawInstance(ctx, *row.ApprovalInstanceID, ""); err != nil {
		return nil, err
	}
	// bpm 的事务已提交（含 OnWithdrawn 置撤回态），回读最新行作为响应。
	updated, err := s.repo.FindLeaveRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, xerr.NotFound("请假申请不存在")
	}
	typeByID, err := s.typeMap(ctx)
	if err != nil {
		return nil, err
	}
	item := leaveRequestItemOf(*updated, typeByID[updated.LeaveTypeID])
	return &item, nil
}

// CancelLeaveRequest 发起人取消已通过的申请（spec §4.8/§5）：
//   - 仅本人可取消（非本人/跨租户一律 404）；
//   - 状态必须是已通过，且业务时区下的今天严格早于开始日期；开始当天及
//     之后返回可读 400（提示联系管理员调整记录）；
//   - 事务内行锁申请与额度行，`used -= duration`（RefundDays，夹零）并置
//     已取消；重复取消在第二次状态校验时 409，不会重复返还。
func (s *Service) CancelLeaveRequest(ctx context.Context, id int64) (*dto.LeaveRequestItem, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	var cancelled *model.LeaveRequest
	var cancelledType *model.LeaveType
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindLeaveRequestForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil || row.UserID != userID {
			return xerr.NotFound("请假申请不存在")
		}
		if err := ValidateLeaveAction(LeaveActionCancel, row.Status, row.StartDate, s.businessNow(), s.businessLocation()); err != nil {
			return err
		}
		typ, err := tx.FindLeaveType(ctx, row.LeaveTypeID)
		if err != nil {
			return err
		}
		if typ == nil {
			return xerr.Internal(fmt.Errorf("oa: 请假申请 %d 的类型 %d 不存在", row.ID, row.LeaveTypeID))
		}
		if typ.BalanceManaged {
			balance, err := tx.FindBalanceForUpdate(ctx, row.UserID, typ.ID, row.StartDate.Year())
			if err != nil {
				return err
			}
			if balance == nil {
				return xerr.Internal(fmt.Errorf(
					"oa: 请假申请 %d 的额度行不存在（user=%d type=%d year=%d）",
					row.ID, row.UserID, typ.ID, row.StartDate.Year(),
				))
			}
			balance.Used = RefundDays(balance.Used, row.DurationDays)
			if err := tx.SaveLeaveBalance(ctx, balance); err != nil {
				return err
			}
		}
		row.Status = model.LeaveRequestStatusCancelled
		if err := tx.SaveLeaveRequest(ctx, row); err != nil {
			return err
		}
		cancelled, cancelledType = row, typ
		return nil
	})
	if err != nil {
		return nil, err
	}
	item := leaveRequestItemOf(*cancelled, cancelledType)
	return &item, nil
}

// ---------- 查询 ----------

// PageLeaveRequests 请假分页（登录即可，按 §4.6 可见范围返回）：status 可选
// 过滤，start_date 倒序；附类型编码/名称。
//
// 可见范围：持 oa:leave:manage → 全租户；部门负责人 → 所负责部门子树内用户
// 的数据（多个部门并集）∪ 本人；其余 → 仅本人。范围外用户的行不会出现。
func (s *Service) PageLeaveRequests(ctx context.Context, req *dto.LeaveRequestPageReq) ([]dto.LeaveRequestItem, int64, error) {
	if CurrentUserID(ctx) <= 0 {
		return nil, 0, xerr.Unauthorized("")
	}
	scope, err := s.VisibilityScopeOf(ctx, "leave")
	if err != nil {
		return nil, 0, err
	}
	// nil 表示全租户；空切片表示什么都看不到（fail-closed）。
	var userFilter []int64
	if !scope.All {
		userFilter = scope.UserIDs
		if userFilter == nil {
			userFilter = []int64{}
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.PageLeaveRequestsByScope(ctx, userFilter, req.Status, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	typeByID, err := s.typeMap(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.LeaveRequestItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, leaveRequestItemOf(row, typeByID[row.LeaveTypeID]))
	}
	return items, total, nil
}

// GetLeaveRequestDetail 请假详情：申请行 + 附件 + bpm 审批时间线
// （草稿无实例时 approval 为 nil）。按 §4.6 可见范围读取：manage → 全租户、
// 部门负责人 → 子树、本人；范围外/跨租户一律 404，不泄露存在性。
// 时间线用 bpm 的业务读接口（本方法已完成可见性授权），部门负责人/HR 也能
// 看到下属申请的审批记录。
func (s *Service) GetLeaveRequestDetail(ctx context.Context, id int64) (*dto.LeaveRequestDetailResp, error) {
	row, err := s.findVisibleLeaveRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	typeByID, err := s.typeMap(ctx)
	if err != nil {
		return nil, err
	}
	detail := &dto.LeaveRequestDetailResp{
		LeaveRequestItem: leaveRequestItemOf(*row, typeByID[row.LeaveTypeID]),
		Files:            []dto.LeaveFileResp{},
	}
	files, err := s.ListLeaveRequestFiles(ctx, id)
	if err != nil {
		return nil, err
	}
	detail.Files = files
	if row.ApprovalInstanceID != nil {
		if s.bpm == nil {
			return nil, xerr.Internal(errors.New("oa: 审批引擎未装配"))
		}
		// 可见性已由本方法按 §4.6 校验，时间线使用业务读接口（跳过 bpm 的
		// 发起人/办理人身份判定），保证部门负责人与 HR 能看到完整时间线。
		approval, err := s.bpm.GetInstanceDetailForBusiness(ctx, *row.ApprovalInstanceID)
		if err != nil {
			return nil, err
		}
		detail.Approval = approval
	}
	return detail, nil
}

// ---------- 附件 ----------

// ListLeaveRequestFiles 列出可见范围内申请的附件（新上传的在前），URL 改写为
// 鉴权下载端点。可见范围与详情一致（manage/部门负责人/本人）。
func (s *Service) ListLeaveRequestFiles(ctx context.Context, id int64) ([]dto.LeaveFileResp, error) {
	if _, err := s.findVisibleLeaveRequest(ctx, id); err != nil {
		return nil, err
	}
	store, err := s.leaveFileStore()
	if err != nil {
		return nil, err
	}
	files, err := store.ListFiles(ctx, LeaveFileBizType, id)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].URL = s.leaveFileDownloadURL(id, files[i].ID)
	}
	return files, nil
}

// UploadLeaveRequestFile 上传附件：仅本人、仅草稿/已驳回（提交后材料随审批冻结）；
// 大小/扩展名白名单由 infra 存储实现按统一口径校验。
func (s *Service) UploadLeaveRequestFile(ctx context.Context, id int64, fh *multipart.FileHeader) (*dto.LeaveFileResp, error) {
	row, err := s.findOwnedLeaveRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if !isEditableLeaveRequest(row.Status) {
		return nil, xerr.BadRequest("仅草稿或已驳回的请假申请可上传附件")
	}
	if fh == nil {
		return nil, xerr.BadRequest("请选择要上传的文件")
	}
	store, err := s.leaveFileStore()
	if err != nil {
		return nil, err
	}
	file, err := store.UploadFile(ctx, LeaveFileBizType, id, fh)
	if err != nil {
		return nil, err
	}
	file.URL = s.leaveFileDownloadURL(id, file.ID)
	return &file, nil
}

// DownloadLeaveRequestFile 打开可见范围内申请的附件供鉴权下载；申请不可见
// （含跨租户）或文件不属于该申请一律 404。
func (s *Service) DownloadLeaveRequestFile(ctx context.Context, id, fileID int64) (*LeaveFileContent, error) {
	if _, err := s.findVisibleLeaveRequest(ctx, id); err != nil {
		return nil, err
	}
	store, err := s.leaveFileStore()
	if err != nil {
		return nil, err
	}
	content, err := store.OpenFile(ctx, LeaveFileBizType, id, fileID)
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

// DeleteLeaveRequestFile 删除本人草稿/已驳回申请的附件。
func (s *Service) DeleteLeaveRequestFile(ctx context.Context, id, fileID int64) error {
	row, err := s.findOwnedLeaveRequest(ctx, id)
	if err != nil {
		return err
	}
	if !isEditableLeaveRequest(row.Status) {
		return xerr.BadRequest("仅草稿或已驳回的请假申请可删除附件")
	}
	store, err := s.leaveFileStore()
	if err != nil {
		return err
	}
	return store.DeleteFile(ctx, LeaveFileBizType, id, fileID)
}

// leaveFileDownloadURL 返回附件鉴权下载地址（带 API 前缀，不暴露静态直链）。
func (s *Service) leaveFileDownloadURL(requestID, fileID int64) string {
	prefix := s.apiPrefix
	if prefix == "" {
		prefix = defaultLeaveAPIPrefix
	}
	return fmt.Sprintf("%s/oa/leave/request/%d/files/%d/download",
		strings.TrimRight(prefix, "/"), requestID, fileID)
}

// leaveFileStore 返回已装配的存储实现；未装配显式报错，不静默丢附件。
func (s *Service) leaveFileStore() (LeaveFileStore, error) {
	if s.files == nil {
		return nil, xerr.Internal(errors.New("oa: 请假附件存储未装配"))
	}
	return s.files, nil
}

// ---------- bpm 回调（spec §4.8/§4.4）----------

// OnApproved 审批通过：置状态通过并扣减额度（balance_managed 类型
// used += duration）。业务写使用 bpm 传入的 tx，与审批状态同事务提交/回滚。
func (s *Service) OnApproved(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	repo := repository.New(tx)
	row, err := s.lockRequestForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	if row.Status == model.LeaveRequestStatusApproved {
		return nil // 幂等：重复回调不重复扣减
	}
	if row.Status != model.LeaveRequestStatusPending {
		return xerr.Conflict("请假申请当前状态不可通过")
	}
	typ, err := repo.FindLeaveType(ctx, row.LeaveTypeID)
	if err != nil {
		return err
	}
	if typ == nil {
		return xerr.Internal(fmt.Errorf("oa: 请假申请 %d 的类型 %d 不存在", row.ID, row.LeaveTypeID))
	}
	if typ.BalanceManaged {
		balance, err := repo.FindBalanceForUpdate(ctx, row.UserID, typ.ID, row.StartDate.Year())
		if err != nil {
			return err
		}
		if balance == nil {
			return xerr.Internal(fmt.Errorf(
				"oa: 请假申请 %d 的额度行不存在（user=%d type=%d year=%d）",
				row.ID, row.UserID, typ.ID, row.StartDate.Year(),
			))
		}
		balance.Used = ConsumeDays(balance.Used, row.DurationDays)
		if err := repo.SaveLeaveBalance(ctx, balance); err != nil {
			return err
		}
	}
	row.Status = model.LeaveRequestStatusApproved
	row.RejectReason = ""
	if err := repo.SaveLeaveRequest(ctx, row); err != nil {
		return err
	}
	// TODO(#62): 考勤重算钩子（考勤模块上线前为空操作，spec §4.8）。
	return nil
}

// OnRejected 审批驳回：置状态驳回并记录原因，不动额度。
func (s *Service) OnRejected(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB, reason string) error {
	repo := repository.New(tx)
	row, err := s.lockRequestForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	if row.Status == model.LeaveRequestStatusRejected {
		return nil
	}
	if row.Status != model.LeaveRequestStatusPending {
		return xerr.Conflict("请假申请当前状态不可驳回")
	}
	row.Status = model.LeaveRequestStatusRejected
	row.RejectReason = truncateRunes(reason, MaxLeaveRejectReasonLen)
	return repo.SaveLeaveRequest(ctx, row)
}

// OnWithdrawn 发起人撤回：置状态撤回，不动额度。
func (s *Service) OnWithdrawn(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	repo := repository.New(tx)
	row, err := s.lockRequestForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	if row.Status == model.LeaveRequestStatusWithdrawn {
		return nil
	}
	if row.Status != model.LeaveRequestStatusPending {
		return xerr.Conflict("请假申请当前状态不可撤回")
	}
	row.Status = model.LeaveRequestStatusWithdrawn
	return repo.SaveLeaveRequest(ctx, row)
}

// lockRequestForCallback 回调内行锁读申请（实例 → 申请，回调串行化）。
func (s *Service) lockRequestForCallback(ctx context.Context, repo *repository.Repository, inst *bpmmodel.ProcessInstance) (*model.LeaveRequest, error) {
	if inst == nil {
		return nil, xerr.Internal(errors.New("oa: 审批回调缺少实例"))
	}
	row, err := repo.FindLeaveRequestForUpdate(ctx, inst.BusinessID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.Internal(fmt.Errorf("oa: 审批实例 %d 关联的请假申请 %d 不存在", inst.ID, inst.BusinessID))
	}
	return row, nil
}

// ---------- 内部辅助 ----------

// findOwnedLeaveRequest 读本人申请（写动作的归属校验）；不存在/跨租户/
// 非本人一律 404。
func (s *Service) findOwnedLeaveRequest(ctx context.Context, id int64) (*model.LeaveRequest, error) {
	row, err := s.repo.FindLeaveRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	userID := CurrentUserID(ctx)
	if row == nil || userID <= 0 || row.UserID != userID {
		return nil, xerr.NotFound("请假申请不存在")
	}
	return row, nil
}

// findVisibleLeaveRequest 读可见范围内的申请（读动作的归属校验，spec §4.6）：
// 持有 oa:leave:manage → 全租户；部门负责人 → 所负责部门子树；否则仅本人。
// 不存在/跨租户/范围外一律 404，不泄露存在性。
func (s *Service) findVisibleLeaveRequest(ctx context.Context, id int64) (*model.LeaveRequest, error) {
	row, err := s.repo.FindLeaveRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("请假申请不存在")
	}
	scope, err := s.VisibilityScopeOf(ctx, "leave")
	if err != nil {
		return nil, err
	}
	if !scope.Permits(row.UserID) {
		return nil, xerr.NotFound("请假申请不存在")
	}
	return row, nil
}

// leaveTypeForRequest 校验发起/提交所引用的类型：存在且启用（停用后不能
// 产生新申请，spec §4.14）。
func (s *Service) leaveTypeForRequest(ctx context.Context, repo *repository.Repository, id int64) (*model.LeaveType, error) {
	if id <= 0 {
		return nil, xerr.BadRequest("请选择请假类型")
	}
	typ, err := repo.FindLeaveType(ctx, id)
	if err != nil {
		return nil, err
	}
	if typ == nil {
		return nil, xerr.NotFound("请假类型不存在")
	}
	if typ.Status != model.LeaveTypeStatusEnabled {
		return nil, xerr.BadRequest("请假类型已停用，不能发起或提交申请")
	}
	return typ, nil
}

// parseLeaveDates 解析起止日期（严格 YYYY-MM-DD，400 可读错误）。
func parseLeaveDates(startDate, endDate string) (time.Time, time.Time, error) {
	start, err := NormalizeDate(startDate)
	if err != nil {
		return time.Time{}, time.Time{}, xerr.BadRequest("开始日期格式应为 YYYY-MM-DD")
	}
	end, err := NormalizeDate(endDate)
	if err != nil {
		return time.Time{}, time.Time{}, xerr.BadRequest("结束日期格式应为 YYYY-MM-DD")
	}
	return start, end, nil
}

// validateLeaveReason 校验事由：去首尾空白后 1–500 字。
func validateLeaveReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", xerr.BadRequest("请假事由不能为空")
	}
	if len([]rune(reason)) > MaxLeaveRequestReasonLen {
		return "", xerr.BadRequest(fmt.Sprintf("请假事由不能超过 %d 字", MaxLeaveRequestReasonLen))
	}
	return reason, nil
}

// resolveLeaveSlots 读取区间内的显式工作日历并计算槽位（纯函数在 #50）。
func (s *Service) resolveLeaveSlots(ctx context.Context, repo *repository.Repository, start, end time.Time, startHalf, endHalf int8) ([]Slot, error) {
	rows, err := repo.ListWorkdaysByRange(ctx, DateKey(start), DateKey(end))
	if err != nil {
		return nil, err
	}
	return ResolveWorkdaySlots(start, end, startHalf, endHalf, explicitDaysOf(rows)), nil
}

// publishedLeaveDefinitionID 取 oa_leave_approval 当前发布版本的定义 ID：
// v1 每个业务类型只用一个 key，同 key 同时只有一个发布版本（ADR 0008）。
func (s *Service) publishedLeaveDefinitionID(ctx context.Context) (int64, error) {
	if s.bpm == nil {
		return 0, xerr.Internal(errors.New("oa: 审批引擎未装配"))
	}
	published := bpmmodel.DefinitionStatusPublished
	rows, _, err := s.bpm.ListDefinitions(ctx, &bpmdto.DefinitionPageReq{
		BusinessType: LeaveApprovalBusinessType,
		Status:       &published,
		PageNo:       1,
		PageSize:     1,
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, xerr.BadRequest("请假审批流程未发布（oa_leave_approval），请联系管理员发布流程定义")
	}
	return rows[0].ID, nil
}

// leaveApprovalTitle 拼装实例标题快照（bpm 只存展示所需的最小信息，ADR 0008）。
func leaveApprovalTitle(row model.LeaveRequest, typ *model.LeaveType, duration money.Money) string {
	typeName := "请假"
	if typ != nil {
		typeName = typ.Name
	}
	return fmt.Sprintf("请假申请：%s 至 %s（%s %s 天）",
		DateKey(row.StartDate), DateKey(row.EndDate), typeName, formatLeaveDays(duration))
}

// isEditableLeaveRequest 报告申请是否可编辑/删除/补附件：草稿或已驳回。
func isEditableLeaveRequest(status int8) bool {
	return status == model.LeaveRequestStatusDraft || status == model.LeaveRequestStatusRejected
}

// leaveRequestItemOf 组装申请行视图（日期归一化为 YYYY-MM-DD）。
func leaveRequestItemOf(row model.LeaveRequest, typ *model.LeaveType) dto.LeaveRequestItem {
	item := dto.LeaveRequestItem{
		ID:                 row.ID,
		UserID:             row.UserID,
		LeaveTypeID:        row.LeaveTypeID,
		StartDate:          DateKey(row.StartDate),
		StartHalf:          row.StartHalf,
		EndDate:            DateKey(row.EndDate),
		EndHalf:            row.EndHalf,
		DurationDays:       row.DurationDays,
		Reason:             row.Reason,
		Status:             row.Status,
		RejectReason:       row.RejectReason,
		ApprovalInstanceID: row.ApprovalInstanceID,
	}
	if typ != nil {
		item.TypeCode = typ.Code
		item.TypeName = typ.Name
	}
	if !row.CreatedAt.IsZero() {
		item.CreateTime = row.CreatedAt.Format(timeLayout)
	}
	if !row.UpdatedAt.IsZero() {
		item.UpdateTime = row.UpdatedAt.Format(timeLayout)
	}
	return item
}

// formatLeaveDays 把天数格式化为可读文本（去掉无意义的尾零：1.00 → 1，0.50 → 0.5）。
func formatLeaveDays(v money.Money) string {
	out := strings.TrimRight(v.String(), "0")
	out = strings.TrimRight(out, ".")
	if out == "" || out == "-" {
		return "0"
	}
	return out
}

// truncateRunes 按字符截断到 max 个字符（列宽按字符计，避免切坏多字节字符）。
func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
