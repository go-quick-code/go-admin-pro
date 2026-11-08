package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock_move_check.go 是库存调拨单（DB）/库存盘点单（PD）的出入参（issue #93，
// docs/erp-mvp-spec.md §3.5/§4.6/§5）。
//
// 契约要点（与 00030 两类杂项单据同构）：
//   - 数量以十进制字符串提交（qty 定点，不经 float64）；调拨 count > 0，
//     盘点 actualCount ≥ 0；
//   - 单号（no）由服务端经 erp_sequence 发放，创建/修改入参没有单号字段
//     ——客户端伪造从契约上不可采纳（spec §4.8）；
//   - 盘点 baseCount 是服务端在创建/编辑保存时的账面快照（仅记录），入参
//     没有该字段，审核也不使用它（spec §4.6）；
//   - update-status 以目标态提交（幂等）：20 审核、10 反审核。

// ---------- 库存调拨单（DB） ----------

// StockMoveItemPayload 调拨明细条目（产品 + 数量，无单价/行金额）。
type StockMoveItemPayload struct {
	ProductId int64   `json:"productId" binding:"required"`
	Count     qty.Qty `json:"count"`
}

// StockMoveCreateReq 新建库存调拨单。
type StockMoveCreateReq struct {
	FromWarehouseId int64                  `json:"fromWarehouseId" binding:"required"`
	ToWarehouseId   int64                  `json:"toWarehouseId" binding:"required"`
	MoveTime        string                 `json:"moveTime" binding:"required"`
	Items           []StockMoveItemPayload `json:"items" binding:"required"`
}

// StockMoveUpdateReq 修改库存调拨单（仅未审核）。
type StockMoveUpdateReq struct {
	ID              int64                  `json:"id" binding:"required"`
	FromWarehouseId int64                  `json:"fromWarehouseId" binding:"required"`
	ToWarehouseId   int64                  `json:"toWarehouseId" binding:"required"`
	MoveTime        string                 `json:"moveTime" binding:"required"`
	Items           []StockMoveItemPayload `json:"items" binding:"required"`
}

// StockMovePageReq 库存调拨单分页筛选。
type StockMovePageReq struct {
	PageNo          int    `form:"pageNo"`
	PageSize        int    `form:"pageSize"`
	No              string `form:"no"`
	FromWarehouseId int64  `form:"fromWarehouseId"`
	ToWarehouseId   int64  `form:"toWarehouseId"`
	Status          *int8  `form:"status"`
	StartTime       string `form:"startTime"`
	EndTime         string `form:"endTime"`
}

// StockMoveRow 库存调拨单分页展示行。
type StockMoveRow struct {
	ID                int64     `json:"id"`
	No                string    `json:"no"`
	FromWarehouseId   int64     `json:"fromWarehouseId"`
	FromWarehouseName string    `json:"fromWarehouseName"`
	ToWarehouseId     int64     `json:"toWarehouseId"`
	ToWarehouseName   string    `json:"toWarehouseName"`
	MoveTime          time.Time `json:"moveTime"`
	Status            int8      `json:"status"`
	StatusName        string    `json:"statusName"`
	CreateTime        time.Time `json:"createTime"`
}

// StockMoveItemRow 调拨单明细展示行。
type StockMoveItemRow struct {
	ID          int64   `json:"id"`
	ProductId   int64   `json:"productId"`
	ProductName string  `json:"productName"`
	Count       qty.Qty `json:"count"`
}

// StockMoveDetail 调拨单详情（头 + 明细）。
type StockMoveDetail struct {
	ID                int64              `json:"id"`
	No                string             `json:"no"`
	FromWarehouseId   int64              `json:"fromWarehouseId"`
	FromWarehouseName string             `json:"fromWarehouseName"`
	ToWarehouseId     int64              `json:"toWarehouseId"`
	ToWarehouseName   string             `json:"toWarehouseName"`
	MoveTime          time.Time          `json:"moveTime"`
	Status            int8               `json:"status"`
	StatusName        string             `json:"statusName"`
	Items             []StockMoveItemRow `json:"items"`
	CreateTime        time.Time          `json:"createTime"`
}

