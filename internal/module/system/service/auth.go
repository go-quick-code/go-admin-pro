package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// Login 处理账号密码登录。
func (s *Service) Login(ctx context.Context, req *dto.LoginReq, ip, userAgent string) (*dto.LoginResp, error) {
	tenantID := req.TenantID
	if tenantID <= 0 {
		tenantID = s.cfg.Tenant.DefaultID
	}
	ctx = tenant.WithTenantID(ctx, tenantID)

	t, err := s.repo.FindTenantByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, xerr.Business(1001, "租户不存在")
	}
	if t.Status != model.StatusEnable {
		return nil, xerr.Business(1002, "租户已被禁用")
	}

	failKey := fmt.Sprintf("%d:%s", tenantID, req.Username)
	if err := s.checkLoginLock(failKey); err != nil {
		return nil, err
	}

	if s.cfg.Security.Captcha.Enabled {
		if !s.VerifyCaptcha(req.CaptchaKey, req.CaptchaCode) {
			s.recordLoginFail(failKey)
			return nil, xerr.Business(1003, "验证码错误")
		}
	}

	user, err := s.repo.FindUserByUsername(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	if user == nil || !CheckPassword(user.Password, req.Password) {
		s.recordLoginFail(failKey)
		s.RecordLoginLog(ctx, 0, req.Username, ip, userAgent, false, "账号或密码错误")
		return nil, xerr.Business(1004, "账号或密码错误")
	}
	if user.Status != model.StatusEnable {
		s.RecordLoginLog(ctx, user.ID, user.Username, ip, userAgent, false, "账号已被禁用")
		return nil, xerr.Business(1005, "账号已被禁用")
	}
	if t.ExpireTime != nil && t.ExpireTime.Before(time.Now()) {
		return nil, xerr.Business(1006, "租户已过期")
	}

	token, expireAt, err := s.jwt.Generate(user.ID, tenantID, user.Username)
	if err != nil {
		return nil, xerr.Internal(err)
	}

	s.resetLoginFail(failKey)
	now := time.Now()
	if err := s.repo.UpdateUserColumns(ctx, user.ID, map[string]interface{}{
		"login_ip":   ip,
		"login_date": now,
	}); err != nil {
		return nil, err
	}
	s.RecordLoginLog(ctx, user.ID, user.Username, ip, userAgent, true, "登录成功")

	return &dto.LoginResp{
		AccessToken: token,
		ExpiresIn:   int64(time.Until(expireAt).Seconds()),
		UserID:      user.ID,
		Username:    user.Username,
		TenantID:    tenantID,
	}, nil
}

// UserInfo 返回登录用户的资料、角色、权限码与菜单树。
func (s *Service) UserInfo(ctx context.Context, userID int64) (*dto.UserInfoResp, error) {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, xerr.Unauthorized("")
	}
	if user.Status != model.StatusEnable {
		return nil, xerr.Business(1005, "账号已被禁用")
	}

	roles, codes, err := s.Permissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	roleIDs, err := s.repo.GetUserRoleIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	postIDs, err := s.repo.GetUserPostIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	user.RoleIDs = roleIDs
	user.PostIDs = postIDs
	if user.DeptID > 0 {
		if dept, err := s.repo.FindDeptByID(ctx, user.DeptID); err == nil && dept != nil {
			user.DeptName = dept.Name
		}
	}

	var menus []model.Menu
	if IsSuperAdmin(roles) {
		menus, err = s.repo.ListMenus(ctx)
	} else {
		roleModels, err2 := s.repo.GetUserRoles(ctx, userID)
		if err2 != nil {
			return nil, err2
		}
		ids := make([]int64, 0, len(roleModels))
		for _, r := range roleModels {
			ids = append(ids, r.ID)
		}
		menus, err = s.repo.GetMenusByRoleIDs(ctx, ids)
	}
	if err != nil {
		return nil, err
	}

	// 只返回启用且可见的目录/菜单给前端路由。
	visible := make([]model.Menu, 0, len(menus))
	for _, m := range menus {
		if m.Type == model.MenuTypeButton || m.Status != model.StatusEnable {
			continue
		}
		visible = append(visible, m)
	}

	return &dto.UserInfoResp{
		User:        user,
		Roles:       roles,
		Permissions: codes,
		Menus:       BuildMenuTree(visible),
	}, nil
}

// UpdateProfile 修改当前用户资料。
func (s *Service) UpdateProfile(ctx context.Context, userID int64, req *dto.ProfileUpdateReq) error {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return xerr.NotFound("用户不存在")
	}
	return s.repo.UpdateUserColumns(ctx, userID, map[string]interface{}{
		"nickname": req.Nickname,
		"mobile":   req.Mobile,
		"email":    req.Email,
		"gender":   req.Gender,
		"avatar":   req.Avatar,
	})
}

// ChangePassword 修改当前用户密码。
func (s *Service) ChangePassword(ctx context.Context, userID int64, oldPassword, newPassword string) error {
	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return xerr.NotFound("用户不存在")
	}
	if !CheckPassword(user.Password, oldPassword) {
		return xerr.Business(1007, "原密码不正确")
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return xerr.Internal(err)
	}
	return s.repo.UpdateUserColumns(ctx, userID, map[string]interface{}{"password": hash})
}

// RecordLoginLog 写入登录日志，失败不影响主流程。
func (s *Service) RecordLoginLog(ctx context.Context, userID int64, username, ip, userAgent string, success bool, message string) {
	result := int8(0)
	if !success {
		result = 1
	}
	log := &model.LoginLog{
		TenantID:  tenant.ID(ctx),
		UserID:    userID,
		Username:  username,
		LoginIP:   ip,
		UserAgent: truncate(userAgent, 500),
		LoginType: 1,
		Result:    result,
		Message:   message,
		LoginTime: time.Now(),
	}
	if err := s.repo.CreateLoginLog(ctx, log); err != nil {
		s.logError("write login log", err)
	}
}

func (s *Service) checkLoginLock(key string) error {
	v, ok := s.failCache.Load(key)
	if !ok {
		return nil
	}
	state := v.(*loginFailState)
	if state.count < s.cfg.Security.Login.MaxFail {
		return nil
	}
	if time.Since(state.lockedAt) < s.cfg.Security.Login.LockDuration {
		return xerr.Business(1008, fmt.Sprintf("登录失败次数过多，请 %d 分钟后再试", int(s.cfg.Security.Login.LockDuration.Minutes())))
	}
	s.failCache.Delete(key)
	return nil
}

func (s *Service) recordLoginFail(key string) {
	v, _ := s.failCache.LoadOrStore(key, &loginFailState{})
	state := v.(*loginFailState)
	state.count++
	if state.count >= s.cfg.Security.Login.MaxFail {
		state.lockedAt = time.Now()
	}
}

func (s *Service) resetLoginFail(key string) {
	s.failCache.Delete(key)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
