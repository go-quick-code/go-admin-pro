package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// purchase.go 是采购三单（订单 CG / 入库 RK / 退货 RT）的出入参（issue #91，
// docs/erp-mvp-spec.md §3.3/§4.1/§4.4/§5）。契约要点与 #90 杂项单据一致：
//   - 数量/单价以十进制字符串提交（qty/money 定点，不经 float64）；
//   - 单号由服务端经 erp_sequence 发放，创建/修改入参没有单号字段；订单编号
//     字段按 spec §3.3 字面为 orderNo（入库/退货沿 00030 的 no）
//     ——客户端伪造从契约上不可采纳（spec §4.8）；
//   - 行金额（totalPrice）与入库/退货主表金额服务端计算，入参没有该字段
//     （spec §4.2 明细不变量）；
//   - 订单明细 inCount/returnCount 为服务端重算的进度回写（入参没有该字段）；
//   - 退货单仓库取自入库单（spec §3.3），入参没有仓库字段；
//   - 入库 paymentPrice / 退货 refundPrice 为核销回写列（本票只落列，#94 回写），
//     详情只读展示；
//   - update-status 以目标态提交（幂等）：20 审核、10 反审核（复用 #90 的
//     StockDocUpdateStatusReq——十二类单据共用一个审核语义，spec §4.1）。

// PurchaseItemPayload 是采购明细条目（创建/修改入参，三单共用；形状与
// 杂项单据的 StockDocItemPayload 相同，别名复用避免重复定义）。
type PurchaseItemPayload = StockDocItemPayload

// ---------- 采购订单（CG） ----------

// PurchaseOrderCreateReq 新建采购订单。
type PurchaseOrderCreateReq struct {
	SupplierId int64                 `json:"supplierId" binding:"required"`
	OrderTime  string                `json:"orderTime" binding:"required"`
	Remark     string                `json:"remark" binding:"max=500"`
	Items      []PurchaseItemPayload `json:"items" binding:"required"`
}

// PurchaseOrderUpdateReq 修改采购订单（仅未审核）。
type PurchaseOrderUpdateReq struct {
	ID         int64                 `json:"id" binding:"required"`
	SupplierId int64                 `json:"supplierId" binding:"required"`
	OrderTime  string                `json:"orderTime" binding:"required"`
	Remark     string                `json:"remark" binding:"max=500"`
	Items      []PurchaseItemPayload `json:"items" binding:"required"`
}

// PurchaseOrderPageReq 采购订单分页筛选。
type PurchaseOrderPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	OrderNo    string `form:"orderNo"`
	SupplierId int64  `form:"supplierId"`
	Status     *int8  `form:"status"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// PurchaseOrderItemRow 订单明细展示行（含进度回写列 inCount/returnCount）。
type PurchaseOrderItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
	InCount      qty.Qty     `json:"inCount"`
	ReturnCount  qty.Qty     `json:"returnCount"`
}

// PurchaseOrderRow 订单分页展示行。
type PurchaseOrderRow struct {
	ID           int64     `json:"id"`
	OrderNo      string    `json:"orderNo"`
	SupplierId   int64     `json:"supplierId"`
	SupplierName string    `json:"supplierName"`
	OrderTime    time.Time `json:"orderTime"`
	Status       int8      `json:"status"`
	StatusName   string    `json:"statusName"`
	Remark       string    `json:"remark"`
	CreateTime   time.Time `json:"createTime"`
}

// PurchaseOrderDetail 订单详情（头 + 明细 + 进度）。
type PurchaseOrderDetail struct {
	ID           int64                  `json:"id"`
	OrderNo      string                 `json:"orderNo"`
	SupplierId   int64                  `json:"supplierId"`
	SupplierName string                 `json:"supplierName"`
	OrderTime    time.Time              `json:"orderTime"`
	Status       int8                   `json:"status"`
	StatusName   string                 `json:"statusName"`
	Remark       string                 `json:"remark"`
	Items        []PurchaseOrderItemRow `json:"items"`
	CreateTime   time.Time              `json:"createTime"`
}

// ---------- 采购入库（RK） ----------

// PurchaseInCreateReq 新建采购入库单（必挂已审核订单，仓库必填）。
type PurchaseInCreateReq struct {
	SupplierId  int64                 `json:"supplierId" binding:"required"`
	OrderId     int64                 `json:"orderId" binding:"required"`
	WarehouseId int64                 `json:"warehouseId" binding:"required"`
	InTime      string                `json:"inTime" binding:"required"`
	Remark      string                `json:"remark" binding:"max=500"`
	Items       []PurchaseItemPayload `json:"items" binding:"required"`
}

// PurchaseInUpdateReq 修改采购入库单（仅未审核；挂单订单不可更换——已审核
// 订单不可编辑、未审核订单不可挂，改挂等于换单，删除重建）。
type PurchaseInUpdateReq struct {
	ID          int64                 `json:"id" binding:"required"`
	SupplierId  int64                 `json:"supplierId" binding:"required"`
	OrderId     int64                 `json:"orderId" binding:"required"`
	WarehouseId int64                 `json:"warehouseId" binding:"required"`
	InTime      string                `json:"inTime" binding:"required"`
	Remark      string                `json:"remark" binding:"max=500"`
	Items       []PurchaseItemPayload `json:"items" binding:"required"`
}

// PurchaseInPageReq 采购入库单分页筛选。
type PurchaseInPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	No         string `form:"no"`
	OrderId    int64  `form:"orderId"`
	SupplierId int64  `form:"supplierId"`
	Status     *int8  `form:"status"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// PurchaseInItemRow 入库单明细展示行。
type PurchaseInItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
}

// PurchaseInRow 入库单分页展示行。
type PurchaseInRow struct {
	ID            int64       `json:"id"`
	No            string      `json:"no"`
	SupplierId    int64       `json:"supplierId"`
	SupplierName  string      `json:"supplierName"`
	OrderId       int64       `json:"orderId"`
	OrderNo       string      `json:"orderNo"`
	WarehouseId   int64       `json:"warehouseId"`
	WarehouseName string      `json:"warehouseName"`
	InTime        time.Time   `json:"inTime"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	TotalPrice    money.Money `json:"totalPrice"`
	PaymentPrice  money.Money `json:"paymentPrice"`
	Remark        string      `json:"remark"`
	CreateTime    time.Time   `json:"createTime"`
}

// PurchaseInDetail 入库单详情（头 + 明细）。
type PurchaseInDetail struct {
	ID            int64               `json:"id"`
	No            string              `json:"no"`
	SupplierId    int64               `json:"supplierId"`
	SupplierName  string              `json:"supplierName"`
	OrderId       int64               `json:"orderId"`
	OrderNo       string              `json:"orderNo"`
	WarehouseId   int64               `json:"warehouseId"`
	WarehouseName string              `json:"warehouseName"`
	InTime        time.Time           `json:"inTime"`
	Status        int8                `json:"status"`
	StatusName    string              `json:"statusName"`
	TotalPrice    money.Money         `json:"totalPrice"`
	PaymentPrice  money.Money         `json:"paymentPrice"`
	Remark        string              `json:"remark"`
	Items         []PurchaseInItemRow `json:"items"`
	CreateTime    time.Time           `json:"createTime"`
}

// ---------- 采购退货（RT） ----------

// PurchaseReturnCreateReq 新建采购退货单（必挂已审核入库单；仓库取自入库单，
// 入参不收仓库字段）。
type PurchaseReturnCreateReq struct {
	SupplierId int64                 `json:"supplierId" binding:"required"`
	InId       int64                 `json:"inId" binding:"required"`
	ReturnTime string                `json:"returnTime" binding:"required"`
	Remark     string                `json:"remark" binding:"max=500"`
	Items      []PurchaseItemPayload `json:"items" binding:"required"`
}

// PurchaseReturnUpdateReq 修改采购退货单（仅未审核；挂单入库不可更换，同入库单理由）。
type PurchaseReturnUpdateReq struct {
	ID         int64                 `json:"id" binding:"required"`
	SupplierId int64                 `json:"supplierId" binding:"required"`
	InId       int64                 `json:"inId" binding:"required"`
	ReturnTime string                `json:"returnTime" binding:"required"`
	Remark     string                `json:"remark" binding:"max=500"`
	Items      []PurchaseItemPayload `json:"items" binding:"required"`
}

// PurchaseReturnPageReq 采购退货单分页筛选。
type PurchaseReturnPageReq struct {
	PageNo     int    `form:"pageNo"`
	PageSize   int    `form:"pageSize"`
	No         string `form:"no"`
	InId       int64  `form:"inId"`
	SupplierId int64  `form:"supplierId"`
	Status     *int8  `form:"status"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// PurchaseReturnItemRow 退货单明细展示行。
type PurchaseReturnItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
}

