package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/repository"
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
	return role, nil
}

func (s *Service) CreateRole(ctx context.Context, req *dto.RoleCreateReq) error {
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
		DataScope: normalizeDataScope(req.DataScope),
		Status:    req.Status,
		Type:      2,
		Remark:    req.Remark,
	}
	// 角色行与菜单绑定在同一事务里落库，绑定失败时不留无菜单的半套角色。
	if err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.CreateRole(ctx, role); err != nil {
			return err
		}
		return tx.ReplaceRoleMenus(ctx, role.ID, req.MenuIDs)
	}); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
}

func (s *Service) UpdateRole(ctx context.Context, req *dto.RoleUpdateReq) error {
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
	role.DataScope = normalizeDataScope(req.DataScope)
	role.Status = req.Status
	role.Remark = req.Remark
	// 角色资料与菜单绑定（角色菜单分配）在同一事务里落库：
	// 绑定失败时角色修改一并回滚，避免出现「改了角色却没改上菜单」的中间态。
	if err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.SaveRole(ctx, role); err != nil {
			return err
		}
		return tx.ReplaceRoleMenus(ctx, role.ID, req.MenuIDs)
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

func normalizeDataScope(scope int8) int8 {
	if scope < model.DataScopeAll || scope > model.DataScopeSelf {
		return model.DataScopeAll
	}
	return scope
}
