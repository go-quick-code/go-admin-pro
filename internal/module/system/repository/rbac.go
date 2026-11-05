package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"gorm.io/gorm"
)

// ---------- 角色 ----------

type RoleFilter struct {
	Name   string
	Code   string
	Status *int8
}

func (r *Repository) ListRoles(ctx context.Context, f RoleFilter, offset, limit int) ([]model.Role, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Role{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Code != "" {
		q = q.Where("code LIKE ?", "%"+f.Code+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Role
	if err := q.Order("sort ASC, id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) ListSimpleRoles(ctx context.Context) ([]model.Role, error) {
	var list []model.Role
	err := r.db.WithContext(ctx).
		Where("status = ?", model.StatusEnable).
		Order("sort ASC, id ASC").
		Find(&list).Error
	return list, err
}

func (r *Repository) FindRoleByID(ctx context.Context, id int64) (*model.Role, error) {
	var role model.Role
	if err := r.db.WithContext(ctx).First(&role, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &role, nil
}

func (r *Repository) FindRolesByIDs(ctx context.Context, ids []int64) ([]model.Role, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Role
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error
	return list, err
}

func (r *Repository) CountRoleByCode(ctx context.Context, code string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Role{}).Where("code = ?", code)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

func (r *Repository) CreateRole(ctx context.Context, role *model.Role) error {
	return r.db.WithContext(ctx).Create(role).Error
}

func (r *Repository) SaveRole(ctx context.Context, role *model.Role) error {
	return r.db.WithContext(ctx).Save(role).Error
}

func (r *Repository) DeleteRole(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", id).Delete(&model.RoleMenu{}).Error; err != nil {
			return err
		}
		if err := tx.Where("role_id = ?", id).Delete(&model.RoleDept{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Role{}, id).Error
	})
}

func (r *Repository) CountRoleUsers(ctx context.Context, roleID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.UserRole{}).Where("role_id = ?", roleID).Count(&total).Error
	return total, err
}

func (r *Repository) GetRoleMenuIDs(ctx context.Context, roleID int64) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.RoleMenu{}).Where("role_id = ?", roleID).Pluck("menu_id", &ids).Error
	return ids, err
}

func (r *Repository) ReplaceRoleMenus(ctx context.Context, roleID int64, menuIDs []int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&model.RoleMenu{}).Error; err != nil {
			return err
		}
		if len(menuIDs) == 0 {
			return nil
		}
		rows := make([]model.RoleMenu, 0, len(menuIDs))
		for _, menuID := range menuIDs {
			rows = append(rows, model.RoleMenu{RoleID: roleID, MenuID: menuID})
		}
		return tx.Create(&rows).Error
	})
}

func (r *Repository) GetRoleDeptIDs(ctx context.Context, roleID int64) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.RoleDept{}).Where("role_id = ?", roleID).Pluck("dept_id", &ids).Error
	return ids, err
}

// ReplaceRoleDepts 整体替换角色的自定义部门集合（删除 + 重建）。
func (r *Repository) ReplaceRoleDepts(ctx context.Context, roleID int64, deptIDs []int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&model.RoleDept{}).Error; err != nil {
			return err
		}
		if len(deptIDs) == 0 {
			return nil
		}
		rows := make([]model.RoleDept, 0, len(deptIDs))
		for _, deptID := range deptIDs {
			rows = append(rows, model.RoleDept{RoleID: roleID, DeptID: deptID})
		}
		return tx.Create(&rows).Error
	})
}

// ---------- 菜单 ----------

func (r *Repository) ListMenus(ctx context.Context) ([]model.Menu, error) {
	var list []model.Menu
	err := r.db.WithContext(ctx).Order("sort ASC, id ASC").Find(&list).Error
	return list, err
}

func (r *Repository) FindMenuByID(ctx context.Context, id int64) (*model.Menu, error) {
	var menu model.Menu
	if err := r.db.WithContext(ctx).First(&menu, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &menu, nil
}

func (r *Repository) CountMenuChildren(ctx context.Context, parentID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Menu{}).Where("parent_id = ?", parentID).Count(&total).Error
	return total, err
}

func (r *Repository) CreateMenu(ctx context.Context, menu *model.Menu) error {
	return r.db.WithContext(ctx).Create(menu).Error
}

func (r *Repository) SaveMenu(ctx context.Context, menu *model.Menu) error {
	return r.db.WithContext(ctx).Save(menu).Error
}

func (r *Repository) DeleteMenu(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("menu_id = ?", id).Delete(&model.RoleMenu{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Menu{}, id).Error
	})
}

// GetMenusByRoleIDs 返回角色集合关联的、已启用的菜单。
func (r *Repository) GetMenusByRoleIDs(ctx context.Context, roleIDs []int64) ([]model.Menu, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	var list []model.Menu
	err := r.db.WithContext(ctx).
		Table("system_menu").
		Joins("JOIN system_role_menu rm ON rm.menu_id = system_menu.id").
		Where("rm.role_id IN ?", roleIDs).
		Where("system_menu.status = ? AND system_menu.deleted_at IS NULL", model.StatusEnable).
		Distinct("system_menu.*").
		Order("system_menu.sort ASC, system_menu.id ASC").
		Find(&list).Error
	return list, err
}

// ---------- 部门 ----------

