// product_category.go 是产品分类的业务方法（issue #88，spec §3.1/§4.9/§4.10）。
// 父分类校验的纯逻辑在 product_category_tree.go；本文件负责落库路径：
// list 全量查询、创建/更新（父校验 + owner 快照）、删除约束（有子分类 409、
// 被产品引用 409，spec §4.9；产品引用计数在 product_refs.go）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListProductCategories 查询当前租户的全部分类（前端组树契约，spec §5：
// GET list 全量返回扁平列表，不带 children），按 owner + 角色数据范围过滤。
func (s *Service) ListProductCategories(ctx context.Context) ([]model.ProductCategory, error) {
	filter, err := s.categoryScopeFilter(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListProductCategories(ctx, filter)
}

// categoryScopeFilter 构造分类列表/导出的数据范围谓词（owner = 创建人，spec §4.10；
// 列表与导出复用同一口径，保证导出内容与当前用户可见范围一致）。
func (s *Service) categoryScopeFilter(ctx context.Context) (repository.ProductCategoryFilter, error) {
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.ProductCategoryFilter{}, err
	}
	filter := repository.ProductCategoryFilter{}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetProductCategory 查询分类详情（404 与列表可见性口径一致：范围外/跨租户一律 404）。
func (s *Service) GetProductCategory(ctx context.Context, id int64) (*model.ProductCategory, error) {
	return s.findCategory(ctx, id)
}

// CreateProductCategory 创建分类（挂在 parentId 下，0 为根；父不存在/自指一律 400）。
// owner = 创建人（spec §4.10），部门快照随 owner 写入。
func (s *Service) CreateProductCategory(ctx context.Context, req *dto.ProductCategoryCreateReq) (*model.ProductCategory, error) {
	status := model.StatusEnable
	if req.Status != nil {
		if !model.ValidStatus(*req.Status) {
			return nil, xerr.BadRequest("分类状态只支持 0 启用 / 1 停用")
		}
		status = *req.Status
	}
	list, err := s.repo.ListProductCategories(ctx, repository.ProductCategoryFilter{})
	if err != nil {
		return nil, err
	}
	if err := ValidateProductCategoryParent(list, req.ParentID, 0); err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	row := &model.ProductCategory{
		ParentID:    req.ParentID,
		Name:        req.Name,
		Code:        req.Code,
		Sort:        req.Sort,
		Status:      status,
		OwnerUserID: ownerID,
		OwnerDeptID: deptID,
	}
	if err := s.repo.CreateProductCategory(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateProductCategory 修改分类：改父走环校验（父不能是自己/自己的子孙），
// 字段全量覆盖；owner 不随更新变化（owner = 创建人，ERP 无转移接口）。
func (s *Service) UpdateProductCategory(ctx context.Context, req *dto.ProductCategoryUpdateReq) error {
	row, err := s.findCategory(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if !model.ValidStatus(*req.Status) {
			return xerr.BadRequest("分类状态只支持 0 启用 / 1 停用")
		}
		row.Status = *req.Status
	}
	list, err := s.repo.ListProductCategories(ctx, repository.ProductCategoryFilter{})
	if err != nil {
		return err
	}
	if err := ValidateProductCategoryParent(list, req.ParentID, req.ID); err != nil {
		return err
	}
	row.ParentID = req.ParentID
	row.Name = req.Name
	row.Code = req.Code
	row.Sort = req.Sort
	return s.repo.SaveProductCategory(ctx, row)
}

// DeleteProductCategory 删除分类（409）：有子分类禁止删；被产品引用禁止删
// （spec §4.9，引用计数见 product_refs.go 的 checkProductCategoryRefs）。
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
		return xerr.Conflict("分类下存在子分类，无法删除")
	}
	if err := s.checkProductCategoryRefs(ctx, row.ID); err != nil {
		return err
	}
	return s.repo.DeleteProductCategory(ctx, row.ID)
}

// findCategory 取分类并做可见性校验：不存在/数据范围外一律 404
// （租户插件隔离跨租户访问；范围外不泄露存在性，spec §4.10）。
func (s *Service) findCategory(ctx context.Context, id int64) (*model.ProductCategory, error) {
	row, err := s.repo.FindProductCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("产品分类不存在")
	}
	if err := s.checkRowVisible(ctx, "产品分类", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
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
