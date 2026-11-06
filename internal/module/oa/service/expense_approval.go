// expense_approval.go 承接报销单的提交与 bpm 审批回调（issue #55，
// docs/oa-mvp-spec.md §4.9）：草稿/已驳回提交（事务内发起 oa_expense_approval
// 实例）、通过/驳回/撤回回调写回状态。
//
// 一致性约定（ADR 0008）：
//   - 提交时「置待审 + 发起审批」在同一事务内完成（StartInstanceInTx）；审批
//     人解析失败（部门负责人缺失、无 oa:expense:manage 持有人）由 bpm 返回
//     400，整笔提交回滚，单据保持草稿；
//   - 审批回调在 bpm 的事务内执行，业务写一律使用回调入参 tx（失败整笔回滚）；
//   - 通过即终态（不付款/不预算/不验真，ADR 0009），通过后明细不可修改——
//     编辑/删除/附件/提交的守卫统一在 ValidateExpenseAction，此处不重复实现；
//   - 驳回回「已驳回」可修改重提；撤回回「撤回」（spec §4.9）。
//
// 审批人规则来自默认流程定义（种子 seed/bpm.go）：节点 1 发起人所在部门负责人，
// 节点 2 权限码 oa:expense:manage（财务/管理员）。
package service

import (
	"context"
	"errors"
	"fmt"

	bpmdto "github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/text"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// ExpenseApprovalBusinessType 是报销审批的 bpm 业务类型/默认流程 key（spec
// §4.9 默认流程 oa_expense_approval：部门负责人 → oa:expense:manage）。
const ExpenseApprovalBusinessType = "oa_expense_approval"

// MaxExpenseRejectReasonLen 是驳回原因落库上限（oa_expense_claim.reject_reason
// 为 varchar(200)；bpm 意见允许 500 字，超长按 200 字截断保留可读前缀）。
const MaxExpenseRejectReasonLen = 200

// ---------- 提交 ----------

// SubmitExpenseClaim 提交报销单（spec §4.9）：
//  1. 事务内行锁读单据（仅本人），状态守卫：仅草稿/已驳回可提交，其余 409；
//  2. 明细必须至少一条（表头合计由明细重算，修复历史脏合计）；
//  3. 取 oa_expense_approval 当前发布版本（未发布 → 400）；
//  4. 事务内 StartInstanceInTx 发起审批，同时置待审并写入实例 ID——业务与
//     审批同生共死；审批人解析不到由 bpm 返回 400（不跳过节点、不自动通过）。
func (s *Service) SubmitExpenseClaim(ctx context.Context, id int64) (*dto.ExpenseClaimDetailResp, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return nil, xerr.Unauthorized("")
	}
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.FindExpenseClaimForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if row == nil || row.UserID != userID {
			return xerr.NotFound("报销单不存在")
		}
		if err := ValidateExpenseAction(ExpenseActionSubmit, row.Status); err != nil {
			return err
		}
		items, err := tx.ListExpenseItemsByClaim(ctx, id)
		if err != nil {
			return err
		}
		amounts := make([]money.Money, 0, len(items))
		for _, item := range items {
			amounts = append(amounts, item.Amount)
		}
		total, err := ValidateExpenseItems(amounts)
		if err != nil {
			return xerr.BadRequest("报销单没有有效明细，不能提交（" + err.Error() + "）")
		}

		definitionID, err := s.publishedExpenseDefinitionID(ctx)
		if err != nil {
			return err
		}
		deptName, err := s.expenseApprovalDeptName(ctx, row.UserID)
		if err != nil {
			return err
		}
		title, summary := expenseApprovalSnapshot(expenseClaimNo(row.ID), row.Title, deptName, total, row.Reason)
		inst, err := s.bpm.StartInstanceInTx(ctx, tx.DB(), bpmservice.StartInstanceInput{
			DefinitionID: definitionID,
			BusinessID:   row.ID,
			Title:        title,
			Summary:      summary,
		})
		if err != nil {
			return err
		}

		row.Status = model.ExpenseStatusPending
		row.TotalAmount = total
		row.RejectReason = ""
		row.ApprovalInstanceID = &inst.ID
		return tx.SaveExpenseClaim(ctx, row)
	})
	if err != nil {
		return nil, err
	}
	return s.GetExpenseClaimDetail(ctx, id)
}

// ---------- bpm 回调（spec §4.9/§4.4）----------

// ExpenseApprovalHandler 是报销审批回调的 bpm.BusinessHandler 适配器（issue
// #55）：oa.Service 本身已作为请假审批（oa_leave_approval）的处理器注册
// （#52），同一对象不能对两个 business_type 使用同一套回调语义；报销用这层
// 轻量适配器单独注册，回调转调 Service 的私有实现。
type ExpenseApprovalHandler struct {
	svc *Service
}

