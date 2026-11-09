package model

// doc_base.go 是单据主表的共享基类（issue #98 后续，ADR 0018）：12 类单据
// 的审核状态与归属字段完全同构，抽成 DocBase 内嵌，使「所有单据头部都有
// status/owner/tenant」成为类型事实而非约定。
//
// 列名与 JSON 字段保持既有口径（tenant_id/status/owner_user_id/owner_dept_id），
// 不产生迁移或对外契约变化；Remark 不在基类（stock_move/sale_out/sale_return
// 没有该列）。

// DocBase 是单据主表的审核状态、归属与租户字段。
type DocBase struct {
	TenantID    int64 `gorm:"index;not null;default:1" json:"tenantId"`
	Status      int8  `gorm:"not null;default:10" json:"status"`
	OwnerUserID int64 `gorm:"index;not null;default:0" json:"ownerUserId"`
	OwnerDeptID int64 `gorm:"index;not null;default:0" json:"ownerDeptId"`
}

// Base 返回自身，供 doc.Header 接口经内嵌提升。
func (b *DocBase) Base() *DocBase { return b }

// GetStatus 返回审核状态（10 未审核 / 20 已审核）。
func (b *DocBase) GetStatus() int8 { return b.Status }

// SetStatus 设置审核状态。
func (b *DocBase) SetStatus(v int8) { b.Status = v }

// GetOwner 返回归属人快照（ownerUserID, ownerDeptID）。
func (b *DocBase) GetOwner() (int64, int64) { return b.OwnerUserID, b.OwnerDeptID }

// SetOwner 写入归属人快照。
func (b *DocBase) SetOwner(userID, deptID int64) {
	b.OwnerUserID = userID
	b.OwnerDeptID = deptID
}
