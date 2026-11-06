// approval.go 是合同审批依赖面与 bpm 实现（issue #16 接缝，issue #47 迁移）。
//
// issue #47 起合同审批统一走 bpm（spec docs/oa-mvp-spec.md §4.5，ADR 0008）：
//   - 提交（草稿 → 待审）在同一事务内启动 crm_contract_approval 实例；
//   - 审批通过/驳回由通用待办中心完成，bpm 在同一事务内回调 CRM 的
//     OnApproved/OnRejected/OnWithdrawn 更新合同状态（见 contract_approval.go）；
//   - 合同状态端点不再提供「待审 → 已签/草稿」的直通审批，审批动作只能经
//     /bpm/task/:id/approve|reject 与 /bpm/instance/:id/withdraw 发生。
package service

import (
	"context"
	"errors"
	"fmt"

	bpmdto "github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	bpmmodel "github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	bpmservice "github.com/go-quick-code/go-admin-pro/internal/module/bpm/service"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ContractApprovalBusinessType 是合同审批的 bpm 业务类型（spec §4.5）：
// 流程定义 key 与 BusinessHandler 注册键都是 crm_contract_approval。
const ContractApprovalBusinessType = "crm_contract_approval"

// MaxContractRejectReasonLen 是驳回原因落库上限（crm_contract.reject_reason
// 为 varchar(500)；bpm 意见允许 500 字，超长按列宽截断保留可读前缀）。
const MaxContractRejectReasonLen = 500

// ApprovalService 是合同审批依赖面（依赖接口而非实现）。
//
// tx 是调用方已开启的事务仓储：合同行已 SELECT ... FOR UPDATE 锁定，并已按
// 锁定后的最新状态复核过流转合法性（PR #39 审查 P1）。实现必须在 tx 上写状态，
// 让「锁定 → 复核 → 发起审批」在同一事务内完成；bpm 实例与合同状态同生共死。
//
// 审批通过与驳回不在此接口：它们由 bpm 待办动作触发，经 BusinessHandler 在
// bpm 事务内回写合同（spec §4.4/§4.5）。
type ApprovalService interface {
	// Submit 提交合同进入审批（草稿 → 待审），同事务启动 bpm 实例。
	Submit(ctx context.Context, tx *repository.Repository, contract *model.Contract) error
}

// bpmApprovalService 是 ApprovalService 的 bpm 实现。
type bpmApprovalService struct {
	bpm *bpmservice.Service
}

// NewBPMApprovalService 创建 bpm 审批实现（app 装配时注入 bpm Service）。
func NewBPMApprovalService(bpm *bpmservice.Service) ApprovalService {
	return &bpmApprovalService{bpm: bpm}
}

// Submit 提交审批（草稿 → 待审）：解析当前发布版本的 crm_contract_approval，
// 在调用方事务内启动实例，再条件更新合同状态并清空上一轮驳回原因。
// 流程未发布/审批人解析为空等错误由 bpm 以可读 400 返回，整笔回滚。
func (s *bpmApprovalService) Submit(ctx context.Context, tx *repository.Repository, contract *model.Contract) error {
	if s.bpm == nil {
		return xerr.Internal(errors.New("crm: 审批引擎未装配"))
	}
	definitionID, err := publishedContractDefinitionID(ctx, s.bpm)
	if err != nil {
		return err
	}
	if _, err := startContractApproval(ctx, tx, s.bpm, definitionID, contract); err != nil {
		return err
	}
	rows, err := tx.TransitionContractStatus(ctx, contract.ID, contract.Status, model.ContractStatusPending, "")
	if err != nil {
		return err
	}
	if rows == 0 {
		return xerr.Conflict("合同状态已被其他操作变更，请刷新后重试")
	}
	contract.Status = model.ContractStatusPending
	contract.RejectReason = ""
	return nil
}

// startContractApproval 在调用方事务内启动 crm_contract_approval 实例。
// 标题/摘要快照只包含展示所需的最小业务信息（合同号/名称/客户/金额，ADR 0008）；
// 发起人取 ctx 中的当前登录主体（存量 backfill 由调用方以合同负责人身份构造）。
func startContractApproval(
	ctx context.Context,
	tx *repository.Repository,
	bpm *bpmservice.Service,
	definitionID int64,
	contract *model.Contract,
) (*bpmmodel.ProcessInstance, error) {
	if bpm == nil {
		return nil, xerr.Internal(errors.New("crm: 审批引擎未装配"))
	}
	customerName := ""
	if contract.CustomerID > 0 {
		customer, err := tx.FindCustomerByID(ctx, contract.CustomerID)
		if err != nil {
			return nil, err
		}
		if customer != nil {
			customerName = customer.Name
		}
	}
	title, summary := contractApprovalSnapshot(contract.ContractNo, contract.Name, customerName, contract.Amount)
	return bpm.StartInstanceInTx(ctx, tx.DB(), bpmservice.StartInstanceInput{
		DefinitionID: definitionID,
		BusinessID:   contract.ID,
		Title:        title,
		Summary:      summary,
	})
}

// publishedContractDefinitionID 取 crm_contract_approval 当前发布版本的定义 ID：
// v1 每个业务类型只用一个 key，同 key 同时只有一个发布版本（ADR 0008）。
func publishedContractDefinitionID(ctx context.Context, bpm *bpmservice.Service) (int64, error) {
	if bpm == nil {
		return 0, xerr.Internal(errors.New("crm: 审批引擎未装配"))
	}
	published := bpmmodel.DefinitionStatusPublished
	rows, _, err := bpm.ListDefinitions(ctx, &bpmdto.DefinitionPageReq{
		BusinessType: ContractApprovalBusinessType,
		Status:       &published,
		PageNo:       1,
		PageSize:     1,
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, xerr.BadRequest(fmt.Sprintf(
			"合同审批流程未发布（%s），请联系管理员发布流程定义", ContractApprovalBusinessType,
		))
	}
	return rows[0].ID, nil
}
