package model

import (
	"encoding/json"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// product_spu.go 是商品域 SPU/SKU 实体（issue #177，docs/mall-mvp-spec.md
// §3.2）。两表全部是租户级数据（TenantTables，ADR 0019）：查询/写入由租户
// 插件自动追加 tenant_id 条件，跨租户业务 ID 一律不可见（404，不泄露存在性）。
//
// 唯一键（不含 deleted_at，照 member_level 先例：软删行保持占位，同值不可
// 重建，撞键由 service 层翻译 409）：
//   - product_spu：UNIQUE(tenant_id, name)——name 租户内唯一；
//   - product_sku：无业务唯一键（同 SPU 的规格组合由 service 层纯函数去重，
//     规格快照 JSON 与主数据解耦）。
//
// JSON 列（slider_pic_urls / properties）用 json.RawMessage 承载：写入侧由
// service 层保证非 nil（空值落 []，满足 NOT NULL），读取侧原样透传给 API
//（数组形状不经过字符串二次转义）；规格语义见 product_spu_rules.go 纯函数。

// 商品上下架状态（spec §3.2「status（下架/上架）；只有上架可被购买」——
// 数值对齐芋道 ProductSpuStatusEnum；与 StatusEnable/StatusDisable 的语义
// 不同（上下架是商品售卖边界，不是记录启停），独立常量避免混用）。
const (
	// ProductSPUStatusOff 下架（默认：新建商品先入库再上架）。
	ProductSPUStatusOff int8 = 0
	// ProductSPUStatusOn 上架（T5 起 C 端只展示/可购买上架商品）。
	ProductSPUStatusOn int8 = 1
)

// 商品规格类型（spec_type，spec §3.2「spec_type（单规格/多规格）」）。
const (
	// ProductSpecTypeSingle 单规格：恰一条空 properties 的 SKU（价格/库存挂该 SKU）。
	ProductSpecTypeSingle int8 = 0
	// ProductSpecTypeMulti 多规格：至少一条非空 properties 的 SKU。
	ProductSpecTypeMulti int8 = 1
)

// ProductSPU 是商品（标准产品单元，租户级）。
type ProductSPU struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// CategoryID 是归属分类 ID（product_category.id，模块内引用；必须挂
	// 叶子分类——纯函数见 service ValidateSPUCategoryLeaf）。
	CategoryID int64 `gorm:"not null" json:"categoryId"`
	// BrandID 是品牌 ID（product_brand.id；0=未关联品牌，照 category parent_id=0 占位先例）。
	BrandID int64  `gorm:"not null;default:0" json:"brandId"`
	Name    string `gorm:"size:128;not null" json:"name"`
	PicURL  string `gorm:"size:255" json:"picUrl"`
	// SliderPicURLs 是轮播图 URL 数组（JSON 列，service 层保证非 nil——空值落 []）。
	SliderPicURLs json.RawMessage `gorm:"type:json;not null" json:"sliderPicUrls"`
	// Detail 是图文详情（C 端展示，富文本；MVP 管理端直填）。
	Detail string `gorm:"type:text" json:"detail"`
	// SpecType 是规格类型：0 单规格 / 1 多规格。
	SpecType int8 `gorm:"not null;default:0" json:"specType"`
	// MarketPrice 是市场价格（Money decimal(12,2)，非负——纯函数校验）。
	MarketPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"marketPrice"`
	// Status 是上下架状态：0 下架 / 1 上架（只有上架可被购买，T5 起生效）。
	Status int8 `gorm:"not null;default:0" json:"status"`
	// Sort 是展示排序（小值在前）。
	Sort int `gorm:"not null;default:0" json:"sort"`

	// Skus 是 SPU 的 SKU 列表（详情返回；非持久化列）。
	Skus []ProductSKU `gorm:"-" json:"skus,omitempty"`
}

func (ProductSPU) TableName() string { return "product_spu" }

// ProductSKU 是商品规格（库存单位，租户级，归属 SPU）。
type ProductSKU struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// SPUID 是归属 SPU ID（product_spu.id，模块内引用）。SPU 删除事务级联
	// 软删其 SKU（service 层收口，本表不孤儿）。显式 column 标签：gorm 默认
	// 命名会把 SPUID 拆成 sp_uid，与迁移列名 spu_id 不符。
	SPUID int64 `gorm:"column:spu_id;not null" json:"spuId"`
	// Properties 是规格 JSON 快照：[{propertyId,propertyName,valueId,valueName}]
	//（单规格为空数组）。propertyId/valueId 引用属性主数据，名称由 service
	// 层落库时按主数据反查写入（快照口径：主数据改名/软删不影响已建 SKU）；
	// 引用存在性与归属校验见 service（repo 查询），结构校验见纯函数。
	Properties json.RawMessage `gorm:"type:json;not null" json:"properties"`
	// Price 是售价（Money decimal(12,2)，非负——纯函数校验；T6 成交价另快照）。
	Price money.Money `gorm:"type:decimal(12,2);not null" json:"price"`
	// MarketPrice 是划线价（Money decimal(12,2)，非负）。
	MarketPrice money.Money `gorm:"type:decimal(12,2);not null;default:0.00" json:"marketPrice"`
	PicURL      string      `gorm:"size:255" json:"picUrl"`
	// Stock 是库存（非负整数；T6 下单行锁条件扣减 UPDATE stock = stock - ?
	// WHERE stock >= ?，本票只做非负存量维护）。
	Stock int `gorm:"not null;default:0" json:"stock"`
	// First 标记 SPU 展示主 SKU（0 否 / 1 是；每 SPU 恰一条 first=1，纯函数锁定）。
	First int8 `gorm:"not null;default:0" json:"first"`
}

func (ProductSKU) TableName() string { return "product_sku" }
