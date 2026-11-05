// Package model 是 bpm 模块的实体层（spec #42 §3.1，issue #44）：
// 流程定义 / 实例 / 任务 / 审批记录四张表。实例、任务、审批记录的运行逻辑
// 属 issue #46，本工单只交付 schema 对齐的模型与定义管理闭环。
package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	basemodel "github.com/go-quick-code/go-admin-pro/internal/pkg/model"
)

// BaseEntity 是统一实体底座（internal/pkg/model），见 migrations/00014_bpm_core.sql。
type BaseEntity = basemodel.BaseEntity

// 流程定义状态（spec §3.1：0 草稿 / 1 发布 / 2 停用）。
const (
	DefinitionStatusDraft     = int8(0)
	DefinitionStatusPublished = int8(1)
	DefinitionStatusDisabled  = int8(2)
)

// DefinitionStatusName 返回定义状态的中文展示名。
func DefinitionStatusName(status int8) string {
	switch status {
	case DefinitionStatusDraft:
		return "草稿"
	case DefinitionStatusPublished:
		return "已发布"
	case DefinitionStatusDisabled:
		return "已停用"
	default:
		return fmt.Sprintf("未知状态(%d)", status)
	}
}

// Node 是流程定义中的一个审批节点（spec §3.1）：节点按数组顺序构成线性
// 审批链（ADR 0008），通过前进、驳回结束。
type Node struct {
	// Name 节点名称（1–64 字）。
	Name string `json:"name"`
	// AssigneeType 审批人规则（六种，spec §4.2）：角色/权限码规则运行期
	// 组成候选池，其余规则解析出单个人（解析逻辑属 #46）。
	AssigneeType string `json:"assigneeType"`
	// AssigneeValue 规则取值：user=用户 ID、role=角色 ID、permission=权限码；
	// initiator/initiator_dept_leader/dept_leader 不使用。
	AssigneeValue string `json:"assigneeValue"`
	// DeptID 仅 AssigneeDeptLeader（指定部门负责人）使用。
	DeptID int64 `json:"deptId,omitempty"`
}

// 六种审批人规则（spec §4.2）。
const (
	AssigneeUser                = "user"                  // 指定用户 ID，单节点单人
	AssigneeRole                = "role"                  // 角色 ID；该角色启用用户组成候选池
	AssigneePermission          = "permission"            // 权限码；持有者组成候选池（super_admin 视为持有全部）
	AssigneeInitiator           = "initiator"             // 发起人本人
	AssigneeInitiatorDeptLeader = "initiator_dept_leader" // 发起人部门负责人，空缺沿部门树向上
	AssigneeDeptLeader          = "dept_leader"           // DeptID 指定部门的负责人，空缺沿部门树向上
)

// ProcessDefinition 是流程定义（spec §3.1）：key 标识业务流程，同一 key 的
// 多个 version 构成版本历史；同一 key 同时只有一个发布版本（发布新版本自动
// 停用旧版本，spec §4.1）。nodes 以 JSON 持久化节点数组。
type ProcessDefinition struct {
	BaseEntity
	TenantID int64 `gorm:"index;not null;default:1" json:"tenantId"`
	// Key 流程标识（业务内唯一，版本历史按 key 聚合）。列名 key 为 MySQL
	// 保留字，GORM 生成 SQL 时统一反引号包裹。
	Key string `gorm:"column:key;size:64;not null" json:"key"`
	// Version 版本号：创建草稿时按同 key 已有版本（含软删）max+1 发放，
	// 落库后不可变。
	Version int    `gorm:"not null;default:1" json:"version"`
	Name    string `gorm:"size:64;not null" json:"name"`
	// BusinessType 业务类型（如 crm_contract_approval），发布时要求已注册
	// BusinessHandler（spec §4.4）。
	BusinessType string `gorm:"size:64;not null" json:"businessType"`
	// Nodes 有序审批节点数组，JSON 列。
	Nodes NodeList `gorm:"type:json;not null" json:"nodes"`
	// Status 0 草稿 / 1 发布 / 2 停用。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// PublishedAt 发布时间；草稿为 NULL。
	PublishedAt *time.Time `gorm:"" json:"publishedAt"`
	Remark      string     `gorm:"size:500" json:"remark"`
}

func (ProcessDefinition) TableName() string { return "bpm_process_definition" }

