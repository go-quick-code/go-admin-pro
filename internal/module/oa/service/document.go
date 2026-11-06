// document.go 承载文档域的编排（issue #58，docs/oa-mvp-spec.md
// §3.8/§4.12/§5）：目录树、目录创建/改名/删除、ACL 设置与可见性过滤。
//
// 判定语义全部在纯函数（document_rule.go）：最近祖先 ACL 解析、四类主体
// 匹配、owner/管理员旁路、非空目录删除约束。本文件只做参数校验、取数与落库。
//
// 可见性有两条入口但只有一个语义：
//   - 列表/详情：仓储单条递归 CTE 谓词（repository/document.go）；
//   - 管理权/403 与 404 区分：同一份目录与 ACL 快照喂给纯函数解析。
//
// 文件本体与上传/下载由 #59 承接；本工单只保证目录级 ACL 与删除约束
// （有文件的目录同样禁止删除）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/oa/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// CreateDocumentFolder 创建目录（POST /oa/document/folder）：
//   - 顶层目录（parentId=0）必须至少设置一条 ACL（spec §4.12），否则 400；
//   - 子目录需要在父目录上有管理权（owner 或 oa:document:manage），acl 留空
//     表示继承父目录；
//   - owner 固定为当前登录用户。
func (s *Service) CreateDocumentFolder(ctx context.Context, req *dto.DocumentFolderCreateReq) (*dto.DocumentFolderItem, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	name, err := ValidateDocumentFolderName(req.Name)
	if err != nil {
		return nil, err
	}
	targets, err := documentACLTargetsOfReq(req.ACL)
	if err != nil {
		return nil, err
	}
	if err := ValidateDocumentFolderACL(targets, req.ParentID == 0); err != nil {
		return nil, err
	}

	row := &model.DocumentFolder{
		ParentID: req.ParentID,
		Name:     name,
		OwnerID:  viewer.UserID,
		Sort:     req.Sort,
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		// 父目录管理权在事务内校验（行锁）：父目录并发删除时不会落出孤儿目录。
		if req.ParentID > 0 {
			if _, err := s.documentFolderForManageLocked(ctx, tx, req.ParentID, viewer); err != nil {
				return err
			}
		}
		if err := tx.CreateDocumentFolder(ctx, row); err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}
		return tx.ReplaceDocumentFolderACLs(ctx, row.ID, documentACLRowsOf(row.ID, targets))
	})
	if err != nil {
		return nil, err
	}
	item := s.documentFolderItemOf(*row, len(targets) > 0)
	return &item, nil
}

// UpdateDocumentFolder 改名/调整排序（PUT /oa/document/folder）：
// 需要目录管理权（owner 或 oa:document:manage）；parentId 不可变（v1 不做
// 跨目录移动）。不可见且不可管理的目录返回 404，不泄露存在性。
func (s *Service) UpdateDocumentFolder(ctx context.Context, req *dto.DocumentFolderUpdateReq) (*dto.DocumentFolderItem, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	if req.ID <= 0 {
		return nil, xerr.BadRequest("目录 ID 不能为空")
	}
	name, err := ValidateDocumentFolderName(req.Name)
	if err != nil {
		return nil, err
	}
	var updated *model.DocumentFolder
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.documentFolderForManageLocked(ctx, tx, req.ID, viewer)
		if err != nil {
			return err
		}
		row.Name = name
		row.Sort = req.Sort
		if err := tx.SaveDocumentFolder(ctx, row); err != nil {
			return err
		}
		updated = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	aclConfigured, err := s.documentFolderHasExplicitACL(ctx, updated.ID)
	if err != nil {
		return nil, err
	}
	item := s.documentFolderItemOf(*updated, aclConfigured)
	return &item, nil
}

// DeleteDocumentFolder 删除目录（DELETE /oa/document/folder/:id）：
// 需要目录管理权；有子目录或文件的目录返回 409（spec §4.12/§4.14）。
// 软删目录并在同事务清理 ACL 关联行。
func (s *Service) DeleteDocumentFolder(ctx context.Context, id int64) error {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return err
	}
	if id <= 0 {
		return xerr.BadRequest("目录 ID 不能为空")
	}
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.documentFolderForManageLocked(ctx, tx, id, viewer)
		if err != nil {
			return err
		}
		childCount, err := tx.CountDocumentFolderChildren(ctx, row.ID)
		if err != nil {
			return err
		}
		fileCount, err := tx.CountDocumentFiles(ctx, row.ID)
		if err != nil {
			return err
		}
		if err := ValidateDocumentFolderDelete(childCount, fileCount); err != nil {
			return err
		}
		if _, err := tx.DeleteDocumentFolder(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteDocumentFolderACLs(ctx, row.ID)
	})
}

