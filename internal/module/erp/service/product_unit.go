// product_unit.go 是产品单位的业务方法（issue #88，spec §3.1/§4.9/§4.10）：
// 单位是仅名称字典（无换算率），分页 CRUD + owner 数据范围过滤；
// 「被产品引用禁删」（spec §4.9）随产品域切片接线（checkProductUnitRefs）。
package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/model"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// ListProductUnits 分页查询单位（筛选 + owner 数据范围）。
func (s *Service) ListProductUnits(ctx context.Context, req *dto.ProductUnitPageReq) ([]model.ProductUnit, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := s.productUnitFilter(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListProductUnits(ctx, filter, page.Offset(), page.PageSize)
}

// productUnitFilter 构造单位列表/导出的筛选与数据范围谓词（导出复用同一口径）。
func (s *Service) productUnitFilter(ctx context.Context, req *dto.ProductUnitPageReq) (repository.ProductUnitFilter, error) {
	if req.Status != nil && !model.ValidStatus(*req.Status) {
		return repository.ProductUnitFilter{}, xerr.BadRequest("单位状态只支持 0 启用 / 1 停用")
	}
	scope, err := s.sys.DataScopeFilter(ctx, CurrentUserID(ctx))
	if err != nil {
		return repository.ProductUnitFilter{}, err
	}
	filter := repository.ProductUnitFilter{Name: req.Name, Status: req.Status}
	applyScope(scope, &filter.OwnerDeptIDs, &filter.SelfOwnerUserID)
	return filter, nil
}

// GetProductUnit 查询单位详情（不存在 404）。
func (s *Service) GetProductUnit(ctx context.Context, id int64) (*model.ProductUnit, error) {
	return s.findUnit(ctx, id)
}

// CreateProductUnit 创建单位。owner = 创建人（spec §4.10），部门快照随 owner 写入。
func (s *Service) CreateProductUnit(ctx context.Context, req *dto.ProductUnitCreateReq) (*model.ProductUnit, error) {
	status := model.StatusEnable
	if req.Status != nil {
		if !model.ValidStatus(*req.Status) {
			return nil, xerr.BadRequest("单位状态只支持 0 启用 / 1 停用")
		}
		status = *req.Status
	}
	ownerID := CurrentUserID(ctx)
	deptID, err := s.ownerDeptSnapshot(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	row := &model.ProductUnit{
		Name:        req.Name,
		Status:      status,
		OwnerUserID: ownerID,
		OwnerDeptID: deptID,
	}
	if err := s.repo.CreateProductUnit(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// UpdateProductUnit 修改单位：字段全量覆盖；owner 不随更新变化。
func (s *Service) UpdateProductUnit(ctx context.Context, req *dto.ProductUnitUpdateReq) error {
	row, err := s.findUnit(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if !model.ValidStatus(*req.Status) {
			return xerr.BadRequest("单位状态只支持 0 启用 / 1 停用")
		}
		row.Status = *req.Status
	}
	row.Name = req.Name
	return s.repo.SaveProductUnit(ctx, row)
}

// DeleteProductUnit 软删除单位；被产品引用禁止删（spec §4.9，
// 引用计数见 product_refs.go 的 checkProductUnitRefs）。
func (s *Service) DeleteProductUnit(ctx context.Context, id int64) error {
	row, err := s.findUnit(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkProductUnitRefs(ctx, row.ID); err != nil {
		return err
	}
	return s.repo.DeleteProductUnit(ctx, row.ID)
}

// findUnit 取单位并做可见性校验：不存在/数据范围外一律 404（不泄露存在性，spec §4.10）。
func (s *Service) findUnit(ctx context.Context, id int64) (*model.ProductUnit, error) {
	row, err := s.repo.FindProductUnitByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("产品单位不存在")
	}
	if err := s.checkRowVisible(ctx, "产品单位", row.OwnerUserID, row.OwnerDeptID); err != nil {
		return nil, err
	}
	return row, nil
}
