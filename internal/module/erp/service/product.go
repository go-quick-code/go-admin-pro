// product.go 是产品的业务方法（issue #88，spec §3.1/§4.9/§4.10）：
// 分页 CRUD + 分类/单位存在性校验 + owner 数据范围过滤；删除跑引用扩展点
// （product_refs.go，单据域引用类型 #90–#94 注册）；Excel 导出见 product_excel.go。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListProducts 分页查询产品（筛选 + owner 数据范围）。
func (s *Service) ListProducts(ctx context.Context, req *dto.ProductPageReq) ([]model.Product, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.productFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListProducts(ctx, filter, page.Offset(), page.PageSize)
}

// productFilter 构造产品列表/导出的筛选与数据范围谓词（导出复用同一口径，
// 保证导出内容与当前用户可见范围一致）。
func (s *Service) productFilter(ctx context.Context, req *dto.ProductPageReq) (repository.ProductFilter, error) {
	if req.Status != nil && !model.ValidStatus(*req.Status) {
		return repository.ProductFilter{}, xerr.BadRequest("产品状态只支持 0 启用 / 1 停用")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.ProductFilter{}, err
	}
	filter := repository.ProductFilter{
		Name:     req.Name,
		BarCode:  req.BarCode,
		Status:   req.Status,
		Category: req.CategoryId,
		Unit:     req.UnitId,
	}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetProduct 查询产品详情（不存在 404）。
func (s *Service) GetProduct(ctx context.Context, id int64) (*model.Product, error) {
	return s.findProduct(ctx, id)
}

// CreateProduct 创建产品：分类/单位必须存在（400），状态枚举校验；
// owner = 创建人（spec §4.10），部门快照随 owner 写入。
func (s *Service) CreateProduct(ctx context.Context, req *dto.ProductCreateReq) (*model.Product, error) {
	status := model.StatusEnable
	if req.Status != nil {
		if !model.ValidStatus(*req.Status) {
			return nil, xerr.BadRequest("产品状态只支持 0 启用 / 1 停用")
		}
		status = *req.Status
	}
	if err := s.validateProductCategory(ctx, req.CategoryId); err != nil {
		return nil, err
	}
	if err := s.validateProductUnit(ctx, req.UnitId); err != nil {
		return nil, err
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	row := &model.Product{
		Name:          req.Name,
		BarCode:       req.BarCode,
		CategoryId:    req.CategoryId,
		UnitId:        req.UnitId,
		Status:        status,
		Standard:      req.Standard,
		ExpiryDay:     req.ExpiryDay,
		Weight:        req.Weight,
		PurchasePrice: req.PurchasePrice,
		SalePrice:     req.SalePrice,
		MinPrice:      req.MinPrice,
		Remark:        req.Remark,
		OwnerUserID:   ownerID,
		OwnerDeptID:   deptID,
	}
	if err := s.repo.CreateProduct(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateProduct 修改产品：分类/单位存在性与状态校验同创建；字段全量覆盖
// （可空展示字段未提交即清空，与表单整体提交契约一致）；owner 不随更新变化。
func (s *Service) UpdateProduct(ctx context.Context, req *dto.ProductUpdateReq) error {
	row, err := s.findProduct(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if !model.ValidStatus(*req.Status) {
			return xerr.BadRequest("产品状态只支持 0 启用 / 1 停用")
		}
		row.Status = *req.Status
	}
	if err := s.validateProductCategory(ctx, req.CategoryId); err != nil {
		return err
	}
	if err := s.validateProductUnit(ctx, req.UnitId); err != nil {
		return err
	}
	row.Name = req.Name
	row.BarCode = req.BarCode
	row.CategoryId = req.CategoryId
	row.UnitId = req.UnitId
	row.Standard = req.Standard
	row.ExpiryDay = req.ExpiryDay
	row.Weight = req.Weight
	row.PurchasePrice = req.PurchasePrice
	row.SalePrice = req.SalePrice
	row.MinPrice = req.MinPrice
	row.Remark = req.Remark
	return s.repo.SaveProduct(ctx, row)
}

// DeleteProduct 软删除产品；删除前跑引用校验扩展点（product_refs.go），
// 本票无单据域引用（空实现放行），#90–#94 注册后自动生效。
func (s *Service) DeleteProduct(ctx context.Context, id int64) error {
	row, err := s.findProduct(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkProductRefs(ctx, row.ID); err != nil {
		return err
	}
	return s.repo.DeleteProduct(ctx, row.ID)
}

// findProduct 取产品并做可见性校验：不存在/数据范围外一律 404（不泄露存在性，spec §4.10）。
func (s *Service) findProduct(ctx context.Context, id int64) (*model.Product, error) {
	row, err := s.repo.FindProductByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("产品不存在")
	}
	if err := s.checkRowVisible(ctx, "产品", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}

// validateProductUnit 校验产品挂的单位存在（不存在 400；与 validateProductCategory 同构）。
func (s *Service) validateProductUnit(ctx context.Context, unitID int64) error {
	if unitID <= 0 {
		return xerr.BadRequest("产品单位不能为空")
	}
	row, err := s.repo.FindProductUnitByID(ctx, unitID)
	if err != nil {
		return err
	}
	if row == nil {
		return xerr.BadRequest("产品单位不存在")
	}
	return nil
}