// ListDocumentFolderTree 目录树（GET /oa/document/folder/tree）：
// 只返回查看者有读权限的目录；父目录不可见时子目录提升为根节点返回
// （否则整棵可见子树会从导航里消失）。同级按 sort ASC、id ASC。
func (s *Service) ListDocumentFolderTree(ctx context.Context) ([]*dto.DocumentFolderNode, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListVisibleDocumentFolders(ctx, documentVisibilityFilterOf(viewer))
	if err != nil {
		return nil, err
	}
	nodes := make(map[int64]*dto.DocumentFolderNode, len(rows))
	for _, row := range rows {
		item := s.documentFolderItemOf(row.DocumentFolder, row.ACLConfigured)
		nodes[row.ID] = &dto.DocumentFolderNode{
			DocumentFolderItem: item,
			Children:           make([]*dto.DocumentFolderNode, 0),
		}
	}
	roots := make([]*dto.DocumentFolderNode, 0, len(rows))
	for _, row := range rows {
		node := nodes[row.ID]
		parent, ok := nodes[row.ParentID]
		if !ok {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	return roots, nil
}

// GetDocumentFolder 目录详情（GET /oa/document/folder/:id）：与树同一可见性
// 谓词；范围外/跨租户/无 ACL 顶层的非 owner 一律 404。
func (s *Service) GetDocumentFolder(ctx context.Context, id int64) (*dto.DocumentFolderItem, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.repo.FindVisibleDocumentFolder(ctx, id, documentVisibilityFilterOf(viewer))
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("目录不存在")
	}
	item := s.documentFolderItemOf(row.DocumentFolder, row.ACLConfigured)
	return &item, nil
}

// GetDocumentFolderACL 读取目录 ACL（GET /oa/document/folder/:id/acl）：
// 需要目录管理权；可见但无管理权返回 403，不可见返回 404。响应同时给出
// 目录自身显式条目（编辑回显）与最近祖先的有效条目（继承说明）。
func (s *Service) GetDocumentFolderACL(ctx context.Context, id int64) (*dto.DocumentFolderACLResp, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.documentFolderForManage(ctx, s.repo, id, viewer)
	if err != nil {
		return nil, err
	}
	folders, aclByFolder, err := s.loadDocumentACLContext(ctx)
	if err != nil {
		return nil, err
	}
	effective, ok := ResolveEffectiveDocumentACL(row.ID, folders, aclByFolder)
	if !ok {
		return nil, xerr.NotFound("目录不存在")
	}
	ownEntries := aclByFolder[row.ID]
	ownItems, err := s.documentACLItems(ctx, ownEntries)
	if err != nil {
		return nil, err
	}
	effectiveItems, err := s.documentACLItems(ctx, effective.Entries)
	if err != nil {
		return nil, err
	}
	sourceName := ""
	if effective.SourceID > 0 {
		if source, ok := folders[effective.SourceID]; ok {
			sourceName = source.Name
		}
	}
	return &dto.DocumentFolderACLResp{
		FolderID:         row.ID,
		OwnerID:          row.OwnerID,
		Explicit:         effective.Explicit,
		SourceFolderID:   effective.SourceID,
		SourceFolderName: sourceName,
		Entries:          ownItems,
		EffectiveEntries: effectiveItems,
	}, nil
}

// SetDocumentFolderACL 整体替换目录 ACL（PUT /oa/document/folder/:id/acl）：
// 需要目录管理权；顶层目录不允许清空（400，spec §4.12）；子目录清空表示
// 改为继承父目录。替换与目录写入同事务。
func (s *Service) SetDocumentFolderACL(ctx context.Context, id int64, req *dto.DocumentFolderACLSetReq) (*dto.DocumentFolderACLResp, error) {
	viewer, err := s.documentViewer(ctx)
	if err != nil {
		return nil, err
	}
	var entries []dto.DocumentACLTargetReq
	if req != nil {
		entries = req.Entries
	}
	targets, err := documentACLTargetsOfReq(entries)
	if err != nil {
		return nil, err
	}
	err = s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		row, err := s.documentFolderForManageLocked(ctx, tx, id, viewer)
		if err != nil {
			return err
		}
		if err := ValidateDocumentFolderACL(targets, row.ParentID == 0); err != nil {
			return err
		}
		return tx.ReplaceDocumentFolderACLs(ctx, row.ID, documentACLRowsOf(row.ID, targets))
	})
	if err != nil {
		return nil, err
	}
	return s.GetDocumentFolderACL(ctx, id)
}

