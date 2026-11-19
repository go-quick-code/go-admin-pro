package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// product_spu.go 是商品 SPU/SKU 的管理端业务（issue #177，
// docs/mall-mvp-spec.md §3.2/§4）：
//   - 分页（分类/品牌/状态/关键词过滤）与详情（含 SKU 列表，主 SKU 恒首位）；
//   - 创建：category 必须存在（404）且为叶子分类（400）、brand 选填（非 0
//     必须存在，404）、name 租户内唯一（撞键 409）、SKU 结构纯函数校验
//    （单规格恰一空 properties SKU / 多规格至少一 SKU）、规格引用的属性值
//     必须存在且归属对应属性（400，repo 查询）——SKU 名称快照由服务端反查
//     写入（客户端只收 ID，名称不采纳）；SPU+SKU 同事务落库。
//   - 更新：字段全量覆盖 + SKU 集全量替换（旧 SKU 软删 + 新 SKU 落库，同
//     事务）；上下架独立端点（PUT :id/status，0 下架 / 1 上架）。
//   - 删除：守卫「被订单引用禁删」（repo.CountOrdersBySPU，T6 trade_order
//     落地前恒 0——照 T3 countSPUsByCategory 预留谓词模式）+ 事务内级联软删
//     全部 SKU（SKU 无独立删除端点，不可孤儿）。
//
// 字段级/结构级校验纯函数见 product_spu_rules.go（外部测试包测试）。

// ---------- 商品 SPU ----------

// PageProductSPUs 分页查询 SPU（分类/品牌/状态/关键词过滤，spec §4）。
func (s *Service) PageProductSPUs(ctx context.Context, req *dto.SPUPageReq) ([]model.ProductSPU, int64, error) {
	if req.Status != nil {
		if err := ValidateSPUStatus(*req.Status); err != nil {
			return nil, 0, err
		}
	}
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	return s.repo.ListSPUs(ctx, mallrepo.SPUFilter{
		Name:       trimString(req.Name),
		CategoryID: req.CategoryID,
		BrandID:    req.BrandID,
		Status:     req.Status,
	}, page)
}

// GetProductSPU SPU 详情（含 SKU 列表：first DESC, id ASC——主 SKU 恒首位）。
// 不存在/跨租户统一 404，不泄露存在性。
func (s *Service) GetProductSPU(ctx context.Context, id int64) (*model.ProductSPU, error) {
	row, err := s.findSPU(ctx, id)
	if err != nil {
		return nil, err
	}
	skus, err := s.repo.ListSKUsBySPU(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	row.Skus = skus
	return row, nil
}

// CreateProductSPU 创建 SPU：校验归属（分类叶子 400 / 分类品牌存在 404）、
// 名称唯一（撞键 409）、SKU 结构（纯函数）与规格引用（属性值存在且归属，
// 400）；SPU+SKU 同事务落库，事务内自动回填 SPU ID。
func (s *Service) CreateProductSPU(ctx context.Context, req *dto.SPUSaveReq) (*model.ProductSPU, error) {
	normalized, err := s.normalizeSPUReq(ctx, req, 0)
	if err != nil {
		return nil, err
	}
	skus, err := s.buildSKUs(ctx, req.SpecType, req.Skus, 0)
	if err != nil {
		return nil, err
	}
	row := &model.ProductSPU{
		CategoryID:    normalized.categoryID,
		BrandID:       normalized.brandID,
		Name:          normalized.name,
		PicURL:        req.PicURL,
		SliderPicURLs: normalized.sliderPicURLs,
		Detail:        req.Detail,
		SpecType:      normalized.specType,
		MarketPrice:   normalized.marketPrice,
		Status:        normalized.status,
		Sort:          req.Sort,
	}
	err = s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if err := tx.CreateSPU(ctx, row); err != nil {
			return err
		}
		for i := range skus {
			skus[i].SPUID = row.ID
			skus[i].TenantID = row.TenantID
		}
		return tx.CreateSKUs(ctx, skus)
	})
	if err != nil {
		if mallrepo.IsDuplicateEntry(err) {
			return nil, xerr.Conflict("商品名称已存在：" + normalized.name)
		}
		return nil, err
	}
	return row, nil
}

