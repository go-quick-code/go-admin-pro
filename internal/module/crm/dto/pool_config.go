package dto

// ---------- 公海租户配置（issue #14，docs/crm-mvp-spec.md §4.2）----------

// PoolConfigUpdateReq 保存租户级公海配置（回收天数/认领上限）。
// 取值边界由 service.ValidatePoolConfig 校验（中文错误文案指明字段），
// 不用 binding 标签：0/负数等非法值也要拿到可读的中文提示而非绑定器原文。
type PoolConfigUpdateReq struct {
	RecycleDays int `json:"recycleDays"`
	ClaimLimit  int `json:"claimLimit"`
}