// ---------- 内部：查看者与 ACL 取数 ----------

// documentViewer 组装目录 ACL 判定上下文：
//   - oa:document:manage 决定管理员旁路（全租户可见 + 全目录可管理）；
//   - 部门祖先链用于「部门子树」ACL（被选部门出现在链上即命中）；
//   - 角色集合用于角色 ACL。
func (s *Service) documentViewer(ctx context.Context) (DocumentViewer, error) {
	userID := CurrentUserID(ctx)
	if userID <= 0 {
		return DocumentViewer{}, xerr.Unauthorized("")
	}
	manage, err := s.sys.UserHasPermission(ctx, userID, DocumentPermissionManage)
	if err != nil {
		return DocumentViewer{}, err
	}
	deptID, ok, err := s.sys.UserDeptID(ctx, userID)
	if err != nil {
		return DocumentViewer{}, err
	}
	deptIDs := make([]int64, 0, 8)
	for depth := 0; ok && deptID > 0 && depth < documentFolderDepthLimit; depth++ {
		deptIDs = append(deptIDs, deptID)
		parent, has, err := s.sys.DeptParentID(ctx, deptID)
		if err != nil {
			return DocumentViewer{}, err
		}
		deptID, ok = parent, has
	}
	roleIDs, err := s.sys.UserRoleIDs(ctx, userID)
	if err != nil {
		return DocumentViewer{}, err
	}
	return DocumentViewerOf(userID, deptIDs, roleIDs, manage), nil
}

// documentVisibilityFilterOf 把纯函数查看者上下文转成仓储谓词入参。
func documentVisibilityFilterOf(viewer DocumentViewer) repository.DocumentVisibilityFilter {
	return repository.DocumentVisibilityFilter{
		UserID:  viewer.UserID,
		DeptIDs: viewer.DeptIDs,
		RoleIDs: viewer.RoleIDs,
		Manage:  viewer.Manage,
	}
}

