package dto

// ProductCategoryCreateReq 新建产品分类（挂在 parentId 下，0 为根）。
type ProductCategoryCreateReq struct {
	ParentID int64  `json:"parentId"`
	Name     string `json:"name" binding:"required,max=64"`
	Code     string `json:"code" binding:"max=64"`
	Sort     int    `json:"sort"`
	Status   *int8  `json:"status"`
}

// ProductCategoryUpdateReq 修改产品分类：改父走环校验（上级不能是自己/自己的子孙）。
type ProductCategoryUpdateReq struct {
	ID       int64  `json:"id" binding:"required"`
	ParentID int64  `json:"parentId"`
	Name     string `json:"name" binding:"required,max=64"`
	Code     string `json:"code" binding:"max=64"`
	Sort     int    `json:"sort"`
	Status   *int8  `json:"status"`
}
