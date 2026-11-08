package dto

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/qty"
)

// stock.go 是库存页/流水页的出入参（issue #90，docs/erp-mvp-spec.md §5）。
// 库存行无 owner 语义（仓库不做独立权限，spec §4.10）；产品/仓库名称服务端解析。

// StockPageReq 库存余额分页筛选：仓库×产品余额。
type StockPageReq struct {
	PageNo      int   `form:"pageNo"`
	PageSize    int   `form:"pageSize"`
	WarehouseId int64 `form:"warehouseId"`
	ProductId   int64 `form:"productId"`
}

// StockRow 库存余额展示行（名称服务端解析，spec §5 库存页）。
type StockRow struct {
	ID            int64     `json:"id"`
	ProductId     int64     `json:"productId"`
	ProductName   string    `json:"productName"`
	WarehouseId   int64     `json:"warehouseId"`
	WarehouseName string    `json:"warehouseName"`
	Count         qty.Qty   `json:"count"`
	UpdateTime    time.Time `json:"updateTime"`
}

// StockCountQuery get-count 查询参数（productId 必填；warehouseId 可选）。
type StockCountQuery struct {
	ProductId   int64 `form:"productId" binding:"required"`
	WarehouseId int64 `form:"warehouseId"`
}

// StockCountResp get-count 响应（产品全仓合计 / 单仓余额，spec §5）。
type StockCountResp struct {
	ProductId   int64   `json:"productId"`
	WarehouseId int64   `json:"warehouseId"`
	Count       qty.Qty `json:"count"`
}

// StockRecordPageReq 流水分页筛选（biz_type/仓库/产品/时间，spec §5）。
type StockRecordPageReq struct {
	PageNo      int    `form:"pageNo"`
	PageSize    int    `form:"pageSize"`
	BizType     string `form:"bizType"`
	WarehouseId int64  `form:"warehouseId"`
	ProductId   int64  `form:"productId"`
	StartTime   string `form:"startTime"`
	EndTime     string `form:"endTime"`
}

// StockRecordRow 流水展示行（count 正入负出 + total_count 结存快照）。
type StockRecordRow struct {
	ID            int64     `json:"id"`
	BizType       string    `json:"bizType"`
	BizNo         string    `json:"bizNo"`
	ProductId     int64     `json:"productId"`
	ProductName   string    `json:"productName"`
	WarehouseId   int64     `json:"warehouseId"`
	WarehouseName string    `json:"warehouseName"`
	Count         qty.Qty   `json:"count"`
	TotalCount    qty.Qty   `json:"totalCount"`
	CreateTime    time.Time `json:"createTime"`
}

// ---------- Excel 导出列（spec §4.11：库存/出入库流水） ----------

// StockExportRow 库存余额导出列。
type StockExportRow struct {
	ProductName   string    `excel:"产品名称,width=26"`
	WarehouseName string    `excel:"仓库,width=20"`
	Count         qty.Qty   `excel:"库存数量,width=14"`
	UpdateTime    time.Time `excel:"更新时间,width=20"`
}

// StockRecordExportRow 流水导出列。
type StockRecordExportRow struct {
	BizType       string    `excel:"业务类型,width=16"`
	BizNo         string    `excel:"单据编号,width=20"`
	ProductName   string    `excel:"产品名称,width=26"`
	WarehouseName string    `excel:"仓库,width=20"`
	Count         qty.Qty   `excel:"变动数量,width=14"`
	TotalCount    qty.Qty   `excel:"结存数量,width=14"`
	CreateTime    time.Time `excel:"入账时间,width=20"`
}
