// contract_approval.go 承接 CRM 合同审批与 bpm 的接缝（issue #47，
// docs/oa-mvp-spec.md §4.5）：提交快照、审批回调（通过/驳回/撤回）与存量
// 待审合同的幂等 backfill。
//
// 一致性约定（ADR 0008）：
//   - 提交时「置待审 + 启动实例」在同一事务内完成（StartInstanceInTx）；
//   - 审批回调在 bpm 的事务内执行，业务写一律使用回调入参 tx（失败整笔回滚）；
//   - 合同表不新增 approval_instance_id 列（#47 不改表）：实例按
//     business_type + business_id 定位，业务详情取最近一次实例的审批时间线。
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// contractApprovalSnapshot 构造实例标题与摘要快照（纯函数，表驱动测试锁定）：
// 标题 `合同审批: {contractNo}`，摘要包含客户名称、合同名称与金额。
// 超长按 bpm 快照列宽截断（标题 128 / 摘要 500），不切坏多字节字符。
func contractApprovalSnapshot(contractNo, contractName, customerName string, amount money.Money) (title, summary string) {
	if strings.TrimSpace(customerName) == "" {
		customerName = "-"
	}
	title = truncateRunes(fmt.Sprintf("合同审批: %s", contractNo), bpmservice.MaxInstanceTitleLen)
	summary = truncateRunes(
		fmt.Sprintf("客户：%s；合同：%s；金额：%s 元", customerName, contractName, amount.String()),
		bpmservice.MaxInstanceSummaryLen,
	)
	return title, summary
}

// truncateContractRejectReason 按列宽截断驳回原因（varchar(500)）。
func truncateContractRejectReason(reason string) string {
	return truncateRunes(strings.TrimSpace(reason), MaxContractRejectReasonLen)
}

// truncateRunes 按字符截断到 max 个字符（列宽按字符计，避免切坏多字节字符）。
func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

// ---------- bpm 回调（spec §4.4/§4.5）----------

// OnApproved 审批通过：合同置已签。业务写使用 bpm 传入的 tx，与审批状态同事务。
// 已签视为幂等重复回调；非待审（如已被并发作废）返回 409，整笔审批动作回滚。
func (s *Service) OnApproved(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	repo := repository.New(tx)
	contract, err := lockContractForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	switch contract.Status {
	case model.ContractStatusSigned:
		return nil
	case model.ContractStatusPending:
		rows, err := repo.TransitionContractStatus(ctx, contract.ID, model.ContractStatusPending, model.ContractStatusSigned, "")
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("合同状态已被其他操作变更，请刷新后重试")
		}
		contract.Status = model.ContractStatusSigned
		return nil
	default:
		return xerr.Conflict("合同当前状态不可通过")
	}
}

// OnRejected 审批驳回：合同回草稿并落驳回原因（按列宽截断），不动其他字段。
// 原因相同的重复回调视为幂等；非待审状态返回 409。
func (s *Service) OnRejected(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB, reason string) error {
	repo := repository.New(tx)
	contract, err := lockContractForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	reason = truncateContractRejectReason(reason)
	switch contract.Status {
	case model.ContractStatusDraft:
		if contract.RejectReason == reason {
			return nil
		}
		return xerr.Conflict("合同当前状态不可驳回")
	case model.ContractStatusPending:
		rows, err := repo.TransitionContractStatus(ctx, contract.ID, model.ContractStatusPending, model.ContractStatusDraft, reason)
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("合同状态已被其他操作变更，请刷新后重试")
		}
		contract.Status = model.ContractStatusDraft
		contract.RejectReason = reason
		return nil
	default:
		return xerr.Conflict("合同当前状态不可驳回")
	}
}

// OnWithdrawn 发起人撤回（或业务作废终止实例）：合同回草稿。
// 草稿/作废视为幂等（作废流程会先终止实例再落作废，重复回调不再改状态）。
func (s *Service) OnWithdrawn(ctx context.Context, inst *bpmmodel.ProcessInstance, tx *gorm.DB) error {
	repo := repository.New(tx)
	contract, err := lockContractForCallback(ctx, repo, inst)
	if err != nil {
		return err
	}
	switch contract.Status {
	case model.ContractStatusDraft, model.ContractStatusVoid:
		return nil
	case model.ContractStatusPending:
		rows, err := repo.TransitionContractStatus(ctx, contract.ID, model.ContractStatusPending, model.ContractStatusDraft, "")
		if err != nil {
			return err
		}
		if rows == 0 {
			return xerr.Conflict("合同状态已被其他操作变更，请刷新后重试")
		}
		contract.Status = model.ContractStatusDraft
		contract.RejectReason = ""
		return nil
	default:
		return xerr.Conflict("合同当前状态不可撤回")
	}
}

