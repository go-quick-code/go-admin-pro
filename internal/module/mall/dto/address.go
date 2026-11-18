package dto

// address.go 是会员收货地址的请求 DTO（issue #174，docs/mall-mvp-spec.md §4）：
// T1 管理端只有「按会员查地址」的只读入口（userId 过滤），无写入端点——
// 地址维护随 C 端自助（T2/T5）。返回直接复用 model.MemberAddress。

// AddressPageReq 地址分页筛选（GET /mall/address/page，权限 mall:member:query
// ——地址是会员详情数据，随会员查询码；spec §7 权限码仅 mall:member:*/mall:level:*）。
// UserID 选填：不填列全租户地址（管理视图），填了按会员过滤。
type AddressPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	UserID   int64  `form:"userId"`
	Receiver string `form:"receiver"`
}
