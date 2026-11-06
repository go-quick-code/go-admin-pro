// document.go 承载文档域的数据访问（issue #58，spec §3.8/§4.12）：
// 目录 CRUD、ACL 整体替换、非空目录删除约束的计数、以及读可见性的
// **单条递归 CTE 谓词**。
//
// 可见性谓词（documentVisibleFoldersSQL）与 service/document_rule.go 的
// ResolveEffectiveDocumentACL + DocumentFolderVisible 逐条对应：
//
//	对每个目录沿 parent_id 向上构造祖先链，取「最近一个有显式 ACL 的祖先」
//	（depth 最小者）；目录对查看者可见当且仅当
//	  owner_id = 查看者
//	  OR 查看者持有 oa:document:manage
//	  OR 最近祖先的 ACL 命中（全员 / 部门 ∈ 查看者祖先链 / 角色 / 用户）
//
// 更远的祖先 ACL 不参与（子目录覆盖父目录，而不是叠加）。链上没有任何
// 显式 ACL 时谓词恒假，与纯函数的 fail-closed 一致。
//
// 递归 CTE 是裸 SQL，不经过租户插件，因此**每张子表都显式带 tenant_id**
// （与 announcement/calendar 的裸 SQL 子查询同约定）；跨租户目录在所有
// 路径上都未命中（service 层表现为 404）。
package repository

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm/clause"
)

// DocumentVisibilityFilter 是目录读可见性的查看者上下文：
//   - UserID 查看者用户 ID（<=0 时谓词恒假）；
//   - DeptIDs 查看者所在部门及祖先链（含自身；部门 ACL 的子树语义依赖它）；
//   - RoleIDs 查看者角色；
//   - Manage 是否持有 oa:document:manage（旁路 ACL，见全部目录）。
type DocumentVisibilityFilter struct {
	UserID  int64
	DeptIDs []int64
	RoleIDs []int64
	Manage  bool
}

// DocumentFolderListRow 是可见目录列表行：目录本体 + 是否配置了显式 ACL
// （派生列，不落库；管理端「继承/显式」标记用）。
type DocumentFolderListRow struct {
	model.DocumentFolder
	ACLConfigured bool `gorm:"column:acl_configured"`
}

// documentFolderDepthLimit 与 service 侧祖先链梯度上限保持一致（防御脏数据成环）。
const documentFolderDepthLimit = 64

// documentVisibleFoldersSQL 是唯一的目录读可见性谓词（递归 CTE）。
// 参数顺序见 documentVisibleFoldersArgs；尾部可追加 `AND f.id = ?`。
const documentVisibleFoldersSQL = "WITH RECURSIVE folder_chain AS (" +
	" SELECT f.id AS folder_id, f.id AS ancestor_id, f.parent_id AS parent_id, 0 AS depth" +
	" FROM oa_document_folder f" +
	" WHERE f.tenant_id = ? AND f.deleted_at IS NULL" +
	" UNION ALL" +
	" SELECT c.folder_id, p.id, p.parent_id, c.depth + 1" +
	" FROM folder_chain c" +
	" JOIN oa_document_folder p" +
	"   ON p.id = c.parent_id AND p.tenant_id = ? AND p.deleted_at IS NULL" +
	" WHERE c.parent_id > 0 AND c.depth < ?" +
	"), effective_acl AS (" +
	" SELECT c.folder_id, c.ancestor_id," +
	"        ROW_NUMBER() OVER (PARTITION BY c.folder_id ORDER BY c.depth ASC) AS rn" +
	" FROM folder_chain c" +
	" WHERE EXISTS (" +
	"   SELECT 1 FROM oa_document_folder_acl a" +
	"   WHERE a.tenant_id = ? AND a.folder_id = c.ancestor_id" +
	" )" +
	")" +
	" SELECT f.id, f.created_at, f.updated_at, f.deleted_at, f.creator, f.updater, f.tenant_id," +
	"        f.parent_id, f.name, f.owner_id, f.sort," +
	"        EXISTS (SELECT 1 FROM oa_document_folder_acl own" +
	"                WHERE own.tenant_id = f.tenant_id AND own.folder_id = f.id) AS acl_configured" +
	" FROM oa_document_folder f" +
	" WHERE f.tenant_id = ? AND f.deleted_at IS NULL" +
	"   AND (" +
	"     f.owner_id = ? OR ? = 1" +
	"     OR EXISTS (" +
	"       SELECT 1 FROM effective_acl e" +
	"       JOIN oa_document_folder_acl a ON a.tenant_id = ? AND a.folder_id = e.ancestor_id" +
	"       WHERE e.folder_id = f.id AND e.rn = 1" +
	"         AND (" +
	"           a.target_type = 0" +
	"           OR (a.target_type = 1 AND a.target_id IN ?)" +
	"           OR (a.target_type = 2 AND a.target_id IN ?)" +
	"           OR (a.target_type = 3 AND a.target_id = ?)" +
	"         )" +
	"     )" +
	"   )"

