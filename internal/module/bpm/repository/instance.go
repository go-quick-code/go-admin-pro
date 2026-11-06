// instance.go 承载流程运行的数据访问（issue #46，spec #42 §4.3）：
// 实例/任务的行锁读写、候选池任务物化、同节点与全实例待办取消、
// 审批记录追加与通用待办分页。
//
// 锁序约定（防死锁）：一律先锁实例行、再锁任务行——approve/reject/transfer
// 由任务路由到实例（任务先无锁预读拿 instance_id），withdraw 直接锁实例；
// 实例行锁串行化同实例的全部状态变更，任务行的写操作只发生在持有实例锁
// 的事务里，因此取消/更新任务无需再加行锁。
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
	"gorm.io/gorm/clause"
)

// CreateInstance 落一条运行中实例（definition_id/version 快照由 service 层
// 从锁定读到的定义回填）。tenant_id 由租户插件按 ctx 回填。
func (r *Repository) CreateInstance(ctx context.Context, inst *model.ProcessInstance) error {
	return r.db.WithContext(ctx).Create(inst).Error
}

// CreateTasks 批量物化候选池任务（每人一条），回填自增 ID（通知扇出按任务
// ID 定位）。tenant_id 由租户插件回填。
func (r *Repository) CreateTasks(ctx context.Context, tasks []model.Task) error {
	if len(tasks) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&tasks).Error
}

// FindInstanceByID 按 ID 读实例；未命中（含跨租户）返回 nil。
func (r *Repository) FindInstanceByID(ctx context.Context, id int64) (*model.ProcessInstance, error) {
	var row model.ProcessInstance
	err := r.db.WithContext(ctx).First(&row, id).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindInstanceByIDForUpdate 行锁读实例（同实例全部状态变更的串行化入口）。
func (r *Repository) FindInstanceByIDForUpdate(ctx context.Context, id int64) (*model.ProcessInstance, error) {
	var row model.ProcessInstance
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindTaskByID 按 ID 读任务（无锁预读：approve/reject/transfer 由任务路由
// 到实例，权威判定在实例锁后的行锁重读）。
func (r *Repository) FindTaskByID(ctx context.Context, id int64) (*model.Task, error) {
	var row model.Task
	err := r.db.WithContext(ctx).First(&row, id).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindTaskByIDForUpdate 行锁读任务（实例锁之后的权威重读）。
func (r *Repository) FindTaskByIDForUpdate(ctx context.Context, id int64) (*model.Task, error) {
	var row model.Task
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// CompleteTask 把任务置为办理完成（通过/驳回），落办理时间。
func (r *Repository) CompleteTask(ctx context.Context, taskID int64, status int8, handledAt time.Time) error {
	return r.db.WithContext(ctx).Model(&model.Task{}).
		Where("id = ?", taskID).
		Updates(map[string]any{"status": status, "handled_at": handledAt}).Error
}

// TransferTask 把任务置为转办（handled_at 不落：转办时间由记录表追溯，
// 列注释口径见 migrations/00014）。
func (r *Repository) TransferTask(ctx context.Context, taskID int64) error {
	return r.db.WithContext(ctx).Model(&model.Task{}).
		Where("id = ?", taskID).
		Update("status", model.TaskStatusTransferred).Error
}

// CancelSiblingTasks 取消同实例同节点的其余待办（先办者胜出，ADR 0008），
// 返回取消行数。调用方持有实例行锁，同实例任务写操作天然串行。
func (r *Repository) CancelSiblingTasks(ctx context.Context, instanceID int64, nodeIndex int, excludeTaskID int64) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Task{}).
		Where("instance_id = ? AND node_index = ? AND status = ? AND id <> ?",
			instanceID, nodeIndex, model.TaskStatusTodo, excludeTaskID).
		Update("status", model.TaskStatusCancelled)
	return res.RowsAffected, res.Error
}

// CancelInstanceTodoTasks 取消实例的全部待办（驳回/撤回结束实例时），返回
// 取消行数。excludeTaskID 通常为 0（无排除）。
func (r *Repository) CancelInstanceTodoTasks(ctx context.Context, instanceID, excludeTaskID int64) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Task{}).
		Where("instance_id = ? AND status = ? AND id <> ?",
			instanceID, model.TaskStatusTodo, excludeTaskID).
		Update("status", model.TaskStatusCancelled)
	return res.RowsAffected, res.Error
}

