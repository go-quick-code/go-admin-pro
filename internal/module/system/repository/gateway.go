package repository

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
)

// ---------- 跨模块网关查询 ----------
// 面向 crm 等业务模块的批量只读查询（展示名称、归属快照），租户插件自动过滤。

// GetUserNicknameMap 批量返回用户昵称，缺失的 ID 不在返回集合中。
func (r *Repository) GetUserNicknameMap(ctx context.Context, userIDs []int64) (map[int64]string, error) {
	if len(userIDs) == 0 {
		return map[int64]string{}, nil
	}
	type row struct {
		ID       int64
		Nickname string
	}
	var rows []row
	if err := r.db.WithContext(ctx).Model(&model.User{}).
		Select("id", "nickname").
		Where("id IN ?", userIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(rows))
	for _, r := range rows {
		names[r.ID] = r.Nickname
	}
	return names, nil
}

// GetDeptNameMap 批量返回部门名称，缺失的 ID 不在返回集合中。
func (r *Repository) GetDeptNameMap(ctx context.Context, deptIDs []int64) (map[int64]string, error) {
	if len(deptIDs) == 0 {
		return map[int64]string{}, nil
	}
	type row struct {
		ID   int64
		Name string
	}
	var rows []row
	if err := r.db.WithContext(ctx).Model(&model.Dept{}).
		Select("id", "name").
		Where("id IN ?", deptIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(rows))
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	return names, nil
}

// ListSimpleUsers 返回启用用户的精简列表（id/昵称/部门），供表单选项与归属选择。
func (r *Repository) ListSimpleUsers(ctx context.Context) ([]model.SimpleUser, error) {
	var users []model.SimpleUser
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Select("id", "nickname", "dept_id").
		Where("status = ?", model.StatusEnable).
		Order("id ASC").
		Find(&users).Error
	return users, err
}

// ListActiveUserIDs 返回租户内启用用户 ID（升序）。
// 公告全员发布展开收件人用（issue #48）：停用用户不投递站内通知。
func (r *Repository) ListActiveUserIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("status = ?", model.StatusEnable).
		Order("id ASC").
		Pluck("id", &ids).Error
	return ids, err
}