// ---------- 库存盘点单（PD） ----------

// StockCheckItemPayload 盘点明细条目（产品 + 实际清点数，baseCount 服务端快照）。
type StockCheckItemPayload struct {
	ProductId   int64   `json:"productId" binding:"required"`
	ActualCount qty.Qty `json:"actualCount"`
}

// StockCheckCreateReq 新建库存盘点单。
type StockCheckCreateReq struct {
	WarehouseId int64                   `json:"warehouseId" binding:"required"`
	CheckTime   string                  `json:"checkTime" binding:"required"`
	Remark      string                  `json:"remark" binding:"max=500"`
	Items       []StockCheckItemPayload `json:"items" binding:"required"`
}

// StockCheckUpdateReq 修改库存盘点单（仅未审核）。
type StockCheckUpdateReq struct {
	ID          int64                   `json:"id" binding:"required"`
	WarehouseId int64                   `json:"warehouseId" binding:"required"`
	CheckTime   string                  `json:"checkTime" binding:"required"`
	Remark      string                  `json:"remark" binding:"max=500"`
	Items       []StockCheckItemPayload `json:"items" binding:"required"`
}

// StockCheckPageReq 库存盘点单分页筛选。
type StockCheckPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	No          string `form:"no"`
	WarehouseId int64  `form:"warehouseId"`
	Status      *int8  `form:"status"`
	StartTime   string `form:"startTime"`
	EndTime     string `form:"endTime"`
}

// StockCheckRow 库存盘点单分页展示行。
type StockCheckRow struct {
	ID            int64     `json:"id"`
	No            string    `json:"no"`
	WarehouseId   int64     `json:"warehouseId"`
	WarehouseName string    `json:"warehouseName"`
	CheckTime     time.Time `json:"checkTime"`
	Status        int8      `json:"status"`
	StatusName    string    `json:"statusName"`
	Remark        string    `json:"remark"`
	CreateTime    time.Time `json:"createTime"`
}

// StockCheckItemRow 盘点单明细展示行（baseCount 为创建时账面快照，仅记录）。
type StockCheckItemRow struct {
	ID          int64   `json:"id"`
	ProductId   int64   `json:"productId"`
	ProductName string  `json:"productName"`
	ActualCount qty.Qty `json:"actualCount"`
	BaseCount   qty.Qty `json:"baseCount"`
}

// StockCheckDetail 盘点单详情（头 + 明细）。
type StockCheckDetail struct {
	ID            int64               `json:"id"`
	No            string              `json:"no"`
	WarehouseId   int64               `json:"warehouseId"`
	WarehouseName string              `json:"warehouseName"`
	CheckTime     time.Time           `json:"checkTime"`
	Status        int8                `json:"status"`
	StatusName    string              `json:"statusName"`
	Remark        string              `json:"remark"`
	Items         []StockCheckItemRow `json:"items"`
	CreateTime    time.Time           `json:"createTime"`
}

// ---------- Excel 导出列（spec §4.11：十二类单据列表导出，本票两类） ----------

// StockMoveExportRow 库存调拨单导出列。
type StockMoveExportRow struct {
	No            string    `excel:"单号,width=20"`
	FromWarehouse string    `excel:"调出仓库,width=18"`
	ToWarehouse   string    `excel:"调入仓库,width=18"`
	MoveTime      time.Time `excel:"调拨时间,width=20"`
	Status        string    `excel:"状态,width=10"`
	OwnerUserName string    `excel:"负责人,width=14"`
	CreateTime    time.Time `excel:"创建时间,width=20"`
}

// StockCheckExportRow 库存盘点单导出列。
type StockCheckExportRow struct {
	No            string    `excel:"单号,width=20"`
	Warehouse     string    `excel:"仓库,width=18"`
	CheckTime     time.Time `excel:"盘点时间,width=20"`
	Status        string    `excel:"状态,width=10"`
	Remark        string    `excel:"备注,width=32"`
	OwnerUserName string    `excel:"负责人,width=14"`
	CreateTime    time.Time `excel:"创建时间,width=20"`
}
