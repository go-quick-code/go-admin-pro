// Package repository 是 message 模块的 DAL 层。
//
// 渠道/模板是平台级表（未登记 TenantTables）：本层所有查询显式 tenant.Skip，
// 表示「这是系统级读取」，不依赖租户插件放行。发送日志是租户级表：
// 查询/写入/删除都走租户插件自动追加 tenant_id 条件，跨租户 ID 一律不可见。
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/message/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Repository 是 message 模块的数据访问对象。
type Repository struct {
	db *gorm.DB
}

// New 创建 message Repository。
func New(db *gorm.DB) *Repository { return &Repository{db: db} }

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// platform 返回平台级表（渠道/模板）的查询句柄：显式 tenant.Skip，
// 避免后续把表登记进 TenantTables 时被插件误加租户条件。
func (r *Repository) platform(ctx context.Context) *gorm.DB {
	return tenant.Skip(r.db.WithContext(ctx))
}

// wrapDuplicateEntry 把 MySQL 1062 重复键错误规整为 gorm.ErrDuplicatedKey 链，
// service 层据此翻译成业务 400（软删行占用编码时由唯一键兜底）。
func wrapDuplicateEntry(err error) error {
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return errors.Join(gorm.ErrDuplicatedKey, err)
	}
	return err
}

// IsDuplicateEntry 报告错误是否为唯一键冲突（wrapDuplicateEntry 规整后的形态）。
func IsDuplicateEntry(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
}

// ---------- 渠道 ----------

// ChannelFilter 是渠道列表查询条件。
type ChannelFilter struct {
	ChannelType string
	Code        string
	Name        string
	Status      *int8
}

