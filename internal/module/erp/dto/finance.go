package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
)

// finance.go 是财务域的出入参（issue #94，docs/erp-mvp-spec.md §3.6/§4.7/§5）。
//
// 契约要点：
//   - 结算账户没有余额字段：余额列由服务端按「已审核收付款净额」实时汇总
//     （纯展示，不落库）；
//   - 收付款明细 bizType 取单据表名字符串（sale_out/sale_return/purchase_in/
//     purchase_return），price 以十进制字符串提交（money 定点，不经 float64）；
//   - 单号（no）由服务端经 erp_sequence 发放，创建/修改入参没有单号字段；
//   - 主表没有 totalPrice 入参与列（spec §3.6 未列），列表/详情金额由明细
//     price 服务端汇总；核销上限只在审核时校验（spec §4.7）；
//   - update-status 以目标态提交（幂等）：20 审核、10 反审核。

// ---------- 结算账户 ----------

// FinanceAccountCreateReq 新建结算账户。
type FinanceAccountCreateReq struct {
	Name          string `json:"name" binding:"required,max=64"`
	No            string `json:"no" binding:"max=32"`
	Remark        string `json:"remark" binding:"max=500"`
	Sort          *int   `json:"sort"`
	Status        *int8  `json:"status"`
	DefaultStatus *int8  `json:"defaultStatus"`
}

// FinanceAccountUpdateReq 修改结算账户（置默认时取消原默认）。
type FinanceAccountUpdateReq struct {
	ID            int64  `json:"id" binding:"required"`
	Name          string `json:"name" binding:"required,max=64"`
	No            string `json:"no" binding:"max=32"`
	Remark        string `json:"remark" binding:"max=500"`
	Sort          *int   `json:"sort"`
	Status        *int8  `json:"status"`
	DefaultStatus *int8  `json:"defaultStatus"`
}

// FinanceAccountPageReq 结算账户分页筛选。
type FinanceAccountPageReq struct {
	PageNo   int    `form:"pageNo"`
	PageSize int    `form:"pageSize"`
	Name     string `form:"name"`
	Status   *int8  `form:"status"`
}

// FinanceAccountRow 结算账户分页行（balance = 已审核收款 − 已审核付款）。
type FinanceAccountRow struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	No            string      `json:"no"`
	Remark        string      `json:"remark"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	Sort          int         `json:"sort"`
	DefaultStatus int8        `json:"defaultStatus"`
	Balance       money.Money `json:"balance"`
	CreateTime    time.Time   `json:"createTime"`
}

// FinanceAccountDetail 结算账户详情（含实时余额）。
type FinanceAccountDetail struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	No            string      `json:"no"`
	Remark        string      `json:"remark"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	Sort          int         `json:"sort"`
	DefaultStatus int8        `json:"defaultStatus"`
	Balance       money.Money `json:"balance"`
	CreateTime    time.Time   `json:"createTime"`
}

// ---------- 收款单（SK） ----------

// FinanceReceiptItemPayload 收款单明细：bizType 只能是 sale_out / sale_return，
// bizId 必须已审核且客户与收款单一致，price 必须 > 0。
type FinanceReceiptItemPayload struct {
	BizType string      `json:"bizType" binding:"required,max=32"`
	BizId   int64       `json:"bizId" binding:"required"`
	Price   money.Money `json:"price"`
}

// FinanceDocUpdateStatusReq 收付款单审核状态目标态（与其它单据同构：20 审核、
// 10 反审核，幂等）。
type FinanceDocUpdateStatusReq = StockDocUpdateStatusReq

// FinanceReceiptCreateReq 新建收款单。
type FinanceReceiptCreateReq struct {
	CustomerId    int64                       `json:"customerId" binding:"required"`
	AccountId     int64                       `json:"accountId" binding:"required"`
	ReceiptUserId int64                       `json:"receiptUserId"`
	ReceiptTime   string                      `json:"receiptTime" binding:"required"`
	Remark        string                      `json:"remark" binding:"max=500"`
	Items         []FinanceReceiptItemPayload `json:"items" binding:"required"`
}

