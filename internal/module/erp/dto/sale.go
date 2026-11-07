package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// sale.go 是销售三单的出入参（issue #92，docs/erp-mvp-spec.md §3.4/§4.1/§4.5/§5）。
// 与 #90 其它入库/出库单（stock_doc.go）同构：
//
//   - 数量/单价以十进制字符串提交（qty/money 定点，不经 float64）；
//   - 单号（orderNo/no）由服务端经 erp_sequence 发放，创建/修改入参没有单号
//     字段——客户端伪造从契约上不可采纳（spec §4.8）；
//   - 行金额（totalPrice）与主表明细合计（totalPrice）服务端计算，入参没有
//     该字段（spec §4.2 明细不变量）；
//   - 进度列（outCount/returnCount）与核销列（receiptPrice/refundPrice）服务端
//     回写，入参没有这些字段；
//   - update-status 以目标态提交（幂等）：20 审核、10 反审核；
//   - 销售退货不提交 warehouseId（仓库取自挂单出库单，服务端派生，spec §3.3
//     采购镜像「取自入库单」口径）。

// SaleItemPayload 销售明细条目（复用 QR/QC 明细载荷：数量 > 0、单价非负，
// 行金额服务端计算）。
type SaleItemPayload = StockDocItemPayload

// SaleDocUpdateStatusReq 销售/采购单据审核状态目标态（与杂项单据同构）。
type SaleDocUpdateStatusReq = StockDocUpdateStatusReq

// ---------- 销售订单（XS） ----------

// SaleOrderCreateReq 新建销售订单。
type SaleOrderCreateReq struct {
	CustomerId int64 `json:"customerId" binding:"required"`
	// SaleUserId 销售员（纯记录，可选；> 0 时校验用户存在）。
	SaleUserId int64             `json:"saleUserId"`
	OrderTime  string            `json:"orderTime" binding:"required"`
	Remark     string            `json:"remark" binding:"max=500"`
	Items      []SaleItemPayload `json:"items" binding:"required"`
}

// SaleOrderUpdateReq 修改销售订单（仅未审核）。
type SaleOrderUpdateReq struct {
	ID         int64             `json:"id" binding:"required"`
	CustomerId int64             `json:"customerId" binding:"required"`
	SaleUserId int64             `json:"saleUserId"`
	OrderTime  string            `json:"orderTime" binding:"required"`
	Remark     string            `json:"remark" binding:"max=500"`
	Items      []SaleItemPayload `json:"items" binding:"required"`
}