// PurchaseReturnRow 退货单分页展示行。
type PurchaseReturnRow struct {
	ID            int64       `json:"id"`
	No            string      `json:"no"`
	SupplierId    int64       `json:"supplierId"`
	SupplierName  string      `json:"supplierName"`
	InId          int64       `json:"inId"`
	InNo          string      `json:"inNo"`
	WarehouseId   int64       `json:"warehouseId"`
	WarehouseName string      `json:"warehouseName"`
	ReturnTime    time.Time   `json:"returnTime"`
	Status        int8        `json:"status"`
	StatusName    string      `json:"statusName"`
	TotalPrice    money.Money `json:"totalPrice"`
	RefundPrice   money.Money `json:"refundPrice"`
	Remark        string      `json:"remark"`
	CreateTime    time.Time   `json:"createTime"`
}

// PurchaseReturnDetail 退货单详情（头 + 明细）。
type PurchaseReturnDetail struct {
	ID            int64                   `json:"id"`
	No            string                  `json:"no"`
	SupplierId    int64                   `json:"supplierId"`
	SupplierName  string                  `json:"supplierName"`
	InId          int64                   `json:"inId"`
	InNo          string                  `json:"inNo"`
	WarehouseId   int64                   `json:"warehouseId"`
	WarehouseName string                  `json:"warehouseName"`
	ReturnTime    time.Time               `json:"returnTime"`
	Status        int8                    `json:"status"`
	StatusName    string                  `json:"statusName"`
	TotalPrice    money.Money             `json:"totalPrice"`
	RefundPrice   money.Money             `json:"refundPrice"`
	Remark        string                  `json:"remark"`
	Items         []PurchaseReturnItemRow `json:"items"`
	CreateTime    time.Time               `json:"createTime"`
}

// ---------- Excel 导出列（spec §4.11：十二类单据列表导出，本票三类） ----------

// PurchaseOrderExportRow 采购订单导出列。
type PurchaseOrderExportRow struct {
	OrderNo       string    `excel:"单号,width=20"`
	Supplier      string    `excel:"供应商,width=18"`
	OrderTime     time.Time `excel:"下单时间,width=20"`
	Status        string    `excel:"状态,width=10"`
	Remark        string    `excel:"备注,width=32"`
	OwnerUserName string    `excel:"负责人,width=14"`
	CreateTime    time.Time `excel:"创建时间,width=20"`
}

// PurchaseInExportRow 采购入库单导出列。
type PurchaseInExportRow struct {
	No            string      `excel:"单号,width=20"`
	Supplier      string      `excel:"供应商,width=18"`
	OrderNo       string      `excel:"采购订单号,width=20"`
	Warehouse     string      `excel:"仓库,width=18"`
	InTime        time.Time   `excel:"入库时间,width=20"`
	TotalPrice    money.Money `excel:"单据金额,width=14"`
	PaymentPrice  money.Money `excel:"已核销金额,width=14"`
	Status        string      `excel:"状态,width=10"`
	Remark        string      `excel:"备注,width=32"`
	OwnerUserName string      `excel:"负责人,width=14"`
	CreateTime    time.Time   `excel:"创建时间,width=20"`
}

// PurchaseReturnExportRow 采购退货单导出列。
type PurchaseReturnExportRow struct {
	No            string      `excel:"单号,width=20"`
	Supplier      string      `excel:"供应商,width=18"`
	InNo          string      `excel:"采购入库单号,width=20"`
	Warehouse     string      `excel:"仓库,width=18"`
	ReturnTime    time.Time   `excel:"退货时间,width=20"`
	TotalPrice    money.Money `excel:"单据金额,width=14"`
	RefundPrice   money.Money `excel:"已核销退款,width=14"`
	Status        string      `excel:"状态,width=10"`
	Remark        string      `excel:"备注,width=32"`
	OwnerUserName string      `excel:"负责人,width=14"`
	CreateTime    time.Time   `excel:"创建时间,width=20"`
}
