package model

// product.go 是商品域基础三件的实体定义（issue #176，docs/mall-mvp-spec.md
// §3.2）：分类（两级树）/品牌/属性名/属性值。SPU/SKU 随 T4 迁移建表后
// 落实体。四表全部是租户级数据（TenantTables，ADR 0019）：查询/写入由租户
// 插件自动追加 tenant_id 条件，跨租户业务 ID 一律不可见（404，不泄露存在性）。
//
// 唯一键（不含 deleted_at，照 member_level 先例：软删行保持占位，同值不可
// 重建，撞键由 service 层翻译 409）：
//   - product_category：UNIQUE(tenant_id, parent_id, name)——同级 name 唯一，
//     跨父级同名允许；
//   - product_brand / product_property：UNIQUE(tenant_id, name)——租户内唯一；
//   - product_property_value：UNIQUE(tenant_id, property_id, name)——同属性
//     下值唯一，不同属性可有同名值。

// ProductCategoryRootID 是分类树的根父级占位（parent_id=0 表示根分类）。
// 两级树：根分类（parent_id=0）下挂子分类，子分类不可再挂子级（父必须是
// 根级，规则见 service/product_rules.go）。
const ProductCategoryRootID int64 = 0

// ProductCategory 是商品分类（租户级，两级树）。
type ProductCategory struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// ParentID 是父分类 ID（0=根；两级树——父必须是根级）。
	ParentID int64  `gorm:"not null;default:0" json:"parentId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	// Sort 是同级排序（小值在前）。
	Sort   int  `gorm:"not null;default:0" json:"sort"`
	Status int8 `gorm:"not null;default:0" json:"status"`
	// Children 是子分类（树查询返回；非持久化列）。
	Children []*ProductCategory `gorm:"-" json:"children,omitempty"`
}

func (ProductCategory) TableName() string { return "product_category" }

// ProductBrand 是商品品牌（租户级）。
type ProductBrand struct {
	BaseEntity
	TenantID int64  `gorm:"not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
	PicURL   string `gorm:"size:255" json:"picUrl"`
	Status   int8   `gorm:"not null;default:0" json:"status"`
	Remark   string `gorm:"size:500" json:"remark"`
}

func (ProductBrand) TableName() string { return "product_brand" }

// ProductProperty 是商品属性名（租户级，如「颜色」）。MVP 规格只有 name
// 一列业务字段（无 status，spec §3.2 未取）；属性值见 ProductPropertyValue。
type ProductProperty struct {
	BaseEntity
	TenantID int64  `gorm:"not null;default:1" json:"tenantId"`
	Name     string `gorm:"size:64;not null" json:"name"`
}

func (ProductProperty) TableName() string { return "product_property" }

// ProductPropertyValue 是商品属性值（租户级，归属属性名，如「颜色→红色」）。
type ProductPropertyValue struct {
	BaseEntity
	TenantID int64 `gorm:"not null;default:1" json:"tenantId"`
	// PropertyID 是归属属性名 ID（product_property.id，模块内引用）。
	PropertyID int64  `gorm:"not null" json:"propertyId"`
	Name       string `gorm:"size:64;not null" json:"name"`
}

func (ProductPropertyValue) TableName() string { return "product_property_value" }
