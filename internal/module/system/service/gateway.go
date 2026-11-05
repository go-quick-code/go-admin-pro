package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
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

// UserHasPermission 判断用户是否持有指定权限码（super_admin 以 "*" 通配）。
// 复用 Permissions 的角色/菜单解析与缓存，与 RequirePermission 中间件
// 同一真值来源；crm 公海释放/转移的「负责人本人 或 权限码」组合校验用（issue #13）。
func (s *Service) UserHasPermission(ctx context.Context, userID int64, permission string) (bool, error) {
	_, codes, err := s.Permissions(ctx, userID)
	if err != nil {
		return false, err
	}
	return HasPermission(codes, permission), nil
}

// ListSimpleUsers 返回启用用户的精简列表（表单选项用，与角色/岗位 simple-list 同位）。
func (s *Service) ListSimpleUsers(ctx context.Context) ([]model.SimpleUser, error) {
	return s.repo.ListSimpleUsers(ctx)
}

// ListEnabledTenantIDs 返回全部启用租户的 ID（系统级）：
// crm 公海自动回收 job 遍历租户用（issue #14，见 crm/service.SystemGateway）。
func (s *Service) ListEnabledTenantIDs(ctx context.Context) ([]int64, error) {
	return s.repo.ListEnabledTenantIDs(ctx)
}
