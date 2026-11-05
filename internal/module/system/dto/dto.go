package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// ---------- 认证 ----------

type LoginReq struct {
	Username    string `json:"username" binding:"required"`
	Password    string `json:"password" binding:"required"`
	TenantID    int64  `json:"tenantId"`
	CaptchaKey  string `json:"captchaKey"`
	CaptchaCode string `json:"captchaCode"`
}

type LoginResp struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int64  `json:"expiresIn"` // 秒
	UserID      int64  `json:"userId"`
	Username    string `json:"username"`
	TenantID    int64  `json:"tenantId"`
}

type CaptchaResp struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key,omitempty"`
	Image   string `json:"image,omitempty"`
}

type UserInfoResp struct {
	User        *model.User   `json:"user"`
	Roles       []string      `json:"roles"`
	Permissions []string      `json:"permissions"`
	Menus       []*model.Menu `json:"menus"`
}

type ProfileUpdateReq struct {
	Nickname string `json:"nickname" binding:"required"`
	Mobile   string `json:"mobile"`
	Email    string `json:"email"`
	Gender   int8   `json:"gender"`
	Avatar   string `json:"avatar"`
}

type ChangePasswordReq struct {
	OldPassword string `json:"oldPassword" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required,min=6,max=32"`
}

// ---------- 用户 ----------

type UserCreateReq struct {
	Username string  `json:"username" binding:"required,min=2,max=64"`
	Password string  `json:"password" binding:"required,min=6,max=32"`
	Nickname string  `json:"nickname" binding:"required"`
	DeptID   int64   `json:"deptId"`
	PostIDs  []int64 `json:"postIds"`
	RoleIDs  []int64 `json:"roleIds"`
	Mobile   string  `json:"mobile"`
	Email    string  `json:"email"`
	Gender   int8    `json:"gender"`
	Avatar   string  `json:"avatar"`
	Status   int8    `json:"status"`
	Remark   string  `json:"remark"`
}

type UserUpdateReq struct {
	ID       int64   `json:"id" binding:"required"`
	Password string  `json:"password"`
	Nickname string  `json:"nickname" binding:"required"`
	DeptID   int64   `json:"deptId"`
	PostIDs  []int64 `json:"postIds"`
	RoleIDs  []int64 `json:"roleIds"`
	Mobile   string  `json:"mobile"`
	Email    string  `json:"email"`
	Gender   int8    `json:"gender"`
	Avatar   string  `json:"avatar"`
	Status   int8    `json:"status"`
	Remark   string  `json:"remark"`
}

type UserPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Username string `form:"username"`
	Nickname string `form:"nickname"`
	Mobile   string `form:"mobile"`
	Status   *int8  `form:"status"`
	DeptID   int64  `form:"deptId"`
}

type UpdateStatusReq struct {
	ID     int64 `json:"id" binding:"required"`
	Status int8  `json:"status"`
}

type ResetPasswordReq struct {
	ID       int64  `json:"id" binding:"required"`
	Password string `json:"password" binding:"required,min=6,max=32"`
}

// ---------- 角色 ----------

type RoleCreateReq struct {
	Name      string  `json:"name" binding:"required"`
	Code      string  `json:"code" binding:"required"`
	Sort      int     `json:"sort"`
	DataScope int8    `json:"dataScope"`
	Status    int8    `json:"status"`
	Remark    string  `json:"remark"`
	MenuIDs   []int64 `json:"menuIds"`
	DeptIDs   []int64 `json:"deptIds"` // 数据范围 2（自定义部门）时生效
}

type RoleUpdateReq struct {
	ID        int64   `json:"id" binding:"required"`
	Name      string  `json:"name" binding:"required"`
	Code      string  `json:"code" binding:"required"`
	Sort      int     `json:"sort"`
	DataScope int8    `json:"dataScope"`
	Status    int8    `json:"status"`
	Remark    string  `json:"remark"`
	MenuIDs   []int64 `json:"menuIds"`
	DeptIDs   []int64 `json:"deptIds"` // 数据范围 2（自定义部门）时生效
}

type RolePageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Code     string `form:"code"`
	Status   *int8  `form:"status"`
}

// ---------- 菜单 ----------

type MenuCreateReq struct {
	Name          string `json:"name" binding:"required"`
	Permission    string `json:"permission"`
	Type          int8   `json:"type" binding:"required"`
	Sort          int    `json:"sort"`
	ParentID      int64  `json:"parentId"`
	Path          string `json:"path"`
	Component     string `json:"component"`
	ComponentName string `json:"componentName"`
	Icon          string `json:"icon"`
	Visible       bool   `json:"visible"`
	Status        int8   `json:"status"`
	KeepAlive     bool   `json:"keepAlive"`
}

