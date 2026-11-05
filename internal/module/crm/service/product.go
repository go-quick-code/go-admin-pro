package service

import (
	"context"
	"fmt"

	"github.com/lautung/go-admin-pro/internal/module/crm/dto"
	"github.com/lautung/go-admin-pro/internal/module/crm/model"
	"github.com/lautung/go-admin-pro/internal/module/crm/repository"
	"github.com/lautung/go-admin-pro/internal/pkg/web"
	"github.com/lautung/go-admin-pro/internal/pkg/xerr"
)

// ---------- 产品（本文件由 codegen 生成（issue #10 样板），接入步骤见生成包 INTEGRATION.md）----------
// 接入补丁（issue #11，「生成物是起点」约定的业务联动，见 INTEGRATION.md 限制一节）：
//   - 产品编码租户内唯一：service 预检（预检看不到软删行，落库撞 uk_crm_product_tenant_code
//     时由 repository.IsDuplicateEntry 兜底翻译 400，软删行不释放编码）；
//   - 分类必须存在（categoryId → crm_product_category，400）；
//   - 删除走 ProductRefCountChecker 扩展点（product_refs.go，#15/#16 注册实现）；
//   - 列表按分类筛选（树选择器联动，codegen 不生成 int64 筛选字段）。

// ListProducts 分页查询产品。
func (s *Service) ListProducts(ctx context.Context, req *dto.ProductPageReq) ([]model.Product, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	filter, err := productFilter(req)
	if err != nil {
		return nil, 0, err
	}

	list, total, err := s.repo.ListProducts(ctx, filter, page.Offset(), page.PageSize)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// productFilter 构造产品列表/导出的筛选条件（导出复用同一口径）。
func productFilter(req *dto.ProductPageReq) (repository.ProductFilter, error) {
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return repository.ProductFilter{}, err
		}
	}
	return repository.ProductFilter{
		Name:       req.Name,
		Code:       req.Code,
		Unit:       req.Unit,
		Status:     req.Status,
		CategoryID: req.CategoryId,
	}, nil
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
	status := int8(1)
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}
	if err := s.validateProductCategory(ctx, req.CategoryId); err != nil {
		return nil, err
	}
	if err := s.checkProductCodeFree(ctx, req.Code, 0); err != nil {
		return nil, err
	}

	row := &model.Product{
		CategoryId:  req.CategoryId,
		Name:        req.Name,
		Code:        req.Code,
		Unit:        req.Unit,
		Price:       req.Price,
		Status:      status,
		Description: req.Description,
	}
	if err := s.repo.CreateProduct(ctx, row); err != nil {
		// 唯一键兜底：预检与落库之间的并发写入或软删行占码在此拦截。
		if repository.IsDuplicateEntry(err) {
			return nil, xerr.BadRequest("产品编码已存在")
		}
		return nil, err
	}
	return row, nil
}

// UpdateProduct 修改产品：只写请求列（PR #39 审查 P1）。status 仅在请求携带时
// 写入（PUT /product/status 是独立入口），不会把读到的旧状态回写覆盖并发下架/上架。
func (s *Service) UpdateProduct(ctx context.Context, req *dto.ProductUpdateReq) error {
	row, err := s.findVisibleProduct(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.Status != nil {
		if err := ValidateProductStatus(*req.Status); err != nil {
			return err
		}
	}
	if err := s.validateProductCategory(ctx, req.CategoryId); err != nil {
		return err
	}
	if err := s.checkProductCodeFree(ctx, req.Code, req.ID); err != nil {
		return err
	}
	fields := map[string]any{
		"category_id": req.CategoryId,
		"name":        req.Name,
		"code":        req.Code,
		"unit":        req.Unit,
		"price":       req.Price,
		"description": req.Description,
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}
	if _, err := s.repo.UpdateProductFields(ctx, row.ID, fields); err != nil {
		if repository.IsDuplicateEntry(err) {
			return xerr.BadRequest("产品编码已存在")
		}
		return err
	}
	return nil
}

// UpdateProductStatus 修改产品状态（spec §5 PUT /product/status）：只改 status，
// 产品必须存在（404），枚举非法 400（0 下架 / 1 上架）。
func (s *Service) UpdateProductStatus(ctx context.Context, req *dto.ProductStatusReq) error {
	if req.Status == nil {
		return xerr.BadRequest("产品状态不能为空：期望 0 下架 / 1 上架")
	}
	if err := ValidateProductStatus(*req.Status); err != nil {
		return err
	}
	row, err := s.findVisibleProduct(ctx, req.ID)
	if err != nil {
		return err
	}
	return s.repo.UpdateProductStatus(ctx, row.ID, *req.Status)
}

// DeleteProduct 软删除产品；删除前跑引用校验扩展点（product_refs.go）。
func (s *Service) DeleteProduct(ctx context.Context, id int64) error {
	row, err := s.findVisibleProduct(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkProductRefs(ctx, row.ID); err != nil {
		return err
	}
	return s.repo.DeleteProduct(ctx, row.ID)
}

// checkProductCodeFree 校验产品编码在当前租户内未被占用（selfID 非 0 时排除自身，
// 即「保持自身编码不变」放行）。预检只看未删除行：软删行占用由唯一键兜底。
func (s *Service) checkProductCodeFree(ctx context.Context, code string, selfID int64) error {
	if code == "" {
		return nil
	}
	row, err := s.repo.FindProductByCode(ctx, code)
	if err != nil {
		return err
	}
	if row != nil && row.ID != selfID {
		return xerr.BadRequest(fmt.Sprintf("产品编码 %q 已存在", code))
	}
	return nil
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

// Excel 导入导出见 product_excel.go（issue #20）：模板下载、全量校验后整批拒绝、
// 事务内批量创建；导出复用 productFilter 的筛选条件。
