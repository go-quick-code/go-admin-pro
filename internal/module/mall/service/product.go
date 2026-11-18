package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// product.go 是商品域基础三件的管理端业务（issue #176，
// docs/mall-mvp-spec.md §3.2/§4）：
//   - 分类（product_category）：树查询 + CRUD；两级树（父必须是根级，纯校验
//     见 product_rules.go）；同级 name 唯一（撞键 409）；删除守卫「有子级禁删」
//     +「被 SPU 引用禁删」（引用计数函数签名本票预留，随 T4 SPU 表接线）。
//   - 品牌（product_brand）：CRUD；name 租户内唯一（撞键 409）。SPU 引用
//     守卫（brand_id）随 T4 SPU 表落地接线，本票品牌删除无引用守卫。
//   - 属性名/属性值（product_property/_value）：属性名 CRUD + 属性内值 CRUD；
//     name 租户内唯一、(property_id, name) 属性内唯一（撞键 409）；删除属性名
//     同事务级联软删其属性值（值无独立意义，孤儿值无意义）。
//
// 树组装纯函数见 product_tree.go；字段级校验纯函数见 product_rules.go
//（外部测试包测试）。

// ---------- 商品分类（两级树） ----------

// TreeProductCategories 查询当前租户的分类树（根与子节点按 sort, id 升序）。
func (s *Service) TreeProductCategories(ctx context.Context) ([]*model.ProductCategory, error) {
	list, err := s.repo.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	return BuildProductCategoryTree(list), nil
}

// GetProductCategory 分类详情。不存在/跨租户统一 404，不泄露存在性。
func (s *Service) GetProductCategory(ctx context.Context, id int64) (*model.ProductCategory, error) {
	return s.findCategory(ctx, id)
}

// CreateProductCategory 创建分类：parentId 0=根；两级树父级合法性（父必须是
// 根级，400）；同级 name 唯一（撞键 409）。
func (s *Service) CreateProductCategory(ctx context.Context, req *dto.CategorySaveReq) (*model.ProductCategory, error) {
	name, status, err := validateCategoryReq(req)
	if err != nil {
		return nil, err
	}
	if err := s.validateCategoryParent(ctx, req.ParentID, 0); err != nil {
		return nil, err
	}
	row := &model.ProductCategory{
		ParentID: req.ParentID,
		Name:     name,
		Sort:     req.Sort,
		Status:   status,
	}
	if err := s.repo.CreateCategory(ctx, row); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("同级分类名称已存在：" + name)
		}
		return nil, err
	}
	return row, nil
}

// UpdateProductCategory 修改分类（id 在 body，与 CRM/ProTable 一致）：改父
// 走两级树校验（父必须是根级；挂子级的根不可降级），name/sort/status 全量
// 覆盖，同级唯一键兜底并发冲突。
func (s *Service) UpdateProductCategory(ctx context.Context, req *dto.CategoryUpdateReq) error {
	row, err := s.findCategory(ctx, req.ID)
	if err != nil {
		return err
	}
	name, status, err := validateCategoryReq(&req.CategorySaveReq)
	if err != nil {
		return err
	}
	if err := s.validateCategoryParent(ctx, req.ParentID, row.ID); err != nil {
		return err
	}
	fields := map[string]any{
		"parent_id": req.ParentID,
		"name":      name,
		"sort":      req.Sort,
	}
	if req.Status != nil {
		fields["status"] = status
	}
	if err := s.repo.UpdateCategoryFields(ctx, row.ID, fields); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return xerr.Conflict("同级分类名称已存在：" + name)
		}
		return err
	}
	return nil
}

// DeleteProductCategory 删除分类（软删）：删除守卫「有子级或被 SPU 引用
// 禁删」（409，spec §4）——子级计数实时查库，SPU 引用计数走 countSPUsByCategory
// （本票预留签名恒 0，T4 SPU 表落地后接线）。
func (s *Service) DeleteProductCategory(ctx context.Context, id int64) error {
	row, err := s.findCategory(ctx, id)
	if err != nil {
		return err
	}
	children, err := s.repo.CountCategoryChildren(ctx, row.ID)
	if err != nil {
		return err
	}
	spuCount, err := s.countSPUsByCategory(ctx, row.ID)
	if err != nil {
		return err
	}
	if err := ValidateCategoryDelete(CategoryDeleteGuard{ChildrenCount: children, SPUCount: spuCount}); err != nil {
		return err
	}
	return s.repo.DeleteCategory(ctx, row.ID)
}

// countSPUsByCategory 统计引用该分类的 SPU 数（删除守卫第二谓词的查询点）。
// SPU 表（product_spu）随 T4 迁移建表，届时替换为
// `s.repo.CountSPUsByCategory(ctx, categoryID)`（product_spu.category_id 引用
// 计数，不带 deleted_at 过滤——SPU 删除不改写分类引用语义的口径与
// member_level 的 CountMembersByLevel 一致，T4 落地时定稿）。本票恒返 0：
// SPU 不存在即无引用，「有子级禁删」分支先行收口并由本票集成测试覆盖。
func (s *Service) countSPUsByCategory(ctx context.Context, categoryID int64) (int64, error) {
	_ = ctx
	_ = categoryID
	return 0, nil
}

