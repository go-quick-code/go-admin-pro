// document_rule.go 承载文档域的核心判定规则（issue #58，
// docs/oa-mvp-spec.md §3.8/§4.12）。全部为**纯函数/纯方法**（无存储访问、
// 无 HTTP 上下文）：
//
//   - 读权限：目录自身有显式 ACL 时以自身为准；否则沿 parent_id 向上找
//     **最近一个有显式 ACL 的祖先**，取该祖先的 ACL 集合判定——子目录 ACL
//     覆盖父目录而不是叠加（链上更远的祖先 ACL 不再参与）；
//   - 链上没有任何显式 ACL（典型的「无 ACL 顶层」历史数据）：只有目录 owner
//     与持有 oa:document:manage 的用户可见；
//   - ACL 主体四类：0 全员（target_id=0）、1 部门（含子树，查看者祖先链
//     命中即在其子树内）、2 角色、3 用户；
//   - 管理权：目录 owner 或 oa:document:manage（上传/改名/删除/设置 ACL）。
//
// 仓储侧用**单条递归 CTE 谓词**复刻同一语义（repository/document.go：
// ListVisibleDocumentFolders / FindVisibleDocumentFolder），本文件的
// ResolveEffectiveDocumentACL + DocumentFolderVisible 是该谓词的内存镜像，
// 表驱动测试锁定二者语义一致。
package service

