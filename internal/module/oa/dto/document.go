// document.go 是文档目录与 ACL 的请求/响应 DTO（issue #58，
// docs/oa-mvp-spec.md §3.8/§4.12/§5）：
//
//   - 目录树：嵌套节点（可见目录才出现；父目录不可见时子目录提升为根节点）；
//   - 创建：parentId=0 为顶层目录，必须带显式 ACL；子目录可留空表示继承；
//   - 改名：仅 name/sort（parentId 不可变，不做跨目录移动）；
//   - ACL 设置：entries 整体替换；顶层目录不允许清空（400）；
//   - ACL 视图：own 为目录自身显式条目（编辑回显），effective 为最近祖先的
//     有效条目（含来源目录），sourceFolderId=0 表示链上无 ACL；
//   - 文件（issue #59）：归属行 oa_document_file + 鉴权下载地址，不暴露
//     静态公开直链。
package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// DocumentACLTargetReq 是一条 ACL 主体（targetType 0 全员 / 1 部门 /
// 2 角色 / 3 用户；全员固定 targetId=0）。
type DocumentACLTargetReq struct {
	TargetType int8  `json:"targetType"`
	TargetID   int64 `json:"targetId"`
}

// DocumentFolderCreateReq 是创建目录请求（POST /oa/document/folder）：
//   - parentId=0 创建顶层目录（必须至少一条 ACL）；
//   - parentId>0 需要在父目录上有管理权；acl 为空表示继承父目录。
type DocumentFolderCreateReq struct {
	ParentID int64                  `json:"parentId"`
	Name     string                 `json:"name" binding:"required"`
	Sort     int                    `json:"sort"`
	ACL      []DocumentACLTargetReq `json:"acl"`
}

// DocumentFolderUpdateReq 是目录改名/排序请求（PUT /oa/document/folder）：
// parentId 不在请求里——v1 不支持跨目录移动（spec §4.12）。
type DocumentFolderUpdateReq struct {
	ID   int64  `json:"id" binding:"required"`
	Name string `json:"name" binding:"required"`
	Sort int    `json:"sort"`
}

// DocumentFolderACLSetReq 是 ACL 整体替换请求
// （PUT /oa/document/folder/:id/acl）。
type DocumentFolderACLSetReq struct {
	Entries []DocumentACLTargetReq `json:"entries"`
}

// DocumentFolderItem 是目录行视图（详情/创建/改名返回共用）：
//   - OwnerID 目录 owner（前端用它判断是否显示管理按钮）；
//   - ACLConfigured 目录自身是否配置了显式 ACL（否 = 继承祖先）。
type DocumentFolderItem struct {
	ID            int64  `json:"id"`
	ParentID      int64  `json:"parentId"`
	Name          string `json:"name"`
	OwnerID       int64  `json:"ownerId"`
	Sort          int    `json:"sort"`
	ACLConfigured bool   `json:"aclConfigured"`
	CreateTime    string `json:"createTime"`
	UpdateTime    string `json:"updateTime"`
}

// DocumentFolderNode 是目录树节点（GET /oa/document/folder/tree）：
// 只包含查看者有读权限的目录；父目录不可见时该节点直接作为根节点返回，
// 避免整棵子树从导航里消失。
type DocumentFolderNode struct {
	DocumentFolderItem
	Children []*DocumentFolderNode `json:"children"`
}

// DocumentACLItem 是 ACL 条目视图（targetName 由 system 网关注入）。
type DocumentACLItem struct {
	TargetType int8   `json:"targetType"`
	TargetID   int64  `json:"targetId"`
	TargetName string `json:"targetName"`
}

// DocumentFolderACLResp 是目录 ACL 视图（GET /oa/document/folder/:id/acl）：
//   - Explicit / SourceFolderID 描述有效 ACL 的来源（自身 vs 最近祖先）；
//   - Entries 目录自身显式条目（编辑回显；继承时为空）；
//   - EffectiveEntries 实际生效的条目（来源目录的 ACL）。
type DocumentFolderACLResp struct {
	FolderID         int64             `json:"folderId"`
	OwnerID          int64             `json:"ownerId"`
	Explicit         bool              `json:"explicit"`
	SourceFolderID   int64             `json:"sourceFolderId"`
	SourceFolderName string            `json:"sourceFolderName"`
	Entries          []DocumentACLItem `json:"entries"`
	EffectiveEntries []DocumentACLItem `json:"effectiveEntries"`
}

// DocumentFilePageReq 是目录内文件分页请求（GET /oa/document/file/page）：
// folderId 必填；目录读权限由服务层校验（不可读 404）。
type DocumentFilePageReq struct {
	web.PageQuery
	FolderID int64 `form:"folderId" binding:"required"`
}

// DocumentFileItem 是目录内文件的展示视图（issue #59，
// GET /oa/document/file/page、POST /oa/document/file/upload）：
//   - ID 是 oa_document_file.id（下载/删除路径上的 fileId）；
//   - FileID 是 infra_file.id（文件本体，仅展示/排障，前端不把它当下载入口）；
//   - URL 是鉴权下载地址（GET /oa/document/file/:id/download），不暴露
//     静态公开直链；
//   - UploaderName 由 system 网关注入（上传人昵称，缺失时为空）。
type DocumentFileItem struct {
	ID           int64  `json:"id"`
	FolderID     int64  `json:"folderId"`
	FileID       int64  `json:"fileId"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Type         string `json:"type"`
	UploaderID   int64  `json:"uploaderId"`
	UploaderName string `json:"uploaderName"`
	CreateTime   string `json:"createTime"`
	URL          string `json:"url"`
}