// documentVisibleFoldersArgs 组装谓词参数（与 SQL 中的 ? 顺序一一对应）。
func documentVisibleFoldersArgs(tenantID int64, f DocumentVisibilityFilter) []any {
	return []any{
		tenantID,                 // CTE 锚点
		tenantID,                 // CTE 递归 join 父目录
		documentFolderDepthLimit, // CTE 深度上限
		tenantID,                 // effective_acl EXISTS
		tenantID,                 // 主表
		f.UserID,                 // owner 旁路
		f.Manage,                 // 管理码旁路
		tenantID,                 // ACL join
		f.DeptIDs,                // 部门 ACL（查看者祖先链）
		f.RoleIDs,                // 角色 ACL
		f.UserID,                 // 用户 ACL
	}
}

// documentTenantID 读取租户上下文（裸 SQL 必须显式带 tenant_id）。
func documentTenantID(ctx context.Context) (int64, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok || id <= 0 {
		return 0, fmt.Errorf("查询文档目录: %w", tenant.ErrNoTenantContext)
	}
	return id, nil
}

// ListVisibleDocumentFolders 按唯一可见性谓词列出目录（sort ASC, id ASC），
// 并派生 acl_configured（目录自身是否有显式 ACL）。结果已按可见性过滤：
// 不可见目录（含无 ACL 顶层的非 owner/非管理员）不出现。
func (r *Repository) ListVisibleDocumentFolders(ctx context.Context, f DocumentVisibilityFilter) ([]DocumentFolderListRow, error) {
	tenantID, err := documentTenantID(ctx)
	if err != nil {
		return nil, err
	}
	query := documentVisibleFoldersSQL + " ORDER BY f.sort ASC, f.id ASC"
	var rows []DocumentFolderListRow
	if err := r.db.WithContext(ctx).Raw(query, documentVisibleFoldersArgs(tenantID, f)...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询可见文档目录: %w", err)
	}
	return rows, nil
}

// FindVisibleDocumentFolder 按同一可见性谓词读单条目录；不可见（含跨租户、
// 无 ACL 顶层）返回 nil，调用方统一 404 不泄露存在性。
func (r *Repository) FindVisibleDocumentFolder(ctx context.Context, id int64, f DocumentVisibilityFilter) (*DocumentFolderListRow, error) {
	tenantID, err := documentTenantID(ctx)
	if err != nil {
		return nil, err
	}
	args := append(documentVisibleFoldersArgs(tenantID, f), id)
	query := documentVisibleFoldersSQL + " AND f.id = ?"
	var rows []DocumentFolderListRow
	if err := r.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询可见文档目录: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// CreateDocumentFolder 落一条目录；tenant_id 由插件回填。
func (r *Repository) CreateDocumentFolder(ctx context.Context, row *model.DocumentFolder) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建文档目录: %w", err)
	}
	return nil
}