// NewExpenseApprovalHandler 创建报销审批回调适配器（app 装配处注册用）。
func NewExpenseApprovalHandler(svc *Service) *ExpenseApprovalHandler {
	return &ExpenseApprovalHandler{svc: svc}
}

// OnApproved 审批通过：置「通过」终态。
func (h *ExpenseApprovalHandler) OnApproved(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	return h.svc.onExpenseApproved(ctx, inst, tx)
}

// OnRejected 审批驳回：置「已驳回」并记录原因。
func (h *ExpenseApprovalHandler) OnRejected(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB, reason string) error {
	return h.svc.onExpenseRejected(ctx, inst, tx, reason)
}

// OnWithdrawn 发起人撤回：置「撤回」。
func (h *ExpenseApprovalHandler) OnWithdrawn(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	return h.svc.onExpenseWithdrawn(ctx, inst, tx)
}

// onExpenseApproved 审批通过：置「通过」终态（通过后明细不可修改由状态守卫
// 覆盖）。业务写使用 bpm 传入的 tx，与审批状态同事务提交/回滚。
func (s *Service) onExpenseApproved(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	repo := repository.New(tx)
	row, err := lockExpenseClaimForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	if row.Status == model.ExpenseStatusApproved {
		return nil // 幂等：重复回调不再改状态
	}
	if row.Status != model.ExpenseStatusPending {
		return xerr.Conflict("报销单当前状态不可通过")
	}
	row.Status = model.ExpenseStatusApproved
	row.RejectReason = ""
	return repo.SaveExpenseClaim(ctx, row)
}

// onExpenseRejected 审批驳回：置「已驳回」并记录原因（按列宽截断），可修改重提。
func (s *Service) onExpenseRejected(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB, reason string) error {
	repo := repository.New(tx)
	row, err := lockExpenseClaimForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	if row.Status == model.ExpenseStatusRejected {
		return nil
	}
	if row.Status != model.ExpenseStatusPending {
		return xerr.Conflict("报销单当前状态不可驳回")
	}
	row.Status = model.ExpenseStatusRejected
	row.RejectReason = text.TruncateRunes(reason, MaxExpenseRejectReasonLen)
	return repo.SaveExpenseClaim(ctx, row)
}

// onExpenseWithdrawn 发起人撤回：置「撤回」（仅首个审批动作前可撤回由
// bpm.WithdrawInstance 权威判定）。
func (s *Service) onExpenseWithdrawn(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	repo := repository.New(tx)
	row, err := lockExpenseClaimForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	if row.Status == model.ExpenseStatusWithdrawn {
		return nil
	}
	if row.Status != model.ExpenseStatusPending {
		return xerr.Conflict("报销单当前状态不可撤回")
	}
	row.Status = model.ExpenseStatusWithdrawn
	return repo.SaveExpenseClaim(ctx, row)
}

// lockExpenseClaimForCallback 回调内行锁读报销单（实例 → 单据，回调串行化）。
func lockExpenseClaimForCallback(ctx context.Context, repo *repository.Repository, inst *bpmmodel.ProcessInstance) (*model.ExpenseClaim, error) {
	if inst == nil {
		return nil, xerr.Internal(errors.New("oa: 审批回调缺少实例"))
	}
	row, err := repo.FindExpenseClaimForUpdate(ctx, inst.BusinessID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.Internal(fmt.Errorf("oa: 审批实例 %d 关联的报销单 %d 不存在", inst.ID, inst.BusinessID))
	}
	return row, nil
}

// ---------- 内部辅助 ----------

// publishedExpenseDefinitionID 取 oa_expense_approval 当前发布版本的定义 ID：
// v1 每个业务类型只用一个 key，同 key 同时只有一个发布版本（ADR 0008）。
func (s *Service) publishedExpenseDefinitionID(ctx context.Context) (int64, error) {
	if s.bpm == nil {
		return 0, xerr.Internal(errors.New("oa: 审批引擎未装配"))
	}
	published := bpmmodel.DefinitionStatusPublished
	rows, _, err := s.bpm.ListDefinitions(ctx, &bpmdto.DefinitionPageReq{
		BusinessType: ExpenseApprovalBusinessType,
		Status:       &published,
		PageNo:       1,
		PageSize:     1,
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, xerr.BadRequest("报销审批流程未发布（oa_expense_approval），请联系管理员发布流程定义")
	}
	return rows[0].ID, nil
}

// expenseApprovalDeptName 返回报销人所属部门名称（实例摘要快照用）；用户无
// 部门时返回空串（快照回退「未知部门」）。读取失败显式返回错误，不静默丢
// 快照字段。
func (s *Service) expenseApprovalDeptName(ctx context.Context, userID int64) (string, error) {
	deptID, ok, err := s.sys.UserDeptID(ctx, userID)
	if err != nil {
		return "", err
	}
	if !ok || deptID <= 0 {
		return "", nil
	}
	names, err := s.sys.DeptNames(ctx, []int64{deptID})
	if err != nil {
		return "", err
	}
	return names[deptID], nil
}
