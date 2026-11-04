package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/tenant"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

func (s *Service) ListTenants(ctx context.Context, req *dto.TenantPageReq) ([]model.Tenant, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.TenantFilter{Name: req.Name, Status: req.Status}
	return s.repo.ListTenants(ctx, filter, page.Offset(), page.PageSize)
}

func (s *Service) GetTenant(ctx context.Context, id int64) (*model.Tenant, error) {
	t, err := s.repo.FindTenantByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, xerr.NotFound("租户不存在")
	}
	return t, nil
}

// CreateTenant 创建租户并初始化租户管理员。
// 新租户默认账号：admin / 123456。
func (s *Service) CreateTenant(ctx context.Context, req *dto.TenantCreateReq) error {
	total, err := s.repo.CountTenantByName(ctx, req.Name, 0)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("租户名称已存在")
	}
	t := &model.Tenant{
		Name:          req.Name,
		ContactName:   req.ContactName,
		ContactMobile: req.ContactMobile,
		Status:        req.Status,
		Domain:        req.Domain,
		ExpireTime:    parseExpireTime(req.ExpireTime),
		AccountCount:  req.AccountCount,
		Remark:        req.Remark,
	}
	if err := s.repo.CreateTenant(ctx, t); err != nil {
		return err
	}
	if err := s.bootstrapTenant(ctx, t.ID); err != nil {
		// 初始化失败时回滚租户，避免留下空壳租户。
		_ = s.repo.DeleteTenant(ctx, t.ID)
		return err
	}
	return nil
}

// bootstrapTenant 为新租户创建默认部门、角色与管理员账号。
func (s *Service) bootstrapTenant(ctx context.Context, tenantID int64) error {
	tctx := tenant.WithTenantID(ctx, tenantID)

	dept := &model.Dept{
		TenantID: tenantID,
		Name:     "总公司",
		ParentID: 0,
		Sort:     0,
		Status:   model.StatusEnable,
	}
	if err := s.repo.CreateDept(tctx, dept); err != nil {
		return err
	}

	role := &model.Role{
		TenantID:  tenantID,
		Name:      "超级管理员",
		Code:      "super_admin",
		Sort:      0,
		DataScope: model.DataScopeAll,
		Status:    model.StatusEnable,
		Type:      1,
		Remark:    "租户内置超级管理员",
	}
	if err := s.repo.CreateRole(tctx, role); err != nil {
		return err
	}

	hash, err := HashPassword("123456")
	if err != nil {
		return err
	}
	user := &model.User{
		TenantID: tenantID,
		Username: "admin",
		Password: hash,
		Nickname: "租户管理员",
		DeptID:   dept.ID,
		Status:   model.StatusEnable,
		Remark:   fmt.Sprintf("由租户 %d 初始化自动创建", tenantID),
	}
	if err := s.repo.CreateUser(tctx, user); err != nil {
		return err
	}
	return s.repo.ReplaceUserRoles(tctx, user.ID, []int64{role.ID})
}

func (s *Service) UpdateTenant(ctx context.Context, req *dto.TenantUpdateReq) error {
	t, err := s.repo.FindTenantByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if t == nil {
		return xerr.NotFound("租户不存在")
	}
	if t.Name != req.Name {
		total, err := s.repo.CountTenantByName(ctx, req.Name, req.ID)
		if err != nil {
			return err
		}
		if total > 0 {
			return xerr.Conflict("租户名称已存在")
		}
	}
	if t.ID == 1 && req.Status == model.StatusDisable {
		return xerr.Business(1701, "内置租户不能被禁用")
	}
	t.Name = req.Name
	t.ContactName = req.ContactName
	t.ContactMobile = req.ContactMobile
	t.Status = req.Status
	t.Domain = req.Domain
	t.ExpireTime = parseExpireTime(req.ExpireTime)
	t.AccountCount = req.AccountCount
	t.Remark = req.Remark
	return s.repo.SaveTenant(ctx, t)
}

// parseExpireTime 兼容 RFC3339 与 "2006-01-02 15:04:05" 两种前端格式。
func parseExpireTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &t
		}
	}
	return nil
}

func (s *Service) UpdateTenantStatus(ctx context.Context, id int64, status int8) error {
	if id == 1 && status == model.StatusDisable {
		return xerr.Business(1702, "内置租户不能被禁用")
	}
	t, err := s.repo.FindTenantByID(ctx, id)
	if err != nil {
		return err
	}
	if t == nil {
		return xerr.NotFound("租户不存在")
	}
	t.Status = status
	return s.repo.SaveTenant(ctx, t)
}

func (s *Service) DeleteTenant(ctx context.Context, id int64) error {
	if id == 1 {
		return xerr.Business(1703, "内置租户不能删除")
	}
	t, err := s.repo.FindTenantByID(ctx, id)
	if err != nil {
		return err
	}
	if t == nil {
		return xerr.NotFound("租户不存在")
	}
	users, err := s.repo.CountUsersByTenantID(ctx, id)
	if err != nil {
		return err
	}
	if users > 0 {
		return xerr.Business(1704, "租户下存在用户，请先清理数据")
	}
	return s.repo.DeleteTenant(ctx, id)
}
