package service

import (
	"context"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）----------
// ListProducts 分页查询产品。
func (s *Service) ListProducts(ctx context.Context, req *dto.ProductPageReq) ([]model.Product, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}

	filter := repository.ProductFilter{
		Name:   req.Name,
		Code:   req.Code,
		Unit:   req.Unit,
		Status: req.Status,
	}

	list, total, err := s.repo.ListProducts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetProduct 查询产品详情。
func (s *Service) GetProduct(ctx context.Context, id int64) (*model.Product, error) {
	row, err := s.findVisibleProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	return row, nil
}

// CreateProduct 创建产品。
func (s *Service) CreateProduct(ctx context.Context, req *dto.ProductCreateReq) (*model.Product, error) {
	Status := int8(1)
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return nil, err
		}
		Status = *req.Status
	}

	row := &model.Product{
		CategoryId:  req.CategoryId,
		Name:        req.Name,
		Code:        req.Code,
		Unit:        req.Unit,
		Price:       req.Price,
		Status:      Status,
		Description: req.Description,
	}
	if err := s.repo.CreateProduct(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateProduct 修改产品：数据范围外一律 404；字段全量覆盖。
func (s *Service) UpdateProduct(ctx context.Context, req *dto.ProductUpdateReq) error {
	row, err := s.findVisibleProduct(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return err
		}
		row.Status = *req.Status
	}
	row.CategoryId = req.CategoryId
	row.Name = req.Name
	row.Code = req.Code
	row.Unit = req.Unit
	row.Price = req.Price
	row.Description = req.Description
	return s.repo.SaveProduct(ctx, row)
}

// DeleteProduct 软删除产品。
func (s *Service) DeleteProduct(ctx context.Context, id int64) error {
	row, err := s.findVisibleProduct(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteProduct(ctx, row.ID)
}

// findVisibleProduct 取产品并做可见性校验：不存在都返回 404。
func (s *Service) findVisibleProduct(ctx context.Context, id int64) (*model.Product, error) {
	row, err := s.repo.FindProductByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("产品不存在")
	}
	return row, nil
}

// TODO(#20): Excel 导入导出接线（internal/pkg/excel，docs/crm-mvp-spec.md §7）——
// 本文件生成时仅预留配置位与注释，不产出可用实现：
//   - 导入：模板下载 + 先全量校验（行号+原因可读），有错整批拒绝；上限 5000 行/10MB；
//   - 导出：复用 ListProducts 的筛选与数据范围谓词，导出内容与权限一致。
