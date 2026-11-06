// instance.go 承载流程运行（issue #46，spec #42 §4.2/§4.3/§4.4）：
// 发起实例、候选池物化、通过/驳回/撤回/转办、BusinessHandler 同事务回调、
// 通知联动（bpm_todo/bpm_result、办理后置已读）与通用待办查询。
//
// 状态机（spec §4.3）：
//
//	任务：待办 → 通过/驳回/转办/取消
//	实例：运行中 → 通过/驳回/撤回
//
// 一致性约定：
//   - 全部动作在单事务内完成，锁序一律「实例行锁 → 任务行锁」，同实例的
//     状态变更串行化（先办者胜出；对非待办任务再次办理返回 409）；
//   - BusinessHandler 在推进实例状态的同一事务内回调，失败整笔回滚
//     （业务状态与审批状态同生共死，spec §4.4）；
//   - 通知经 NotifyGateway 在同一事务写入：新待办 bpm_todo（BizType
//     bpm_task、按任务 ID 定位），结果 bpm_result（BizType bpm_instance、
//     发给发起人），任务处理后把办理人对应 bpm_todo 置已读（spec §4.13）；
//   - 审批记录追加型不可变：每次动作追加一条，记录操作人/时间/意见。
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/repository"
	notifymodel "github.com/go-quick-code/go-admin-pro/internal/module/notify/model"
	notifyservice "github.com/go-quick-code/go-admin-pro/internal/module/notify/service"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/authn"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
	"gorm.io/gorm"
)

// 通知定位用的业务类型（notify_message.biz_type，spec §4.13）。
const (
	// BizTypeTask 待办通知按任务定位（biz_id = 任务 ID；任务办理后据此置已读）。
	BizTypeTask = "bpm_task"
	// BizTypeInstance 结果通知按实例定位（biz_id = 实例 ID）。
	BizTypeInstance = "bpm_instance"
)

// PermInstanceQuery 是实例查询权限码（spec §7.2 菜单 512 段）：实例详情的
// 第三类访问者（非发起人、非办理人）须持有。
const PermInstanceQuery = "bpm:instance:query"

// 审批意见与快照字段的取值边界（spec §4.3：意见 1–500 字，驳回必填）。
const (
	MaxInstanceTitleLen   = 128
	MaxInstanceSummaryLen = 500
	MaxCommentLen         = 500
)

// StartInstanceInput 是发起实例的入参：业务模块（CRM/请假/报销）提交单据时
// 调用 StartInstance（自开事务）或 StartInstanceInTx（并入调用方事务）。
// 标题/摘要是快照字段——bpm 只存展示所需的最小业务信息（ADR 0008）。
type StartInstanceInput struct {
	// DefinitionID 目标定义 ID：必须处于发布状态（停用/草稿不可发起）。
	DefinitionID int64
	// BusinessID 业务记录 ID（业务表自持，bpm 只存 ID，详情由业务页渲染）。
	BusinessID int64
	// Title 标题快照（1–128 字，如「请假申请：2026-11-06 年假」）。
	Title string
	// Summary 摘要快照（≤500 字，可空）。
	Summary string
}

// instanceStatusName 返回实例状态中文名（409/提示文案用）。
func instanceStatusName(status int8) string {
	switch status {
	case model.InstanceStatusRunning:
		return "运行中"
	case model.InstanceStatusApproved:
		return "通过"
	case model.InstanceStatusRejected:
		return "驳回"
	case model.InstanceStatusWithdrawn:
		return "撤回"
	default:
		return fmt.Sprintf("未知状态(%d)", status)
	}
}

func runeLen(s string) int { return len([]rune(s)) }

// requireOperator 返回当前登录用户 ID（审批动作的收口主体，fail-closed）。
func requireOperator(ctx context.Context) (int64, error) {
	if p, ok := authn.From(ctx); ok && p.UserID > 0 {
		return p.UserID, nil
	}
	return 0, xerr.Unauthorized("未登录")
}

// ---------- 发起 ----------

// StartInstance 发起流程实例（spec §4.1/§4.2/§4.4）：
//  1. 行锁读定义，状态机校验（仅发布状态可发起；停用不可发起）；
//  2. BusinessHandler 注册 guard（未注册 business_type 不允许发起）；
//  3. 解析首节点审批人（解析不到 → 400，不跳过节点、不自动通过）；
//  4. 落实例（锁定 definition_id + version 快照、标题/摘要快照）并按候选池
//     物化首节点任务（每候选人一条）；
//  5. 同事务投递 bpm_todo 通知。
func (s *Service) StartInstance(ctx context.Context, in StartInstanceInput) (*model.ProcessInstance, error) {
	var inst *model.ProcessInstance
	err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.startInstance(ctx, tx, in)
		if err != nil {
			return err
		}
		inst = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inst, nil
}

