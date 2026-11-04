package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// BuildMenuTree 把扁平菜单列表组装成树。
func BuildMenuTree(list []model.Menu) []*model.Menu {
	nodes := make(map[int64]*model.Menu, len(list))
	for i := range list {
		m := list[i]
		m.Children = nil
		nodes[m.ID] = &m
	}
	roots := make([]*model.Menu, 0)
	for i := range list {
		node := nodes[list[i].ID]
		if list[i].ParentID != 0 {
			if parent, ok := nodes[list[i].ParentID]; ok {
				parent.Children = append(parent.Children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	return roots
}

func (s *Service) ListMenuTree(ctx context.Context) ([]*model.Menu, error) {
	list, err := s.repo.ListMenus(ctx)
	if err != nil {
		return nil, err
	}
	return BuildMenuTree(list), nil
}

func (s *Service) GetMenu(ctx context.Context, id int64) (*model.Menu, error) {
	menu, err := s.repo.FindMenuByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if menu == nil {
		return nil, xerr.NotFound("菜单不存在")
	}
	return menu, nil
}

func (s *Service) CreateMenu(ctx context.Context, req *dto.MenuCreateReq) error {
	if err := s.validateMenuParent(ctx, req.ParentID, 0); err != nil {
		return err
	}
	menu := &model.Menu{
		Name:          req.Name,
		Permission:    req.Permission,
		Type:          req.Type,
		Sort:          req.Sort,
		ParentID:      req.ParentID,
		Path:          req.Path,
		Component:     req.Component,
		ComponentName: req.ComponentName,
		Icon:          req.Icon,
		Visible:       req.Visible,
		Status:        req.Status,
		KeepAlive:     req.KeepAlive,
	}
	if err := s.repo.CreateMenu(ctx, menu); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
}

func (s *Service) UpdateMenu(ctx context.Context, req *dto.MenuUpdateReq) error {
	menu, err := s.repo.FindMenuByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if menu == nil {
		return xerr.NotFound("菜单不存在")
	}
	if req.ParentID == req.ID {
		return xerr.BadRequest("上级菜单不能是自己")
	}
	if err := s.validateMenuParent(ctx, req.ParentID, req.ID); err != nil {
		return err
	}
	menu.Name = req.Name
	menu.Permission = req.Permission
	menu.Type = req.Type
	menu.Sort = req.Sort
	menu.ParentID = req.ParentID
	menu.Path = req.Path
	menu.Component = req.Component
	menu.ComponentName = req.ComponentName
	menu.Icon = req.Icon
	menu.Visible = req.Visible
	menu.Status = req.Status
	menu.KeepAlive = req.KeepAlive
	if err := s.repo.SaveMenu(ctx, menu); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
}

func (s *Service) DeleteMenu(ctx context.Context, id int64) error {
	menu, err := s.repo.FindMenuByID(ctx, id)
	if err != nil {
		return err
	}
	if menu == nil {
		return xerr.NotFound("菜单不存在")
	}
	children, err := s.repo.CountMenuChildren(ctx, id)
	if err != nil {
		return err
	}
	if children > 0 {
		return xerr.Business(1301, "存在子菜单，无法删除")
	}
	if err := s.repo.DeleteMenu(ctx, id); err != nil {
		return err
	}
	s.ClearAllPermissionCache()
	return nil
}

// validateMenuParent 校验父菜单存在，且不会形成环。
func (s *Service) validateMenuParent(ctx context.Context, parentID, selfID int64) error {
	if parentID == 0 {
		return nil
	}
	all, err := s.repo.ListMenus(ctx)
	if err != nil {
		return err
	}
	parents := make(map[int64]int64, len(all))
	for _, m := range all {
		parents[m.ID] = m.ParentID
	}
	if _, ok := parents[parentID]; !ok {
		return xerr.BadRequest("上级菜单不存在")
	}
	if selfID == 0 {
		return nil
	}
	for cur, guard := parentID, 0; cur != 0 && guard < 1000; guard++ {
		if cur == selfID {
			return xerr.BadRequest("上级菜单不能是自己的子菜单")
		}
		cur = parents[cur]
	}
	return nil
}