// UpdateProductSPU 修改 SPU（id 在 body，与 CRM/ProTable 一致）：字段全量
// 覆盖 + SKU 集全量替换（同事务：旧 SKU 软删 → 新 SKU 批量落库）。名称
// 唯一键兜底并发冲突；规格引用重新校验（全量替换语义下旧引用不豁免）。
func (s *Service) UpdateProductSPU(ctx context.Context, req *dto.SPUUpdateReq) error {
	row, err := s.findSPU(ctx, req.ID)
	if err != nil {
		return err
	}
	normalized, err := s.normalizeSPUReq(ctx, &req.SPUSaveReq, row.ID)
	if err != nil {
		return err
	}
	skus, err := s.buildSKUs(ctx, req.SpecType, req.Skus, 0)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"category_id":     normalized.categoryID,
		"brand_id":        normalized.brandID,
		"name":            normalized.name,
		"pic_url":         req.PicURL,
		"slider_pic_urls": []byte(normalized.sliderPicURLs),
		"detail":          req.Detail,
		"spec_type":       normalized.specType,
		"market_price":    normalized.marketPrice,
		"sort":            req.Sort,
	}
	if req.Status != nil {
		fields["status"] = normalized.status
	}
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if err := tx.UpdateSPUFields(ctx, row.ID, fields); err != nil {
			return err
		}
		if err := tx.DeleteSKUsBySPU(ctx, row.ID); err != nil {
			return err
		}
		for i := range skus {
			skus[i].SPUID = row.ID
			skus[i].TenantID = row.TenantID
		}
		return tx.CreateSKUs(ctx, skus)
	})
}

// UpdateProductSPUStatus 上下架（PUT :id/status）：0 下架 / 1 上架，双向
// 可逆（上下架是售卖边界不是状态机——无迁移约束，spec §3.4 状态机只锁订单）。
func (s *Service) UpdateProductSPUStatus(ctx context.Context, id int64, status int8) error {
	row, err := s.findSPU(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidateSPUStatus(status); err != nil {
		return err
	}
	return s.repo.UpdateSPUFields(ctx, row.ID, map[string]any{"status": status})
}

// DeleteProductSPU 删除 SPU（软删）：守卫「被订单引用禁删」（409，spec §4）
// ——订单引用计数走 repo.CountOrdersBySPU（trade_order 随 T6 建表，本票恒
// 返 0，谓词调用点与纯函数先落位，T6 只换查询实现）；可删时同事务级联软删
// 全部 SKU（SKU 只经 SPU 创建/替换产生，SPU 删除后不可残留未删 SKU）。
func (s *Service) DeleteProductSPU(ctx context.Context, id int64) error {
	row, err := s.findSPU(ctx, id)
	if err != nil {
		return err
	}
	orderCount, err := s.repo.CountOrdersBySPU(ctx, row.ID)
	if err != nil {
		return err
	}
	if err := ValidateSPUDelete(SPUDeleteGuard{OrderCount: orderCount}); err != nil {
		return err
	}
	return s.repo.Transaction(ctx, func(tx *mallrepo.Repository) error {
		if err := tx.DeleteSKUsBySPU(ctx, row.ID); err != nil {
			return err
		}
		return tx.DeleteSPU(ctx, row.ID)
	})
}

// findSPU 取 SPU 并做存在性校验（不存在/跨租户统一 404，不泄露存在性）。
func (s *Service) findSPU(ctx context.Context, id int64) (*model.ProductSPU, error) {
	row, err := s.repo.FindSPUByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, xerr.NotFound("商品不存在")
	}
	return row, nil
}

// spuNormalized 是 SPU 保存请求的归一化结果（校验后的可信字段集）。
type spuNormalized struct {
	categoryID    int64
	brandID       int64
	name          string
	sliderPicURLs json.RawMessage
	specType      int8
	marketPrice   money.Money
	status        int8
}