// lockContractForCallback 回调内行锁读合同（实例 → 合同，回调串行化）。
func lockContractForCallback(ctx context.Context, repo *repository.Repository, inst *bpmmodel.ProcessInstance) (*model.Contract, error) {
	if inst == nil {
		return nil, xerr.Internal(errors.New("crm: 审批回调缺少实例"))
	}
	contract, err := repo.LockContractByID(ctx, inst.BusinessID)
	if err != nil {
		return nil, err
	}
	if contract == nil {
		return nil, xerr.Internal(fmt.Errorf("crm: 审批实例 %d 关联的合同 %d 不存在", inst.ID, inst.BusinessID))
	}
	return contract, nil
}

// ---------- 存量 backfill（spec §4.5）----------

// ContractBackfillResult 汇总一次存量合同审批补建结果。
type ContractBackfillResult struct {
	Tenants int // 扫过的启用租户数
	Scanned int // 待审合同数
	Created int // 补建实例数
	Skipped int // 跳过数（已有运行中实例 / 状态已变化）
	Failed  int // 失败数
}

// BackfillContractApprovals 为存量待审合同补建 crm_contract_approval 实例：
//   - 系统级动作：先以 tenant.Skip 列出启用租户，再逐租户携带租户上下文执行
//     （仓库租户插件 fail-closed，只碰本租户数据）；
//   - 只处理「待审且无运行中实例」的合同：逐合同在事务内锁合同行、复核状态
//     并再次检查运行中实例，重复执行或并发执行都不会重复创建；
//   - 实例发起人取合同负责人（存量单据没有提交人记录，负责人是业务归属人），
//     标题/摘要快照与正常提交一致。
//
// 单个合同失败不阻塞其他合同；错误聚合返回第一条（其余计入 Failed）。
func (s *Service) BackfillContractApprovals(ctx context.Context) (ContractBackfillResult, error) {
	var result ContractBackfillResult
	if s.bpm == nil {
		return result, xerr.Internal(errors.New("crm: 审批引擎未装配"))
	}
	tenantIDs, err := s.sys.ListEnabledTenantIDs(ctx)
	if err != nil {
		return result, err
	}
	result.Tenants = len(tenantIDs)
	var errs []error
	for _, id := range tenantIDs {
		res, err := s.backfillTenantContracts(tenant.WithTenantID(ctx, id), id)
		result.Scanned += res.Scanned
		result.Created += res.Created
		result.Skipped += res.Skipped
		result.Failed += res.Failed
		if err != nil {
			errs = append(errs, fmt.Errorf("租户 %d 合同审批 backfill 失败: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return result, errs[0]
	}
	return result, nil
}

// backfillTenantContracts 对单个租户执行 backfill，返回本租户计数。
func (s *Service) backfillTenantContracts(ctx context.Context, tenantID int64) (ContractBackfillResult, error) {
	var result ContractBackfillResult
	ids, err := s.repo.ListPendingContractIDs(ctx)
	if err != nil {
		return result, err
	}
	result.Scanned = len(ids)
	if len(ids) == 0 {
		return result, nil
	}
	definitionID, err := publishedContractDefinitionID(ctx, s.bpm)
	if err != nil {
		result.Failed = len(ids)
		return result, err
	}

	var errs []error
	for _, id := range ids {
		err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
			contract, err := tx.LockContractByID(ctx, id)
			if err != nil {
				return err
			}
			if contract == nil || contract.Status != model.ContractStatusPending {
				result.Skipped++
				return nil
			}
			running, err := s.bpm.FindRunningInstanceByBusinessInTx(ctx, tx.DB(), ContractApprovalBusinessType, id)
			if err != nil {
				return err
			}
			if running != nil {
				result.Skipped++
				return nil
			}
			if contract.OwnerUserID <= 0 {
				return xerr.BadRequest("合同缺少负责人，无法补建审批实例")
			}
			// 以合同负责人为发起人构造认证上下文（存量单据无提交人记录）。
			instCtx := authn.With(ctx, authn.Principal{
				UserID:   contract.OwnerUserID,
				TenantID: tenantID,
			})
			if _, err := startContractApproval(instCtx, tx, s.bpm, definitionID, contract); err != nil {
				return err
			}
			result.Created++
			return nil
		})
		if err != nil {
			result.Failed++
			errs = append(errs, fmt.Errorf("合同 %d: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return result, errs[0]
	}
	return result, nil
}
