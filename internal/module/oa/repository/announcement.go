// announcement.go 承载公告域的数据访问（issue #48/#49，spec §3.4/§4.7）：
// 公告 CRUD/分页、受众目标整体替换、员工可见列表的单一受众谓词、已读
// 幂等写入与已读统计（issue #49）。三表均已登记 TenantTables，租户条件
// 由插件追加；受众/已读 EXISTS 子查询显式带主表 tenant_id（子查询是手写
// SQL，不经过插件，不能依赖自动补条件）。
package repository

import (
	"context"
	"fmt"
	"time"

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

// AnnouncementReadRow 是公告列表行：公告本体 + 当前查看者已读状态
// （is_read 是查询派生列，不落库；issue #49 列表未读标记）。
type AnnouncementReadRow struct {
	model.Announcement
	Read bool `gorm:"column:is_read"`
}

// announcementReadStateSelect 是列表查询的已读状态派生列：EXISTS 命中
// oa_announcement_read 即已读。子查询手写 SQL，显式带 tenant_id = 主表
// tenant_id，防止跨租户命中；参数是当前查看者 user_id。
const announcementReadStateSelect = "`oa_announcement`.*, EXISTS (" +
	"SELECT 1 FROM `oa_announcement_read` r " +
	"WHERE r.`tenant_id` = `oa_announcement`.`tenant_id` " +
	"AND r.`announcement_id` = `oa_announcement`.`id` " +
	"AND r.`user_id` = ?) AS `is_read`"

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

// PageAnnouncements 管理端分页（全部状态）：置顶在前、id 倒序稳定分页；
// viewerID 用于派生当前管理员的已读状态（与详情/员工列表同口径）。
func (r *Repository) PageAnnouncements(ctx context.Context, f AnnouncementFilter, viewerID int64, offset, limit int) ([]AnnouncementReadRow, int64, error) {
	q := r.db.WithContext(ctx).Table("oa_announcement")
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
	var rows []AnnouncementReadRow
	if err := q.Select(announcementReadStateSelect, viewerID).
		Order("`pinned` DESC, `id` DESC").
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
// 部门祖先链（含自身）即可。列表行同时派生当前查看者的已读状态（issue #49）。
func (r *Repository) PageVisibleAnnouncements(ctx context.Context, v AudienceViewer, keyword string, offset, limit int) ([]AnnouncementReadRow, int64, error) {
	q := r.db.WithContext(ctx).Table("oa_announcement").
		Where("`status` = ?", model.AnnouncementStatusPublished)
	q = applyAudience(q, v)
	if keyword != "" {
		q = q.Where("`title` LIKE ?", "%"+keyword+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计我的公告: %w", err)
	}
	var rows []AnnouncementReadRow
	if err := q.Select(announcementReadStateSelect, v.UserID).
		Order("`pinned` DESC, `published_at` DESC, `id` DESC").
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

// ---------- 已读记录与统计（issue #49，spec §4.7）----------

// MarkAnnouncementRead 幂等写入已读记录：唯一键
// (tenant_id, announcement_id, user_id) 命中时不做任何更新（保留首次
// read_at；MySQL 侧落为 ON DUPLICATE KEY UPDATE id=id）。tenant_id 由租户
// 插件在 Create 回调回填。
func (r *Repository) MarkAnnouncementRead(ctx context.Context, announcementID, userID int64, readAt time.Time) error {
	if announcementID <= 0 || userID <= 0 {
		return fmt.Errorf("公告已读记录参数非法: announcement_id=%d user_id=%d", announcementID, userID)
	}
	row := model.AnnouncementRead{
		AnnouncementID: announcementID,
		UserID:         userID,
		ReadAt:         readAt,
	}
	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error; err != nil {
		return fmt.Errorf("写入公告已读: %w", err)
	}
	return nil
}

// HasAnnouncementRead 报告用户在公告上是否已有已读记录（详情/列表状态
// 一致性用）。tenant_id 由插件追加。
func (r *Repository) HasAnnouncementRead(ctx context.Context, announcementID, userID int64) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.AnnouncementRead{}).
		Where("`announcement_id` = ? AND `user_id` = ?", announcementID, userID).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("查询公告已读状态: %w", err)
	}
	return count > 0, nil
}

// PageAnnouncementReads 分页读取已读名单（按首次已读时间升序、id 升序稳定
// 分页）。唯一键保证同一用户至多一行，因此 total 即去重后的已读人数；
// 撤回/重新发布不改写本表（spec §4.7 历史已读保留）。
func (r *Repository) PageAnnouncementReads(ctx context.Context, announcementID int64, offset, limit int) ([]model.AnnouncementRead, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.AnnouncementRead{}).
		Where("`announcement_id` = ?", announcementID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计公告已读: %w", err)
	}
	var rows []model.AnnouncementRead
	if err := q.Order("`read_at` ASC, `id` ASC").
		Offset(offset).Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("查询公告已读: %w", err)
	}
	return rows, total, nil
}
