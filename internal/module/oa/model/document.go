// document.go 承载文档域的持久化模型（issue #58，docs/oa-mvp-spec.md
// §3.8/§4.12）：目录、目录 ACL、文件三表。
//
// 目录树是租户级共享资源；读权限按「最近一个有显式 ACL 的祖先」判定，
// 子目录 ACL 覆盖父目录而不是叠加（纯函数见 service/document_rule.go）。
// ACL 主体四类：0 全员 / 1 部门（含子树）/ 2 角色 / 3 用户；全员固定
// target_id=0。
//
// 文件本体存 infra_file（file_id），本表只存归属与展示字段；上传/列表/
// 下载/软删除的编排在 service/document_file.go（issue #59）。
package model

import "time"

// 目录 ACL 主体类型（spec §3.8/§4.12）。
const (
	DocumentACLTargetAll  = int8(0) // 全员（target_id 固定 0）
	DocumentACLTargetDept = int8(1) // 部门（含子树）
	DocumentACLTargetRole = int8(2) // 角色
	DocumentACLTargetUser = int8(3) // 用户
)

// ValidDocumentACLTargetType 报告 ACL 主体类型是否合法。
func ValidDocumentACLTargetType(t int8) bool {
	switch t {
	case DocumentACLTargetAll, DocumentACLTargetDept, DocumentACLTargetRole, DocumentACLTargetUser:
		return true
	}
	return false
}

// DocumentFolder 是目录树节点（spec §3.8）：
//   - parent_id=0 为顶层目录；顶层目录创建时必须显式设置至少一条 ACL，
//     否则正常数据里只会出现 owner/管理员可见的「无 ACL 顶层」历史行；
//   - owner_id 是目录 owner：owner 恒可见，且无需 oa:document:manage 即可
//     改名/删除/设置 ACL（spec §4.12 的管理权定义）。
type DocumentFolder struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// ParentID 父目录 ID（0 为顶层）。
	ParentID int64 `gorm:"not null;default:0" json:"parentId"`
	// Name 目录名称（1–100 字）。
	Name string `gorm:"size:100;not null" json:"name"`
	// OwnerID 目录 owner（system_user，租户内）。
	OwnerID int64 `gorm:"not null" json:"ownerId"`
	// Sort 同级显示顺序（小者在前）。
	Sort int `gorm:"not null;default:0" json:"sort"`
}

func (DocumentFolder) TableName() string { return "oa_document_folder" }

// DocumentFolderACL 是目录的显式 ACL 行（关联表：复合主键 + tenant_id，
// 不设软删除，spec §3 表结构总则）。目录删除时在事务内一并清理。
type DocumentFolderACL struct {
	TenantID   int64     `gorm:"primaryKey;not null;default:1" json:"tenantId"`
	FolderID   int64     `gorm:"primaryKey;not null" json:"folderId"`
	TargetType int8      `gorm:"primaryKey;not null" json:"targetType"`
	TargetID   int64     `gorm:"primaryKey;not null;default:0" json:"targetId"`
	CreatedAt  time.Time `json:"createTime"`
}

func (DocumentFolderACL) TableName() string { return "oa_document_folder_acl" }

// DocumentFile 是目录内文件的归属与展示行（spec §3.8）。文件本体存
// infra_file（file_id）；删除为软删除（BaseEntity.DeletedAt），软删行不再
// 参与列表/下载/目录删除计数（issue #59）。
type DocumentFile struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// FolderID 所属目录 ID。
	FolderID int64 `gorm:"not null" json:"folderId"`
	// FileID 文件本体 ID（infra_file.id）。
	FileID int64 `gorm:"not null" json:"fileId"`
	// Name 文件名（展示用，≤200 字）。
	Name string `gorm:"size:200;not null" json:"name"`
	// Size 文件大小（字节）。
	Size int64 `gorm:"not null;default:0" json:"size"`
	// Type MIME 类型。
	Type string `gorm:"size:100" json:"type"`
	// UploaderID 上传人（system_user，租户内）。
	UploaderID int64 `gorm:"not null" json:"uploaderId"`
}

func (DocumentFile) TableName() string { return "oa_document_file" }