func (r *Repository) ListDepts(ctx context.Context, status *int8) ([]model.Dept, error) {
	q := r.db.WithContext(ctx).Model(&model.Dept{})
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	var list []model.Dept
	err := q.Order("sort ASC, id ASC").Find(&list).Error
	return list, err
}

func (r *Repository) FindDeptByID(ctx context.Context, id int64) (*model.Dept, error) {
	var dept model.Dept
	if err := r.db.WithContext(ctx).First(&dept, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &dept, nil
}

func (r *Repository) CountDeptChildren(ctx context.Context, parentID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.Dept{}).Where("parent_id = ?", parentID).Count(&total).Error
	return total, err
}

func (r *Repository) CreateDept(ctx context.Context, dept *model.Dept) error {
	return r.db.WithContext(ctx).Create(dept).Error
}

func (r *Repository) SaveDept(ctx context.Context, dept *model.Dept) error {
	return r.db.WithContext(ctx).Save(dept).Error
}

func (r *Repository) DeleteDept(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Dept{}, id).Error
}

// ListDeptIDsByLeader 返回用户担任负责人的部门 ID 集合（leader_user_id 命中）。
// oa 可见范围 helper 判定「部门负责人」身份用（issue #43）；租户插件自动
// 限定当前租户，跨租户的 leader_user_id 不可能命中。
func (r *Repository) ListDeptIDsByLeader(ctx context.Context, leaderUserID int64) ([]int64, error) {
	if leaderUserID <= 0 {
		return nil, nil
	}
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.Dept{}).
		Where("leader_user_id = ?", leaderUserID).Pluck("id", &ids).Error
	return ids, err
}

// ---------- bpm 审批人候选池解析（issue #46，spec #42 §4.2）----------

// ListActiveUserIDsByRole 返回持有指定角色且自身启用的用户 ID 集合。
// bpm 候选池物化用：角色与用户都要启用，停用用户不进候选池（spec §4.2）。
// 主表 system_user 经租户插件限定租户；跨租户的角色绑定不可能命中本租户用户。
func (r *Repository) ListActiveUserIDsByRole(ctx context.Context, roleID int64) ([]int64, error) {
	if roleID <= 0 {
		return nil, nil
	}
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN system_user_role ur ON ur.user_id = system_user.id").
		Joins("JOIN system_role ro ON ro.id = ur.role_id AND ro.status = ? AND ro.deleted_at IS NULL", model.StatusEnable).
		Where("ur.role_id = ?", roleID).
		Where("system_user.status = ?", model.StatusEnable).
		Pluck("system_user.id", &ids).Error
	return ids, err
}

// ListActiveUserIDsByPermission 返回持有指定权限码且自身启用的用户 ID 集合：
// 用户经启用角色绑定到持有该权限码的启用菜单，或角色为内置超管
// （super_admin 持全部权限码，与 service.Permissions 的 "*" 口径一致）。
func (r *Repository) ListActiveUserIDsByPermission(ctx context.Context, permission string) ([]int64, error) {
	if permission == "" {
		return nil, nil
	}
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN system_user_role ur ON ur.user_id = system_user.id").
		Joins("JOIN system_role ro ON ro.id = ur.role_id AND ro.status = ? AND ro.deleted_at IS NULL", model.StatusEnable).
		Joins("LEFT JOIN system_role_menu rm ON rm.role_id = ro.id").
		Joins("LEFT JOIN system_menu m ON m.id = rm.menu_id AND m.status = ? AND m.deleted_at IS NULL AND m.permission = ?", model.StatusEnable, permission).
		Where("system_user.status = ?", model.StatusEnable).
		Where("m.id IS NOT NULL OR ro.code = ?", "super_admin").
		Pluck("system_user.id", &ids).Error
	return ids, err
}

// ---------- 岗位 ----------

type PostFilter struct {
	Name   string
	Code   string
	Status *int8
}

func (r *Repository) ListPosts(ctx context.Context, f PostFilter, offset, limit int) ([]model.Post, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Post{})
	if f.Name != "" {
		q = q.Where("name LIKE ?", "%"+f.Name+"%")
	}
	if f.Code != "" {
		q = q.Where("code LIKE ?", "%"+f.Code+"%")
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Post
	if err := q.Order("sort ASC, id ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *Repository) ListSimplePosts(ctx context.Context) ([]model.Post, error) {
	var list []model.Post
	err := r.db.WithContext(ctx).Where("status = ?", model.StatusEnable).Order("sort ASC, id ASC").Find(&list).Error
	return list, err
}

func (r *Repository) FindPostByID(ctx context.Context, id int64) (*model.Post, error) {
	var post model.Post
	if err := r.db.WithContext(ctx).First(&post, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &post, nil
}

func (r *Repository) CountPostByCode(ctx context.Context, code string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Post{}).Where("code = ?", code)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var total int64
	err := q.Count(&total).Error
	return total, err
}

func (r *Repository) CountPostUsers(ctx context.Context, postID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&model.UserPost{}).Where("post_id = ?", postID).Count(&total).Error
	return total, err
}

func (r *Repository) CreatePost(ctx context.Context, post *model.Post) error {
	return r.db.WithContext(ctx).Create(post).Error
}

func (r *Repository) SavePost(ctx context.Context, post *model.Post) error {
	return r.db.WithContext(ctx).Save(post).Error
}

func (r *Repository) DeletePost(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Post{}, id).Error
}
