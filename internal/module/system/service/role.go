package service

import (
	"context"
	"fmt"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/datascope"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

func (s *Service) ListRoles(ctx context.Context, req *dto.RolePageReq) ([]model.Role, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.RoleFilter{Name: req.Name, Code: req.Code, Status: req.Status}
	return s.repo.ListRoles(ctx, filter, page.Offset(), page.PageSize)
}

func (s *Service) ListSimpleRoles(ctx context.Context) ([]model.Role, error) {
	return s.repo.ListSimpleRoles(ctx)
}

func (s *Service) GetRole(ctx context.Context, id int64) (*model.Role, error) {
	role, err := s.repo.FindRoleByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if role == nil {
		return nil, xerr.NotFound("角色不存在")
	}
	if role.MenuIDs, err = s.repo.GetRoleMenuIDs(ctx, id); err != nil {
		return nil, err
	}
	if role.DeptIDs, err = s.repo.GetRoleDeptIDs(ctx, id); err != nil {
		return nil, err
	}
	return role, nil
}

func (s *Service) CreateRole(ctx context.Context, req *dto.RoleCreateReq) error {
	if err := s.validateRoleDataScope(ctx, req.DataScope, req.DeptIDs); err != nil {
		return err
	}
	total, err := s.repo.CountRoleByCode(ctx, req.Code, 0)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("角色编码已存在")
	}
	role := &model.Role{
		TenantID:  s.TenantID(ctx),
		Name:      req.Name,
		Code:      req.Code,
		Sort:      req.Sort,
		DataScope: req.DataScope,
		Status:    req.Status,
		Type:      2,
		Remark:    req.Remark,
	}
	// 角色行、菜单绑定与自定义部门绑定在同一事务里落库，
	// 任一步失败整体回滚，不留半套角色。
	if err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.CreateRole(ctx, role); err != nil {
			return err
		}
		if err := tx.ReplaceRoleMenus(ctx, role.ID, req.MenuIDs); err != nil {
			return err
		}
		return tx.ReplaceRoleDepts(ctx, role.ID, req.DeptIDs)
	}); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
}

func (s *Service) UpdateRole(ctx context.Context, req *dto.RoleUpdateReq) error {
	if err := s.validateRoleDataScope(ctx, req.DataScope, req.DeptIDs); err != nil {
		return err
	}
	role, err := s.repo.FindRoleByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if role == nil {
		return xerr.NotFound("角色不存在")
	}
	if role.Type != 1 {
		total, err := s.repo.CountRoleByCode(ctx, req.Code, req.ID)
		if err != nil {
			return err
		}
		if total > 0 {
			return xerr.Conflict("角色编码已存在")
		}
		role.Code = req.Code
	}
	role.Name = req.Name
	role.Sort = req.Sort
	role.DataScope = req.DataScope
	role.Status = req.Status
	role.Remark = req.Remark
	// 角色资料、菜单绑定与自定义部门绑定在同一事务里落库：
	// 任一步失败整体回滚，避免出现「改了角色却没改上菜单/部门」的中间态。
	if err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.SaveRole(ctx, role); err != nil {
			return err
		}
		if err := tx.ReplaceRoleMenus(ctx, role.ID, req.MenuIDs); err != nil {
			return err
		}
		return tx.ReplaceRoleDepts(ctx, role.ID, req.DeptIDs)
	}); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
}

func (s *Service) DeleteRole(ctx context.Context, id int64) error {
	role, err := s.repo.FindRoleByID(ctx, id)
	if err != nil {
		return err
	}
	if role == nil {
		return xerr.NotFound("角色不存在")
	}
	if role.Type == 1 || role.Code == "super_admin" {
		return xerr.Business(1201, "内置角色不能删除")
	}
	users, err := s.repo.CountRoleUsers(ctx, id)
	if err != nil {
		return err
	}
	if users > 0 {
		return xerr.Business(1202, "该角色已分配给用户，无法删除")
	}
	if err := s.repo.DeleteRole(ctx, id); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
}

// validateRoleDataScope 校验数据范围取值与自定义部门集合：
// 非法值直接返回 400 并说明期望格式，绝不静默回退「全部」（issue #5 的越权漏洞修复点）。
func (s *Service) validateRoleDataScope(ctx context.Context, scope int8, deptIDs []int64) error {
	if !datascope.Scope(scope).Valid() {
		return xerr.BadRequest(fmt.Sprintf(
			"数据范围 dataScope 取值 %d 非法，期望：1 全部数据 / 2 自定义部门 / 3 本部门 / 4 本部门及以下 / 5 仅本人",
			scope))
	}
	if len(deptIDs) == 0 {
		return nil
	}
	depts, err := s.repo.ListDepts(ctx, nil)
	if err != nil {
		return err
	}
	known := make(map[int64]struct{}, len(depts))
	for _, d := range depts {
		known[d.ID] = struct{}{}
	}
	for _, id := range deptIDs {
		if _, ok := known[id]; !ok {
			return xerr.BadRequest(fmt.Sprintf("自定义部门不存在：部门 ID %d", id))
		}
	}
	return nil
}
