package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/money"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock_doc.go 是其它入库单（QR）/其它出库单（QC）的出入参（issue #90，
// docs/erp-mvp-spec.md §3.5/§4.1/§5）。两类单据镜像同构。
//
// 契约要点：
//   - 数量/单价以十进制字符串提交（qty/money 定点，不经 float64）；
//   - 单号（no）由服务端经 erp_sequence 发放，创建/修改入参没有单号字段
//     ——客户端伪造从契约上不可采纳（spec §4.8）；
//   - 行金额（totalPrice）服务端按 单价 × 数量 计算，入参同样没有该字段
//     （spec §4.2 明细不变量）；
//   - update-status 以目标态提交（幂等）：20 审核、10 反审核。

// StockDocItemPayload 单据明细条目（创建/修改入参，QR/QC 共用）。
type StockDocItemPayload struct {
	ProductId    int64       `json:"productId" binding:"required"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
}

// StockInCreateReq 新建其它入库单。
type StockInCreateReq struct {
	WarehouseId int64                 `json:"warehouseId" binding:"required"`
	InTime      string                `json:"inTime" binding:"required"`
	Type        *int8                 `json:"type"`
	Remark      string                `json:"remark" binding:"max=500"`
	Items       []StockDocItemPayload `json:"items" binding:"required"`
}

// StockInUpdateReq 修改其它入库单（仅未审核）。
type StockInUpdateReq struct {
	ID          int64                 `json:"id" binding:"required"`
	WarehouseId int64                 `json:"warehouseId" binding:"required"`
	InTime      string                `json:"inTime" binding:"required"`
	Type        *int8                 `json:"type"`
	Remark      string                `json:"remark" binding:"max=500"`
	Items       []StockDocItemPayload `json:"items" binding:"required"`
}

// StockInPageReq 其它入库单分页筛选。
type StockInPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	No          string `form:"no"`
	WarehouseId int64  `form:"warehouseId"`
	Type        *int8  `form:"type"`
	Status      *int8  `form:"status"`
	StartTime   string `form:"startTime"`
	EndTime     string `form:"endTime"`
}

// StockDocUpdateStatus 审核/反审核请求（目标态幂等，spec §4.1/§5）。
type StockDocUpdateStatusReq struct {
	ID     int64 `json:"id" binding:"required"`
	Status int8  `json:"status" binding:"required"`
}

// StockDocRow 单据分页展示行（QR/QC 共用：docTime 为 inTime/outTime）。
type StockDocRow struct {
	ID            int64     `json:"id"`
	No            string    `json:"no"`
	WarehouseId   int64     `json:"warehouseId"`
	WarehouseName string    `json:"warehouseName"`
	DocTime       time.Time `json:"docTime"`
	Type          int8      `json:"type"`
	TypeName      string    `json:"typeName"`
	Status        int8      `json:"status"`
	StatusName    string    `json:"statusName"`
	Remark        string    `json:"remark"`
	CreateTime    time.Time `json:"createTime"`
}

// StockInItemRow 入库单明细展示行。
type StockInItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
}

// StockInDetail 入库单详情（头 + 明细）。
type StockInDetail struct {
	ID            int64            `json:"id"`
	No            string           `json:"no"`
	WarehouseId   int64            `json:"warehouseId"`
	WarehouseName string           `json:"warehouseName"`
	InTime        time.Time        `json:"inTime"`
	Type          int8             `json:"type"`
	TypeName      string           `json:"typeName"`
	Status        int8             `json:"status"`
	StatusName    string           `json:"statusName"`
	Remark        string           `json:"remark"`
	Items         []StockInItemRow `json:"items"`
	CreateTime    time.Time        `json:"createTime"`
}

// StockOutCreateReq 新建其它出库单。
type StockOutCreateReq struct {
	WarehouseId int64                 `json:"warehouseId" binding:"required"`
	OutTime     string                `json:"outTime" binding:"required"`
	Type        *int8                 `json:"type"`
	Remark      string                `json:"remark" binding:"max=500"`
	Items       []StockDocItemPayload `json:"items" binding:"required"`
}

// StockOutUpdateReq 修改其它出库单（仅未审核）。
type StockOutUpdateReq struct {
	ID          int64                 `json:"id" binding:"required"`
	WarehouseId int64                 `json:"warehouseId" binding:"required"`
	OutTime     string                `json:"outTime" binding:"required"`
	Type        *int8                 `json:"type"`
	Remark      string                `json:"remark" binding:"max=500"`
	Items       []StockDocItemPayload `json:"items" binding:"required"`
}

// StockOutPageReq 其它出库单分页筛选。
type StockOutPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	No          string `form:"no"`
	WarehouseId int64  `form:"warehouseId"`
	Type        *int8  `form:"type"`
	Status      *int8  `form:"status"`
	StartTime   string `form:"startTime"`
	EndTime     string `form:"endTime"`
}

// StockOutItemRow 出库单明细展示行。
type StockOutItemRow struct {
	ID           int64       `json:"id"`
	ProductId    int64       `json:"productId"`
	ProductName  string      `json:"productName"`
	Count        qty.Qty     `json:"count"`
	ProductPrice money.Money `json:"productPrice"`
	TotalPrice   money.Money `json:"totalPrice"`
}

// StockOutDetail 出库单详情（头 + 明细）。
type StockOutDetail struct {
	ID            int64             `json:"id"`
	No            string            `json:"no"`
	WarehouseId   int64             `json:"warehouseId"`
	WarehouseName string            `json:"warehouseName"`
	OutTime       time.Time         `json:"outTime"`
	Type          int8              `json:"type"`
	TypeName      string            `json:"typeName"`
	Status        int8              `json:"status"`
	StatusName    string            `json:"statusName"`
	Remark        string            `json:"remark"`
	Items         []StockOutItemRow `json:"items"`
	CreateTime    time.Time         `json:"createTime"`
}

// ---------- Excel 导出列（spec §4.11：十二类单据列表导出，本票两类） ----------

// StockInExportRow 其它入库单导出列。
type StockInExportRow struct {
	No            string    `excel:"单号,width=20"`
	Warehouse     string    `excel:"仓库,width=18"`
	InTime        time.Time `excel:"入库时间,width=20"`
	Type          string    `excel:"类型,width=10"`
	Status        string    `excel:"状态,width=10"`
	Remark        string    `excel:"备注,width=32"`
	OwnerUserName string    `excel:"负责人,width=14"`
	CreateTime    time.Time `excel:"创建时间,width=20"`
}

// StockOutExportRow 其它出库单导出列。
type StockOutExportRow struct {
	No            string    `excel:"单号,width=20"`
	Warehouse     string    `excel:"仓库,width=18"`
	OutTime       time.Time `excel:"出库时间,width=20"`
	Type          string    `excel:"类型,width=10"`
	Status        string    `excel:"状态,width=10"`
	Remark        string    `excel:"备注,width=32"`
	OwnerUserName string    `excel:"负责人,width=14"`
	CreateTime    time.Time `excel:"创建时间,width=20"`
}