// FinanceReceiptUpdateReq 修改收款单（仅未审核）。
type FinanceReceiptUpdateReq struct {
	ID            int64                       `json:"id" binding:"required"`
	CustomerId    int64                       `json:"customerId" binding:"required"`
	AccountId     int64                       `json:"accountId" binding:"required"`
	ReceiptUserId int64                       `json:"receiptUserId"`
	ReceiptTime   string                      `json:"receiptTime" binding:"required"`
	Remark        string                      `json:"remark" binding:"max=500"`
	Items         []FinanceReceiptItemPayload `json:"items" binding:"required"`
}

// FinanceReceiptPageReq 收款单分页筛选。
type FinanceReceiptPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	No         string `form:"no"`
	CustomerId int64  `form:"customerId"`
	AccountId  int64  `form:"accountId"`
	Status     *int8  `form:"status"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// FinanceReceiptRow 收款单分页行（totalPrice = 明细 price 合计，服务端汇总）。
type FinanceReceiptRow struct {
	ID            int64       `json:"id"`
	No            string      `json:"no"`
	CustomerId    int64       `json:"customerId"`
	CustomerName  string      `json:"customerName"`
	AccountId     int64       `json:"accountId"`
	AccountName   string      `json:"accountName"`
	ReceiptUserId int64       `json:"receiptUserId"`
	ReceiptTime   time.Time   `json:"receiptTime"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	TotalPrice    money.Money `json:"totalPrice"`
	Remark        string      `json:"remark"`
	CreateTime    time.Time   `json:"createTime"`
}

// FinanceReceiptItemRow 收款单明细展示行（bizNo = 被核销单据单号）。
type FinanceReceiptItemRow struct {
	ID          int64       `json:"id"`
	BizType     string      `json:"bizType"`
	BizTypeName string      `json:"bizTypeName"`
	BizId       int64       `json:"bizId"`
	BizNo       string      `json:"bizNo"`
	Price       money.Money `json:"price"`
}

// FinanceReceiptDetail 收款单详情（头 + 明细 + 核销合计）。
type FinanceReceiptDetail struct {
	ID            int64                   `json:"id"`
	No            string                  `json:"no"`
	CustomerId    int64                   `json:"customerId"`
	CustomerName  string                  `json:"customerName"`
	AccountId     int64                   `json:"accountId"`
	AccountName   string                  `json:"accountName"`
	ReceiptUserId int64                   `json:"receiptUserId"`
	ReceiptTime   time.Time               `json:"receiptTime"`
	Status        int8                    `json:"status"`
	StatusName    string                  `json:"statusName"`
	TotalPrice    money.Money             `json:"totalPrice"`
	Remark        string                  `json:"remark"`
	Items         []FinanceReceiptItemRow `json:"items"`
	CreateTime    time.Time               `json:"createTime"`
}

// FinanceReceiptExportRow 收款单导出列。
type FinanceReceiptExportRow struct {
	No          string      `excel:"收款单号,width=20"`
	Customer    string      `excel:"客户,width=20"`
	Account     string      `excel:"结算账户,width=18"`
	ReceiptTime time.Time   `excel:"收款时间,width=20"`
	Status      string      `excel:"状态,width=10"`
	TotalPrice  money.Money `excel:"核销金额合计,width=14"`
	OwnerUserID int64       `excel:"负责人ID,width=10"`
	CreateTime  time.Time   `excel:"创建时间,width=20"`
}

// ---------- 付款单（FK） ----------

// FinancePaymentItemPayload 付款单明细：bizType 只能是 purchase_in /
// purchase_return，bizId 必须已审核且供应商与付款单一致，price 必须 > 0。
type FinancePaymentItemPayload struct {
	BizType string      `json:"bizType" binding:"required,max=32"`
	BizId   int64       `json:"bizId" binding:"required"`
	Price   money.Money `json:"price"`
}

