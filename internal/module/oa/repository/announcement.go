// announcement.go 承载公告域的数据访问（issue #48，spec §3.4/§4.7）：
// 公告 CRUD/分页、受众目标整体替换、员工可见列表的单一受众谓词。
// 三表均已登记 TenantTables，租户条件由插件追加；受众 EXISTS 子查询显式
// 带主表 tenant_id（子查询是手写 SQL，不经过插件，不能依赖自动补条件）。
package repository

import (
	"context"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/module/oa/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AnnouncementFilter 是公告管理分页的筛选条件（nil 表示不过滤）。
type AnnouncementFilter struct {
	Status       *int8
	AudienceType *int8
	Pinned       *bool
	Keyword      string
}

// AudienceViewer 是员工列表的查看者输入（存储无关）：
// DeptIDs 为查看者所在部门及祖先部门链（含自身），RoleIDs 为查看者角色。
type AudienceViewer struct {
	UserID  int64
	DeptIDs []int64
	RoleIDs []int64
}

// CreateAnnouncement 落一条公告（tenant_id 由插件回填；草稿/时间戳由 service 设置）。
func (r *Repository) CreateAnnouncement(ctx context.Context, row *model.Announcement) error {
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("创建公告: %w", err)
	}
	return nil
}

// SaveAnnouncement 更新公告的可变字段（title/content/audience_type/pinned/
// status/published_at/withdrawn_at）。状态流转走事务内的锁读 + 本方法。
func (r *Repository) SaveAnnouncement(ctx context.Context, row *model.Announcement) error {
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("更新公告: %w", err)
	}
	return nil
}

// FindAnnouncement 按 ID 读公告（跨租户/已删除返回 nil）。
func (r *Repository) FindAnnouncement(ctx context.Context, id int64) (*model.Announcement, error) {
	var row model.Announcement
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindAnnouncementForUpdate 锁读公告（发布/撤回/删除的状态判定与写入之间
// 不允许并发穿透；必须在 Transaction 返回的仓储上调用）。
func (r *Repository) FindAnnouncementForUpdate(ctx context.Context, id int64) (*model.Announcement, error) {
	var row model.Announcement
	if err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// DeleteAnnouncement 软删公告（service 已校验仅草稿）；返回是否删到行。
func (r *Repository) DeleteAnnouncement(ctx context.Context, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Delete(&model.Announcement{}, id)
	if result.Error != nil {
		return false, fmt.Errorf("删除公告: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// PageAnnouncements 管理端分页（全部状态）：置顶在前、id 倒序稳定分页。
func (r *Repository) PageAnnouncements(ctx context.Context, f AnnouncementFilter, offset, limit int) ([]model.Announcement, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Announcement{})
	if f.Status != nil {
		q = q.Where("`status` = ?", *f.Status)
	}
	if f.AudienceType != nil {
		q = q.Where("`audience_type` = ?", *f.AudienceType)
	}
	if f.Pinned != nil {
		q = q.Where("`pinned` = ?", *f.Pinned)
	}
	if f.Keyword != "" {
		q = q.Where("`title` LIKE ?", "%"+f.Keyword+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计公告: %w", err)
	}
	var rows []model.Announcement
	if err := q.Order("`pinned` DESC, `id` DESC").
		Offset(offset).Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询公告: %w", err)
	}
	return rows, total, nil
}

// PageVisibleAnnouncements 员工「我的公告」分页：只返回已发布且命中受众的
// 公告；置顶在前，其余按发布时间倒序。受众判定是单一 EXISTS 谓词（spec §4.7：
// audience_type=0 全员，否则所选部门子树 ∪ 所选角色的并集，命中任一即可见）。
// 部门子树语义在谓词里表现为「被选部门是查看者部门的祖先」——查看者传入
// 部门祖先链（含自身）即可。
func (r *Repository) PageVisibleAnnouncements(ctx context.Context, v AudienceViewer, keyword string, offset, limit int) ([]model.Announcement, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Announcement{}).
		Where("`status` = ?", model.AnnouncementStatusPublished)
	q = applyAudience(q, v)
	if keyword != "" {
		q = q.Where("`title` LIKE ?", "%"+keyword+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计我的公告: %w", err)
	}
	var rows []model.Announcement
	if err := q.Order("`pinned` DESC, `published_at` DESC, `id` DESC").
		Offset(offset).Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询我的公告: %w", err)
	}
	return rows, total, nil
}

// applyAudience 是公告可见性的单一仓储谓词（与 service.Audience.Permits 同语义）：
// audience_type=0 直接命中；否则 EXISTS 受众行，部门目标匹配查看者祖先部门
// （被选部门在祖先链上 ⇒ 查看者在被选部门子树内），角色目标匹配查看者角色。
// 子查询手写 SQL，必须显式带 t.tenant_id = 主表 tenant_id，防止跨租户命中。
func applyAudience(q *gorm.DB, v AudienceViewer) *gorm.DB {
	return q.Where(
		"(`audience_type` = ? OR EXISTS ("+
			"SELECT 1 FROM `oa_announcement_target` t "+
			"WHERE t.`tenant_id` = `oa_announcement`.`tenant_id` "+
			"AND t.`announcement_id` = `oa_announcement`.`id` "+
			"AND ((t.`target_type` = ? AND t.`target_id` IN ?) "+
			"OR (t.`target_type` = ? AND t.`target_id` IN ?))))",
		model.AudienceTypeAll,
		model.AnnouncementTargetDept, v.DeptIDs,
		model.AnnouncementTargetRole, v.RoleIDs,
	)
}

// ListAnnouncementTargets 读公告的自定义受众（管理详情/编辑回显用）。
func (r *Repository) ListAnnouncementTargets(ctx context.Context, announcementID int64) ([]model.AnnouncementTarget, error) {
	var rows []model.AnnouncementTarget
	if err := r.db.WithContext(ctx).
		Where("`announcement_id` = ?", announcementID).
		Order("`target_type` ASC, `target_id` ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询公告受众: %w", err)
	}
	return rows, nil
}

// ReplaceAnnouncementTargets 整体替换公告受众（先删后插；必须在事务内调用，
// 与公告主表更新同生共死）。tenant_id 由插件回填。
func (r *Repository) ReplaceAnnouncementTargets(ctx context.Context, announcementID int64, rows []model.AnnouncementTarget) error {
	if err := r.db.WithContext(ctx).
		Where("`announcement_id` = ?", announcementID).
		Delete(&model.AnnouncementTarget{}).Error; err != nil {
		return fmt.Errorf("清理公告受众: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Create(&rows).Error; err != nil {
		return fmt.Errorf("写入公告受众: %w", err)
	}
	return nil
}