// SaleOrderPageReq 销售订单分页筛选。
type SaleOrderPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	OrderNo    string `form:"orderNo"`
	CustomerId int64  `form:"customerId"`
	Status     *int8  `form:"status"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// SaleOrderRow 销售订单分页展示行（进度在详情明细里，列表不带）。
type SaleOrderRow struct {
	ID           int64     `json:"id"`
	OrderNo      string    `json:"orderNo"`
	CustomerId   int64     `json:"customerId"`
	CustomerName string    `json:"customerName"`
	SaleUserId   int64     `json:"saleUserId"`
	OrderTime    time.Time `json:"orderTime"`
	Status       int8      `json:"status"`
	StatusName   string    `json:"statusName"`
	Remark       string    `json:"remark"`
	CreateTime   time.Time `json:"createTime"`
}

// SaleOrderItemRow 订单明细展示行（含进度回写列）。
type SaleOrderItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
	// OutCount/ReturnCount 进度（只计已审核，spec §4.5）。
	OutCount    qty.Qty `json:"outCount"`
	ReturnCount qty.Qty `json:"returnCount"`
}

// SaleOrderDetail 销售订单详情（头 + 明细 + 进度）。
type SaleOrderDetail struct {
	ID           int64              `json:"id"`
	OrderNo      string             `json:"orderNo"`
	CustomerId   int64              `json:"customerId"`
	CustomerName string             `json:"customerName"`
	SaleUserId   int64              `json:"saleUserId"`
	OrderTime    time.Time          `json:"orderTime"`
	Status       int8               `json:"status"`
	StatusName   string             `json:"statusName"`
	Remark       string             `json:"remark"`
	Items        []SaleOrderItemRow `json:"items"`
	CreateTime   time.Time          `json:"createTime"`
}

// SaleOrderExportRow 销售订单导出列。
type SaleOrderExportRow struct {
	OrderNo     string    `excel:"订单号,width=20"`
	Customer    string    `excel:"客户,width=20"`
	SaleUserID  int64     `excel:"销售员ID,width=10"`
	OrderTime   time.Time `excel:"下单时间,width=20"`
	Status      string    `excel:"状态,width=10"`
	Remark      string    `excel:"备注,width=32"`
	OwnerUserID int64     `excel:"负责人ID,width=10"`
	CreateTime  time.Time `excel:"创建时间,width=20"`
}

// ---------- 销售出库（CK） ----------

// SaleOutCreateReq 新建销售出库单（必挂已审核销售订单）。
type SaleOutCreateReq struct {
	CustomerId  int64             `json:"customerId" binding:"required"`
	OrderId     int64             `json:"orderId" binding:"required"`
	WarehouseId int64             `json:"warehouseId" binding:"required"`
	OutTime     string            `json:"outTime" binding:"required"`
	Items       []SaleItemPayload `json:"items" binding:"required"`
}

// SaleOutUpdateReq 修改销售出库单（仅未审核）。
type SaleOutUpdateReq struct {
	ID          int64             `json:"id" binding:"required"`
	CustomerId  int64             `json:"customerId" binding:"required"`
	OrderId     int64             `json:"orderId" binding:"required"`
	WarehouseId int64             `json:"warehouseId" binding:"required"`
	OutTime     string            `json:"outTime" binding:"required"`
	Items       []SaleItemPayload `json:"items" binding:"required"`
}

// SaleOutPageReq 销售出库单分页筛选。
type SaleOutPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	No          string `form:"no"`
	OrderId     int64  `form:"orderId"`
	CustomerId  int64  `form:"customerId"`
	WarehouseId int64  `form:"warehouseId"`
	Status      *int8  `form:"status"`
	StartTime   string `form:"startTime"`
	EndTime     string `form:"endTime"`
}

// SaleOutRow 销售出库单分页展示行。
type SaleOutRow struct {
	ID            int64       `json:"id"`
	No            string      `json:"no"`
	OrderId       int64       `json:"orderId"`
	OrderNo       string      `json:"orderNo"`
	CustomerId    int64       `json:"customerId"`
	CustomerName  string      `json:"customerName"`
	WarehouseId   int64       `json:"warehouseId"`
	WarehouseName string      `json:"warehouseName"`
	OutTime       time.Time   `json:"outTime"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	TotalPrice    money.Money `json:"totalPrice"`
	ReceiptPrice  money.Money `json:"receiptPrice"`
	CreateTime    time.Time   `json:"createTime"`
}

// SaleOutDetail 销售出库单详情（头 + 明细）。
type SaleOutDetail struct {
	ID            int64            `json:"id"`
	No            string           `json:"no"`
	OrderId       int64            `json:"orderId"`
	OrderNo       string           `json:"orderNo"`
	CustomerId    int64            `json:"customerId"`
	CustomerName  string           `json:"customerName"`
	WarehouseId   int64            `json:"warehouseId"`
	WarehouseName string           `json:"warehouseName"`
	OutTime       time.Time        `json:"outTime"`
	Status        int8             `json:"status"`
	StatusName    string           `json:"statusName"`
	TotalPrice    money.Money      `json:"totalPrice"`
	ReceiptPrice  money.Money      `json:"receiptPrice"`
	Items         []SaleOutItemRow `json:"items"`
	CreateTime    time.Time        `json:"createTime"`
}

// SaleOutItemRow 出库单明细展示行。
type SaleOutItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
}