// StartInstanceInTx 在调用方事务内发起实例：业务模块「写业务单据 + 发起审批」
// 原子化的接点（与 notify.CreateInTx 同款约定，tx 由调用方开启并提交）。
func (s *Service) StartInstanceInTx(ctx context.Context, tx *gorm.DB, in StartInstanceInput) (*model.ProcessInstance, error) {
	if tx == nil {
		return nil, xerr.Internal(errors.New("bpm: StartInstanceInTx 需要调用方事务句柄，独立发起请用 StartInstance"))
	}
	return s.startInstance(ctx, repository.New(tx), in)
}

func (s *Service) startInstance(ctx context.Context, tx *repository.Repository, in StartInstanceInput) (*model.ProcessInstance, error) {
	operator, err := requireOperator(ctx)
	if err != nil {
		return nil, err
	}
	if in.BusinessID <= 0 {
		return nil, xerr.BadRequest("业务记录 ID 不能为空")
	}
	if err := validateText("标题", in.Title, MaxInstanceTitleLen, true); err != nil {
		return nil, err
	}
	if err := validateText("摘要", in.Summary, MaxInstanceSummaryLen, false); err != nil {
		return nil, err
	}

	// 行锁读定义：与停用/发布动作互斥，状态判定不被并发迁移。
	def, err := tx.FindDefinitionByIDForUpdate(ctx, in.DefinitionID)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, xerr.NotFound("流程定义不存在")
	}
	switch def.Status {
	case model.DefinitionStatusDraft:
		return nil, xerr.BadRequest("流程定义未发布，不可发起新实例")
	case model.DefinitionStatusDisabled:
		return nil, xerr.BadRequest("流程定义已停用，不可发起新实例（在途实例不受影响）")
	}
	if _, ok := s.handlerFor(def.BusinessType); !ok {
		return nil, xerr.BadRequest(fmt.Sprintf("业务类型 %s 未注册处理器，不能发起", def.BusinessType))
	}
	nodes := def.Nodes
	if len(nodes) == 0 {
		return nil, xerr.BadRequest("流程定义没有审批节点，不可发起")
	}

	// 首节点审批人解析：解析不到 → 400（spec §4.2），整笔回滚。
	assignees, err := ResolveAssignees(ctx, s.sys, nodes[0], operator)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	inst := &model.ProcessInstance{
		DefinitionID:      def.ID,
		DefinitionKey:     def.Key,
		DefinitionVersion: def.Version,
		BusinessType:      def.BusinessType,
		BusinessID:        in.BusinessID,
		InitiatorID:       operator,
		Title:             in.Title,
		Summary:           in.Summary,
		Status:            model.InstanceStatusRunning,
		StartedAt:         now,
	}
	if err := tx.CreateInstance(ctx, inst); err != nil {
		return nil, err
	}

	// 候选池物化：每候选人一条待办任务（节点名快照）。
	tasks := make([]model.Task, 0, len(assignees))
	for _, assignee := range assignees {
		tasks = append(tasks, model.Task{
			InstanceID: inst.ID,
			NodeIndex:  0,
			NodeName:   nodes[0].Name,
			AssigneeID: assignee,
			Status:     model.TaskStatusTodo,
		})
	}
	if err := tx.CreateTasks(ctx, tasks); err != nil {
		return nil, err
	}
	if err := s.notifyTodo(ctx, tx.DB(), inst.Title, tasks); err != nil {
		return nil, err
	}
	return inst, nil
}

// notifyTodo 给候选池任务批量投递 bpm_todo 通知（跳转任务详情）。
func (s *Service) notifyTodo(ctx context.Context, tx *gorm.DB, title string, tasks []model.Task) error {
	inputs := make([]notifyservice.Input, 0, len(tasks))
	for _, task := range tasks {
		inputs = append(inputs, notifyservice.Input{
			UserID:      task.AssigneeID,
			Type:        notifymodel.TypeBpmTodo,
			Title:       "您有新的审批任务",
			Content:     title,
			BizType:     BizTypeTask,
			BizID:       task.ID,
			Route:       "/bpm/task",
			RouteParams: fmt.Sprintf(`{"taskId":%d}`, task.ID),
		})
	}
	return s.notify.CreateInTx(ctx, tx, inputs)
}

