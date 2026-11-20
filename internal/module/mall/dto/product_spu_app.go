package dto

import (
	"encoding/json"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// product_spu_app.go 是商品域 C 端浏览的请求/响应 DTO（issue #178，T5；
// docs/mall-mvp-spec.md §4「商品：上架 SPU 分页（分类/品牌过滤）、SPU 详情
//（含 SKU 列表）」）。与管理端 product_spu.go 分立：
//   - 分页项收窄：不含 detail 图文大字段（列表不背富文本流量，detail 保留
//     给详情端点）与 status/tenantId 管理面字段（C 端分页恒上架，status 恒 1）；
//   - 详情直接复用 service 层 model.ProductSPU 序列化（含 skus 数组与
//     detail），与管理端 GetProductSPU 同形状——SKU 全量（价格/库存/规格
//     快照）一次带出。

// AppSPUPageReq C 端上架 SPU 分页（GET /mall/app/product/spu/page，需会员
// 令牌）：分类/品牌/关键词（名称模糊）过滤，分页。status 不收——服务端恒
// 上架过滤（C 端看不到下架商品，spec §3.2「只有上架可被购买/展示」）。
type AppSPUPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	Keyword    string `form:"keyword"`
	CategoryID int64  `form:"categoryId"`
	BrandID    int64  `form:"brandId"`
}

// AppSPUItem 是 C 端 SPU 分页项（收窄自 model.ProductSPU）。
type AppSPUItem struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	CategoryID    int64           `json:"categoryId"`
	BrandID       int64           `json:"brandId"`
	PicURL        string          `json:"picUrl"`
	SliderPicURLs json.RawMessage `json:"sliderPicUrls"`
	SpecType      int8            `json:"specType"`
	MarketPrice   money.Money     `json:"marketPrice"`
	Sort          int             `json:"sort"`
}