// normalizeSPUReq 校验并归一化 SPU 保存请求公共字段：名称（trim + 长度）、
// 上下架状态与规格类型枚举、市场价格非负（Money）、轮播图 JSON 化（空集
// 落 []）；分类必须存在且为叶子分类、品牌非 0 必须存在（引用校验带 IO，
// 故在 service 层而非纯函数）。
func (s *Service) normalizeSPUReq(ctx context.Context, req *dto.SPUSaveReq, selfID int64) (*spuNormalized, error) {
	_ = selfID // 预留：名称唯一由 DB 唯一键兜底（撞键 409），无自指语义要排除
	name := trimString(req.Name)
	if err := ValidateSPUName(name); err != nil {
		return nil, err
	}
	specType := req.SpecType
	if err := ValidateSPUSpecType(specType); err != nil {
		return nil, err
	}
	status := model.ProductSPUStatusOff
	if req.Status != nil {
		if err := ValidateSPUStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}
	marketPrice := money.FromCents(0)
	if req.MarketPrice != nil {
		marketPrice = *req.MarketPrice
		if err := ValidateNonNegativeMoney("市场价格", marketPrice); err != nil {
			return nil, err
		}
	}
	category, err := s.validateSPUCategory(ctx, req.CategoryID)
	if err != nil {
		return nil, err
	}
	brandID := req.BrandID
	if brandID != 0 {
		if _, err := s.findBrand(ctx, brandID); err != nil {
			return nil, err
		}
	}
	slider, err := marshalStringSlice(req.SliderPicURLs)
	if err != nil {
		return nil, xerr.BadRequest("轮播图 URL 序列化失败：" + err.Error())
	}
	return &spuNormalized{
		categoryID:    category.ID,
		brandID:       brandID,
		name:          name,
		sliderPicURLs: slider,
		specType:      specType,
		marketPrice:   marketPrice,
		status:        status,
	}, nil
}

