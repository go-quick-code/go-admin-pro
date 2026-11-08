package dto

// WarehouseCreateReq 新建仓库（issue #89，spec §3.1）：address/principal 纯展示。
type WarehouseCreateReq struct {
	Name      string `json:"name" binding:"required,max=64"`
	Address   string `json:"address" binding:"max=255"`
	Principal string `json:"principal" binding:"max=64"`
	Sort      *int   `json:"sort"`
	Status    *int8  `json:"status"`
}

// WarehouseUpdateReq 修改仓库。
type WarehouseUpdateReq struct {
	ID        int64  `json:"id" binding:"required"`
	Name      string `json:"name" binding:"required,max=64"`
	Address   string `json:"address" binding:"max=255"`
	Principal string `json:"principal" binding:"max=64"`
	Sort      *int   `json:"sort"`
	Status    *int8  `json:"status"`
}

// WarehousePageReq 仓库分页筛选：枚举传非法值直接 400，绝不静默当成「无此筛选」。
type WarehousePageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}
