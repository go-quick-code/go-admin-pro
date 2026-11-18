package dto

// product.go 是商品域基础三件的请求 DTO（issue #176，docs/mall-mvp-spec.md
// §4）：分类树+CRUD / 品牌 CRUD / 属性名+属性值 CRUD。列表返回直接复用
// model 序列化（同会员域），树返回 model.ProductCategory（含 children）。

// ---------- 商品分类（mall:category:*） ----------

// CategorySaveReq 创建分类（POST /mall/product/category）：parentId 0=根，
// 两级树（父必须是根级）；name 同级唯一（撞键 409）。
type CategorySaveReq struct {
	ParentID int64  `json:"parentId"`
	Name     string `json:"name" binding:"required,max=64"`
	Sort     int    `json:"sort"`
	Status   *int8  `json:"status"`
}

// CategoryUpdateReq 修改分类（PUT /mall/product/category，id 在 body）。
type CategoryUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	CategorySaveReq
}

// ---------- 商品品牌（mall:brand:*） ----------

// BrandPageReq 品牌分页筛选（GET /mall/product/brand/page，权限 mall:brand:query）。
type BrandPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// BrandSaveReq 创建品牌（POST /mall/product/brand）：name 租户内唯一（撞键 409）。
type BrandSaveReq struct {
	Name   string `json:"name" binding:"required,max=64"`
	PicURL string `json:"picUrl" binding:"max=255"`
	Status *int8  `json:"status"`
	Remark string `json:"remark" binding:"max=500"`
}

// BrandUpdateReq 修改品牌（PUT /mall/product/brand，id 在 body）。
type BrandUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	BrandSaveReq
}

// ---------- 商品属性名（mall:property:*） ----------

// PropertyPageReq 属性名分页筛选（GET /mall/product/property/page，
// 权限 mall:property:query）。
type PropertyPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
}

// PropertySaveReq 创建属性名（POST /mall/product/property）：name 租户内
// 唯一（撞键 409）。属性名无 status（spec §3.2 只落 name 一列业务字段）。
type PropertySaveReq struct {
	Name string `json:"name" binding:"required,max=64"`
}

// PropertyUpdateReq 修改属性名（PUT /mall/product/property，id 在 body）。
type PropertyUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	PropertySaveReq
}

// ---------- 商品属性值（随 mall:property:*） ----------

// PropertyValueSaveReq 创建属性值（POST /mall/product/property/value）：
// propertyId 归属属性名（不存在/跨租户 404），name 属性内唯一（撞键 409）。
type PropertyValueSaveReq struct {
	PropertyID int64  `json:"propertyId" binding:"required"`
	Name       string `json:"name" binding:"required,max=64"`
}

// PropertyValueUpdateReq 修改属性值（PUT /mall/product/property/value，id
// 在 body；归属属性不可改——改归属等价于删了重建，避免跨属性搬运语义）。
type PropertyValueUpdateReq struct {
	ID   int64  `json:"id" binding:"required"`
	Name string `json:"name" binding:"required,max=64"`
}
