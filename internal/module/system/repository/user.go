package repository

import (
	"context"
	"errors"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"gorm.io/gorm"
)

// Repository 是 system 模块的 DAL 层。
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

// UserFilter 是用户列表查询条件。
type UserFilter struct {
	Username string
	Nickname string
	Mobile   string
	Status   *int8
	DeptID   int64
	// DeptIDs 与 SelfUserID 共同表达数据范围谓词（见 internal/pkg/datascope.Filter）：
	// DeptIDs 非 nil 时按 (dept_id IN DeptIDs OR id = SelfUserID) 过滤；
	// 空切片 + SelfUserID = 0 表示谓词恒假（无可见部门）。
	DeptIDs    []int64
	SelfUserID int64 // >0 时附加本人行（数据范围「仅本人」）
}

func (r *Repository) FindUserByID(ctx context.Context, id int64) (*model.User, error) {
	var u model.User
	if err := r.db.WithContext(ctx).First(&u, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

func (r *Repository) FindUserByUsername(ctx context.Context, username string) (*model.User, error) {
	var u model.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&u).Error; err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

func (r *Repository) CountUserByUsername(ctx context.Context, username string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.User{}).Where("username = ?", username)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

func (r *Repository) CountUsersByDeptID(ctx context.Context, deptID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("dept_id = ?", deptID).Count(&total).Error
	return total, err
}

// CountUsersByTenantID 统计指定租户下的用户数（跨租户查询，跳过租户过滤）。
func (r *Repository) CountUsersByTenantID(ctx context.Context, tenantID int64) (int64, error) {
	var total int64
	err := tenant.Skip(r.db.WithContext(ctx)).Model(&model.User{}).Where("tenant_id = ?", tenantID).Count(&total).Error
	return total, err
}

func (r *Repository) ListUsers(ctx context.Context, f UserFilter, offset, limit int) ([]model.User, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.User{})
	if f.Username != "" {
		q = q.Where("username LIKE ?", "%"+f.Username+"%")
	}
	if f.Nickname != "" {
		q = q.Where("nickname LIKE ?", "%"+f.Nickname+"%")
	}
	if f.Mobile != "" {
		q = q.Where("mobile LIKE ?", "%"+f.Mobile+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.DeptID > 0 {
		q = q.Where("dept_id = ?", f.DeptID)
	}
	if f.DeptIDs != nil || f.SelfUserID > 0 {
		switch {
		case len(f.DeptIDs) > 0 && f.SelfUserID > 0:
			q = q.Where("(dept_id IN ? OR id = ?)", f.DeptIDs, f.SelfUserID)
		case len(f.DeptIDs) > 0:
			q = q.Where("dept_id IN ?", f.DeptIDs)
		case f.SelfUserID > 0:
			q = q.Where("id = ?", f.SelfUserID)
		default:
			q = q.Where("1 = 0")
		}
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.User
	if err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListUserIDsByDeptIDs 返回部门集合内（dept_id IN）的用户 ID 集合。
// oa 可见范围 helper 把部门子树翻译成用户集合用（issue #43）；含停用用户
// （停用用户的历史单据仍需对负责人可见），租户插件自动限定当前租户。
func (r *Repository) ListUserIDsByDeptIDs(ctx context.Context, deptIDs []int64) ([]int64, error) {
	if len(deptIDs) == 0 {
		return nil, nil
	}
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("dept_id IN ?", deptIDs).Pluck("id", &ids).Error
	return ids, err
}

func (r *Repository) CreateUser(ctx context.Context, u *model.User) error {
	return r.db.WithContext(ctx).Create(u).Error
}

func (r *Repository) SaveUser(ctx context.Context, u *model.User) error {
	return r.db.WithContext(ctx).Save(u).Error
}

func (r *Repository) UpdateUserColumns(ctx context.Context, id int64, values map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Updates(values).Error
}

func (r *Repository) DeleteUser(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&model.UserRole{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.UserPost{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.User{}, id).Error
	})
}

func (r *Repository) GetUserRoleIDs(ctx context.Context, userID int64) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.UserRole{}).Where("user_id = ?", userID).Pluck("role_id", &ids).Error
	return ids, err
}

func (r *Repository) GetUserPostIDs(ctx context.Context, userID int64) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.UserPost{}).Where("user_id = ?", userID).Pluck("post_id", &ids).Error
	return ids, err
}

func (r *Repository) ReplaceUserRoles(ctx context.Context, userID int64, roleIDs []int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserRole{}).Error; err != nil {
			return err
		}
		if len(roleIDs) == 0 {
			return nil
		}
		rows := make([]model.UserRole, 0, len(roleIDs))
		for _, roleID := range roleIDs {
			rows = append(rows, model.UserRole{UserID: userID, RoleID: roleID})
		}
		return tx.Create(&rows).Error
	})
}

func (r *Repository) ReplaceUserPosts(ctx context.Context, userID int64, postIDs []int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserPost{}).Error; err != nil {
			return err
		}
		if len(postIDs) == 0 {
			return nil
		}
		rows := make([]model.UserPost, 0, len(postIDs))
		for _, postID := range postIDs {
			rows = append(rows, model.UserPost{UserID: userID, PostID: postID})
		}
		return tx.Create(&rows).Error
	})
}

// GetUserRoleCodes 返回用户已启用角色的编码。
func (r *Repository) GetUserRoleCodes(ctx context.Context, userID int64) ([]string, error) {
	var codes []string
	err := r.db.WithContext(ctx).
		Table("system_role").
		Joins("JOIN system_user_role ur ON ur.role_id = system_role.id").
		Where("ur.user_id = ? AND system_role.status = ? AND system_role.deleted_at IS NULL", userID, model.StatusEnable).
		Pluck("system_role.code", &codes).Error
	return codes, err
}

// GetUserRoles 返回用户已启用的角色，用于计算数据权限。
func (r *Repository) GetUserRoles(ctx context.Context, userID int64) ([]model.Role, error) {
	var roles []model.Role
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Where("id IN (?)", r.db.WithContext(ctx).Model(&model.UserRole{}).Select("role_id").Where("user_id = ?", userID)).
		Order("sort ASC, id ASC").
		Find(&roles).Error
	return roles, err
}

func (r *Repository) GetUserDeptID(ctx context.Context, userID int64) (int64, error) {
	var deptID int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", userID).Pluck("dept_id", &deptID).Error
	return deptID, err
}
