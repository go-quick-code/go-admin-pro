package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// GetActiveUser 获取启用状态的用户，供鉴权中间件使用。
func (s *Service) GetActiveUser(ctx context.Context, id int64) (*model.User, error) {
	user, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, xerr.Unauthorized("用户不存在或已被删除")
	}
	if user.Status != model.StatusEnable {
		return nil, xerr.Unauthorized("账号已被禁用")
	}
	return user, nil
}

// ListUsers 分页查询用户，并按数据范围谓词过滤（行级权限引擎，见 internal/pkg/datascope）。
func (s *Service) ListUsers(ctx context.Context, req *dto.UserPageReq) ([]model.User, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()

	filter := repository.UserFilter{
		Username: req.Username,
		Nickname: req.Nickname,
		Mobile:   req.Mobile,
		Status:   req.Status,
		DeptID:   req.DeptID,
	}

	scope, err := s.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return nil, 0, err
	}
	if !scope.All {
		if scope.IncludeSelf {
			filter.SelfUserID = scope.UserID
		}
		switch {
		case len(scope.DeptIDs) > 0:
			filter.DeptIDs = scope.DeptIDs
		case !scope.IncludeSelf:
			// 无可见部门且不含本人：谓词恒假（fail-closed，绝不回退全部）。
			filter.DeptIDs = []int64{}
		}
	}

	list, total, err := s.repo.ListUsers(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range list {
		if list[i].DeptID > 0 {
			if dept, err := s.repo.FindDeptByID(ctx, list[i].DeptID); err == nil && dept != nil {
				list[i].DeptName = dept.Name
			}
		}
	}
	return list, total, nil
}

// GetUser 查询用户详情，附带角色与岗位。
func (s *Service) GetUser(ctx context.Context, id int64) (*model.User, error) {
	user, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, xerr.NotFound("用户不存在")
	}
	if user.DeptID > 0 {
		if dept, err := s.repo.FindDeptByID(ctx, user.DeptID); err == nil && dept != nil {
			user.DeptName = dept.Name
		}
	}
	if user.RoleIDs, err = s.repo.GetUserRoleIDs(ctx, id); err != nil {
		return nil, err
	}
	if user.PostIDs, err = s.repo.GetUserPostIDs(ctx, id); err != nil {
		return nil, err
	}
	return user, nil
}

// CreateUser 创建用户。用户行、角色绑定、岗位绑定在同一事务里落库，
// 任一步失败整体回滚，不会留下没有角色/岗位的半套账号。
func (s *Service) CreateUser(ctx context.Context, req *dto.UserCreateReq) error {
	total, err := s.repo.CountUserByUsername(ctx, req.Username, 0)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("用户名已存在")
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return xerr.Internal(err)
	}
	user := &model.User{
		TenantID: s.TenantID(ctx),
		Username: req.Username,
		Password: hash,
		Nickname: req.Nickname,
		DeptID:   req.DeptID,
		Mobile:   req.Mobile,
		Email:    req.Email,
		Gender:   req.Gender,
		Avatar:   req.Avatar,
		Status:   req.Status,
		Remark:   req.Remark,
	}
	if err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.CreateUser(ctx, user); err != nil {
			return err
		}
		if err := tx.ReplaceUserRoles(ctx, user.ID, req.RoleIDs); err != nil {
			return err
		}
		return tx.ReplaceUserPosts(ctx, user.ID, req.PostIDs)
	}); err != nil {
		return err
	}
	s.ClearPermissionCache(user.ID)
	return nil
}

// UpdateUser 修改用户。
func (s *Service) UpdateUser(ctx context.Context, req *dto.UserUpdateReq) error {
	user, err := s.repo.FindUserByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if user == nil {
		return xerr.NotFound("用户不存在")
	}
	user.Nickname = req.Nickname
	user.DeptID = req.DeptID
	user.Mobile = req.Mobile
	user.Email = req.Email
	user.Gender = req.Gender
	user.Avatar = req.Avatar
	user.Status = req.Status
	user.Remark = req.Remark
	if req.Password != "" {
		hash, err := HashPassword(req.Password)
		if err != nil {
			return xerr.Internal(err)
		}
		user.Password = hash
	}
	// 用户资料与角色/岗位绑定在同一事务里落库，绑定失败时资料修改一并回滚。
	if err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.SaveUser(ctx, user); err != nil {
			return err
		}
		if err := tx.ReplaceUserRoles(ctx, user.ID, req.RoleIDs); err != nil {
			return err
		}
		return tx.ReplaceUserPosts(ctx, user.ID, req.PostIDs)
	}); err != nil {
		return err
	}
	s.ClearPermissionCache(user.ID)
	return nil
}

// UpdateUserStatus 启用/停用用户。
func (s *Service) UpdateUserStatus(ctx context.Context, id int64, status int8) error {
	if id == 1 && status == model.StatusDisable {
		return xerr.Business(1101, "内置管理员不能被停用")
	}
	if id == CurrentUserID(ctx) && status == model.StatusDisable {
		return xerr.Business(1102, "不能停用当前登录账号")
	}
	user, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return err
	}
	if user == nil {
		return xerr.NotFound("用户不存在")
	}
	s.ClearPermissionCache(id)
	return s.repo.UpdateUserColumns(ctx, id, map[string]interface{}{"status": status})
}

// DeleteUser 删除用户。
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	if id == 1 {
		return xerr.Business(1103, "内置管理员不能删除")
	}
	if id == CurrentUserID(ctx) {
		return xerr.Business(1104, "不能删除当前登录账号")
	}
	user, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return err
	}
	if user == nil {
		return xerr.NotFound("用户不存在")
	}
	s.ClearPermissionCache(id)
	return s.repo.DeleteUser(ctx, id)
}

// ResetUserPassword 重置密码。
func (s *Service) ResetUserPassword(ctx context.Context, id int64, password string) error {
	user, err := s.repo.FindUserByID(ctx, id)
	if err != nil {
		return err
	}
	if user == nil {
		return xerr.NotFound("用户不存在")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return xerr.Internal(err)
	}
	return s.repo.UpdateUserColumns(ctx, id, map[string]interface{}{"password": hash})
}
