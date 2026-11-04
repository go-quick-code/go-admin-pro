package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/system/dto"
	"github.com/lautung/go-admin-pro/internal/module/system/model"
	"github.com/lautung/go-admin-pro/internal/module/system/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

func (s *Service) ListPosts(ctx context.Context, req *dto.PostPageReq) ([]model.Post, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter := repository.PostFilter{Name: req.Name, Code: req.Code, Status: req.Status}
	return s.repo.ListPosts(ctx, filter, page.Offset(), page.PageSize)
}

func (s *Service) ListSimplePosts(ctx context.Context) ([]model.Post, error) {
	return s.repo.ListSimplePosts(ctx)
}

func (s *Service) CreatePost(ctx context.Context, req *dto.PostCreateReq) error {
	total, err := s.repo.CountPostByCode(ctx, req.Code, 0)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("岗位编码已存在")
	}
	post := &model.Post{
		TenantID: s.TenantID(ctx),
		Name:     req.Name,
		Code:     req.Code,
		Sort:     req.Sort,
		Status:   req.Status,
		Remark:   req.Remark,
	}
	return s.repo.CreatePost(ctx, post)
}

func (s *Service) UpdatePost(ctx context.Context, req *dto.PostUpdateReq) error {
	post, err := s.repo.FindPostByID(ctx, req.ID)
	if err != nil {
		return err
	}
	if post == nil {
		return xerr.NotFound("岗位不存在")
	}
	total, err := s.repo.CountPostByCode(ctx, req.Code, req.ID)
	if err != nil {
		return err
	}
	if total > 0 {
		return xerr.Conflict("岗位编码已存在")
	}
	post.Name = req.Name
	post.Code = req.Code
	post.Sort = req.Sort
	post.Status = req.Status
	post.Remark = req.Remark
	return s.repo.SavePost(ctx, post)
}

func (s *Service) DeletePost(ctx context.Context, id int64) error {
	post, err := s.repo.FindPostByID(ctx, id)
	if err != nil {
		return err
	}
	if post == nil {
		return xerr.NotFound("岗位不存在")
	}
	users, err := s.repo.CountPostUsers(ctx, id)
	if err != nil {
		return err
	}
	if users > 0 {
		return xerr.Business(1501, "岗位已分配给用户，无法删除")
	}
	return s.repo.DeletePost(ctx, id)
}