// SaleOutExportRow 销售出库单导出列。
type SaleOutExportRow struct {
	No          string      `excel:"出库单号,width=20"`
	OrderNo     string      `excel:"销售订单号,width=20"`
	Customer    string      `excel:"客户,width=20"`
	Warehouse   string      `excel:"仓库,width=18"`
	OutTime     time.Time   `excel:"出库时间,width=20"`
	Status      string      `excel:"状态,width=10"`
	TotalPrice  money.Money `excel:"出库总金额,width=14"`
	OwnerUserID int64       `excel:"负责人ID,width=10"`
	CreateTime  time.Time   `excel:"创建时间,width=20"`
}

// ---------- 销售退货（ST） ----------

// SaleReturnCreateReq 新建销售退货单（必挂已审核销售出库；仓库取自出库单）。
type SaleReturnCreateReq struct {
	CustomerId int64             `json:"customerId" binding:"required"`
	OutId      int64             `json:"outId" binding:"required"`
	ReturnTime string            `json:"returnTime" binding:"required"`
	Items      []SaleItemPayload `json:"items" binding:"required"`
}

// SaleReturnUpdateReq 修改销售退货单（仅未审核；换挂出库单时仓库随新出库单）。
type SaleReturnUpdateReq struct {
	ID         int64             `json:"id" binding:"required"`
	CustomerId int64             `json:"customerId" binding:"required"`
	OutId      int64             `json:"outId" binding:"required"`
	ReturnTime string            `json:"returnTime" binding:"required"`
	Items      []SaleItemPayload `json:"items" binding:"required"`
}

// SaleReturnPageReq 销售退货单分页筛选。
type SaleReturnPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	No         string `form:"no"`
	OutId      int64  `form:"outId"`
	CustomerId int64  `form:"customerId"`
	Status     *int8  `form:"status"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// SaleReturnRow 销售退货单分页展示行。
type SaleReturnRow struct {
	ID            int64       `json:"id"`
	No            string      `json:"no"`
	OutId         int64       `json:"outId"`
	OutNo         string      `json:"outNo"`
	CustomerId    int64       `json:"customerId"`
	CustomerName  string      `json:"customerName"`
	WarehouseId   int64       `json:"warehouseId"`
	WarehouseName string      `json:"warehouseName"`
	ReturnTime    time.Time   `json:"returnTime"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	TotalPrice    money.Money `json:"totalPrice"`
	RefundPrice   money.Money `json:"refundPrice"`
	CreateTime    time.Time   `json:"createTime"`
}

// SaleReturnItemRow 退货单明细展示行。
type SaleReturnItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
}

// SaleReturnDetail 销售退货单详情（头 + 明细）。
type SaleReturnDetail struct {
	ID            int64               `json:"id"`
	No            string              `json:"no"`
	OutId         int64               `json:"outId"`
	OutNo         string              `json:"outNo"`
	CustomerId    int64               `json:"customerId"`
	CustomerName  string              `json:"customerName"`
	WarehouseId   int64               `json:"warehouseId"`
	WarehouseName string              `json:"warehouseName"`
	ReturnTime    time.Time           `json:"returnTime"`
	Status        int8                `json:"status"`
	StatusName    string              `json:"statusName"`
	TotalPrice    money.Money         `json:"totalPrice"`
	RefundPrice   money.Money         `json:"refundPrice"`
	Items         []SaleReturnItemRow `json:"items"`
	CreateTime    time.Time           `json:"createTime"`
}

// SaleReturnExportRow 销售退货单导出列。
type SaleReturnExportRow struct {
	No          string      `excel:"退货单号,width=20"`
	OutNo       string      `excel:"销售出库单号,width=20"`
	Customer    string      `excel:"客户,width=20"`
	Warehouse   string      `excel:"仓库,width=18"`
	ReturnTime  time.Time   `excel:"退货时间,width=20"`
	Status      string      `excel:"状态,width=10"`
	TotalPrice  money.Money `excel:"退货总金额,width=14"`
	OwnerUserID int64       `excel:"负责人ID,width=10"`
	CreateTime  time.Time   `excel:"创建时间,width=20"`
}
