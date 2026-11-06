// document_file.go 承载文档文件归属行的数据访问（issue #59，
// docs/oa-mvp-spec.md §3.8/§4.12）：
//
//   - oa_document_file 只存归属与展示字段，文件本体在 infra_file（file_id）；
//   - 所有读取路径都按 tenant_id 限定；列表/删除另按 folder_id 校验归属，
//     跨租户/跨目录一律未命中（service 统一映射成 404，不泄露存在性）；
//   - 删除是软删除（deleted_at），软删行不再出现在列表/下载/目录删除计数里。
//
// 文件表已登记租户插件，本文件的普通 GORM 查询自动追加 tenant_id；
// 计数/列表都走模型 Scope，软删行由 GORM 的 deleted_at 默认条件排除。
package repository

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
)

// CreateDocumentFile 落一条文件归属行（tenant_id 由租户插件回填）。
func (r *Repository) CreateDocumentFile(ctx context.Context, row *model.DocumentFile) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建文档文件: %w", err)
	}
	return nil
}

// PageDocumentFiles 分页列出目录内未软删的文件（新上传的在前）。
func (r *Repository) PageDocumentFiles(ctx context.Context, folderID int64, offset, limit int) ([]model.DocumentFile, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.DocumentFile{}).Where("`folder_id` = ?", folderID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计文档文件: %w", err)
	}
	var rows []model.DocumentFile
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询文档文件: %w", err)
	}
	return rows, total, nil
}

// FindDocumentFileByID 按 ID 读取文件归属行（租户插件自动限定租户）：
// 不存在/跨租户/已软删返回 nil，调用方统一 404。下载/删除走这个入口后
// 必须再校验文件所属目录的读权限/管理权（spec §4.12：不允许靠文件 ID
// 绕过 ACL）。
func (r *Repository) FindDocumentFileByID(ctx context.Context, id int64) (*model.DocumentFile, error) {
	var row model.DocumentFile
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindDocumentFile 按 (folder_id, id) 读取文件归属行：不存在、不属于该目录、
// 跨租户或已软删都返回 nil（调用方统一 404）。写路径（删除）在事务内用
// folder_id 条件二次校验归属。
func (r *Repository) FindDocumentFile(ctx context.Context, folderID, id int64) (*model.DocumentFile, error) {
	var row model.DocumentFile
	if err := r.db.WithContext(ctx).
		Where("`id` = ? AND `folder_id` = ?", id, folderID).
		First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// SoftDeleteDocumentFile 软删目录内的文件归属行（仅置 deleted_at，不物理
// 删除 infra_file 本体）；返回是否删到行（并发重复删除时 false）。
func (r *Repository) SoftDeleteDocumentFile(ctx context.Context, folderID, id int64) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("`folder_id` = ?", folderID).
		Delete(&model.DocumentFile{}, id)
	if result.Error != nil {
		return false, fmt.Errorf("删除文档文件: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}
