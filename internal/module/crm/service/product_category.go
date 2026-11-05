// product_category.go 是产品分类的业务方法（issue #11，手写：codegen 不生成树逻辑）。
// 树组装与父分类校验的纯逻辑在 product_category_tree.go；本文件负责落库路径：
// 树查询、创建/更新（父校验）、删除约束（有子分类 1801 / 有产品 1802 禁止删）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/crm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/crm/model"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// 分类删除约束的业务码（对齐 dept 1401/1402、tenant 17xx 的分段习惯，18xx 归产品域）。
const (
	bizCodeProductCategoryHasChildren = 1801
	bizCodeProductCategoryHasProducts = 1802
)

// TreeProductCategories 查询当前租户的分类树（根与子节点按 sort, id 升序）。
func (s *Service) TreeProductCategories(ctx context.Context) ([]*model.ProductCategory, error) {
	list, err := s.repo.ListProductCategories(ctx)
	if err != nil {
		return nil, err
	}
	return BuildProductCategoryTree(list), nil
}

// CreateProductCategory 创建分类（挂在 parentId 下，0 为根；父不存在/自指一律 400）。
func (s *Service) CreateProductCategory(ctx context.Context, req *dto.ProductCategoryCreateReq) (*model.ProductCategory, error) {
	status := model.CategoryStatusEnable
	if req.Status != nil {
		if !model.ValidProductCategoryStatus(*req.Status) {
			return nil, xerr.BadRequest("分类状态只支持 0 启用 / 1 停用")
		}
		status = *req.Status
	}
	if err := s.validateCategoryParent(ctx, req.ParentID, 0); err != nil {
		return nil, err
	}
	row := &model.ProductCategory{
		ParentID: req.ParentID,
		Name:     req.Name,
		Sort:     req.Sort,
		Status:   status,
	}
	if err := s.repo.CreateProductCategory(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateProductCategory 修改分类：改父走环校验（父不能是自己/自己的子孙），字段全量覆盖。
func (s *Service) UpdateProductCategory(ctx context.Context, req *dto.ProductCategoryUpdateReq) error {
	row, err := s.findCategory(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if !model.ValidProductCategoryStatus(*req.Status) {
			return xerr.BadRequest("分类状态只支持 0 启用 / 1 停用")
		}
		row.Status = *req.Status
	}
	if err := s.validateCategoryParent(ctx, req.ParentID, req.ID); err != nil {
		return err
	}
	row.ParentID = req.ParentID
	row.Name = req.Name
	row.Sort = req.Sort
	return s.repo.SaveProductCategory(ctx, row)
}

// DeleteProductCategory 删除分类：有子分类（1801）或有产品（1802）禁止删，先删子与产品。
func (s *Service) DeleteProductCategory(ctx context.Context, id int64) error {
	row, err := s.findCategory(ctx, id)
	if err != nil {
		return err
	}
	children, err := s.repo.CountProductCategoryChildren(ctx, row.ID)
	if err != nil {
		return err
	}
	if children > 0 {
		return xerr.Business(bizCodeProductCategoryHasChildren, "分类下存在子分类，无法删除")
	}
	products, err := s.repo.CountProductsByCategory(ctx, row.ID)
	if err != nil {
		return err
	}
	if products > 0 {
		return xerr.Business(bizCodeProductCategoryHasProducts, "分类下存在产品，无法删除")
	}
	return s.repo.DeleteProductCategory(ctx, row.ID)
}

// findCategory 取分类；不存在一律 404（租户插件隔离跨租户访问）。
func (s *Service) findCategory(ctx context.Context, id int64) (*model.ProductCategory, error) {
	row, err := s.repo.FindProductCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("产品分类不存在")
	}
	return row, nil
}

// validateCategoryParent 用全量分类做父合法性校验（纯函数见 product_category_tree.go）。
func (s *Service) validateCategoryParent(ctx context.Context, parentID, selfID int64) error {
	list, err := s.repo.ListProductCategories(ctx)
	if err != nil {
		return err
	}
	return ValidateProductCategoryParent(list, parentID, selfID)
}

// validateProductCategory 校验产品挂的分类存在（product 侧共用；不存在 400）。
func (s *Service) validateProductCategory(ctx context.Context, categoryID int64) error {
	if categoryID <= 0 {
		return xerr.BadRequest("产品分类不能为空")
	}
	row, err := s.repo.FindProductCategoryByID(ctx, categoryID)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.BadRequest("产品分类不存在")
	}
	return nil
}
