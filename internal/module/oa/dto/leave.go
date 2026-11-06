// Package dto 是 oa 模块的请求/响应 DTO。
package dto

import (
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// ---------- 请假类型（issue #51，docs/oa-mvp-spec.md §3.5/§5）----------

// LeaveTypeCreateReq 是类型创建请求（POST /oa/leave/type）。
// code 创建后不可变（与 bpm 定义的 key 同语义）；名称/编码租户内唯一（409）。
type LeaveTypeCreateReq struct {
	// Name 类型名称 ≤50 字。
	Name string `json:"name" binding:"required"`
	// Code 类型编码 ≤50 字符，小写字母/数字/连字符。
	Code string `json:"code" binding:"required"`
	// BalanceManaged 是否占用额度。
	BalanceManaged bool `json:"balanceManaged"`
	// AnnualQuota 年度额度（天，一位小数；balanceManaged=false 时应为 0）。
	AnnualQuota money.Money `json:"annualQuota"`
	// Sort 显示顺序（小者在前）。
	Sort int `json:"sort"`
	// Remark 备注 ≤200 字。
	Remark string `json:"remark"`
}

// LeaveTypeUpdateReq 是类型更新请求（PUT /oa/leave/type）。
// code 不可变；名称仍需租户内唯一（排除自身）。
type LeaveTypeUpdateReq struct {
	ID             int64       `json:"id" binding:"required"`
	Name           string      `json:"name" binding:"required"`
	BalanceManaged bool        `json:"balanceManaged"`
	AnnualQuota    money.Money `json:"annualQuota"`
	Sort           int         `json:"sort"`
	Remark         string      `json:"remark"`
}

// LeaveTypeStatusReq 是类型启停请求（PUT /oa/leave/type/:id/status）：
// status 0 启用 / 1 停用。
type LeaveTypeStatusReq struct {
	Status int8 `json:"status"`
}

// LeaveTypeListReq 是类型列表筛选（GET /oa/leave/type/list）：
// status 空表示全部；keyword 模糊匹配名称/编码。
type LeaveTypeListReq struct {
	Status  *int8  `form:"status"`
	Keyword string `form:"keyword"`
}

// ---------- 请假额度（issue #51，docs/oa-mvp-spec.md §3.5/§4.8/§5）----------

// BalancePageReq 是额度管理分页筛选（GET /oa/leave/balance/page，
// oa:leave:manage）：userId/leaveTypeId/year 均可选精确过滤。
type BalancePageReq struct {
	web.PageQuery
	UserID      int64 `form:"userId"`
	LeaveTypeID int64 `form:"leaveTypeId"`
	Year        int   `form:"year"`
}

// BalanceInitReq 是额度初始化请求（POST /oa/leave/balance/init）：
// 对 (userIds × 类型, year) 幂等补行——已有 (用户, 类型, 年) 行不重复建、
// 不覆盖（调整过的数据不动），缺失行以类型年度额度快照落行。
// typeIds 省略时默认全部启用中的占用额度类型。
type BalanceInitReq struct {
	Year    int     `json:"year" binding:"required"`
	UserIDs []int64 `json:"userIds" binding:"required"`
	TypeIDs []int64 `json:"typeIds"`
}

// BalanceInitResp 是初始化响应：created 实际新建行数，total 应存在组合数
// （created ≤ total，差值即已存在的幂等跳过数）。
type BalanceInitResp struct {
	Created int `json:"created"`
	Total   int `json:"total"`
}

// BalanceAdjustReq 是管理员手工调整请求（POST /oa/leave/balance/adjust）：
// adjustment 累加 delta（可负），原因必填并留痕（adjust_reason + updater）。
// 额度行不存在时先按类型年度额度补行再调整（调整即初始化）。
type BalanceAdjustReq struct {
	UserID      int64       `json:"userId" binding:"required"`
	LeaveTypeID int64       `json:"leaveTypeId" binding:"required"`
	Year        int         `json:"year" binding:"required"`
	Delta       money.Money `json:"delta"`
	Reason      string      `json:"reason" binding:"required"`
}

// BalanceItemResp 是额度行视图（管理分页与本人查询共用）：
// Available = quota + adjustment - used（AvailableDays 纯函数）；
// 非占用额度类型不落额度行，只在 MineResp 里给出标记。
type BalanceItemResp struct {
	ID             int64       `json:"id"`
	UserID         int64       `json:"userId"`
	UserName       string      `json:"userName"`
	LeaveTypeID    int64       `json:"leaveTypeId"`
	TypeCode       string      `json:"typeCode"`
	TypeName       string      `json:"typeName"`
	BalanceManaged bool        `json:"balanceManaged"`
	Year           int         `json:"year"`
	Quota          money.Money `json:"quota"`
	Adjustment     money.Money `json:"adjustment"`
	Used           money.Money `json:"used"`
	Available      money.Money `json:"available"`
	AdjustReason   string      `json:"adjustReason"`
	Updater        int64       `json:"updater"`
	UpdateTime     string      `json:"updateTime"`
}

// MineBalanceItemResp 是「我的额度」行：对启用中的每个类型各给一行，
// 占用额度类型缺行时余额全零（尚未初始化）；不占额度类型仅作展示。
type MineBalanceItemResp struct {
	LeaveTypeID    int64        `json:"leaveTypeId"`
	TypeCode       string       `json:"typeCode"`
	TypeName       string       `json:"typeName"`
	BalanceManaged bool         `json:"balanceManaged"`
	AnnualQuota    money.Money  `json:"annualQuota"`
	Balance        *BalanceItem `json:"balance"`
}

// BalanceItem 是额度数值（占用额度类型；nil 表示尚无额度行）。
type BalanceItem struct {
	Year         int         `json:"year"`
	Quota        money.Money `json:"quota"`
	Adjustment   money.Money `json:"adjustment"`
	Used         money.Money `json:"used"`
	Available    money.Money `json:"available"`
	AdjustReason string      `json:"adjustReason"`
	Updater      int64       `json:"updater"`
	UpdateTime   string      `json:"updateTime"`
}

// MineBalancesResp 是「我的额度」响应（GET /oa/leave/balance/mine，登录即可）：
// year 回显请求年度，items 覆盖全部启用中类型（sort 升序）。
type MineBalancesResp struct {
	Year  int                   `json:"year"`
	Items []MineBalanceItemResp `json:"items"`
}
