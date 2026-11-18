package dto

// level.go 是会员等级的请求 DTO（issue #174，docs/mall-mvp-spec.md §4）：
// 等级 CRUD（name/level 双租户内唯一，撞键 409）。列表返回直接复用
// model.MemberLevel（JSON 契约即模型序列化，同 pay 应用域）。

// LevelPageReq 等级分页筛选（GET /mall/level/page，权限 mall:level:query）。
type LevelPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// LevelSaveReq 创建等级（POST /mall/level）：name 与 level 租户内唯一。
type LevelSaveReq struct {
	Name       string `json:"name" binding:"required,max=64"`
	Level      int    `json:"level" binding:"required"`
	Experience int    `json:"experience"`
	Status     *int8  `json:"status"`
	Remark     string `json:"remark" binding:"max=500"`
}

// LevelUpdateReq 修改等级（PUT /mall/level，id 在 body）。
type LevelUpdateReq struct {
	ID int64 `json:"id" binding:"required"`
	LevelSaveReq
}
