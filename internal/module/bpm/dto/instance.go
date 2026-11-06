// Package dto 是 bpm 模块的请求/响应 DTO。实例/任务运行部分属 issue #46
// （spec #42 §5 bpm 节）。
package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/bpm/model"
)

// 通用待办页的三个 tab（spec §5：GET /bpm/task/page tab=todo/done/initiated）。
const (
	TaskTabTodo      = "todo"      // 待办：我的待办任务
	TaskTabDone      = "done"      // 已办：我办理过的任务（通过/驳回/转办）
	TaskTabInitiated = "initiated" // 我发起：我发起的实例
)

// TaskPageReq 我的任务分页（tab=todo/done/initiated，默认 todo）。
type TaskPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Tab      string `form:"tab"`
}

// TaskItem 是待办/已办 tab 的一行：任务本身 + 实例快照（业务摘要与
// 业务详情入口所需字段，spec：业务摘要、审批时间线、业务详情入口）。
type TaskItem struct {
	// 任务字段。
	ID         int64      `json:"id"`
	InstanceID int64      `json:"instanceId"`
	NodeIndex  int        `json:"nodeIndex"`
	NodeName   string     `json:"nodeName"`
	Status     int8       `json:"status"`
	HandledAt  *time.Time `json:"handledAt"`
	CreateTime time.Time  `json:"createTime"`
	// 实例快照字段（业务摘要/入口）。
	InstanceTitle     string `json:"instanceTitle"`
	InstanceSummary   string `json:"instanceSummary"`
	BusinessType      string `json:"businessType"`
	BusinessID        int64  `json:"businessId"`
	InstanceStatus    int8   `json:"instanceStatus"`
	InitiatorID       int64  `json:"initiatorId"`
	DefinitionKey     string `json:"definitionKey"`
	DefinitionVersion int    `json:"definitionVersion"`
}

// InstancePageReq 实例管理分页（bpm:instance:query 管理视图）。
type InstancePageReq struct {
	PageNo       int    `form:"pageNo"`
	PageSize     int    `form:"pageSize"`
	BusinessType string `form:"businessType"`
	Status       *int8  `form:"status"`
}

// TaskActionReq 通过/驳回/撤回的通用请求体：comment 为审批意见（驳回必填
// 1–500 字，其余可选 ≤500 字；校验在 service 层给中文错误，spec §4.3）。
type TaskActionReq struct {
	Comment string `json:"comment"`
}

// TaskTransferReq 转办请求：目标为同租户在职用户（spec §4.3）。
type TaskTransferReq struct {
	ToUserID int64  `json:"toUserId" binding:"required"`
	Comment  string `json:"comment"`
}

// RecordItem 是审批时间线的一行：记录 + 展示名（操作人/转办目标昵称）。
type RecordItem struct {
	model.ApprovalRecord
	OperatorName       string `json:"operatorName"`
	TransferToUserName string `json:"transferToUserName,omitempty"`
}

// TaskRow 是实例详情里的节点任务视图：任务 + 办理人昵称。
type TaskRow struct {
	model.Task
	AssigneeName string `json:"assigneeName"`
}

// InstanceDetail 是实例详情（审批时间线 + 节点任务视图；spec：通用待办页
// 的业务摘要、审批时间线与业务详情入口）。
type InstanceDetail struct {
	Instance *model.ProcessInstance `json:"instance"`
	Records  []RecordItem           `json:"records"`
	Tasks    []TaskRow              `json:"tasks"`
	// Current 标记业务记录当前生效的实例（spec §4.3：详情页倒序展示历次
	// 审批尝试。由业务读接口按 approval_instance_id 或最新实例回填；
	// 通用实例详情接口不设置，前端按缺省 false 处理）。
	Current bool `json:"current,omitempty"`
}
