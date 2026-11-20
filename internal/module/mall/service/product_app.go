package service

import (
	"context"

	"github.com/go-quick-code/go-admin-pro/internal/module/mall/dto"
	"github.com/go-quick-code/go-admin-pro/internal/module/mall/model"
	mallrepo "github.com/go-quick-code/go-admin-pro/internal/module/mall/repository"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/xerr"
)

// product_app.go 是商品域的 C 端浏览业务（issue #178，T5；
// docs/mall-mvp-spec.md §4「上架 SPU 分页（分类/品牌过滤）、SPU 详情（含
// SKU 列表）」）。**只可见上架商品**（spec §3.2「只有上架可被购买/展示」）：
// 分页 status 恒过滤 ProductSPUStatusOn（复用管理端 SPU 分页 repo/service
// 谓词，不另写查询）；详情对下架/不存在/跨租户统一 404「商品不存在」——
// 不泄露存在性（下架商品的直链 ID 与未存在 ID 同答）。
//
// 请求上下文带会员租户（memberAuth 中间件自校验令牌后注入）：租户插件
// 隔离下，会员只看得到本租户的上架商品。

// AppPageProductSPUs C 端上架 SPU 分页：复用管理端 ListSPUs 谓词（分类/
// 品牌/关键词过滤），status 恒上架；返回收窄 DTO（不含 detail 大字段，
// 见 dto.AppSPUItem）。
func (s *Service) AppPageProductSPUs(ctx context.Context, req *dto.AppSPUPageReq) ([]dto.AppSPUItem, int64, error) {
	statusOn := model.ProductSPUStatusOn
	page := web.PageQuery{PageNo: req.PageNo, PageSize: req.PageSize}
	page.Normalize()
	rows, total, err := s.repo.ListSPUs(ctx, mallrepo.SPUFilter{
		Name:       trimString(req.Keyword),
		CategoryID: req.CategoryID,
		BrandID:    req.BrandID,
		Status:     &statusOn,
	}, page)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.AppSPUItem, 0, len(rows))
	for i := range rows {
		items = append(items, appSPUItem(&rows[i]))
	}
	return items, total, nil
}

// AppGetProductSPU C 端 SPU 详情（含 SKU 全量：价格/库存/规格快照，主 SKU
// 恒首位）：仅上架可见——下架/不存在/跨租户统一 404「商品不存在」，detail
// 图文大字段保留给详情端点（分页项收窄，见 dto/product_spu_app.go）。
func (s *Service) AppGetProductSPU(ctx context.Context, id int64) (*model.ProductSPU, error) {
	row, err := s.repo.FindSPUByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil || row.Status != model.ProductSPUStatusOn {
		return nil, xerr.NotFound("商品不存在")
	}
	skus, err := s.repo.ListSKUsBySPU(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	row.Skus = skus
	return row, nil
}

// appSPUItem 把 SPU 实体收窄为 C 端分页项：剥离 detail 富文本与 status/
// tenantId 管理面字段（sliderPicURLs 已由 service 层保证非 nil JSON）。
func appSPUItem(row *model.ProductSPU) dto.AppSPUItem {
	return dto.AppSPUItem{
		ID:            row.ID,
		Name:          row.Name,
		CategoryID:    row.CategoryID,
		BrandID:       row.BrandID,
		PicURL:        row.PicURL,
		SliderPicURLs: row.SliderPicURLs,
		SpecType:      row.SpecType,
		MarketPrice:   row.MarketPrice,
		Sort:          row.Sort,
	}
}