// FinancePaymentCreateReq 新建付款单。
type FinancePaymentCreateReq struct {
	SupplierId    int64                       `json:"supplierId" binding:"required"`
	AccountId     int64                       `json:"accountId" binding:"required"`
	PaymentUserId int64                       `json:"paymentUserId"`
	PaymentTime   string                      `json:"paymentTime" binding:"required"`
	Remark        string                      `json:"remark" binding:"max=500"`
	Items         []FinancePaymentItemPayload `json:"items" binding:"required"`
}

// FinancePaymentUpdateReq 修改付款单（仅未审核）。
type FinancePaymentUpdateReq struct {
	ID            int64                       `json:"id" binding:"required"`
	SupplierId    int64                       `json:"supplierId" binding:"required"`
	AccountId     int64                       `json:"accountId" binding:"required"`
	PaymentUserId int64                       `json:"paymentUserId"`
	PaymentTime   string                      `json:"paymentTime" binding:"required"`
	Remark        string                      `json:"remark" binding:"max=500"`
	Items         []FinancePaymentItemPayload `json:"items" binding:"required"`
}

// FinancePaymentPageReq 付款单分页筛选。
type FinancePaymentPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	No         string `form:"no"`
	SupplierId int64  `form:"supplierId"`
	AccountId  int64  `form:"accountId"`
	Status     *int8  `form:"status"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// FinancePaymentRow 付款单分页行。
type FinancePaymentRow struct {
	ID            int64       `json:"id"`
	No            string      `json:"no"`
	SupplierId    int64       `json:"supplierId"`
	SupplierName  string      `json:"supplierName"`
	AccountId     int64       `json:"accountId"`
	AccountName   string      `json:"accountName"`
	PaymentUserId int64       `json:"paymentUserId"`
	PaymentTime   time.Time   `json:"paymentTime"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	TotalPrice    money.Money `json:"totalPrice"`
	Remark        string      `json:"remark"`
	CreateTime    time.Time   `json:"createTime"`
}

// FinancePaymentItemRow 付款单明细展示行。
type FinancePaymentItemRow struct {
	ID          int64       `json:"id"`
	BizType     string      `json:"bizType"`
	BizTypeName string      `json:"bizTypeName"`
	BizId       int64       `json:"bizId"`
	BizNo       string      `json:"bizNo"`
	Price       money.Money `json:"price"`
}

// FinancePaymentDetail 付款单详情（头 + 明细 + 核销合计）。
type FinancePaymentDetail struct {
	ID            int64                   `json:"id"`
	No            string                  `json:"no"`
	SupplierId    int64                   `json:"supplierId"`
	SupplierName  string                  `json:"supplierName"`
	AccountId     int64                   `json:"accountId"`
	AccountName   string                  `json:"accountName"`
	PaymentUserId int64                   `json:"paymentUserId"`
	PaymentTime   time.Time               `json:"paymentTime"`
	Status        int8                    `json:"status"`
	StatusName    string                  `json:"statusName"`
	TotalPrice    money.Money             `json:"totalPrice"`
	Remark        string                  `json:"remark"`
	Items         []FinancePaymentItemRow `json:"items"`
	CreateTime    time.Time               `json:"createTime"`
}

// FinancePaymentExportRow 付款单导出列。
type FinancePaymentExportRow struct {
	No          string      `excel:"付款单号,width=20"`
	Supplier    string      `excel:"供应商,width=20"`
	Account     string      `excel:"结算账户,width=18"`
	PaymentTime time.Time   `excel:"付款时间,width=20"`
	Status      string      `excel:"状态,width=10"`
	TotalPrice  money.Money `excel:"核销金额合计,width=14"`
	OwnerUserID int64       `excel:"负责人ID,width=10"`
	CreateTime  time.Time   `excel:"创建时间,width=20"`
}