// ListInstanceTodoTasks 读实例的全部待办（撤回前收集发起人名下任务 ID，
// 联动置通知已读用）。
func (r *Repository) ListInstanceTodoTasks(ctx context.Context, instanceID int64) ([]model.Task, error) {
	var rows []model.Task
	err := r.db.WithContext(ctx).
		Where("instance_id = ? AND status = ?", instanceID, model.TaskStatusTodo).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// FinishInstance 把实例置为终态（通过/驳回/撤回）并落结束时间。
func (r *Repository) FinishInstance(ctx context.Context, instanceID int64, status int8, endedAt time.Time) error {
	return r.db.WithContext(ctx).Model(&model.ProcessInstance{}).
		Where("id = ?", instanceID).
		Updates(map[string]any{"status": status, "ended_at": endedAt}).Error
}

// CreateRecord 追加一条审批记录（追加型表，不可变；spec §4.3）。
func (r *Repository) CreateRecord(ctx context.Context, rec *model.ApprovalRecord) error {
	return r.db.WithContext(ctx).Create(rec).Error
}

// CountRecordsByInstanceActions 统计实例上指定动作的记录数（撤回前置条件：
// 尚无任何通过/驳回记录，spec §4.3）。
func (r *Repository) CountRecordsByInstanceActions(ctx context.Context, instanceID int64, actions ...int8) (int64, error) {
	if len(actions) == 0 {
		return 0, nil
	}
	var n int64
	err := r.db.WithContext(ctx).Model(&model.ApprovalRecord{}).
		Where("instance_id = ? AND `action` IN ?", instanceID, actions).
		Count(&n).Error
	return n, err
}

// ListRecordsByInstance 读实例的审批记录时间线（时间升序，spec §4.3：
// 详情页按时间展示审批尝试）。
func (r *Repository) ListRecordsByInstance(ctx context.Context, instanceID int64) ([]model.ApprovalRecord, error) {
	var rows []model.ApprovalRecord
	err := r.db.WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// ListTasksByInstance 读实例的全部任务（详情页节点视图）。
func (r *Repository) ListTasksByInstance(ctx context.Context, instanceID int64) ([]model.Task, error) {
	var rows []model.Task
	err := r.db.WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Order("node_index ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

// ExistsTaskByInstanceAssignee 判定用户是否参与过实例（任意任务，含已办）。
// 实例详情访问判定用（spec §5：发起人/办理人/bpm:instance:query）。
func (r *Repository) ExistsTaskByInstanceAssignee(ctx context.Context, instanceID, userID int64) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Task{}).
		Where("instance_id = ? AND assignee_id = ?", instanceID, userID).
		Count(&n).Error
	return n > 0, err
}

// TaskStatusFilter 是我的任务分页的状态过滤。
type TaskStatusFilter int

const (
	// TaskStatusFilterTodo 待办（status = 0）。
	TaskStatusFilterTodo TaskStatusFilter = iota
	// TaskStatusFilterDone 已办（status IN 通过/驳回/转办；取消是系统行为，
	// 未被本人办理，不属于已办）。
	TaskStatusFilterDone
)

// PageTasksByAssignee 按办理人分页任务（id 倒序，走
// idx_bpm_task_tenant_assignee_status）。租户条件由插件追加。
func (r *Repository) PageTasksByAssignee(ctx context.Context, assigneeID int64, filter TaskStatusFilter, offset, limit int) ([]model.Task, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Task{}).
		Where("assignee_id = ?", assigneeID)
	switch filter {
	case TaskStatusFilterTodo:
		q = q.Where("status = ?", model.TaskStatusTodo)
	case TaskStatusFilterDone:
		q = q.Where("status IN ?", []int8{model.TaskStatusApproved, model.TaskStatusRejected, model.TaskStatusTransferred})
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计审批任务: %w", err)
	}
	var rows []model.Task
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// PageInstancesByInitiator 按发起人分页实例（我发起 tab）。
func (r *Repository) PageInstancesByInitiator(ctx context.Context, initiatorID int64, offset, limit int) ([]model.ProcessInstance, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.ProcessInstance{}).
		Where("initiator_id = ?", initiatorID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计我发起的实例: %w", err)
	}
	var rows []model.ProcessInstance
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// InstanceFilter 是实例管理分页的筛选条件。
type InstanceFilter struct {
	BusinessType string
	Status       *int8
}

// PageInstances 分页查询实例（bpm:instance:query 的管理视图）。
func (r *Repository) PageInstances(ctx context.Context, filter InstanceFilter, offset, limit int) ([]model.ProcessInstance, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.ProcessInstance{})
	if filter.BusinessType != "" {
		q = q.Where("business_type = ?", filter.BusinessType)
	}
	if filter.Status != nil {
		q = q.Where("status = ?", filter.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计流程实例: %w", err)
	}
	var rows []model.ProcessInstance
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// ListInstancesByIDs 批量读实例（我的任务分页装配实例快照用；空集 no-op）。
func (r *Repository) ListInstancesByIDs(ctx context.Context, ids []int64) ([]model.ProcessInstance, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []model.ProcessInstance
	err := r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Find(&rows).Error
	return rows, err
}

// ListInstancesByBusiness 按业务类型 + 业务 ID 读全部实例（id 倒序），
// 未命中返回空切片。业务详情展示历次审批尝试用（spec §4.3：每次重新提交
// 创建新实例，业务记录保留全部历史实例）。
func (r *Repository) ListInstancesByBusiness(ctx context.Context, businessType string, businessID int64) ([]model.ProcessInstance, error) {
	var rows []model.ProcessInstance
	err := r.db.WithContext(ctx).
		Where("business_type = ? AND business_id = ?", businessType, businessID).
		Order("id DESC").
		Find(&rows).Error
	return rows, err
}

// FindLatestInstanceByBusiness 按业务类型 + 业务 ID 读最近一次实例（id 倒序），
// 未命中（含跨租户）返回 nil。业务详情展示审批时间线用（issue #47）。
func (r *Repository) FindLatestInstanceByBusiness(ctx context.Context, businessType string, businessID int64) (*model.ProcessInstance, error) {
	var row model.ProcessInstance
	err := r.db.WithContext(ctx).
		Where("business_type = ? AND business_id = ?", businessType, businessID).
		Order("id DESC").
		First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindRunningInstanceByBusiness 按业务类型 + 业务 ID 读运行中实例，未命中返回 nil。
// issue #47 存量 backfill 的幂等判据与业务作废时的实例终止判据。
func (r *Repository) FindRunningInstanceByBusiness(ctx context.Context, businessType string, businessID int64) (*model.ProcessInstance, error) {
	var row model.ProcessInstance
	err := r.db.WithContext(ctx).
		Where("business_type = ? AND business_id = ? AND status = ?",
			businessType, businessID, model.InstanceStatusRunning).
		Order("id DESC").
		First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}
