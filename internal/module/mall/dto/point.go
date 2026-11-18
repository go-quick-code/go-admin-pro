package dto

// point.go 是积分域的请求/响应 DTO（issue #175，docs/mall-mvp-spec.md §4）。
// 流水列表返回直接复用 model.MemberPointRecord（JSON 契约即模型序列化，
// 同会员域其它 DTO）；配置返回复用 model.MemberPointConfig。

// PointAdjustReq 积分调整（POST /mall/member/adjust-point，权限
// mall:member:update）：point 可正可负，0 拒绝（binding:required 对 int 零值
// 拒绝 + service 层纯函数双保险）；余额扣成负数 409（唯一入账入口裁决）。
type PointAdjustReq struct {
	UserID int64  `json:"userId" binding:"required"`
	Point  int    `json:"point" binding:"required"`
	Remark string `json:"remark" binding:"max=500"`
}

// PointRecordPageReq 会员积分流水分页（GET /mall/member/:id/point-records，
// 权限 mall:member:query）：会员 ID 在路径；biz_type 过滤选填（∈ 三类枚举，
// 非法 400）。
type PointRecordPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	BizType  string `form:"bizType"`
}

// PointConfigSaveReq 更新积分配置（PUT /mall/point-config，权限
// mall:point:update）：point_per_yuan ≥ 0（0 = 支付不入账，合法）。
type PointConfigSaveReq struct {
	PointPerYuan int    `json:"pointPerYuan" binding:"gte=0"`
	Remark       string `json:"remark" binding:"max=500"`
}
