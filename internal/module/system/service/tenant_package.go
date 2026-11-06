package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-quick-code/go-admin-pro/internal/module/system/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/system/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListTenantPackages 租户套餐分页（平台级）。
func (s *Service) ListTenantPackages(ctx context.Context, req *dto.TenantPackagePageReq) ([]model.TenantPackage, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.TenantPackageFilter{Name: strings.TrimSpace(req.Name), Status: req.Status}
	return s.repo.ListTenantPackages(ctx, filter, page.Offset(), page.PageSize)
}

func (s *Service) ListSimpleTenantPackages(ctx context.Context) ([]model.TenantPackage, error) {
	return s.repo.ListSimpleTenantPackages(ctx)
}

func (s *Service) GetTenantPackage(ctx context.Context, id int64) (*model.TenantPackage, error) {
	return s.findTenantPackage(ctx, id)
}

// findTenantPackage 统一处理仓储的「未找到」语义：notFound 把
// gorm.ErrRecordNotFound 转成 (nil, nil)，调用方必须显式判空并返回 404。
func (s *Service) findTenantPackage(ctx context.Context, id int64) (*model.TenantPackage, error) {
	pkg, err := s.repo.FindTenantPackageByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if pkg == nil {
		return nil, xerr.NotFound("租户套餐不存在")
	}
	return pkg, nil
}

func (s *Service) CreateTenantPackage(ctx context.Context, req *dto.TenantPackageCreateReq) error {
	name, menuIDs, err := s.validateTenantPackage(ctx, req.Name, req.MenuIDs, req.Status, req.MaxUsers, req.MaxStorageMB, req.MaxSMSMonthly, req.MaxMailMonthly)
	if err != nil {
		return err
	}
	if err := s.ensureTenantPackageNameFree(ctx, name, 0); err != nil {
		return err
	}
	pkg := &model.TenantPackage{
		Name:           name,
		Status:         req.Status,
		MenuIDs:        menuIDs,
		MaxUsers:       req.MaxUsers,
		MaxStorageMB:   req.MaxStorageMB,
		MaxSMSMonthly:  req.MaxSMSMonthly,
		MaxMailMonthly: req.MaxMailMonthly,
		Remark:         strings.TrimSpace(req.Remark),
	}
	return s.repo.CreateTenantPackage(ctx, pkg)
}

func (s *Service) UpdateTenantPackage(ctx context.Context, req *dto.TenantPackageUpdateReq) error {
	pkg, err := s.findTenantPackage(ctx, req.ID)
	if err != nil {
		return err
	}
	name, menuIDs, err := s.validateTenantPackage(ctx, req.Name, req.MenuIDs, req.Status, req.MaxUsers, req.MaxStorageMB, req.MaxSMSMonthly, req.MaxMailMonthly)
	if err != nil {
		return err
	}
	if err := s.ensureTenantPackageNameFree(ctx, name, req.ID); err != nil {
		return err
	}
	pkg.Name = name
	pkg.Status = req.Status
	pkg.MenuIDs = menuIDs
	pkg.MaxUsers = req.MaxUsers
	pkg.MaxStorageMB = req.MaxStorageMB
	pkg.MaxSMSMonthly = req.MaxSMSMonthly
	pkg.MaxMailMonthly = req.MaxMailMonthly
	pkg.Remark = strings.TrimSpace(req.Remark)
	return s.repo.SaveTenantPackage(ctx, pkg)
}

func (s *Service) UpdateTenantPackageStatus(ctx context.Context, id int64, status int8) error {
	if err := validateStatus(status); err != nil {
		return err
	}
	pkg, err := s.findTenantPackage(ctx, id)
	if err != nil {
		return err
	}
	pkg.Status = status
	return s.repo.SaveTenantPackage(ctx, pkg)
}

func (s *Service) DeleteTenantPackage(ctx context.Context, id int64) error {
	if _, err := s.findTenantPackage(ctx, id); err != nil {
		return err
	}
	referenced, err := s.repo.CountTenantsByPackageID(ctx, id)
	if err != nil {
		return err
	}
	if referenced > 0 {
		return xerr.Conflict(fmt.Sprintf("套餐已被 %d 个租户引用，不能删除", referenced))
	}
	return s.repo.DeleteTenantPackage(ctx, id)
}

// validateTenantPackage 归一化名称、校验状态/配额/菜单 ID。
// menu_ids 只允许目录与菜单；按钮由绑定套餐时按父菜单展开（spec §4.1）。
func (s *Service) validateTenantPackage(ctx context.Context, rawName string, rawMenuIDs []int64, status int8, maxUsers, maxStorageMB, maxSMSMonthly, maxMailMonthly int64) (string, model.Int64List, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "", nil, xerr.BadRequest("套餐名称不能为空")
	}
	if err := validateStatus(status); err != nil {
		return "", nil, err
	}
	if maxUsers < 0 || maxStorageMB < 0 || maxSMSMonthly < 0 || maxMailMonthly < 0 {
		return "", nil, xerr.BadRequest("配额不能为负数（0 表示不限）")
	}

	menus, err := s.repo.ListMenus(ctx)
	if err != nil {
		return "", nil, err
	}
	types := make(map[int64]int8, len(menus))
	for _, menu := range menus {
		types[menu.ID] = menu.Type
	}
	seen := make(map[int64]struct{}, len(rawMenuIDs))
	out := make(model.Int64List, 0, len(rawMenuIDs))
	for _, id := range rawMenuIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		menuType, ok := types[id]
		if !ok {
			return "", nil, xerr.BadRequest(fmt.Sprintf("菜单 %d 不存在", id))
		}
		if menuType == model.MenuTypeButton {
			return "", nil, xerr.BadRequest(fmt.Sprintf("菜单 %d 是按钮，套餐只能包含目录或菜单", id))
		}
		out = append(out, id)
	}
	return name, out, nil
}

func (s *Service) ensureTenantPackageNameFree(ctx context.Context, name string, excludeID int64) error {
	total, err := s.repo.CountTenantPackageByName(ctx, name, excludeID)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("套餐名称已存在")
	}
	return nil
}

func validateStatus(status int8) error {
	if status != model.StatusEnable && status != model.StatusDisable {
		return xerr.BadRequest(fmt.Sprintf("状态非法：%d（0 启用 / 1 停用）", status))
	}
	return nil
}