// validateSPUCategory 校验 SPU 归属分类：必须存在（404，跨租户不可见）且为
// 叶子分类（400，spec §3.2「叶子才可挂 SPU」——两级树下有子级的根分类不可
// 挂商品；子分类恒为叶子）。
func (s *Service) validateSPUCategory(ctx context.Context, categoryID int64) (*model.ProductCategory, error) {
	row, err := s.findCategory(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	children, err := s.repo.CountCategoryChildren(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	if children > 0 {
		return nil, xerr.BadRequest(fmt.Sprintf("分类 %q 存在子分类：商品只能挂叶子分类（spec §3.2）", row.Name))
	}
	return row, nil
}

// skuPropertySnapshot 是 SKU properties JSON 快照的元素形状（spec §3.2：
// [{propertyId,propertyName,valueId,valueName}]）。ID 引用主数据，名称由
// 服务端落库时反查写入（快照口径：主数据改名/软删不影响已建 SKU 与 T6
// 订单成交快照的展示）。
type skuPropertySnapshot struct {
	PropertyID   int64  `json:"propertyId"`
	PropertyName string `json:"propertyName"`
	ValueID      int64  `json:"valueId"`
	ValueName    string `json:"valueName"`
}

// buildSKUs 校验并构建 SKU 实体集：
//  1. 拍平 dto → 纯函数结构校验（单/多规格 SKU 数量、规格项、主 SKU 至多一个）；
//  2. 逐规格项校验引用：属性值必须存在（400，跨租户/软删不可见）、必须归属
//     所声明的属性（400）、属性名存在（400）——名称快照反查带缓存；
//  3. 金额非负 + 库存非负（纯函数）；主 SKU 缺省取首条（前端不选也能落库）。
//
// spuID 传 0 表示创建场景（SPU ID 落库后由调用方回填）。
func (s *Service) buildSKUs(ctx context.Context, specType int8, reqs []dto.SPUSkuReq, spuID int64) ([]model.ProductSKU, error) {
	inputs := make([]SkuRuleInput, 0, len(reqs))
	for _, req := range reqs {
		refs := make([]PropertyValueRef, 0, len(req.Properties))
		for _, p := range req.Properties {
			refs = append(refs, PropertyValueRef{PropertyID: p.PropertyID, ValueID: p.ValueID})
		}
		marketPrice := money.FromCents(0)
		if req.MarketPrice != nil {
			marketPrice = *req.MarketPrice
		}
		inputs = append(inputs, SkuRuleInput{
			Properties:  refs,
			Price:       req.Price,
			MarketPrice: marketPrice,
			Stock:       req.Stock,
			First:       req.First,
		})
	}
	if err := ValidateSPUSKUs(specType, inputs); err != nil {
		return nil, err
	}
	// 名称快照反查缓存：同一属性值在同一 SPU 的多条 SKU 中重复出现只查一次。
	propNameCache := make(map[int64]string)
	valueCache := make(map[int64]model.ProductPropertyValue)
	skus := make([]model.ProductSKU, 0, len(reqs))
	// 主 SKU 兜底：多规格全未显式指定 first 时取首条（展示口径稳定）；
	// 单规格唯一 SKU 恒为主 SKU。
	hasExplicitFirst := false
	for _, req := range reqs {
		if req.First {
			hasExplicitFirst = true
			break
		}
	}
	for i, req := range reqs {
		snapshots := make([]skuPropertySnapshot, 0, len(req.Properties))
		for _, p := range req.Properties {
			value, err := s.lookupPropertyValue(ctx, p.PropertyID, p.ValueID, propNameCache, valueCache)
			if err != nil {
				return nil, err
			}
			snapshots = append(snapshots, skuPropertySnapshot{
				PropertyID:   p.PropertyID,
				PropertyName: propNameCache[p.PropertyID],
				ValueID:      p.ValueID,
				ValueName:    value.Name,
			})
		}
		properties, err := json.Marshal(snapshots)
		if err != nil {
			return nil, fmt.Errorf("序列化 SKU 规格快照: %w", err)
		}
		marketPrice := money.FromCents(0)
		if req.MarketPrice != nil {
			marketPrice = *req.MarketPrice
		}
		if err := ValidateNonNegativeMoney("SKU 售价", req.Price); err != nil {
			return nil, err
		}
		if err := ValidateNonNegativeMoney("SKU 划线价", marketPrice); err != nil {
			return nil, err
		}
		if err := ValidateSKUStock(req.Stock); err != nil {
			return nil, fmt.Errorf("第 %d 条 SKU：%w", i+1, err)
		}
		first := int8(0)
		switch {
		case req.First:
			first = 1
		case specType == model.ProductSpecTypeSingle:
			// 单规格唯一 SKU 恒为主 SKU。
			first = 1
		case !hasExplicitFirst && i == 0:
			// 多规格全未指定主 SKU：首条兜底。
			first = 1
		}
		skus = append(skus, model.ProductSKU{
			SPUID:       spuID,
			Properties:  json.RawMessage(properties),
			Price:       req.Price,
			MarketPrice: marketPrice,
			PicURL:      req.PicURL,
			Stock:       req.Stock,
			First:       first,
		})
	}
	return skus, nil
}

// lookupPropertyValue 校验并缓存规格引用的属性值与属性名：属性值必须存在
// （400：SKU 规格引用是请求体结构的一部分，引用失效即非法输入——照 T3
// 分类父级不存在的 400 口径；租户插件隔离下跨租户/软删 ID 同样落到此处，
// 不泄露存在性）、必须归属所声明的属性（400）、归属属性名必须存在（400）。
func (s *Service) lookupPropertyValue(
	ctx context.Context,
	propertyID, valueID int64,
	propNameCache map[int64]string,
	valueCache map[int64]model.ProductPropertyValue,
) (model.ProductPropertyValue, error) {
	if value, ok := valueCache[valueID]; ok {
		if value.PropertyID != propertyID {
			return model.ProductPropertyValue{}, xerr.BadRequest(
				fmt.Sprintf("SKU 规格引用不归属：属性值 %d 属于属性 %d，声明为 %d", valueID, value.PropertyID, propertyID))
		}
		return value, nil
	}
	row, err := s.repo.FindPropertyValueByID(ctx, valueID)
	if err != nil {
		return model.ProductPropertyValue{}, err
	}
	if row == nil {
		return model.ProductPropertyValue{}, xerr.BadRequest(
			fmt.Sprintf("SKU 规格引用的属性值不存在（valueId=%d）", valueID))
	}
	if row.PropertyID != propertyID {
		return model.ProductPropertyValue{}, xerr.BadRequest(
			fmt.Sprintf("SKU 规格引用不归属：属性值 %q 属于属性 %d，声明为 %d", row.Name, row.PropertyID, propertyID))
	}
	if _, ok := propNameCache[row.PropertyID]; !ok {
		property, err := s.findProperty(ctx, row.PropertyID)
		if err != nil {
			return model.ProductPropertyValue{}, err
		}
		propNameCache[row.PropertyID] = property.Name
	}
	valueCache[valueID] = *row
	return *row, nil
}

// marshalStringSlice 把字符串数组序列化为 JSON（空集落 []，满足 JSON 列
// NOT NULL 约束）。
func marshalStringSlice(items []string) (json.RawMessage, error) {
	if len(items) == 0 {
		return json.RawMessage("[]"), nil
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
