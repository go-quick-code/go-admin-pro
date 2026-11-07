package dto

// ProductUnitCreateReq 新建产品单位（仅名称字典，spec §3.1）。
type ProductUnitCreateReq struct {
	Name   string `json:"name" binding:"required,max=64"`
	Status *int8  `json:"status"`
}

// ProductUnitUpdateReq 修改产品单位。
type ProductUnitUpdateReq struct {
	ID     int64  `json:"id" binding:"required"`
	Name   string `json:"name" binding:"required,max=64"`
	Status *int8  `json:"status"`
}

// ProductUnitPageReq 单位分页筛选：枚举传非法值直接 400，绝不静默当成「无此筛选」。
type ProductUnitPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}