// notifyResult 给发起人投递审批结果通知（bpm_result，跳转我发起 tab）。
func (s *Service) notifyResult(ctx context.Context, tx *gorm.DB, inst *model.ProcessInstance, title string) error {
	return s.notify.CreateInTx(ctx, tx, []notifyservice.Input{{
		UserID:      inst.InitiatorID,
		Type:        notifymodel.TypeBpmResult,
		Title:       title,
		Content:     inst.Title,
		BizType:     BizTypeInstance,
		BizID:       inst.ID,
		Route:       "/bpm/task",
		RouteParams: fmt.Sprintf(`{"tab":"initiated","instanceId":%d}`, inst.ID),
	}})
}

// markTodoRead 把办理人名下指定任务的 bpm_todo 通知置已读（与任务状态变更
// 同事务，spec §4.13）。幂等：已读行不再命中。
func (s *Service) markTodoRead(ctx context.Context, tx *gorm.DB, userID int64, taskIDs []int64) error {
	return s.notify.MarkBizReadInTx(ctx, tx, userID, BizTypeTask, taskIDs)
}

// resolveTodoNotifies 把任务集合的 bpm_todo 通知按办理人分组置已读：任务离开
// 待办态（办理/取消）后，通知指向的任务不再可办理——先办者胜出的落选者与
// 随实例结束被取消的候选人，其待办红点一并消化（spec §4.13 联动的完整口径）。
func (s *Service) resolveTodoNotifies(ctx context.Context, tx *gorm.DB, tasks []model.Task) error {
	byUser := make(map[int64][]int64, len(tasks))
	for _, task := range tasks {
		byUser[task.AssigneeID] = append(byUser[task.AssigneeID], task.ID)
	}
	for userID, taskIDs := range byUser {
		if err := s.markTodoRead(ctx, tx, userID, taskIDs); err != nil {
			return err
		}
	}
	return nil
}

// ---------- 动作统一前置 ----------

// lockForAction 以「实例行锁 → 任务行锁」的统一锁序锁定待办理的任务与实例
// （锁序约定见 repository/instance.go：同实例状态变更串行化，防死锁）。
// 返回行锁重读后的任务与实例；办理人身份与实例运行态在此统一把关。
func (s *Service) lockForAction(ctx context.Context, tx *repository.Repository, taskID, operatorID int64) (*model.Task, *model.ProcessInstance, error) {
	// 无锁预读：仅用于由任务路由到实例（权威判定在行锁重读后）。
	task, err := tx.FindTaskByID(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, xerr.NotFound("审批任务不存在")
	}
	inst, err := tx.FindInstanceByIDForUpdate(ctx, task.InstanceID)
	if err != nil {
		return nil, nil, err
	}
	if inst == nil {
		return nil, nil, xerr.NotFound("流程实例不存在")
	}
	if inst.Status != model.InstanceStatusRunning {
		return nil, nil, xerr.Conflict(fmt.Sprintf("流程已%s，不能再办理", instanceStatusName(inst.Status)))
	}
	task, err = tx.FindTaskByIDForUpdate(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, xerr.NotFound("审批任务不存在")
	}
	if task.AssigneeID != operatorID {
		return nil, nil, xerr.Forbidden("仅任务办理人可操作该任务")
	}
	return task, inst, nil
}

// checkTodo 断言任务仍待办：并发方已办理（先办者胜出）时返回 409。
func checkTodo(task *model.Task) error {
	if task.Status != model.TaskStatusTodo {
		return xerr.Conflict("任务已被办理或取消，请刷新后重试")
	}
	return nil
}

// validateComment 校验审批意见（spec §4.3）：required 时驳回必填 1–500 字，
// 其余可选 ≤500 字。
func validateComment(comment string, required bool) error {
	comment = strings.TrimSpace(comment)
	if required && comment == "" {
		return xerr.BadRequest("驳回原因不能为空")
	}
	if runeLen(comment) > MaxCommentLen {
		return xerr.BadRequest("审批意见不能超过 500 字")
	}
	return nil
}

// loadDefinitionNodes 读实例锁定的定义节点链。定义发布后不可变且存在实例
// 时不可删除，读不到属于数据异常。
func (s *Service) loadDefinitionNodes(ctx context.Context, tx *repository.Repository, inst *model.ProcessInstance) (model.NodeList, error) {
	def, err := tx.FindDefinitionByID(ctx, inst.DefinitionID)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, xerr.NotFound("流程定义不存在")
	}
	return def.Nodes, nil
}

// ---------- 通过 ----------

