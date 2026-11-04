package model

import "time"

// User 是系统用户。
type User struct {
	BaseEntity
	TenantID  int64      `gorm:"index;not null;default:1" json:"tenantId"`
	Username  string     `gorm:"size:64;not null" json:"username"`
	Password  string     `gorm:"size:128;not null" json:"-"`
	Nickname  string     `gorm:"size:64;not null" json:"nickname"`
	Remark    string     `gorm:"size:500" json:"remark"`
	DeptID    int64      `gorm:"index" json:"deptId"`
	Mobile    string     `gorm:"size:32" json:"mobile"`
	Email     string     `gorm:"size:128" json:"email"`
	Gender    int8       `gorm:"not null;default:0" json:"gender"` // 0 未知 1 男 2 女
	Avatar    string     `gorm:"size:512" json:"avatar"`
	Status    int8       `gorm:"not null;default:0" json:"status"`
	LoginIP   string     `gorm:"size:64" json:"loginIp"`
	LoginDate *time.Time `json:"loginDate"`

	// 非持久化字段，用于接口返回与表单提交。
	DeptName string  `gorm:"-" json:"deptName,omitempty"`
	RoleIDs  []int64 `gorm:"-" json:"roleIds,omitempty"`
	PostIDs  []int64 `gorm:"-" json:"postIds,omitempty"`
}

func (User) TableName() string { return "system_user" }

// SimpleUser 是用户精简项：表单选项（如 CRM 负责人选择）用，不含敏感字段。
type SimpleUser struct {
	ID       int64  `json:"id"`
	Nickname string `json:"nickname"`
	DeptID   int64  `json:"deptId"`
}

// Role 是角色，数据权限挂在角色上。
type Role struct {
	BaseEntity
	TenantID  int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name      string `gorm:"size:64;not null" json:"name"`
	Code      string `gorm:"size:64;not null" json:"code"`
	Sort      int    `gorm:"not null;default:0" json:"sort"`
	DataScope int8   `gorm:"not null;default:1" json:"dataScope"`
	Status    int8   `gorm:"not null;default:0" json:"status"`
	Type      int8   `gorm:"not null;default:2" json:"type"` // 1 内置 2 自定义
	Remark    string `gorm:"size:500" json:"remark"`

	MenuIDs []int64 `gorm:"-" json:"menuIds,omitempty"`
	DeptIDs []int64 `gorm:"-" json:"deptIds,omitempty"` // 数据范围 2（自定义部门）的部门集合
}

func (Role) TableName() string { return "system_role" }

// Menu 是菜单（目录/菜单/按钮），菜单全局共享，角色按租户绑定。
type Menu struct {
	BaseEntity
	Name          string  `gorm:"size:64;not null" json:"name"`
	Permission    string  `gorm:"size:128" json:"permission"`
	Type          int8    `gorm:"not null;default:2" json:"type"`
	Sort          int     `gorm:"not null;default:0" json:"sort"`
	ParentID      int64   `gorm:"index;not null;default:0" json:"parentId"`
	Path          string  `gorm:"size:200" json:"path"`
	Component     string  `gorm:"size:200" json:"component"`
	ComponentName string  `gorm:"size:100" json:"componentName"`
	Icon          string  `gorm:"size:100" json:"icon"`
	Visible       bool    `gorm:"not null;default:true" json:"visible"`
	Status        int8    `gorm:"not null;default:0" json:"status"`
	KeepAlive     bool    `gorm:"not null;default:true" json:"keepAlive"`
	Children      []*Menu `gorm:"-" json:"children,omitempty"`
}

func (Menu) TableName() string { return "system_menu" }

// Dept 是部门树。
type Dept struct {
	BaseEntity
	TenantID     int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name         string `gorm:"size:64;not null" json:"name"`
	ParentID     int64  `gorm:"index;not null;default:0" json:"parentId"`
	Sort         int    `gorm:"not null;default:0" json:"sort"`
	LeaderUserID int64  `gorm:"not null;default:0" json:"leaderUserId"`
	Phone        string `gorm:"size:32" json:"phone"`
	Email        string `gorm:"size:128" json:"email"`
	Status       int8   `gorm:"not null;default:0" json:"status"`

	Children []*Dept `gorm:"-" json:"children,omitempty"`
}

func (Dept) TableName() string { return "system_dept" }

// Post 是岗位。
type Post struct {
	BaseEntity
	TenantID int64  `gorm:"index;not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	Code     string `gorm:"size:64;not null" json:"code"`
	Sort     int    `gorm:"not null;default:0" json:"sort"`
	Status   int8   `gorm:"not null;default:0" json:"status"`
	Remark   string `gorm:"size:500" json:"remark"`
}

func (Post) TableName() string { return "system_post" }

// UserRole 是用户-角色关联。关联表用复合主键 + tenant_id，不设软删除。
type UserRole struct {
	UserID   int64 `gorm:"primaryKey" json:"userId"`
	RoleID   int64 `gorm:"primaryKey" json:"roleId"`
	TenantID int64 `gorm:"index;not null;default:1" json:"tenantId"`
}

func (UserRole) TableName() string { return "system_user_role" }

// UserPost 是用户-岗位关联。关联表用复合主键 + tenant_id，不设软删除。
type UserPost struct {
	UserID   int64 `gorm:"primaryKey" json:"userId"`
	PostID   int64 `gorm:"primaryKey" json:"postId"`
	TenantID int64 `gorm:"index;not null;default:1" json:"tenantId"`
}

func (UserPost) TableName() string { return "system_user_post" }

// RoleMenu 是角色-菜单关联。关联表用复合主键 + tenant_id，不设软删除。
type RoleMenu struct {
	RoleID   int64 `gorm:"primaryKey" json:"roleId"`
	MenuID   int64 `gorm:"primaryKey" json:"menuId"`
	TenantID int64 `gorm:"index;not null;default:1" json:"tenantId"`
}

func (RoleMenu) TableName() string { return "system_role_menu" }

// RoleDept 是角色-部门关联（自定义数据范围），见 ADR 0004。关联表用复合主键 + tenant_id，不设软删除。
type RoleDept struct {
	RoleID   int64 `gorm:"primaryKey" json:"roleId"`
	DeptID   int64 `gorm:"primaryKey" json:"deptId"`
	TenantID int64 `gorm:"index;not null;default:1" json:"tenantId"`
}

func (RoleDept) TableName() string { return "system_role_dept" }
