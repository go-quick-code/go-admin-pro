package repository

import (
	"context"
	"errors"

	"github.com/lautung/go-admin-pro/internal/module/infra/model"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Repository { return &Repository{db: db} }

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

func (r *Repository) DeleteFile(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.File{}, id).Error
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
