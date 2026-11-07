package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/tenant"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
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
	if err := s.validateTenantPackageForAssign(ctx, req.PackageID, 0); err != nil {
		return err
	}
	t := &model.Tenant{
		Name:          req.Name,
		ContactName:   req.ContactName,
		ContactMobile: req.ContactMobile,
		Status:        req.Status,
		PackageID:     req.PackageID,
		Domain:        req.Domain,
		ExpireTime:    parseExpireTime(req.ExpireTime),
		AccountCount:  req.AccountCount,
		Remark:        req.Remark,
	}
	// 租户行与引导数据在同一事务里落库：引导失败整体回滚，
	// 不会留下空壳租户或半套初始化数据（旧实现用补偿删除，失败时会有残留）。
	return s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.CreateTenant(ctx, t); err != nil {
			return err
		}
		return s.bootstrapTenant(ctx, tx, t.ID, req.PackageID)
	})
}

// bootstrapTenant 为新租户创建默认部门、角色与管理员账号。
// repo 参数是调用方事务里的 DAL：引导写入必须全部走它，才能一起回滚。
func (s *Service) bootstrapTenant(ctx context.Context, repo *repository.Repository, tenantID, packageID int64) error {
	tctx := tenant.WithTenantID(ctx, tenantID)

	dept := &model.Dept{
		TenantID: tenantID,
		Name:     "总公司",
		ParentID: 0,
		Sort:     0,
		Status:   model.StatusEnable,
	}
	if err := repo.CreateDept(tctx, dept); err != nil {
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
	if err := repo.CreateRole(tctx, role); err != nil {
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
	if err := repo.CreateUser(tctx, user); err != nil {
		return err
	}
	if err := repo.ReplaceUserRoles(tctx, user.ID, []int64{role.ID}); err != nil {
		return err
	}
	// 套餐绑定：未指定套餐时绑定全部启用菜单（保持旧行为）；指定套餐时
	// 只绑定套餐菜单 + 其下按钮（spec §4.1）。
	menuIDs, err := s.menuIDsForTenant(tctx, repo, packageID)
	if err != nil {
		return err
	}
	return repo.ReplaceRoleMenus(tctx, role.ID, menuIDs)
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
	packageChanged := t.PackageID != req.PackageID
	// 套餐可绑定校验只在「真的换了套餐」时执行（PR #86 审查意见）：未改套餐
	// 的编辑原样回传 packageId，不做停用校验——否则套餐停用后，所有绑定该
	// 套餐的租户编辑入口会被 400 锁死；改绑到停用/不存在的套餐仍然报错。
	if packageChanged {
		if err := s.validateTenantPackageForAssign(ctx, req.PackageID, t.ID); err != nil {
			return err
		}
	}
	t.Name = req.Name
	t.ContactName = req.ContactName
	t.ContactMobile = req.ContactMobile
	t.Status = req.Status
	t.PackageID = req.PackageID
	t.Domain = req.Domain
	t.ExpireTime = parseExpireTime(req.ExpireTime)
	t.AccountCount = req.AccountCount
	t.Remark = req.Remark
	if !packageChanged {
		return s.repo.SaveTenant(ctx, t)
	}
	// 换套餐与超管菜单重绑必须同事务：任一失败整体回滚，避免租户配置与
	// 实际权限不一致。
	if err := s.repo.Transaction(ctx, func(tx *repository.Repository) error {
		if err := tx.SaveTenant(ctx, t); err != nil {
			return err
		}
		return s.syncTenantSuperAdminMenus(ctx, tx, t.ID, t.PackageID)
	}); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
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

// validateTenantPackageForAssign 校验套餐可绑定：平台租户不允许套餐；
// 套餐必须存在且启用。tenantID 为 0 表示新建租户（尚未有平台租户限制）。
func (s *Service) validateTenantPackageForAssign(ctx context.Context, packageID, tenantID int64) error {
	if packageID <= 0 {
		return nil
	}
	if tenantID == tenant.DefaultID {
		return xerr.BadRequest("平台租户不能绑定套餐")
	}
	pkg, err := s.findTenantPackage(ctx, packageID)
	if err != nil {
		return err
	}
	if pkg.Status != model.StatusEnable {
		return xerr.BadRequest("套餐已停用，不能绑定")
	}
	return nil
}

// menuIDsForTenant 计算租户超管应绑定的菜单集合：
//   - packageID > 0：套餐选中的目录/菜单 + 其下按钮（不隐式补全子菜单）；
//   - packageID == 0：全部启用菜单/按钮（未绑定套餐的兼容行为）。
func (s *Service) menuIDsForTenant(ctx context.Context, repo *repository.Repository, packageID int64) ([]int64, error) {
	menus, err := repo.ListMenus(ctx)
	if err != nil {
		return nil, err
	}
	if packageID <= 0 {
		return allEnabledMenuIDs(menus), nil
	}
	pkg, err := repo.FindTenantPackageByID(ctx, packageID)
	if err != nil {
		return nil, err
	}
	if pkg == nil {
		return nil, xerr.NotFound("套餐不存在")
	}
	return expandPackageMenuIDs(menus, pkg.MenuIDs), nil
}

// syncTenantSuperAdminMenus 重写目标租户全部 super_admin 角色的菜单绑定。
// 调用方负责提供事务仓储；角色/菜单读写显式使用目标租户上下文。
func (s *Service) syncTenantSuperAdminMenus(ctx context.Context, repo *repository.Repository, tenantID, packageID int64) error {
	roles, err := repo.ListRolesByTenantAndCode(ctx, tenantID, "super_admin")
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		return nil
	}
	menuIDs, err := s.menuIDsForTenant(ctx, repo, packageID)
	if err != nil {
		return err
	}
	tctx := tenant.WithTenantID(ctx, tenantID)
	for _, role := range roles {
		if err := repo.ReplaceRoleMenus(tctx, role.ID, menuIDs); err != nil {
			return err
		}
	}
	return nil
}

// allEnabledMenuIDs 返回全部启用菜单（含目录/菜单/按钮）。
func allEnabledMenuIDs(menus []model.Menu) []int64 {
	ids := make([]int64, 0, len(menus))
	for _, menu := range menus {
		if menu.Status == model.StatusEnable {
			ids = append(ids, menu.ID)
		}
	}
	return ids
}

// expandPackageMenuIDs 把套餐选中的目录/菜单展开为「选中项 + 其下按钮」。
// 按钮是菜单的操作权限，必须随菜单一起授予；子菜单不隐式选中（由套餐显式勾选）。
func expandPackageMenuIDs(menus []model.Menu, selected model.Int64List) []int64 {
	enabled := make(map[int64]struct{}, len(menus))
	buttonsByParent := make(map[int64][]int64)
	for _, menu := range menus {
		if menu.Status != model.StatusEnable {
			continue
		}
		enabled[menu.ID] = struct{}{}
		if menu.Type == model.MenuTypeButton {
			buttonsByParent[menu.ParentID] = append(buttonsByParent[menu.ParentID], menu.ID)
		}
	}
	out := make([]int64, 0, len(selected))
	seen := make(map[int64]struct{}, len(selected))
	add := func(id int64) {
		if _, ok := enabled[id]; !ok {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range selected {
		add(id)
		for _, buttonID := range buttonsByParent[id] {
			add(buttonID)
		}
	}
	return out
}
