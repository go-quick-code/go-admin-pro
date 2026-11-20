package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// product_spu.go 是商品 SPU/SKU 的请求 DTO（issue #177，docs/mall-mvp-spec.md
// §4）。金额一律 money.Money（JSON 十进制字符串，精度不经 float64）；SKU
// 规格项只收 propertyId/valueId 引用——propertyName/valueName 由服务端按
// 主数据反查写入快照（客户端不采纳，照 pay_money 服务端定口径先例）。
// 列表/详情返回直接复用 model 序列化（SPU 详情含 skus 数组）。

// ---------- 商品 SPU（mall:spu:*） ----------

// SPUPageReq SPU 分页筛选（GET /mall/product/spu/page，权限 mall:spu:query）：
// 分类/品牌/状态/关键词（名称模糊）过滤。
type SPUPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	Name       string `form:"name"`
	CategoryID int64  `form:"categoryId"`
	BrandID    int64  `form:"brandId"`
	Status     *int8  `form:"status"`
}

// SPUSkuPropertyReq 是 SKU 规格项引用（properties 数组元素）：只收 ID，
// 名称服务端反查（归属校验：valueId 必须存在且属于 propertyId，400/404）。
type SPUSkuPropertyReq struct {
	PropertyID int64 `json:"propertyId" binding:"required"`
	ValueID    int64 `json:"valueId" binding:"required"`
}

// SPUSkuReq 是 SKU 保存项：单规格恰一条且 properties 空、多规格至少一条且
// 每条至少一个规格项（结构校验见 service ValidateSPUSKUs 纯函数）。price
// 非负（Money 口径）；stock 非负整数；first 主 SKU 标记（至多一个，零个由
// 服务端默认首条）。
type SPUSkuReq struct {
	Properties  []SPUSkuPropertyReq `json:"properties"`
	Price       money.Money         `json:"price"`
	MarketPrice *money.Money        `json:"marketPrice"`
	PicURL      string              `json:"picUrl" binding:"max=255"`
	Stock       int                 `json:"stock"`
	First       bool                `json:"first"`
}

// SPUSaveReq 创建 SPU（POST /mall/product/spu）：category 必须存在且为叶子
// 分类（404/400）、brand 选填（非 0 必须存在，404）、name 租户内唯一（撞键
// 409）、SKU 集合与 SPU 同事务落库。
type SPUSaveReq struct {
	CategoryID int64  `json:"categoryId" binding:"required"`
	BrandID    int64  `json:"brandId"`
	Name       string `json:"name" binding:"required,max=128"`
	PicURL     string `json:"picUrl" binding:"max=255"`
	// SliderPicURLs 是轮播图 URL 数组（空数组/缺省落 JSON []）。
	SliderPicURLs []string     `json:"sliderPicUrls"`
	Detail        string       `json:"detail"`
	SpecType      int8         `json:"specType"`
	MarketPrice   *money.Money `json:"marketPrice"`
	Status        *int8        `json:"status"`
	Sort          int          `json:"sort"`
	Skus          []SPUSkuReq  `json:"skus"`
}

// SPUUpdateReq 修改 SPU（PUT /mall/product/spu，id 在 body）：字段全量覆盖，
// SKU 集全量替换（旧 SKU 软删 + 新 SKU 批量落库，同事务）。
type SPUUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	SPUSaveReq
}

// SPUStatusReq 上下架（PUT /mall/product/spu/:id/status）：0 下架 / 1 上架。
type SPUStatusReq struct {
	Status int8 `json:"status"`
}
