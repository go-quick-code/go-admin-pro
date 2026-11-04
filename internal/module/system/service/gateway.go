package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/model"
)

// ---------- 跨模块网关方法 ----------
// 供 crm 等业务模块消费的最小能力面：数据范围之外的归属快照与展示名称。
// 只读、租户插件自动过滤；crm 侧通过接口依赖这些方法（见 crm/service.SystemGateway）。

// UserDeptID 返回用户所属部门 ID；用户不存在时 ok=false。
// crm 负责人快照用：负责人必须真实存在，部门缺失时快照为 0。
func (s *Service) UserDeptID(ctx context.Context, userID int64) (int64, bool, error) {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return 0, false, err
	}
	if user == nil {
		return 0, false, nil
	}
	return user.DeptID, true, nil
}

// UserNicknames 批量返回用户昵称，缺失的 ID 不在返回集合中。
func (s *Service) UserNicknames(ctx context.Context, userIDs []int64) (map[int64]string, error) {
	names, err := s.repo.GetUserNicknameMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	return names, nil
}

// DeptNames 批量返回部门名称，缺失的 ID 不在返回集合中。
func (s *Service) DeptNames(ctx context.Context, deptIDs []int64) (map[int64]string, error) {
	names, err := s.repo.GetDeptNameMap(ctx, deptIDs)
	if err != nil {
		return nil, err
	}
	return names, nil
}

// ListSimpleUsers 返回启用用户的精简列表（表单选项用，与角色/岗位 simple-list 同位）。
func (s *Service) ListSimpleUsers(ctx context.Context) ([]model.SimpleUser, error) {
	return s.repo.ListSimpleUsers(ctx)
}