// FindChannelByID 按 ID 查渠道，不存在返回 nil。
func (r *Repository) FindChannelByID(ctx context.Context, id int64) (*model.Channel, error) {
	var row model.Channel
	if err := r.platform(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindChannelByCode 按编码查渠道，不存在返回 nil。
func (r *Repository) FindChannelByCode(ctx context.Context, code string) (*model.Channel, error) {
	var row model.Channel
	if err := r.platform(ctx).Where("code = ?", code).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListChannels 按筛选分页查询渠道。
func (r *Repository) ListChannels(ctx context.Context, f ChannelFilter, offset, limit int) ([]model.Channel, int64, error) {
	q := r.channelQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Channel
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabledChannelsByType 返回指定类型全部启用的渠道（按 ID 升序）：
// 发送请求未指定 channelCode 时取第一条作为默认渠道。
func (r *Repository) ListEnabledChannelsByType(ctx context.Context, channelType string) ([]model.Channel, error) {
	var list []model.Channel
	if err := r.platform(ctx).
		Where("channel_type = ? AND status = ?", channelType, model.StatusEnable).
		Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *Repository) channelQuery(ctx context.Context, f ChannelFilter) *gorm.DB {
	q := r.platform(ctx).Model(&model.Channel{})
	if f.ChannelType != "" {
		q = q.Where("channel_type = ?", f.ChannelType)
	}
	if f.Code != "" {
		q = q.Where("code LIKE ?", "%"+f.Code+"%")
	}
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// CreateChannel 创建渠道；撞 uk_message_channel_code 时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) CreateChannel(ctx context.Context, row *model.Channel) error {
	return wrapDuplicateEntry(r.platform(ctx).Create(row).Error)
}

// UpdateChannelFields 只更新请求列（不覆盖并发状态变更）。
func (r *Repository) UpdateChannelFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.platform(ctx).Model(&model.Channel{}).
		Where("id = ?", id).Updates(fields).Error)
}

// UpdateChannelStatus 只更新渠道状态列。
func (r *Repository) UpdateChannelStatus(ctx context.Context, id int64, status int8) error {
	return r.platform(ctx).Model(&model.Channel{}).
		Where("id = ?", id).Update("status", status).Error
}

// DeleteChannel 软删除渠道。
func (r *Repository) DeleteChannel(ctx context.Context, id int64) error {
	return r.platform(ctx).Delete(&model.Channel{}, id).Error
}

// ---------- 模板 ----------

// TemplateFilter 是模板列表查询条件。
type TemplateFilter struct {
	ChannelType string
	Code        string
	Name        string
	Status      *int8
}

// FindTemplateByID 按 ID 查模板，不存在返回 nil。
func (r *Repository) FindTemplateByID(ctx context.Context, id int64) (*model.Template, error) {
	var row model.Template
	if err := r.platform(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// FindTemplateByCode 按 (channelType, code) 查模板，不存在返回 nil。
func (r *Repository) FindTemplateByCode(ctx context.Context, channelType, code string) (*model.Template, error) {
	var row model.Template
	if err := r.platform(ctx).
		Where("channel_type = ? AND code = ?", channelType, code).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListTemplates 按筛选分页查询模板。
func (r *Repository) ListTemplates(ctx context.Context, f TemplateFilter, offset, limit int) ([]model.Template, int64, error) {
	q := r.templateQuery(ctx, f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Template
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) templateQuery(ctx context.Context, f TemplateFilter) *gorm.DB {
	q := r.platform(ctx).Model(&model.Template{})
	if f.ChannelType != "" {
		q = q.Where("channel_type = ?", f.ChannelType)
	}
	if f.Code != "" {
		q = q.Where("code LIKE ?", "%"+f.Code+"%")
	}
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	return q
}

// CreateTemplate 创建模板；撞 uk_message_template_type_code 时返回 IsDuplicateEntry 可识别的错误。
func (r *Repository) CreateTemplate(ctx context.Context, row *model.Template) error {
	return wrapDuplicateEntry(r.platform(ctx).Create(row).Error)
}

// UpdateTemplateFields 只更新请求列。
func (r *Repository) UpdateTemplateFields(ctx context.Context, id int64, fields map[string]any) error {
	return wrapDuplicateEntry(r.platform(ctx).Model(&model.Template{}).
		Where("id = ?", id).Updates(fields).Error)
}

// DeleteTemplate 软删除模板。
func (r *Repository) DeleteTemplate(ctx context.Context, id int64) error {
	return r.platform(ctx).Delete(&model.Template{}, id).Error
}

// ---------- 发送日志 ----------

// LogFilter 是发送日志列表查询条件。
type LogFilter struct {
	ChannelType  string
	TemplateCode string
	Recipient    string
	Status       *int8
}

// FindLogByID 按 ID 查日志（租户插件自动限定当前租户），不存在/跨租户返回 nil。
func (r *Repository) FindLogByID(ctx context.Context, id int64) (*model.Log, error) {
	var row model.Log
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &row, nil
}

// ListLogs 按筛选分页查询当前租户的发送日志。
func (r *Repository) ListLogs(ctx context.Context, f LogFilter, offset, limit int) ([]model.Log, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Log{})
	if f.ChannelType != "" {
		q = q.Where("channel_type = ?", f.ChannelType)
	}
	if f.TemplateCode != "" {
		q = q.Where("template_code LIKE ?", "%"+f.TemplateCode+"%")
	}
	if f.Recipient != "" {
		q = q.Where("recipient LIKE ?", "%"+f.Recipient+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Log
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateLog 写入发送日志：tenant_id 由租户插件回填（service 也显式赋值）。
func (r *Repository) CreateLog(ctx context.Context, row *model.Log) error {
	return r.db.WithContext(ctx).Create(row).Error
}

// CountLogsInMonth 统计租户在 [start, end) 内指定渠道的日志行数
// （月度配额用量口径，spec §4.2.4：成功与失败都算，不做软删过滤——
// message_log 是 append-only 表，本就无 deleted_at）。
// 显式带 tenant_id 条件并 tenant.Skip：配额用量查询可能来自平台侧
// （app 装配的全量配额提供者可查任意租户），不能依赖租户插件的上下文注入；
// 发送校验路径传入的也是当前租户 ID，口径一致。
// 时间边界按应用时区计算后传入（service.monthWindow），go-sql-driver 以
// DSN loc（= app.timezone）序列化两端，比较语义为绝对时刻。
func (r *Repository) CountLogsInMonth(ctx context.Context, tenantID int64, channelType string, start, end time.Time) (int64, error) {
	var total int64
	err := tenant.Skip(r.db.WithContext(ctx)).Model(&model.Log{}).
		Where("tenant_id = ? AND channel_type = ? AND created_at >= ? AND created_at < ?",
			tenantID, channelType, start, end).
		Count(&total).Error
	return total, err
}

// UpdateLogAttempt 记录一次重发结果：更新原行，不新增行（spec §4.3.7）。
func (r *Repository) UpdateLogAttempt(ctx context.Context, id int64, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.Log{}).
		Where("id = ?", id).Updates(fields).Error
}

// DeleteLog 按 ID 硬删除当前租户的日志（append-only 表不设 deleted_at）。
func (r *Repository) DeleteLog(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Log{}, id).Error
}

// ClearLogs 清空当前租户的全部发送日志。显式带 tenant_id 条件：
// GORM 拒绝无条件的全表删除，租户插件也会再追加一次同样的条件。
func (r *Repository) ClearLogs(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ?", tenant.ID(ctx)).
		Delete(&model.Log{}).Error
}
