// expense.go 是报销单的请求/响应 DTO（issue #54，docs/oa-mvp-spec.md
// §3.6/§4.9/§5）：草稿保存（表头 + 明细整体提交）、可见范围内的分页、
// 详情（明细 + 附件；审批时间线由 #55 填充）。
//
// 金额字段一律是 money.Money：JSON 以十进制字符串承载（如 "128.50"），
// 拒绝数字字面量，避免 JavaScript double 往返丢精度。请求里的 totalAmount
// 仅为「前端显示的旧合计」保留位置，服务端一律按明细求和覆盖（spec §4.9）。
package dto

import (
	bpmdto "github.com/go-quick-code/go-admin-pro/internal/module/bpm/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/web"
)

// ExpenseItemSaveReq 是一条明细行的保存请求。草稿保存整体替换明细行集，
// 因此 ID 只用于前端回显（服务端不按 ID 增量更新）。
type ExpenseItemSaveReq struct {
	// ID 明细行 ID（可选，前端回显用；服务端整体替换行集）。
	ID int64 `json:"id"`
	// Category 费用类别（系统字典 oa_expense_category 的启用值）。
	Category string `json:"category"`
	// Amount 金额（十进制字符串，> 0，最多两位小数）。
	Amount money.Money `json:"amount"`
	// ExpenseDate 发生日期 YYYY-MM-DD。
	ExpenseDate string `json:"expenseDate"`
	// Remark 备注（≤200 字）。
	Remark string `json:"remark"`
	// Sort 显示顺序（服务端按数组顺序归一化为 0..n-1，忽略传入值）。
	Sort int `json:"sort"`
}

// ExpenseClaimSaveReq 是报销单草稿的创建/更新请求（POST/PUT
// /oa/expense/claim）：表头标题/事由 + 全部明细行。
//   - 至少一条明细、每条金额 > 0（服务端校验，纯函数 ValidateExpenseItems）；
//   - 表头金额由明细求和，TotalAmount 请求值忽略（spec §4.9）；
//   - 无明细、零/负金额、字典外类别一律 400。
type ExpenseClaimSaveReq struct {
	// ID 更新时必填（仅草稿/已驳回可改）。
	ID int64 `json:"id"`
	// Title 报销标题（1–200 字）。
	Title string `json:"title" binding:"required"`
	// Reason 报销事由（1–500 字）。
	Reason string `json:"reason" binding:"required"`
	// TotalAmount 表头金额：仅为兼容前端表单保留，服务端忽略并重算。
	TotalAmount money.Money `json:"totalAmount"`
	// Items 明细行集合（整体替换；至少一条）。
	Items []ExpenseItemSaveReq `json:"items"`
}

// ExpenseClaimPageReq 是报销单分页（GET /oa/expense/claim/page）：
// status 可选精确过滤（0 草稿/1 待审/2 通过/3 驳回/4 撤回）。
type ExpenseClaimPageReq struct {
	web.PageQuery
	Status *int8 `form:"status"`
}

// ExpenseItemResp 是明细行视图（列表详情共用）：日期归一化为 YYYY-MM-DD。
type ExpenseItemResp struct {
	ID          int64       `json:"id"`
	Category    string      `json:"category"`
	Amount      money.Money `json:"amount"`
	ExpenseDate string      `json:"expenseDate"`
	Remark      string      `json:"remark"`
	Sort        int         `json:"sort"`
}

// ExpenseClaimItem 是报销单行视图（列表/详情/保存返回共用）：
// totalAmount 恒等于明细合计（服务端计算）；itemCount 便于列表展示。
type ExpenseClaimItem struct {
	ID                 int64       `json:"id"`
	UserID             int64       `json:"userId"`
	Title              string      `json:"title"`
	TotalAmount        money.Money `json:"totalAmount"`
	Reason             string      `json:"reason"`
	Status             int8        `json:"status"`
	RejectReason       string      `json:"rejectReason"`
	ApprovalInstanceID *int64      `json:"approvalInstanceId"`
	ItemCount          int         `json:"itemCount"`
	CreateTime         string      `json:"createTime"`
	UpdateTime         string      `json:"updateTime"`
}

// ExpenseClaimDetailResp 是报销单详情（GET /oa/expense/claim/{id}）：
// 表头 + 明细行 + 附件 + 审批时间线（#55 提交后填充，草稿为 nil）。
type ExpenseClaimDetailResp struct {
	ExpenseClaimItem
	Items     []ExpenseItemResp       `json:"items"`
	Files     []ExpenseFileResp       `json:"files"`
	Approvals []bpmdto.InstanceDetail `json:"approvals,omitempty"`
	Approval  *bpmdto.InstanceDetail  `json:"approval,omitempty"`
}
