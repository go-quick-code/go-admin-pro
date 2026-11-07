package repository

import (
	"context"
	"errors"

	"github.com/go-quick-code/go-admin-pro/internal/module/infra/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// DB 暴露底层 gorm 连接，供数据库文档等直接读取 information_schema 的
// 能力使用（issue #83）：information_schema 不是租户表，不走仓储方法。
func (r *Repository) DB() *gorm.DB { return r.db }

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// ---------- 文件 ----------

type FileFilter struct {
	Name string
	Type string
	// GenericOnly 只返回 biz_type='' 的通用文件（默认分页口径）。
	GenericOnly bool
	// BizType/BizID 显式按业务归属过滤（服务层已做业务授权）。
	BizType string
	BizID   int64
}

func (r *Repository) CreateFile(ctx context.Context, f *model.File) error {
	return r.db.WithContext(ctx).Create(f).Error
}

func (r *Repository) ListFiles(ctx context.Context, f FileFilter, offset, limit int) ([]model.File, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.File{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Type != "" {
		q = q.Where("type LIKE ?", "%"+f.Type+"%")
	}
	if f.GenericOnly {
		q = q.Where("biz_type = ''")
	}
	if f.BizType != "" {
		q = q.Where("biz_type = ?", f.BizType)
		if f.BizID > 0 {
			q = q.Where("biz_id = ?", f.BizID)
		}
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.File
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) FindFileByID(ctx context.Context, id int64) (*model.File, error) {
	var f model.File
	if err := r.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

// ListFilesByBiz 按业务归属（biz_type + biz_id）列出文件，新上传的在前。
// 租户条件由租户插件追加；调用方须已校验归属键合法。
func (r *Repository) ListFilesByBiz(ctx context.Context, bizType string, bizID int64) ([]model.File, error) {
	var list []model.File
	if err := r.db.WithContext(ctx).
		Where("biz_type = ? AND biz_id = ?", bizType, bizID).
		Order("id DESC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// FindFileByBiz 按 ID + 业务归属查文件：不属于该业务（或跨租户）时返回 nil。
// 这是「文件归属校验」的唯一入口（删除前必须经过它），调用方据此返回 404。
func (r *Repository) FindFileByBiz(ctx context.Context, id int64, bizType string, bizID int64) (*model.File, error) {
	var f model.File
	if err := r.db.WithContext(ctx).
		Where("id = ? AND biz_type = ? AND biz_id = ?", id, bizType, bizID).
		First(&f).Error; err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

func (r *Repository) DeleteFile(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.File{}, id).Error
}

// SumFileSize 统计指定租户未删除文件的 size 之和（存储配额用量口径，
// issue #71，spec §4.2.3）。跨租户查询：显式 tenant_id 条件 + tenant.Skip，
// 与 system 的 CountUsersByTenantID 同构（平台查看任意租户配额）。
func (r *Repository) SumFileSize(ctx context.Context, tenantID int64) (int64, error) {
	var total int64
	err := tenant.Skip(r.db.WithContext(ctx)).Model(&model.File{}).
		Where("tenant_id = ?", tenantID).
		Select("COALESCE(SUM(size), 0)").
		Scan(&total).Error
	return total, err
}

// ---------- 参数配置 ----------

type ConfigFilter struct {
	Name     string
	Key      string
	Category string
}

func (r *Repository) ListConfigs(ctx context.Context, f ConfigFilter, offset, limit int) ([]model.Config, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Config{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Key != "" {
		q = q.Where("config_key LIKE ?", "%"+f.Key+"%")
	}
	if f.Category != "" {
		q = q.Where("category = ?", f.Category)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Config
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) FindConfigByID(ctx context.Context, id int64) (*model.Config, error) {
	var c model.Config
	if err := r.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (r *Repository) FindConfigByKey(ctx context.Context, key string) (*model.Config, error) {
	var c model.Config
	if err := r.db.WithContext(ctx).Where("config_key = ?", key).First(&c).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (r *Repository) CreateConfig(ctx context.Context, c *model.Config) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *Repository) SaveConfig(ctx context.Context, c *model.Config) error {
	return r.db.WithContext(ctx).Save(c).Error
}

func (r *Repository) DeleteConfig(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Config{}, id).Error
}

// ---------- 定时任务 ----------

type JobFilter struct {
	Name        string
	HandlerName string
	Status      *int8
}

func (r *Repository) ListJobs(ctx context.Context, f JobFilter, offset, limit int) ([]model.Job, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Job{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.HandlerName != "" {
		q = q.Where("handler_name LIKE ?", "%"+f.HandlerName+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Job
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabledJobs 跨租户加载所有启用任务，供调度器启动时使用。
// 调度器启动没有租户上下文，这里显式 tenant.Skip（系统级路径）。
func (r *Repository) ListEnabledJobs(ctx context.Context) ([]model.Job, error) {
	var list []model.Job
	err := tenant.Skip(r.db.WithContext(ctx)).Where("status = ?", int8(0)).Find(&list).Error
	return list, err
}

func (r *Repository) FindJobByID(ctx context.Context, id int64) (*model.Job, error) {
	var j model.Job
	if err := r.db.WithContext(ctx).First(&j, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &j, nil
}

func (r *Repository) CreateJob(ctx context.Context, j *model.Job) error {
	return r.db.WithContext(ctx).Create(j).Error
}

func (r *Repository) SaveJob(ctx context.Context, j *model.Job) error {
	return r.db.WithContext(ctx).Save(j).Error
}

func (r *Repository) UpdateJobColumns(ctx context.Context, id int64, values map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.Job{}).Where("id = ?", id).Updates(values).Error
}

func (r *Repository) DeleteJob(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Job{}, id).Error
}

func (r *Repository) CreateJobLog(ctx context.Context, l *model.JobLog) error {
	return r.db.WithContext(ctx).Create(l).Error
}

type JobLogFilter struct {
	JobID       int64
	HandlerName string
	Result      *int8
}

func (r *Repository) ListJobLogs(ctx context.Context, f JobLogFilter, offset, limit int) ([]model.JobLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.JobLog{})
	if f.JobID > 0 {
		q = q.Where("job_id = ?", f.JobID)
	}
	if f.HandlerName != "" {
		q = q.Where("handler_name LIKE ?", "%"+f.HandlerName+"%")
	}
	if f.Result != nil {
		q = q.Where("result = ?", *f.Result)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.JobLog
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// SoftDeleteFilesByBiz 软删除业务归属下的全部文件记录（仅置 deleted_at，
// 不删除磁盘文件；公告草稿删除时保留附件记录，issue #48）。
func (r *Repository) SoftDeleteFilesByBiz(ctx context.Context, bizType string, bizID int64) error {
	return r.db.WithContext(ctx).
		Where("biz_type = ? AND biz_id = ?", bizType, bizID).
		Delete(&model.File{}).Error
}

// SoftDeleteFileByBiz 软删除单条业务归属文件（仅置 deleted_at，不删磁盘；
// 文档文件软删除时同步关闭 infra 侧读取入口，issue #59）。归属不符时
// 影响 0 行，不误删其他业务的文件。
func (r *Repository) SoftDeleteFileByBiz(ctx context.Context, id int64, bizType string, bizID int64) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND biz_type = ? AND biz_id = ?", id, bizType, bizID).
		Delete(&model.File{}).Error
}
