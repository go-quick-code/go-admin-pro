package dto

import (
	"github.com/lautung/go-admin-pro/internal/pkg/money"
)

// ---------- 回款计划（issue #18，docs/crm-mvp-spec.md §3 回款节/§5）----------
// 金额以十进制字符串提交（如 "1234.56"），换算不经过 float64（internal/pkg/money）。
// 计划不单独编号，periodNo 标识合同内期次；到期日必填（派生逾期）。
// 合同不可经编辑接口改动：计划归属合同一经创建固定。

// ReceivablePlanCreateReq 新建回款计划。金额不变量 SUM(计划金额) ≤ 合同金额
// 由 service 层校验（spec §4.3），违反返回用户可读 400。
type ReceivablePlanCreateReq struct {
	ContractID int64       `json:"contractId" binding:"required"`
	PeriodNo   int         `json:"periodNo"`
	Amount     money.Money `json:"amount"`
	DueDate    string      `json:"dueDate" binding:"required"`
	Remark     string      `json:"remark" binding:"max=500"`
}

// ReceivablePlanUpdateReq 修改回款计划：所属合同不可改（contractId 不参与）；
// 金额校验排除自身（SUM(其余计划) + 本期 ≤ 合同金额）。
type ReceivablePlanUpdateReq struct {
	ID       int64       `json:"id" binding:"required"`
	PeriodNo int         `json:"periodNo"`
	Amount   money.Money `json:"amount"`
	DueDate  string      `json:"dueDate" binding:"required"`
	Remark   string      `json:"remark" binding:"max=500"`
}

// ReceivablePlanPageReq 回款计划分页筛选：所属合同、派生状态、是否逾期。
// 状态/逾期均为派生值（不落库），筛选在 SQL 里按同一判定口径翻译；
// 非法状态直接 400，绝不静默当成「无此筛选」。
type ReceivablePlanPageReq struct {
	PageNo     int   `form:"pageNo"`
	PageSize   int   `form:"pageSize"`
	ContractID int64 `form:"contractId"`
	Status     *int8 `form:"status"`
	Overdue    *bool `form:"overdue"`
}

// ReceivableRecordCreateReq 登记回款记录：所属计划必填，合同由计划带出；
// 到账日期必填；金额不变量 SUM(记录金额) ≤ 计划金额由 service 层校验。
type ReceivableRecordCreateReq struct {
	PlanID      int64       `json:"planId" binding:"required"`
	Amount      money.Money `json:"amount"`
	ReceiptDate string      `json:"receiptDate" binding:"required"`
	Method      string      `json:"method" binding:"max=32"`
	Remark      string      `json:"remark" binding:"max=500"`
}

// ReceivableRecordPageReq 回款记录分页筛选：所属合同、所属计划
// （期次抽屉按 planId 锁定；合同详情/回款页按 contractId 汇总）。
type ReceivableRecordPageReq struct {
	PageNo     int   `form:"pageNo"`
	PageSize   int   `form:"pageSize"`
	ContractID int64 `form:"contractId"`
	PlanID     int64 `form:"planId"`
}