type MenuUpdateReq struct {
	ID            int64  `json:"id" binding:"required"`
	Name          string `json:"name" binding:"required"`
	Permission    string `json:"permission"`
	Type          int8   `json:"type" binding:"required"`
	Sort          int    `json:"sort"`
	ParentID      int64  `json:"parentId"`
	Path          string `json:"path"`
	Component     string `json:"component"`
	ComponentName string `json:"componentName"`
	Icon          string `json:"icon"`
	Visible       bool   `json:"visible"`
	Status        int8   `json:"status"`
	KeepAlive     bool   `json:"keepAlive"`
}

// ---------- 部门 ----------

type DeptCreateReq struct {
	Name         string `json:"name" binding:"required"`
	ParentID     int64  `json:"parentId"`
	Sort         int    `json:"sort"`
	LeaderUserID int64  `json:"leaderUserId"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	Status       int8   `json:"status"`
}

type DeptUpdateReq struct {
	ID           int64  `json:"id" binding:"required"`
	Name         string `json:"name" binding:"required"`
	ParentID     int64  `json:"parentId"`
	Sort         int    `json:"sort"`
	LeaderUserID int64  `json:"leaderUserId"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	Status       int8   `json:"status"`
}

// ---------- 岗位 ----------

type PostCreateReq struct {
	Name   string `json:"name" binding:"required"`
	Code   string `json:"code" binding:"required"`
	Sort   int    `json:"sort"`
	Status int8   `json:"status"`
	Remark string `json:"remark"`
}

type PostUpdateReq struct {
	ID     int64  `json:"id" binding:"required"`
	Name   string `json:"name" binding:"required"`
	Code   string `json:"code" binding:"required"`
	Sort   int    `json:"sort"`
	Status int8   `json:"status"`
	Remark string `json:"remark"`
}

type PostPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Code     string `form:"code"`
	Status   *int8  `form:"status"`
}

// ---------- 字典 ----------

type DictTypeCreateReq struct {
	Name   string `json:"name" binding:"required"`
	Type   string `json:"type" binding:"required"`
	Status int8   `json:"status"`
	Remark string `json:"remark"`
}

type DictTypeUpdateReq struct {
	ID     int64  `json:"id" binding:"required"`
	Name   string `json:"name" binding:"required"`
	Type   string `json:"type" binding:"required"`
	Status int8   `json:"status"`
	Remark string `json:"remark"`
}

type DictTypePageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Type     string `form:"type"`
	Status   *int8  `form:"status"`
}

type DictDataCreateReq struct {
	DictType  string `json:"dictType" binding:"required"`
	Label     string `json:"label" binding:"required"`
	Value     string `json:"value" binding:"required"`
	Sort      int    `json:"sort"`
	Status    int8   `json:"status"`
	ColorType string `json:"colorType"`
	CSSClass  string `json:"cssClass"`
	Remark    string `json:"remark"`
}

type DictDataUpdateReq struct {
	ID        int64  `json:"id" binding:"required"`
	DictType  string `json:"dictType" binding:"required"`
	Label     string `json:"label" binding:"required"`
	Value     string `json:"value" binding:"required"`
	Sort      int    `json:"sort"`
	Status    int8   `json:"status"`
	ColorType string `json:"colorType"`
	CSSClass  string `json:"cssClass"`
	Remark    string `json:"remark"`
}

// ---------- 租户 ----------

type TenantCreateReq struct {
	Name          string `json:"name" binding:"required"`
	ContactName   string `json:"contactName"`
	ContactMobile string `json:"contactMobile"`
	Status        int8   `json:"status"`
	Domain        string `json:"domain"`
	ExpireTime    string `json:"expireTime"`
	AccountCount  int    `json:"accountCount"`
	Remark        string `json:"remark"`
}

type TenantUpdateReq struct {
	ID            int64  `json:"id" binding:"required"`
	Name          string `json:"name" binding:"required"`
	ContactName   string `json:"contactName"`
	ContactMobile string `json:"contactMobile"`
	Status        int8   `json:"status"`
	Domain        string `json:"domain"`
	ExpireTime    string `json:"expireTime"`
	AccountCount  int    `json:"accountCount"`
	Remark        string `json:"remark"`
}

type TenantPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// ---------- 日志 ----------

type LoginLogPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Username string `form:"username"`
	LoginIP  string `form:"loginIp"`
	Result   *int8  `form:"result"`
}

type OperateLogPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Username string `form:"username"`
	Module   string `form:"module"`
	Status   *int8  `form:"status"`
}
