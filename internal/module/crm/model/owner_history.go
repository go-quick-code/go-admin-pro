package model

import "time"

// 负责人历史操作类型（docs/crm-mvp-spec.md §4.1：负责人变更写 crm_customer_owner_history）。
// recycle（自动回收）由 #14 引入：系统定时任务清空负责人时落 recycle 行，
// 操作人为 0（系统动作，无登录用户）。
const (
	OwnerHistoryCreate   = "create"   // 创建：落首任负责人（含创建即入公海的空场景，to_owner 为 0）
	OwnerHistoryClaim    = "claim"    // 认领：从公海认领到自己名下
	OwnerHistoryRelease  = "release"  // 释放：释放回公海
	OwnerHistoryTransfer = "transfer" // 转移：变更负责人给同租户其他用户
	OwnerHistoryRecycle  = "recycle"  // 回收：超期未跟进由系统自动回收入池（#14）
)

// ValidOwnerHistoryAction 报告操作类型是否合法。
func ValidOwnerHistoryAction(action string) bool {
	switch action {
	case OwnerHistoryCreate, OwnerHistoryClaim, OwnerHistoryRelease,
		OwnerHistoryTransfer, OwnerHistoryRecycle:
		return true
	}
	return false
}

// CustomerOwnerHistory 是客户负责人变更的追加型历史行（issue #13）。
//
// 与实体不同，本表不套用 BaseEntity（ADR 0002 底座为「可变实体」设计：
// updated_at/deleted_at/creator/updater 服务于改写与软删除路径）；历史行
// 写入后不再变更、不软删除，故只保留 created_at 一列时间（即操作时间），
// 操作人落显式业务列 operator_user_id，行随客户软删除保留以满足审计追溯。
// owner 为 0 表示「无」（原负责人 0 = 从公海/首任，新负责人 0 = 释放入池）。
type CustomerOwnerHistory struct {
	ID              int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	CreatedAt       time.Time `json:"createTime"`
	TenantID        int64     `gorm:"index:idx_crm_owner_history_tenant_customer,priority:1" json:"tenantId"`
	CustomerID      int64     `gorm:"index:idx_crm_owner_history_tenant_customer,priority:2;not null" json:"customerId"`
	Action          string    `gorm:"size:16;not null" json:"action"`
	FromOwnerUserID int64     `json:"fromOwnerUserId"`
	ToOwnerUserID   int64     `json:"toOwnerUserId"`
	FromOwnerDeptID int64     `json:"fromOwnerDeptId"`
	ToOwnerDeptID   int64     `json:"toOwnerDeptId"`
	OperatorUserID  int64     `json:"operatorUserId"`

	// 非持久化字段：操作人/负责人昵称与部门名称，由 service 层批量补齐供展示。
	FromOwnerUserName string `gorm:"-" json:"fromOwnerUserName,omitempty"`
	ToOwnerUserName   string `gorm:"-" json:"toOwnerUserName,omitempty"`
	OperatorUserName  string `gorm:"-" json:"operatorUserName,omitempty"`
	FromOwnerDeptName string `gorm:"-" json:"fromOwnerDeptName,omitempty"`
	ToOwnerDeptName   string `gorm:"-" json:"toOwnerDeptName,omitempty"`
}

func (CustomerOwnerHistory) TableName() string { return "crm_customer_owner_history" }