// ApproveTask 通过当前节点（spec §4.3）：记录通过；同节点兄弟任务取消
// （先办者胜出）；还有下一节点则物化下一批任务（解析不到 → 400 整笔回滚），
// 否则实例通过并在同事务回调 OnApproved；任务处理后把本人 bpm_todo 置已读。
func (s *Service) ApproveTask(ctx context.Context, taskID int64, comment string) (*model.ProcessInstance, error) {
	operator, err := requireOperator(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateComment(comment, false); err != nil {
		return nil, err
	}
	var inst *model.ProcessInstance
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.approveTask(ctx, tx, taskID, operator, comment)
		if err != nil {
			return err
		}
		inst = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 事务已提交：可选的提交后钩子在已提交数据上执行（issue #62 请假通过后
	// 的考勤结果重算）。钩子失败不回滚审批，由实现方自行记录。
	if inst != nil {
		if handler, ok := s.handlerFor(inst.BusinessType); ok {
			if hook, ok := handler.(ApprovalCommitHook); ok {
				hook.AfterApproved(ctx, inst)
			}
		}
	}
	return inst, nil
}

func (s *Service) approveTask(ctx context.Context, tx *repository.Repository, taskID, operator int64, comment string) (*model.ProcessInstance, error) {
	task, inst, err := s.lockForAction(ctx, tx, taskID, operator)
	if err != nil {
		return nil, err
	}
	if err := checkTodo(task); err != nil {
		return nil, err
	}
	nodes, err := s.loadDefinitionNodes(ctx, tx, inst)
	if err != nil {
		return nil, err
	}
	if task.NodeIndex < 0 || task.NodeIndex >= len(nodes) {
		return nil, xerr.Internal(fmt.Errorf("bpm: 任务 %d 节点序号 %d 越界（共 %d 节点）", task.ID, task.NodeIndex, len(nodes)))
	}

	now := time.Now()
	// 先收集当前待办（含本人与兄弟任务；须在推进下一节点之前——新节点的
	// 待办通知不能被误消化）。
	todos, err := tx.ListInstanceTodoTasks(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.CompleteTask(ctx, task.ID, model.TaskStatusApproved, now); err != nil {
		return nil, err
	}
	if err := tx.CreateRecord(ctx, &model.ApprovalRecord{
		InstanceID: inst.ID,
		TaskID:     task.ID,
		NodeIndex:  task.NodeIndex,
		NodeName:   task.NodeName,
		OperatorID: operator,
		Action:     model.RecordActionApprove,
		Comment:    strings.TrimSpace(comment),
		CreatedAt:  now,
	}); err != nil {
		return nil, err
	}
	// 先办者胜出：取消同节点兄弟待办。
	if _, err := tx.CancelSiblingTasks(ctx, inst.ID, task.NodeIndex, task.ID); err != nil {
		return nil, err
	}

	nextIndex := task.NodeIndex + 1
	if nextIndex < len(nodes) {
		// 推进下一节点：解析下一批审批人（空 → 400，整笔回滚，任务保持待办）。
		next := nodes[nextIndex]
		assignees, err := ResolveAssignees(ctx, s.sys, next, inst.InitiatorID)
		if err != nil {
			return nil, err
		}
		tasks := make([]model.Task, 0, len(assignees))
		for _, assignee := range assignees {
			tasks = append(tasks, model.Task{
				InstanceID: inst.ID,
				NodeIndex:  nextIndex,
				NodeName:   next.Name,
				AssigneeID: assignee,
				Status:     model.TaskStatusTodo,
			})
		}
		if err := tx.CreateTasks(ctx, tasks); err != nil {
			return nil, err
		}
		if err := s.notifyTodo(ctx, tx.DB(), inst.Title, tasks); err != nil {
			return nil, err
		}
	} else {
		// 最后一节点通过：实例通过，同事务回调业务。
		if err := tx.FinishInstance(ctx, inst.ID, model.InstanceStatusApproved, now); err != nil {
			return nil, err
		}
		inst.Status = model.InstanceStatusApproved
		inst.EndedAt = &now
		handler, ok := s.handlerFor(inst.BusinessType)
		if !ok {
			return nil, xerr.Internal(fmt.Errorf("bpm: 业务类型 %s 未注册处理器", inst.BusinessType))
		}
		if err := handler.OnApproved(ctx, inst, tx.DB()); err != nil {
			return nil, err
		}
		if err := s.notifyResult(ctx, tx.DB(), inst, "您发起的审批已通过"); err != nil {
			return nil, err
		}
	}
	// 本人任务（已通过）+ 被取消兄弟任务的待办通知一并消化（按办理人分组）。
	todos = append(todos, *task)
	if err := s.resolveTodoNotifies(ctx, tx.DB(), todos); err != nil {
		return nil, err
	}
	return inst, nil
}

// ---------- 驳回 ----------

// RejectTask 驳回（spec §4.3）：原因必填 1–500 字；记录驳回，实例驳回，
// 同实例所有待办取消，同事务回调 OnRejected。
func (s *Service) RejectTask(ctx context.Context, taskID int64, reason string) (*model.ProcessInstance, error) {
	operator, err := requireOperator(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateComment(reason, true); err != nil {
		return nil, err
	}
	var inst *model.ProcessInstance
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.rejectTask(ctx, tx, taskID, operator, strings.TrimSpace(reason))
		if err != nil {
			return err
		}
		inst = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inst, nil
}

func (s *Service) rejectTask(ctx context.Context, tx *repository.Repository, taskID, operator int64, reason string) (*model.ProcessInstance, error) {
	task, inst, err := s.lockForAction(ctx, tx, taskID, operator)
	if err != nil {
		return nil, err
	}
	if err := checkTodo(task); err != nil {
		return nil, err
	}
	now := time.Now()
	// 先收集全部待办（取消后查不到）：驳回后按办理人分组置其通知已读。
	todos, err := tx.ListInstanceTodoTasks(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.CompleteTask(ctx, task.ID, model.TaskStatusRejected, now); err != nil {
		return nil, err
	}
	if err := tx.CreateRecord(ctx, &model.ApprovalRecord{
		InstanceID: inst.ID,
		TaskID:     task.ID,
		NodeIndex:  task.NodeIndex,
		NodeName:   task.NodeName,
		OperatorID: operator,
		Action:     model.RecordActionReject,
		Comment:    reason,
		CreatedAt:  now,
	}); err != nil {
		return nil, err
	}
	// 实例驳回：同实例所有待办取消（含同节点兄弟）。
	if _, err := tx.CancelInstanceTodoTasks(ctx, inst.ID, task.ID); err != nil {
		return nil, err
	}
	if err := tx.FinishInstance(ctx, inst.ID, model.InstanceStatusRejected, now); err != nil {
		return nil, err
	}
	inst.Status = model.InstanceStatusRejected
	inst.EndedAt = &now
	handler, ok := s.handlerFor(inst.BusinessType)
	if !ok {
		return nil, xerr.Internal(fmt.Errorf("bpm: 业务类型 %s 未注册处理器", inst.BusinessType))
	}
	if err := handler.OnRejected(ctx, inst, tx.DB(), reason); err != nil {
		return nil, err
	}
	if err := s.notifyResult(ctx, tx.DB(), inst, "您发起的审批被驳回"); err != nil {
		return nil, err
	}
	// 办理人已读自己处理的任务；全部候选人的待办通知随任务取消一并消化。
	todos = append(todos, *task)
	if err := s.resolveTodoNotifies(ctx, tx.DB(), todos); err != nil {
		return nil, err
	}
	return inst, nil
}

// ---------- 撤回 ----------

// WithdrawInstance 发起人撤回（spec §4.3）：仅限实例运行中且尚无任何通过/
// 驳回记录；记录撤回，实例撤回，全部待办取消，同事务回调 OnWithdrawn。
func (s *Service) WithdrawInstance(ctx context.Context, instanceID int64, comment string) (*model.ProcessInstance, error) {
	operator, err := requireOperator(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateComment(comment, false); err != nil {
		return nil, err
	}
	var inst *model.ProcessInstance
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.withdrawInstance(ctx, tx, instanceID, operator, strings.TrimSpace(comment))
		if err != nil {
			return err
		}
		inst = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inst, nil
}

func (s *Service) withdrawInstance(ctx context.Context, tx *repository.Repository, instanceID, operator int64, comment string) (*model.ProcessInstance, error) {
	inst, err := tx.FindInstanceByIDForUpdate(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, xerr.NotFound("流程实例不存在")
	}
	if inst.InitiatorID != operator {
		return nil, xerr.Forbidden("仅发起人可撤回")
	}
	if inst.Status != model.InstanceStatusRunning {
		return nil, xerr.Conflict(fmt.Sprintf("流程已%s，不可撤回", instanceStatusName(inst.Status)))
	}
	count, err := tx.CountRecordsByInstanceActions(ctx, inst.ID, model.RecordActionApprove, model.RecordActionReject)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, xerr.Conflict("已存在审批动作，不可撤回")
	}

	// 收集全部待办（取消后查不到）：撤回后按办理人分组置其通知已读。
	todos, err := tx.ListInstanceTodoTasks(ctx, inst.ID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if err := tx.CreateRecord(ctx, &model.ApprovalRecord{
		InstanceID: inst.ID,
		TaskID:     0,
		NodeIndex:  0,
		NodeName:   "",
		OperatorID: operator,
		Action:     model.RecordActionWithdraw,
		Comment:    comment,
		CreatedAt:  now,
	}); err != nil {
		return nil, err
	}
	if _, err := tx.CancelInstanceTodoTasks(ctx, inst.ID, 0); err != nil {
		return nil, err
	}
	if err := tx.FinishInstance(ctx, inst.ID, model.InstanceStatusWithdrawn, now); err != nil {
		return nil, err
	}
	inst.Status = model.InstanceStatusWithdrawn
	inst.EndedAt = &now
	handler, ok := s.handlerFor(inst.BusinessType)
	if !ok {
		return nil, xerr.Internal(fmt.Errorf("bpm: 业务类型 %s 未注册处理器", inst.BusinessType))
	}
	if err := handler.OnWithdrawn(ctx, inst, tx.DB()); err != nil {
		return nil, err
	}
	if err := s.notifyResult(ctx, tx.DB(), inst, "您发起的审批已撤回"); err != nil {
		return nil, err
	}
	if err := s.resolveTodoNotifies(ctx, tx.DB(), todos); err != nil {
		return nil, err
	}
	return inst, nil
}

// ---------- 转办 ----------

// TransferTask 转办（spec §4.3）：当前任务关闭，为目标用户创建同节点新任务
// （目标须为同租户在职用户）；记录转办；实例继续运行。
func (s *Service) TransferTask(ctx context.Context, taskID, toUserID int64, comment string) (*model.Task, error) {
	operator, err := requireOperator(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateComment(comment, false); err != nil {
		return nil, err
	}
	var created *model.Task
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.transferTask(ctx, tx, taskID, toUserID, operator, strings.TrimSpace(comment))
		if err != nil {
			return err
		}
		created = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Service) transferTask(ctx context.Context, tx *repository.Repository, taskID, toUserID, operator int64, comment string) (*model.Task, error) {
	task, inst, err := s.lockForAction(ctx, tx, taskID, operator)
	if err != nil {
		return nil, err
	}
	if err := checkTodo(task); err != nil {
		return nil, err
	}
	active, err := s.sys.ActiveUserExists(ctx, toUserID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, xerr.BadRequest("转办目标用户不存在或已停用")
	}

	now := time.Now()
	if err := tx.TransferTask(ctx, task.ID); err != nil {
		return nil, err
	}
	newTask := model.Task{
		InstanceID: inst.ID,
		NodeIndex:  task.NodeIndex,
		NodeName:   task.NodeName,
		AssigneeID: toUserID,
		Status:     model.TaskStatusTodo,
	}
	if err := tx.CreateTasks(ctx, []model.Task{newTask}); err != nil {
		return nil, err
	}
	if err := tx.CreateRecord(ctx, &model.ApprovalRecord{
		InstanceID:       inst.ID,
		TaskID:           task.ID,
		NodeIndex:        task.NodeIndex,
		NodeName:         task.NodeName,
		OperatorID:       operator,
		Action:           model.RecordActionTransfer,
		Comment:          comment,
		TransferToUserID: toUserID,
		CreatedAt:        now,
	}); err != nil {
		return nil, err
	}
	if err := s.notifyTodo(ctx, tx.DB(), inst.Title, []model.Task{newTask}); err != nil {
		return nil, err
	}
	// 原任务的待办通知随转办关闭置已读。
	if err := s.markTodoRead(ctx, tx.DB(), operator, []int64{task.ID}); err != nil {
		return nil, err
	}
	return &newTask, nil
}

// ---------- 查询（通用待办页，spec §5）----------

// PageMyTasks 我的任务分页（tab=todo/done/initiated）：
//   - todo：我的待办任务（候选池物化，每候选人一行）；
//   - done：我办理过的任务（通过/驳回/转办；取消是系统行为不属于已办）；
//   - initiated：我发起的实例（返回模型行）。
//
// todo/done 行内联实例快照（业务摘要与业务详情入口字段）。
func (s *Service) PageMyTasks(ctx context.Context, req *dto.TaskPageReq) (any, int64, error) {
	operator, err := requireOperator(ctx)
	if err != nil {
		return nil, 0, err
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()

	switch req.Tab {
	case dto.TaskTabInitiated:
		rows, total, err := s.repo.PageInstancesByInitiator(ctx, operator, page.Offset(), page.PageSize)
		if err != nil {
			return nil, 0, err
		}
		return rows, total, nil
	case dto.TaskTabDone:
		rows, total, err := s.repo.PageTasksByAssignee(ctx, operator, repository.TaskStatusFilterDone, page.Offset(), page.PageSize)
		if err != nil {
			return nil, 0, err
		}
		items, err := s.assembleTaskItems(ctx, rows)
		return items, total, err
	default:
		rows, total, err := s.repo.PageTasksByAssignee(ctx, operator, repository.TaskStatusFilterTodo, page.Offset(), page.PageSize)
		if err != nil {
			return nil, 0, err
		}
		items, err := s.assembleTaskItems(ctx, rows)
		return items, total, err
	}
}

// assembleTaskItems 装配任务行 + 实例快照（跨模块展示字段一次性批量取）。
func (s *Service) assembleTaskItems(ctx context.Context, rows []model.Task) ([]dto.TaskItem, error) {
	ids := make([]int64, 0, len(rows))
	for _, task := range rows {
		ids = append(ids, task.InstanceID)
	}
	instances, err := s.repo.ListInstancesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]model.ProcessInstance, len(instances))
	for _, inst := range instances {
		byID[inst.ID] = inst
	}
	items := make([]dto.TaskItem, 0, len(rows))
	for _, task := range rows {
		inst, ok := byID[task.InstanceID]
		if !ok {
			// 实例行缺失属于数据异常（实例不软删）；跳过坏行而非中断列表。
			continue
		}
		items = append(items, dto.TaskItem{
			ID:                task.ID,
			InstanceID:        task.InstanceID,
			NodeIndex:         task.NodeIndex,
			NodeName:          task.NodeName,
			Status:            task.Status,
			HandledAt:         task.HandledAt,
			CreateTime:        task.CreatedAt,
			InstanceTitle:     inst.Title,
			InstanceSummary:   inst.Summary,
			BusinessType:      inst.BusinessType,
			BusinessID:        inst.BusinessID,
			InstanceStatus:    inst.Status,
			InitiatorID:       inst.InitiatorID,
			DefinitionKey:     inst.DefinitionKey,
			DefinitionVersion: inst.DefinitionVersion,
		})
	}
	return items, nil
}

// PageInstances 实例管理分页（bpm:instance:query 管理视图）。
func (s *Service) PageInstances(ctx context.Context, req *dto.InstancePageReq) ([]model.ProcessInstance, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.InstanceFilter{
		BusinessType: req.BusinessType,
		Status:       req.Status,
	}
	return s.repo.PageInstances(ctx, filter, page.Offset(), page.PageSize)
}

// GetInstanceDetail 实例详情（spec §5：发起人/办理人/持有 bpm:instance:query）：
// 实例 + 审批记录时间线（时间升序）+ 节点任务视图，展示名批量解析。
func (s *Service) GetInstanceDetail(ctx context.Context, id int64) (*dto.InstanceDetail, error) {
	operator, err := requireOperator(ctx)
	if err != nil {
		return nil, err
	}
	inst, err := s.repo.FindInstanceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, xerr.NotFound("流程实例不存在")
	}
	if inst.InitiatorID != operator {
		participant, err := s.repo.ExistsTaskByInstanceAssignee(ctx, inst.ID, operator)
		if err != nil {
			return nil, err
		}
		if !participant {
			granted, err := s.sys.UserHasPermission(ctx, operator, PermInstanceQuery)
			if err != nil {
				return nil, err
			}
			if !granted {
				return nil, xerr.Forbidden("仅发起人、办理人或持有实例查询权限的用户可查看流程详情")
			}
		}
	}
	return s.instanceDetailOf(ctx, inst)
}

// GetInstanceDetailForBusiness 实例详情（业务模块专用读接口，issue #53）：
// 跳过「发起人/办理人/bpm:instance:query」访问判定。调用方必须已按自己的
// 数据权限完成可见性校验（例如 OA 请假详情按 spec §4.6 的 manage → 部门
// 负责人子树 → 本人），再以实例 ID 读取时间线；不得把它暴露为绕过权限的
// 通用 HTTP 读接口。跨租户实例仍由租户插件过滤为 404。
func (s *Service) GetInstanceDetailForBusiness(ctx context.Context, id int64) (*dto.InstanceDetail, error) {
	inst, err := s.repo.FindInstanceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, xerr.NotFound("流程实例不存在")
	}
	return s.instanceDetailOf(ctx, inst)
}

// GetLatestInstanceDetailForBusiness 读业务记录最近一次实例的详情（issue #47）：
// 业务详情页展示审批时间线与当前待办。访问判定由调用方按自己的业务数据权限
// 完成后调用（与 GetInstanceDetailForBusiness 同款约定，不得直接暴露为 HTTP 接口）。
// 业务记录从未发起过审批时返回 (nil, nil)。
func (s *Service) GetLatestInstanceDetailForBusiness(ctx context.Context, businessType string, businessID int64) (*dto.InstanceDetail, error) {
	inst, err := s.repo.FindLatestInstanceByBusiness(ctx, businessType, businessID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, nil
	}
	return s.instanceDetailOf(ctx, inst)
}

// FindRunningInstanceByBusinessInTx 在调用方事务内查业务记录运行中实例（issue #47）：
// 存量 backfill 幂等判据与业务作废时终止实例的定位。无运行中实例返回 (nil, nil)。
func (s *Service) FindRunningInstanceByBusinessInTx(ctx context.Context, tx *gorm.DB, businessType string, businessID int64) (*model.ProcessInstance, error) {
	if tx == nil {
		return nil, xerr.Internal(errors.New("bpm: FindRunningInstanceByBusinessInTx 需要事务句柄"))
	}
	return repository.New(tx).FindRunningInstanceByBusiness(ctx, businessType, businessID)
}

// CancelInstanceInTx 业务侧终止运行中实例（issue #47：合同作废时审批流程自动
// 终止）：跳过「仅发起人可撤回」判定（业务单据的生命周期由业务模块负责），
// 记录一条撤回动作、取消全部待办、实例置撤回，并在同一事务内回调 OnWithdrawn，
// 与业务状态同生共死。无运行中实例返回 (false, nil)；已结束实例不再变更。
func (s *Service) CancelInstanceInTx(ctx context.Context, tx *gorm.DB, instanceID int64, comment string) (bool, error) {
	if tx == nil {
		return false, xerr.Internal(errors.New("bpm: CancelInstanceInTx 需要调用方事务句柄"))
	}
	operator, err := requireOperator(ctx)
	if err != nil {
		return false, err
	}
	if err := validateComment(comment, false); err != nil {
		return false, err
	}
	comment = strings.TrimSpace(comment)

	repo := repository.New(tx)
	inst, err := repo.FindInstanceByIDForUpdate(ctx, instanceID)
	if err != nil {
		return false, err
	}
	if inst == nil {
		return false, xerr.NotFound("流程实例不存在")
	}
	if inst.Status != model.InstanceStatusRunning {
		return false, nil
	}
	handler, ok := s.handlerFor(inst.BusinessType)
	if !ok {
		return false, xerr.Internal(fmt.Errorf("bpm: 业务类型 %s 未注册处理器", inst.BusinessType))
	}

	// 取消后查不到待办，先收集（按办理人分组消化 bpm_todo 通知）。
	todos, err := repo.ListInstanceTodoTasks(ctx, inst.ID)
	if err != nil {
		return false, err
	}

	now := time.Now()
	if err := repo.CreateRecord(ctx, &model.ApprovalRecord{
		InstanceID: inst.ID,
		TaskID:     0,
		NodeIndex:  0,
		NodeName:   "",
		OperatorID: operator,
		Action:     model.RecordActionWithdraw,
		Comment:    comment,
		CreatedAt:  now,
	}); err != nil {
		return false, err
	}
	if _, err := repo.CancelInstanceTodoTasks(ctx, inst.ID, 0); err != nil {
		return false, err
	}
	if err := repo.FinishInstance(ctx, inst.ID, model.InstanceStatusWithdrawn, now); err != nil {
		return false, err
	}
	inst.Status = model.InstanceStatusWithdrawn
	inst.EndedAt = &now
	if err := handler.OnWithdrawn(ctx, inst, tx); err != nil {
		return false, err
	}
	if err := s.notifyResult(ctx, tx, inst, "您发起的审批已终止"); err != nil {
		return false, err
	}
	if err := s.resolveTodoNotifies(ctx, tx, todos); err != nil {
		return false, err
	}
	return true, nil
}

// instanceDetailOf 组装实例详情（调用方已完成访问/业务可见性判定）。
func (s *Service) instanceDetailOf(ctx context.Context, inst *model.ProcessInstance) (*dto.InstanceDetail, error) {
	records, err := s.repo.ListRecordsByInstance(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	tasks, err := s.repo.ListTasksByInstance(ctx, inst.ID)
	if err != nil {
		return nil, err
	}

	// 展示名批量解析：记录操作人/转办目标 + 任务办理人。
	userIDs := make([]int64, 0, len(records)+len(tasks))
	for _, rec := range records {
		userIDs = append(userIDs, rec.OperatorID)
		if rec.TransferToUserID > 0 {
			userIDs = append(userIDs, rec.TransferToUserID)
		}
	}
	for _, task := range tasks {
		userIDs = append(userIDs, task.AssigneeID)
	}
	names, err := s.sys.UserNicknames(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	recordItems := make([]dto.RecordItem, 0, len(records))
	for _, rec := range records {
		recordItems = append(recordItems, dto.RecordItem{
			ApprovalRecord:     rec,
			OperatorName:       names[rec.OperatorID],
			TransferToUserName: names[rec.TransferToUserID],
		})
	}
	taskRows := make([]dto.TaskRow, 0, len(tasks))
	for _, task := range tasks {
		taskRows = append(taskRows, dto.TaskRow{Task: task, AssigneeName: names[task.AssigneeID]})
	}
	return &dto.InstanceDetail{
		Instance: inst,
		Records:  recordItems,
		Tasks:    taskRows,
	}, nil
}
