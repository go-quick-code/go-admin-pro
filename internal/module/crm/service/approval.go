// approval.go 是合同审批依赖面（issue #16，docs/crm-mvp-spec.md §4.4 审批）。
//
// v1 直通实现：有 crm:contract:approve 权限的用户审批/驳回时直接改合同状态，
// 不产生流程实例、不留审批人轨迹；提交审批只需编辑侧权限（路由码 crm:contract:update），
// 提交人可以自己审批（规格未约束提交与审批分离，保持简单，见提交说明）。
//
// bpm 上线后替换：GLOSSARY 的审批流引擎（流程定义/任务/表单域）落地后，
// 由 bpm 侧实现本接口（Submit 创建流程实例，Approve/Reject 完成审批任务并
// 回写合同状态），internal/app 装配处 SetApprovalService 替换直通实现即可，
// 合同服务与 HTTP 层不需要任何改动——状态机校验（CanTransitionContractStatus）
// 与条件更新（repository.TransitionContractStatus）两侧都不感知审批实现。
package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// PermContractApprove 是合同审批权限码（internal/seed 菜单种子保持一致）：
// 审批通过与驳回必须持有；v1 直通实现据此放行。
const PermContractApprove = "crm:contract:approve"

// ApprovalService 是合同审批依赖面（依赖接口而非实现）。
type ApprovalService interface {
	// Submit 提交合同进入审批（草稿 → 待审）。
	Submit(ctx context.Context, contract *model.Contract) error
	// Approve 审批通过（待审 → 已签）。
	Approve(ctx context.Context, contract *model.Contract) error
	// Reject 驳回回草稿（待审 → 草稿），reason 为驳回原因（可空）。
	Reject(ctx context.Context, contract *model.Contract, reason string) error
}

// PassthroughApprovalService 是 ApprovalService 的 v1 直通实现：
// 审批人持 crm:contract:approve 即直接条件更新合同状态（防并发覆盖）。
type PassthroughApprovalService struct {
	repo *repository.Repository
	sys  SystemGateway
}

// NewPassthroughApprovalService 创建直通审批实现（app 装配时注入 crm 仓储与 system 网关）。
func NewPassthroughApprovalService(repo *repository.Repository, sys SystemGateway) *PassthroughApprovalService {
	return &PassthroughApprovalService{repo: repo, sys: sys}
}

// Submit 提交审批（草稿 → 待审）：v1 无审批配置可查，直接条件更新。
func (s *PassthroughApprovalService) Submit(ctx context.Context, contract *model.Contract) error {
	return s.transition(ctx, contract, model.ContractStatusPending, "")
}

// Approve 审批通过（待审 → 已签）：须持 crm:contract:approve。
func (s *PassthroughApprovalService) Approve(ctx context.Context, contract *model.Contract) error {
	if err := s.requireApprovePermission(ctx); err != nil {
		return err
	}
	return s.transition(ctx, contract, model.ContractStatusSigned, "")
}

// Reject 驳回回草稿（待审 → 草稿）：须持 crm:contract:approve，reason 落库供
// 草稿负责人查看（重新提交时由 TransitionContractStatus 清空）。
func (s *PassthroughApprovalService) Reject(ctx context.Context, contract *model.Contract, reason string) error {
	if err := s.requireApprovePermission(ctx); err != nil {
		return err
	}
	return s.transition(ctx, contract, model.ContractStatusDraft, reason)
}

// requireApprovePermission 校验当前用户持有审批权限码（super_admin 以 "*" 通配）。
func (s *PassthroughApprovalService) requireApprovePermission(ctx context.Context) error {
	ok, err := s.sys.UserHasPermission(ctx, CurrentUserID(ctx), PermContractApprove)
	if err != nil {
		return err
	}
	if !ok {
		return xerr.Forbidden("没有审批权限：" + PermContractApprove)
	}
	return nil
}

// transition 条件更新状态并把新状态回填到内存模型（0 行 = 状态已被并发请求变更）。
func (s *PassthroughApprovalService) transition(ctx context.Context, contract *model.Contract, to int8, reason string) error {
	rows, err := s.repo.TransitionContractStatus(ctx, contract.ID, contract.Status, to, reason)
	if err != nil {
		return err
	}
	if rows == 0 {
		return xerr.Conflict("合同状态已被其他操作变更，请刷新后重试")
	}
	contract.Status = to
	return nil
}

// SetApprovalService 注入审批依赖面实现（bpm 上线后的替换点，见文件头注释）。
func (s *Service) SetApprovalService(a ApprovalService) {
	s.approvals = a
}