// findCategory 取分类并做存在性校验（不存在/跨租户统一 404，不泄露存在性）。
func (s *Service) findCategory(ctx context.Context, id int64) (*model.ProductCategory, error) {
	row, err := s.repo.FindCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("商品分类不存在")
	}
	return row, nil
}

// validateCategoryParent 用全量分类做父合法性校验（纯函数见 product_rules.go）。
func (s *Service) validateCategoryParent(ctx context.Context, parentID, selfID int64) error {
	list, err := s.repo.ListCategories(ctx)
	if err != nil {
		return err
	}
	return ValidateCategoryParent(list, parentID, selfID)
}

// validateCategoryReq 校验分类公共字段：名称必填 ≤64、状态枚举。返回归一化
// 名称与状态（Status nil 默认启用）。
func validateCategoryReq(req *dto.CategorySaveReq) (name string, status int8, err error) {
	name = trimString(req.Name)
	if err = ValidateCategoryName(name); err != nil {
		return "", 0, err
	}
	status = model.StatusEnable
	if req.Status != nil {
		if err = ValidateCategoryStatus(*req.Status); err != nil {
			return "", 0, err
		}
		status = *req.Status
	}
	return name, status, nil
}

// ---------- 商品品牌 ----------

// ListBrands 分页查询品牌。
func (s *Service) ListBrands(ctx context.Context, req *dto.BrandPageReq) ([]model.ProductBrand, int64, error) {
	if req.Status != nil {
		if err := ValidateBrandStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListBrands(ctx, mallrepo.BrandFilter{
		Name:   trimString(req.Name),
		Status: req.Status,
	}, page)
}

// GetBrand 品牌详情。不存在/跨租户统一 404，不泄露存在性。
func (s *Service) GetBrand(ctx context.Context, id int64) (*model.ProductBrand, error) {
	return s.findBrand(ctx, id)
}

// CreateBrand 创建品牌：name 租户内唯一（撞键 409）。
func (s *Service) CreateBrand(ctx context.Context, req *dto.BrandSaveReq) (*model.ProductBrand, error) {
	name, status, err := validateBrandReq(req)
	if err != nil {
		return nil, err
	}
	row := &model.ProductBrand{
		Name:   name,
		PicURL: req.PicURL,
		Status: status,
		Remark: req.Remark,
	}
	if err := s.repo.CreateBrand(ctx, row); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("品牌名称已存在：" + name)
		}
		return nil, err
	}
	return row, nil
}

// UpdateBrand 修改品牌（id 在 body）：name/pic/remark 可改，唯一键兜底并发冲突。
func (s *Service) UpdateBrand(ctx context.Context, req *dto.BrandUpdateReq) error {
	row, err := s.findBrand(ctx, req.ID)
	if err != nil {
		return err
	}
	name, status, err := validateBrandReq(&req.BrandSaveReq)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"name":    name,
		"pic_url": req.PicURL,
		"remark":  req.Remark,
	}
	if req.Status != nil {
		fields["status"] = status
	}
	if err := s.repo.UpdateBrandFields(ctx, row.ID, fields); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return xerr.Conflict("品牌名称已存在：" + name)
		}
		return err
	}
	return nil
}

// DeleteBrand 删除品牌（软删）。SPU 引用守卫（product_spu.brand_id）随 T4
// SPU 表落地接线——本票品牌删除无引用守卫（spec §3.2 未约束）。
func (s *Service) DeleteBrand(ctx context.Context, id int64) error {
	row, err := s.findBrand(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeleteBrand(ctx, row.ID)
}

// findBrand 取品牌并做存在性校验（不存在/跨租户统一 404，不泄露存在性）。
func (s *Service) findBrand(ctx context.Context, id int64) (*model.ProductBrand, error) {
	row, err := s.repo.FindBrandByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("商品品牌不存在")
	}
	return row, nil
}

// validateBrandReq 校验品牌公共字段：名称必填 ≤64、状态枚举。返回归一化
// 名称与状态（Status nil 默认启用）。
func validateBrandReq(req *dto.BrandSaveReq) (name string, status int8, err error) {
	name = trimString(req.Name)
	if err = ValidateBrandName(name); err != nil {
		return "", 0, err
	}
	status = model.StatusEnable
	if req.Status != nil {
		if err = ValidateBrandStatus(*req.Status); err != nil {
			return "", 0, err
		}
		status = *req.Status
	}
	return name, status, nil
}

// ---------- 商品属性名 ----------

// ListProperties 分页查询属性名。
func (s *Service) ListProperties(ctx context.Context, req *dto.PropertyPageReq) ([]model.ProductProperty, int64, error) {
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListProperties(ctx, mallrepo.PropertyFilter{
		Name: trimString(req.Name),
	}, page)
}