// SaveDocumentFolder 更新目录的可变字段（name/sort；parent_id 不可变，
// spec §4.12 不做跨目录移动）。
func (r *Repository) SaveDocumentFolder(ctx context.Context, row *model.DocumentFolder) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("更新文档目录: %w", err)
	}
	return nil
}

// DeleteDocumentFolder 软删目录（service 已校验非空约束）；返回是否删到行。
func (r *Repository) DeleteDocumentFolder(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Delete(&model.DocumentFolder{}, id)
	if result.Error != nil {
		return false, fmt.Errorf("删除文档目录: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// FindDocumentFolder 按 ID 读目录（不过滤可见性）；未命中（含跨租户）返回 nil。
func (r *Repository) FindDocumentFolder(ctx context.Context, id int64) (*model.DocumentFolder, error) {
	var row model.DocumentFolder
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindDocumentFolderForUpdate 行锁读目录（改名/删除/ACL 替换的写入口）。
func (r *Repository) FindDocumentFolderForUpdate(ctx context.Context, id int64) (*model.DocumentFolder, error) {
	var row model.DocumentFolder
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListDocumentFolders 读租户内全部目录（ACL 解析的取数入口；租户插件自动
// 限定 tenant_id，软删行不返回）。
func (r *Repository) ListDocumentFolders(ctx context.Context) ([]model.DocumentFolder, error) {
	var rows []model.DocumentFolder
	if err := r.db.WithContext(ctx).Order("sort ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询文档目录: %w", err)
	}
	return rows, nil
}

// ListDocumentFolderACLs 读目录 ACL；folderIDs 为空表示全部（ACL 解析/树
// 标记用）。按 folder_id/target_type/target_id 稳定排序。
func (r *Repository) ListDocumentFolderACLs(ctx context.Context, folderIDs []int64) ([]model.DocumentFolderACL, error) {
	q := r.db.WithContext(ctx).Model(&model.DocumentFolderACL{})
	if len(folderIDs) > 0 {
		q = q.Where("`folder_id` IN ?", folderIDs)
	}
	var rows []model.DocumentFolderACL
	if err := q.Order("`folder_id` ASC, `target_type` ASC, `target_id` ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询文档目录 ACL: %w", err)
	}
	return rows, nil
}

// ReplaceDocumentFolderACLs 整体替换目录 ACL（先删后插；必须在事务内调用，
// 与目录写入同生共死）。tenant_id 由插件回填。
func (r *Repository) ReplaceDocumentFolderACLs(ctx context.Context, folderID int64, rows []model.DocumentFolderACL) error {
	if err := r.db.WithContext(ctx).
		Where("`folder_id` = ?", folderID).
		Delete(&model.DocumentFolderACL{}).Error; err != nil {
		return fmt.Errorf("清理文档目录 ACL: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入文档目录 ACL: %w", err)
	}
	return nil
}

// DeleteDocumentFolderACLs 清理目录 ACL（目录删除时同事务调用）。
func (r *Repository) DeleteDocumentFolderACLs(ctx context.Context, folderID int64) error {
	if err := r.db.WithContext(ctx).
		Where("`folder_id` = ?", folderID).
		Delete(&model.DocumentFolderACL{}).Error; err != nil {
		return fmt.Errorf("清理文档目录 ACL: %w", err)
	}
	return nil
}

// CountDocumentFolderChildren 统计直接子目录数（删除约束用）。
func (r *Repository) CountDocumentFolderChildren(ctx context.Context, folderID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.DocumentFolder{}).
		Where("`parent_id` = ?", folderID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("统计子目录: %w", err)
	}
	return count, nil
}

// CountDocumentFiles 统计目录内文件数（删除约束用；file 表写入由 #59 承接）。
func (r *Repository) CountDocumentFiles(ctx context.Context, folderID int64) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.DocumentFile{}).
		Where("`folder_id` = ?", folderID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("统计目录文件: %w", err)
	}
	return count, nil
}