import (
	"fmt"
	"sort"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 文档权限码（菜单种子 526 段：526 查询、5261 创建、5262 管理；员工侧
// 菜单 537 复用查询码；文件上传码 5263 留给 #59）。
const (
	// DocumentPermissionQuery 目录树/详情/可见目录读取。
	DocumentPermissionQuery = "oa:document:query"
	// DocumentPermissionCreate 创建目录（顶层目录另需在请求内给出 ACL）。
	DocumentPermissionCreate = "oa:document:create"
	// DocumentPermissionManage 全租户可见与目录管理旁路（owner 之外的管理员码）。
	DocumentPermissionManage = "oa:document:manage"
	// DocumentPermissionUpload 文件上传按钮码（菜单 5263，issue #59 沿用）。
	// 后端上传/删除只校验目录管理权（owner 或 Manage），不强制该码：owner
	// 可能没有管理员授予的按钮码（与目录改名/删除一致，spec §4.12）。
	DocumentPermissionUpload = "oa:document:upload"
)

// 目录/文件字段边界。
const (
	// MaxDocumentFolderNameLen 是目录名长度上限（oa_document_folder.name 列宽）。
	MaxDocumentFolderNameLen = 100
	// MaxDocumentFileNameLen 是文件名长度上限（oa_document_file.name 列宽，
	// issue #59）。
	MaxDocumentFileNameLen = 200
	// MaxDocumentACLEntries 是单个目录显式 ACL 的条目上限（防止误配过大）。
	MaxDocumentACLEntries = 200
	// documentFolderDepthLimit 是祖先链解析的梯度上限（防御脏数据成环）。
	documentFolderDepthLimit = 64
)

// DocumentACLTarget 是一条 ACL 主体的存储无关描述：
//   - TargetType 取 model.DocumentACLTarget*；
//   - TargetID 为部门/角色/用户 ID；全员固定 0。
type DocumentACLTarget struct {
	TargetType int8
	TargetID   int64
}

// DocumentViewer 是可见性判定所需的查看者上下文（存储无关）：
//   - UserID 查看者用户 ID（<=0 视为未登录，什么都看不到）；
//   - DeptIDs 查看者所在部门及其**祖先链**（含自身；部门 ACL 的子树语义
//     依赖它：被选部门出现在祖先链上 ⇒ 查看者在被选部门子树内）；
//   - RoleIDs 查看者持有的角色 ID；
//   - Manage 是否持有 oa:document:manage（全租户读取与目录管理旁路）。
type DocumentViewer struct {
	UserID  int64
	DeptIDs []int64
	RoleIDs []int64
	Manage  bool
}

// DocumentViewerOf 归一化查看者上下文（纯函数）：userID<=0 时 fail-closed
// 返回空视图（无部门/角色且 Manage 强制 false）；部门与角色去重升序，
// 便于断言与 SQL IN 的稳定入参。
func DocumentViewerOf(userID int64, deptIDs, roleIDs []int64, manage bool) DocumentViewer {
	if userID <= 0 {
		return DocumentViewer{DeptIDs: []int64{}, RoleIDs: []int64{}}
	}
	return DocumentViewer{
		UserID:  userID,
		DeptIDs: uniqueSortedIDs(deptIDs),
		RoleIDs: uniqueSortedIDs(roleIDs),
		Manage:  manage,
	}
}

// EffectiveDocumentACL 是目录的有效 ACL 解析结果：
//   - FolderID 被解析的目录；
//   - OwnerID 目录 owner（owner 恒可见、可管理）；
//   - SourceID 最近一个有显式 ACL 的祖先目录（含自身）；0 表示链上无 ACL；
//   - Explicit SourceID == FolderID（目录自身有显式 ACL，覆盖祖先）；
//   - Entries 来源目录的显式 ACL 集合（SourceID=0 时为空）。
type EffectiveDocumentACL struct {
	FolderID int64
	OwnerID  int64
	SourceID int64
	Explicit bool
	Entries  []DocumentACLTarget
}

// PermitsACL 判断查看者是否命中一组显式 ACL（纯函数）：
//   - 查看者无登录主体（UserID<=0）→ false；
//   - 全员条目直接命中；
//   - 部门条目：被选部门 ∈ 查看者部门祖先链（⇒ 查看者在部门子树内）；
//   - 角色/用户条目按集合/相等匹配；
//   - 未知主体类型与空集合不命中（fail-closed）。
func (v DocumentViewer) PermitsACL(entries []DocumentACLTarget) bool {
	if v.UserID <= 0 {
		return false
	}
	for _, entry := range entries {
		switch entry.TargetType {
		case model.DocumentACLTargetAll:
			return true
		case model.DocumentACLTargetDept:
			if containsID(v.DeptIDs, entry.TargetID) {
				return true
			}
		case model.DocumentACLTargetRole:
			if containsID(v.RoleIDs, entry.TargetID) {
				return true
			}
		case model.DocumentACLTargetUser:
			if entry.TargetID == v.UserID {
				return true
			}
		}
	}
	return false
}

// ResolveEffectiveDocumentACL 解析目录的有效 ACL（纯函数）：
// 从目录自身沿 parent_id 向上，返回第一个有显式 ACL 的节点；父目录的 ACL
// 不与该节点叠加。链上没有 ACL 时 SourceID=0（只有 owner/管理员可见）。
// 目录不存在返回 ok=false；脏数据成环时在梯度上限处停止并返回 SourceID=0。
func ResolveEffectiveDocumentACL(
	folderID int64,
	folders map[int64]model.DocumentFolder,
	aclByFolder map[int64][]DocumentACLTarget,
) (EffectiveDocumentACL, bool) {
	folder, ok := folders[folderID]
	if !ok {
		return EffectiveDocumentACL{}, false
	}
	effective := EffectiveDocumentACL{FolderID: folderID, OwnerID: folder.OwnerID}
	current := folder
	for depth := 0; depth < documentFolderDepthLimit; depth++ {
		if entries := aclByFolder[current.ID]; len(entries) > 0 {
			effective.SourceID = current.ID
			effective.Explicit = current.ID == folderID
			effective.Entries = entries
			return effective, true
		}
		if current.ParentID <= 0 {
			break
		}
		parent, ok := folders[current.ParentID]
		if !ok {
			break
		}
		current = parent
	}
	return effective, true
}

// DocumentFolderVisible 判断目录对查看者是否可读（纯函数）：
//   - 未登录 → false；
//   - owner 或 oa:document:manage → true（旁路 ACL，spec §4.12）；
//   - 链上无显式 ACL → false（无 ACL 顶层只对 owner/管理员可见）；
//   - 否则按最近祖先的 ACL 集合判定（不叠加更远祖先）。
func DocumentFolderVisible(viewer DocumentViewer, folder model.DocumentFolder, effective EffectiveDocumentACL) bool {
	if viewer.UserID <= 0 {
		return false
	}
	if viewer.Manage || folder.OwnerID == viewer.UserID {
		return true
	}
	if effective.SourceID == 0 {
		return false
	}
	return viewer.PermitsACL(effective.Entries)
}

// DocumentFolderCanManage 报告查看者是否有目录管理权（owner 或
// oa:document:manage；spec §4.12）。未登录恒 false（即使 Manage 置位属于
// 异常上下文）。
func DocumentFolderCanManage(folder model.DocumentFolder, viewer DocumentViewer) bool {
	if viewer.UserID <= 0 {
		return false
	}
	return viewer.Manage || folder.OwnerID == viewer.UserID
}

// ValidateDocumentFolderName 校验目录名：去首尾空白后 1–100 字。
func ValidateDocumentFolderName(name string) (string, error) {
	name = trimSpace(name)
	if name == "" {
		return "", xerr.BadRequest("目录名称不能为空")
	}
	if len([]rune(name)) > MaxDocumentFolderNameLen {
		return "", xerr.BadRequest(fmt.Sprintf("目录名称不能超过 %d 字", MaxDocumentFolderNameLen))
	}
	return name, nil
}

// NormalizeDocumentACLTargets 校验并归一化 ACL 条目（纯函数）：
//   - target_type 必须是 0–3；
//   - 全员固定 target_id=0；部门/角色/用户 target_id 必须 > 0；
//   - 去重后按 (target_type, target_id) 升序，便于断言与落库稳定；
//   - 条目数不超过 MaxDocumentACLEntries。
func NormalizeDocumentACLTargets(entries []DocumentACLTarget) ([]DocumentACLTarget, error) {
	if len(entries) > MaxDocumentACLEntries {
		return nil, xerr.BadRequest(fmt.Sprintf("ACL 条目不能超过 %d 条", MaxDocumentACLEntries))
	}
	seen := make(map[DocumentACLTarget]struct{}, len(entries))
	out := make([]DocumentACLTarget, 0, len(entries))
	for _, entry := range entries {
		if !model.ValidDocumentACLTargetType(entry.TargetType) {
			return nil, xerr.BadRequest(fmt.Sprintf("非法的 ACL 主体类型：%d", entry.TargetType))
		}
		if entry.TargetType == model.DocumentACLTargetAll {
			if entry.TargetID != 0 {
				return nil, xerr.BadRequest("全员 ACL 的 targetId 必须为 0")
			}
		} else if entry.TargetID <= 0 {
			return nil, xerr.BadRequest("部门/角色/用户 ACL 的 targetId 必须大于 0")
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TargetType != out[j].TargetType {
			return out[i].TargetType < out[j].TargetType
		}
		return out[i].TargetID < out[j].TargetID
	})
	return out, nil
}

// ValidateDocumentFileName 校验文件展示名（issue #59）：去首尾空白后
// 1–MaxDocumentFileNameLen 字。上传时文件名来自 multipart 的 base name，
// 超长直接 400，避免截断后与磁盘文件不一致。
func ValidateDocumentFileName(name string) (string, error) {
	name = trimSpace(name)
	if name == "" {
		return "", xerr.BadRequest("文件名不能为空")
	}
	if len([]rune(name)) > MaxDocumentFileNameLen {
		return "", xerr.BadRequest(fmt.Sprintf("文件名不能超过 %d 字", MaxDocumentFileNameLen))
	}
	return name, nil
}

// ValidateDocumentFolderACL 校验 ACL 是否允许落库（spec §4.12）：
// 顶层目录必须显式设置至少一条 ACL；子目录允许空集合（表示继承父目录）。
func ValidateDocumentFolderACL(entries []DocumentACLTarget, topLevel bool) error {
	if topLevel && len(entries) == 0 {
		return xerr.BadRequest("顶层目录必须设置至少一条 ACL")
	}
	return nil
}

// ValidateDocumentFolderDelete 校验目录删除约束（spec §4.12/§4.14）：
// 有子目录或文件时禁止删除，返回 409。
func ValidateDocumentFolderDelete(childCount, fileCount int64) error {
	if childCount > 0 {
		return xerr.Conflict("目录存在子目录，不能删除")
	}
	if fileCount > 0 {
		return xerr.Conflict("目录存在文件，不能删除")
	}
	return nil
}

// containsID 报告 ID 集合是否包含目标（非正 ID 不命中）。
func containsID(ids []int64, target int64) bool {
	if target <= 0 {
		return false
	}
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