// ProcessInstance 是流程实例（spec §3.1）：创建时锁定 definition_id 与
// definition_version，后续定义变化不影响在途实例（spec §4.1）。
// issue #44 只交付 schema，运行逻辑见 #46。
type ProcessInstance struct {
	BaseEntity
	TenantID          int64  `gorm:"not null;default:1" json:"tenantId"`
	DefinitionID      int64  `gorm:"not null" json:"definitionId"`
	DefinitionKey     string `gorm:"size:64;not null" json:"definitionKey"`
	DefinitionVersion int    `gorm:"not null" json:"definitionVersion"`
	BusinessType      string `gorm:"size:64;not null" json:"businessType"`
	BusinessID        int64  `gorm:"not null" json:"businessId"`
	InitiatorID       int64  `gorm:"not null" json:"initiatorId"`
	Title             string `gorm:"size:128;not null" json:"title"`
	Summary           string `gorm:"size:500" json:"summary"`
	// Status 0 运行中 / 1 通过 / 2 驳回 / 3 撤回（spec §3.1）。
	Status    int8       `gorm:"not null;default:0" json:"status"`
	StartedAt time.Time  `gorm:"" json:"startedAt"`
	EndedAt   *time.Time `gorm:"" json:"endedAt"`
}

func (ProcessInstance) TableName() string { return "bpm_process_instance" }

// 实例状态（spec §3.1）。
const (
	InstanceStatusRunning   = int8(0)
	InstanceStatusApproved  = int8(1)
	InstanceStatusRejected  = int8(2)
	InstanceStatusWithdrawn = int8(3)
)

// Task 是审批任务（spec §3.1）：候选池物化为每候选人一条任务，任意一人
// 办理后同节点其余任务取消（ADR 0008）。运行逻辑见 #46。
type Task struct {
	BaseEntity
	TenantID   int64  `gorm:"not null;default:1" json:"tenantId"`
	InstanceID int64  `gorm:"not null" json:"instanceId"`
	NodeIndex  int    `gorm:"not null" json:"nodeIndex"`
	NodeName   string `gorm:"size:64;not null" json:"nodeName"`
	AssigneeID int64  `gorm:"not null" json:"assigneeId"`
	// Status 0 待办 / 1 通过 / 2 驳回 / 3 转办 / 4 取消（spec §3.1）。
	Status    int8       `gorm:"not null;default:0" json:"status"`
	HandledAt *time.Time `gorm:"" json:"handledAt"`
}

func (Task) TableName() string { return "bpm_task" }

// 任务状态（spec §3.1）。
const (
	TaskStatusTodo        = int8(0)
	TaskStatusApproved    = int8(1)
	TaskStatusRejected    = int8(2)
	TaskStatusTransferred = int8(3)
	TaskStatusCancelled   = int8(4)
)

// ApprovalRecord 是审批记录（spec §3.1）：追加型表，不设软删除与
// creator/updater，只保留业务时间 created_at。运行逻辑见 #46。
type ApprovalRecord struct {
	// ID/CreateTime：追加型底座，不用 BaseEntity（无 updated_at/deleted_at/审计字段）。
	ID         int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID   int64  `gorm:"not null;default:1" json:"tenantId"`
	InstanceID int64  `gorm:"not null" json:"instanceId"`
	TaskID     int64  `gorm:"not null;default:0" json:"taskId"`
	NodeIndex  int    `gorm:"not null" json:"nodeIndex"`
	NodeName   string `gorm:"size:64" json:"nodeName"`
	OperatorID int64  `gorm:"not null" json:"operatorId"`
	// Action 1 通过 / 2 驳回 / 3 撤回 / 4 转办（spec §3.1）。
	Action  int8   `gorm:"not null" json:"action"`
	Comment string `gorm:"size:500" json:"comment"`
	// TransferToUserID 仅转办记录使用。
	TransferToUserID int64     `gorm:"not null;default:0" json:"transferToUserId"`
	CreatedAt        time.Time `gorm:"" json:"createTime"`
}

func (ApprovalRecord) TableName() string { return "bpm_approval_record" }

// 审批记录动作（spec §3.1）。
const (
	RecordActionApprove  = int8(1)
	RecordActionReject   = int8(2)
	RecordActionWithdraw = int8(3)
	RecordActionTransfer = int8(4)
)

// NodeList 是 nodes JSON 列的 GORM 映射：[]Node 与 JSON 互转。
type NodeList []Node

// Scan 实现 sql.Scanner：从 JSON 字节反序列化节点数组。
func (n *NodeList) Scan(value any) error {
	if value == nil {
		*n = nil
		return nil
	}
	raw, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("bpm: nodes 列类型异常 %T", value)
	}
	var list NodeList
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("bpm: nodes 反序列化失败: %w", err)
	}
	*n = list
	return nil
}

// Value 实现 driver.Valuer：序列化为 JSON 字节。
func (n NodeList) Value() (driver.Value, error) {
	if n == nil {
		n = NodeList{}
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("bpm: nodes 序列化失败: %w", err)
	}
	return raw, nil
}