// documentFolderForManage 读取目录并校验管理权（只读，不锁）：
//   - 不存在/跨租户 → 404；
//   - 有管理权 → 返回目录；
//   - 只读可见但无管理权 → 403；
//   - 不可见且无管理权 → 404（不泄露存在性）。
func (s *Service) documentFolderForManage(ctx context.Context, repo *repository.Repository, id int64, viewer DocumentViewer) (*model.DocumentFolder, error) {
	if id <= 0 {
		return nil, xerr.NotFound("目录不存在")
	}
	row, err := repo.FindDocumentFolder(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.checkDocumentFolderManage(ctx, repo, row, viewer)
}

// documentFolderForManageLocked 与 documentFolderForManage 同语义，但用行锁
// 读取（改名/删除/ACL 替换的写入口，串行化并发管理动作）。
func (s *Service) documentFolderForManageLocked(ctx context.Context, tx *repository.Repository, id int64, viewer DocumentViewer) (*model.DocumentFolder, error) {
	if id <= 0 {
		return nil, xerr.NotFound("目录不存在")
	}
	row, err := tx.FindDocumentFolderForUpdate(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.checkDocumentFolderManage(ctx, tx, row, viewer)
}

// checkDocumentFolderManage 是管理权判定的公共部分（404/403 区分）。
func (s *Service) checkDocumentFolderManage(ctx context.Context, repo *repository.Repository, row *model.DocumentFolder, viewer DocumentViewer) (*model.DocumentFolder, error) {
	if row == nil {
		return nil, xerr.NotFound("目录不存在")
	}
	if DocumentFolderCanManage(*row, viewer) {
		return row, nil
	}
	visible, err := repo.FindVisibleDocumentFolder(ctx, row.ID, documentVisibilityFilterOf(viewer))
	if err != nil {
		return nil, err
	}
	if visible == nil {
		return nil, xerr.NotFound("目录不存在")
	}
	return nil, xerr.Forbidden("需要目录管理权（目录 owner 或 oa:document:manage）")
}

// loadDocumentACLContext 取本租户全部目录与 ACL（目录树是租户级资源，
// 规模小；一次取全用于有效 ACL 解析）。
func (s *Service) loadDocumentACLContext(ctx context.Context) (map[int64]model.DocumentFolder, map[int64][]DocumentACLTarget, error) {
	folders, err := s.repo.ListDocumentFolders(ctx)
	if err != nil {
		return nil, nil, err
	}
	acls, err := s.repo.ListDocumentFolderACLs(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	folderMap := make(map[int64]model.DocumentFolder, len(folders))
	for _, folder := range folders {
		folderMap[folder.ID] = folder
	}
	aclMap := make(map[int64][]DocumentACLTarget, len(acls))
	for _, row := range acls {
		aclMap[row.FolderID] = append(aclMap[row.FolderID], DocumentACLTarget{
			TargetType: row.TargetType,
			TargetID:   row.TargetID,
		})
	}
	return folderMap, aclMap, nil
}

// documentFolderHasExplicitACL 报告目录自身是否有显式 ACL（创建/改名回显）。
func (s *Service) documentFolderHasExplicitACL(ctx context.Context, folderID int64) (bool, error) {
	rows, err := s.repo.ListDocumentFolderACLs(ctx, []int64{folderID})
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// documentACLItems 把 ACL 目标补上展示名称（全员/部门/角色/用户）。
func (s *Service) documentACLItems(ctx context.Context, targets []DocumentACLTarget) ([]dto.DocumentACLItem, error) {
	items := make([]dto.DocumentACLItem, 0, len(targets))
	deptIDs := make([]int64, 0, len(targets))
	roleIDs := make([]int64, 0, len(targets))
	userIDs := make([]int64, 0, len(targets))
	for _, target := range targets {
		switch target.TargetType {
		case model.DocumentACLTargetDept:
			deptIDs = append(deptIDs, target.TargetID)
		case model.DocumentACLTargetRole:
			roleIDs = append(roleIDs, target.TargetID)
		case model.DocumentACLTargetUser:
			userIDs = append(userIDs, target.TargetID)
		}
	}
	var deptNames, roleNames, userNames map[int64]string
	var err error
	if len(deptIDs) > 0 {
		if deptNames, err = s.sys.DeptNames(ctx, deptIDs); err != nil {
			return nil, err
		}
	}
	if len(roleIDs) > 0 {
		if roleNames, err = s.sys.RoleNames(ctx, roleIDs); err != nil {
			return nil, err
		}
	}
	if len(userIDs) > 0 {
		if userNames, err = s.sys.UserNicknames(ctx, userIDs); err != nil {
			return nil, err
		}
	}
	for _, target := range targets {
		name := ""
		switch target.TargetType {
		case model.DocumentACLTargetAll:
			name = "全员"
		case model.DocumentACLTargetDept:
			name = deptNames[target.TargetID]
		case model.DocumentACLTargetRole:
			name = roleNames[target.TargetID]
		case model.DocumentACLTargetUser:
			name = userNames[target.TargetID]
		}
		items = append(items, dto.DocumentACLItem{
			TargetType: target.TargetType,
			TargetID:   target.TargetID,
			TargetName: name,
		})
	}
	return items, nil
}

// ---------- 内部：DTO 映射 ----------

// documentACLTargetsOfReq 校验并归一化请求里的 ACL 条目。
func documentACLTargetsOfReq(entries []dto.DocumentACLTargetReq) ([]DocumentACLTarget, error) {
	targets := make([]DocumentACLTarget, 0, len(entries))
	for _, entry := range entries {
		targets = append(targets, DocumentACLTarget{TargetType: entry.TargetType, TargetID: entry.TargetID})
	}
	return NormalizeDocumentACLTargets(targets)
}

// documentACLRowsOf 把归一化后的 ACL 目标转成落库行（tenant_id 由插件回填）。
func documentACLRowsOf(folderID int64, targets []DocumentACLTarget) []model.DocumentFolderACL {
	rows := make([]model.DocumentFolderACL, 0, len(targets))
	for _, target := range targets {
		rows = append(rows, model.DocumentFolderACL{
			FolderID:   folderID,
			TargetType: target.TargetType,
			TargetID:   target.TargetID,
		})
	}
	return rows
}

// documentFolderItemOf 组装目录行视图（时间按业务时区墙钟格式化）。
func (s *Service) documentFolderItemOf(row model.DocumentFolder, aclConfigured bool) dto.DocumentFolderItem {
	loc := s.businessLocation()
	item := dto.DocumentFolderItem{
		ID:            row.ID,
		ParentID:      row.ParentID,
		Name:          row.Name,
		OwnerID:       row.OwnerID,
		Sort:          row.Sort,
		ACLConfigured: aclConfigured,
	}
	if !row.CreatedAt.IsZero() {
		item.CreateTime = row.CreatedAt.In(loc).Format(timeLayout)
	}
	if !row.UpdatedAt.IsZero() {
		item.UpdateTime = row.UpdatedAt.In(loc).Format(timeLayout)
	}
	return item
}