// GetProductProperty 属性名详情（不含值——值清单走 ListPropertyValues）。
// 不存在/跨租户统一 404，不泄露存在性。
func (s *Service) GetProductProperty(ctx context.Context, id int64) (*model.ProductProperty, error) {
	return s.findProperty(ctx, id)
}

// CreateProperty 创建属性名：name 租户内唯一（撞键 409）。
func (s *Service) CreateProperty(ctx context.Context, req *dto.PropertySaveReq) (*model.ProductProperty, error) {
	name := trimString(req.Name)
	if err := ValidatePropertyName(name); err != nil {
		return nil, err
	}
	row := &model.ProductProperty{Name: name}
	if err := s.repo.CreateProperty(ctx, row); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("属性名已存在：" + name)
		}
		return nil, err
	}
	return row, nil
}

// UpdateProperty 修改属性名（id 在 body）：name 可改，唯一键兜底并发冲突。
func (s *Service) UpdateProperty(ctx context.Context, req *dto.PropertyUpdateReq) error {
	row, err := s.findProperty(ctx, req.ID)
	if err != nil {
		return err
	}
	name := trimString(req.Name)
	if err := ValidatePropertyName(name); err != nil {
		return err
	}
	if err := s.repo.UpdatePropertyFields(ctx, row.ID, map[string]any{"name": name}); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return xerr.Conflict("属性名已存在：" + name)
		}
		return err
	}
	return nil
}

// DeleteProperty 删除属性名（软删）：同事务级联软删其属性值——值只依附
// 属性名存在，孤儿值无意义（SKU 引用是 JSON 成交快照，随 T4 落地后天然
// 与主数据解耦）。
func (s *Service) DeleteProperty(ctx context.Context, id int64) error {
	row, err := s.findProperty(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if err := tx.DeletePropertyValuesByProperty(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteProperty(ctx, row.ID)
	})
}

// findProperty 取属性名并做存在性校验（不存在/跨租户统一 404，不泄露存在性）。
func (s *Service) findProperty(ctx context.Context, id int64) (*model.ProductProperty, error) {
	row, err := s.repo.FindPropertyByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("商品属性不存在")
	}
	return row, nil
}

// ---------- 商品属性值 ----------

// ListPropertyValues 属性名下的全部属性值（id ASC）。属性名不存在/跨租户
// 404，不泄露存在性。
func (s *Service) ListPropertyValues(ctx context.Context, propertyID int64) ([]model.ProductPropertyValue, error) {
	if _, err := s.findProperty(ctx, propertyID); err != nil {
		return nil, err
	}
	return s.repo.ListPropertyValuesByProperty(ctx, propertyID)
}

// CreatePropertyValue 创建属性值：归属属性必须存在（404），name 属性内唯一
// （撞键 409）。
func (s *Service) CreatePropertyValue(ctx context.Context, req *dto.PropertyValueSaveReq) (*model.ProductPropertyValue, error) {
	property, err := s.findProperty(ctx, req.PropertyID)
	if err != nil {
		return nil, err
	}
	name := trimString(req.Name)
	if err := ValidatePropertyValueName(name); err != nil {
		return nil, err
	}
	row := &model.ProductPropertyValue{PropertyID: property.ID, Name: name}
	if err := s.repo.CreatePropertyValue(ctx, row); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("该属性下已存在同名属性值：" + name)
		}
		return nil, err
	}
	return row, nil
}

// UpdatePropertyValue 修改属性值（id 在 body）：只可改名（归属属性不可改
// ——改归属等价删了重建，dto 层不收 propertyId）。唯一键兜底并发冲突。
func (s *Service) UpdatePropertyValue(ctx context.Context, req *dto.PropertyValueUpdateReq) error {
	row, err := s.findPropertyValue(ctx, req.ID)
	if err != nil {
		return err
	}
	name := trimString(req.Name)
	if err := ValidatePropertyValueName(name); err != nil {
		return err
	}
	if err := s.repo.UpdatePropertyValueFields(ctx, row.ID, map[string]any{"name": name}); err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return xerr.Conflict("该属性下已存在同名属性值：" + name)
		}
		return err
	}
	return nil
}

// DeletePropertyValue 删除属性值（软删）。SKU 引用守卫（SKU properties JSON
// 引用 valueId）随 T4 SPU/SKU 表落地接线——本票属性值删除无引用守卫
// （spec §3.2 未约束；JSON 快照与主数据天然解耦）。
func (s *Service) DeletePropertyValue(ctx context.Context, id int64) error {
	row, err := s.findPropertyValue(ctx, id)
	if err != nil {
		return err
	}
	return s.repo.DeletePropertyValue(ctx, row.ID)
}

// findPropertyValue 取属性值并做存在性校验（不存在/跨租户统一 404，
// 不泄露存在性）。
func (s *Service) findPropertyValue(ctx context.Context, id int64) (*model.ProductPropertyValue, error) {
	row, err := s.repo.FindPropertyValueByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("商品属性值不存在")
	}
	return row, nil
}
