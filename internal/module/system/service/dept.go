package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// BuildDeptTree 把扁平部门列表组装成树。
func BuildDeptTree(list []model.Dept) []*model.Dept {
	nodes := make(map[int64]*model.Dept, len(list))
	for i := range list {
		d := list[i]
		d.Children = nil
		nodes[d.ID] = &d
	}
	roots := make([]*model.Dept, 0)
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

func (s *Service) ListDeptTree(ctx context.Context) ([]*model.Dept, error) {
	list, err := s.repo.ListDepts(ctx, nil)
	if err != nil {
		return nil, err
	}
	return BuildDeptTree(list), nil
}

func (s *Service) GetDept(ctx context.Context, id int64) (*model.Dept, error) {
	dept, err := s.repo.FindDeptByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if dept == nil {
		return nil, xerr.NotFound("部门不存在")
	}
	return dept, nil
}

func (s *Service) CreateDept(ctx context.Context, req *dto.DeptCreateReq) error {
	if err := s.validateDeptParent(ctx, req.ParentID, 0); err != nil {
		return err
	}
	dept := &model.Dept{
		TenantID:     s.TenantID(ctx),
		Name:         req.Name,
		ParentID:     req.ParentID,
		Sort:         req.Sort,
		LeaderUserID: req.LeaderUserID,
		Phone:        req.Phone,
		Email:        req.Email,
		Status:       req.Status,
	}
	return s.repo.CreateDept(ctx, dept)
}

func (s *Service) UpdateDept(ctx context.Context, req *dto.DeptUpdateReq) error {
	dept, err := s.repo.FindDeptByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if dept == nil {
		return xerr.NotFound("部门不存在")
	}
	if req.ParentID == req.ID {
		return xerr.BadRequest("上级部门不能是自己")
	}
	if err := s.validateDeptParent(ctx, req.ParentID, req.ID); err != nil {
		return err
	}
	dept.Name = req.Name
	dept.ParentID = req.ParentID
	dept.Sort = req.Sort
	dept.LeaderUserID = req.LeaderUserID
	dept.Phone = req.Phone
	dept.Email = req.Email
	dept.Status = req.Status
	return s.repo.SaveDept(ctx, dept)
}

func (s *Service) DeleteDept(ctx context.Context, id int64) error {
	dept, err := s.repo.FindDeptByID(ctx, id)
	if err != nil {
		return err
	}
	if dept == nil {
		return xerr.NotFound("部门不存在")
	}
	children, err := s.repo.CountDeptChildren(ctx, id)
	if err != nil {
		return err
	}
	if children > 0 {
		return xerr.Business(1401, "存在子部门，无法删除")
	}
	users, err := s.repo.CountUsersByDeptID(ctx, id)
	if err != nil {
		return err
	}
	if users > 0 {
		return xerr.Business(1402, "部门下存在用户，无法删除")
	}
	return s.repo.DeleteDept(ctx, id)
}

func (s *Service) validateDeptParent(ctx context.Context, parentID, selfID int64) error {
	if parentID == 0 {
		return nil
	}
	all, err := s.repo.ListDepts(ctx, nil)
	if err != nil {
		return err
	}
	parents := make(map[int64]int64, len(all))
	for _, d := range all {
		parents[d.ID] = d.ParentID
	}
	if _, ok := parents[parentID]; !ok {
		return xerr.BadRequest("上级部门不存在")
	}
	if selfID == 0 {
		return nil
	}
	for cur, guard := parentID, 0; cur != 0 && guard < 1000; guard++ {
		if cur == selfID {
			return xerr.BadRequest("上级部门不能是自己的子部门")
		}
		cur = parents[cur]
	}
	return nil
}
