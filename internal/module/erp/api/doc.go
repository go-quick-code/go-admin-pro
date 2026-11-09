package api

import (
	"github.com/gin-gonic/gin"

	"github.com/go-quick-code/go-admin-pro/internal/module/erp/doctype"
	"github.com/go-quick-code/go-admin-pro/internal/module/erp/dto"
	"github.com/go-quick-code/go-admin-pro/internal/pkg/response"
)

// ---------- 12 类单据的统一 HTTP 层（issue #125，ADR 0020）----------
// 每类单据的 7 个端点在注册表只声明一次：docSpec 内嵌 doctype 条目并挂 7 个
// 薄闭包（Page/Get/Create/Update/Status/Delete/Export），registerDocRoutes
// 循环注册 7 条路由（组内静态路由先于 /:id）。Page/Get/Create 用闭包把具体
// 返回类型擦成 any；Update/Status/Delete/Export 直接内联服务调用。
// 新增一类单据 = doctype 一行元数据 + 本文件一个 docSpec 条目。
//
// 历史：7 端点形状来自 #88 产品域并在 #89–#94 逐票复制为 12 份同构 handler；
// #125 删除 12 个 RegisterXxxRoutes 与各自 7 个方法（不留兼容层），HTTP 契约
// 逐字节不变（路径、动词、状态码、msg 文案、Excel 文件名）。

// docSpec 是一类单据的 HTTP 规格：注册表条目 + 七个端点闭包。闭包集合与
// 注册表条目用同一 key 类型（doctype.Key）索引。
type docSpec struct {
	doctype.Type
	page   gin.HandlerFunc
	get    gin.HandlerFunc
	create gin.HandlerFunc
	update gin.HandlerFunc
	status gin.HandlerFunc
	del    gin.HandlerFunc
	export gin.HandlerFunc
}

// docSpecs 返回全部单据类型的 HTTP 规格，按 doctype.Key 索引。完备性（每个
// 注册表条目都有规格、每个规格都有条目）由 api 白盒路由树单测断言
// （issue #125 S2），不在启动期 panic。
func (h *Handler) docSpecs() map[doctype.Key]docSpec {
	return map[doctype.Key]docSpec{
		doctype.StockIn.Key: {
			Type: doctype.StockIn,
			page: func(c *gin.Context) {
				var req dto.StockInPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListStockIns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetStockIn(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.StockInCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateStockIn(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.StockInUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockIn(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockInStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteStockIn(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.StockInPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportStockIns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.StockIn.ExcelFile, doctype.StockIn.ExcelTitle, data)
			},
		},
		doctype.StockOut.Key: {
			Type: doctype.StockOut,
			page: func(c *gin.Context) {
				var req dto.StockOutPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListStockOuts(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetStockOut(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.StockOutCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateStockOut(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.StockOutUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockOut(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockOutStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteStockOut(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.StockOutPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportStockOuts(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.StockOut.ExcelFile, doctype.StockOut.ExcelTitle, data)
			},
		},
		doctype.StockMove.Key: {
			Type: doctype.StockMove,
			page: func(c *gin.Context) {
				var req dto.StockMovePageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListStockMoves(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetStockMove(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.StockMoveCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateStockMove(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.StockMoveUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockMove(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockMoveStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteStockMove(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.StockMovePageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportStockMoves(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.StockMove.ExcelFile, doctype.StockMove.ExcelTitle, data)
			},
		},
		doctype.StockCheck.Key: {
			Type: doctype.StockCheck,
			page: func(c *gin.Context) {
				var req dto.StockCheckPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListStockChecks(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetStockCheck(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.StockCheckCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateStockCheck(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.StockCheckUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockCheck(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateStockCheckStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteStockCheck(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.StockCheckPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportStockChecks(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.StockCheck.ExcelFile, doctype.StockCheck.ExcelTitle, data)
			},
		},
		doctype.PurchaseOrder.Key: {
			Type: doctype.PurchaseOrder,
			page: func(c *gin.Context) {
				var req dto.PurchaseOrderPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListPurchaseOrders(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetPurchaseOrder(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.PurchaseOrderCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreatePurchaseOrder(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.PurchaseOrderUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdatePurchaseOrder(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdatePurchaseOrderStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeletePurchaseOrder(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.PurchaseOrderPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportPurchaseOrders(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.PurchaseOrder.ExcelFile, doctype.PurchaseOrder.ExcelTitle, data)
			},
		},
		doctype.PurchaseIn.Key: {
			Type: doctype.PurchaseIn,
			page: func(c *gin.Context) {
				var req dto.PurchaseInPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListPurchaseIns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetPurchaseIn(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.PurchaseInCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreatePurchaseIn(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.PurchaseInUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdatePurchaseIn(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdatePurchaseInStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeletePurchaseIn(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.PurchaseInPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportPurchaseIns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.PurchaseIn.ExcelFile, doctype.PurchaseIn.ExcelTitle, data)
			},
		},
		doctype.PurchaseReturn.Key: {
			Type: doctype.PurchaseReturn,
			page: func(c *gin.Context) {
				var req dto.PurchaseReturnPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListPurchaseReturns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetPurchaseReturn(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.PurchaseReturnCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreatePurchaseReturn(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.PurchaseReturnUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdatePurchaseReturn(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdatePurchaseReturnStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeletePurchaseReturn(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.PurchaseReturnPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportPurchaseReturns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.PurchaseReturn.ExcelFile, doctype.PurchaseReturn.ExcelTitle, data)
			},
		},
		doctype.SaleOrder.Key: {
			Type: doctype.SaleOrder,
			page: func(c *gin.Context) {
				var req dto.SaleOrderPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListSaleOrders(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetSaleOrder(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.SaleOrderCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateSaleOrder(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.SaleOrderUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateSaleOrder(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateSaleOrderStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteSaleOrder(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.SaleOrderPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportSaleOrders(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.SaleOrder.ExcelFile, doctype.SaleOrder.ExcelTitle, data)
			},
		},
		doctype.SaleOut.Key: {
			Type: doctype.SaleOut,
			page: func(c *gin.Context) {
				var req dto.SaleOutPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListSaleOuts(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetSaleOut(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.SaleOutCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateSaleOut(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.SaleOutUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateSaleOut(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateSaleOutStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteSaleOut(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.SaleOutPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportSaleOuts(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.SaleOut.ExcelFile, doctype.SaleOut.ExcelTitle, data)
			},
		},
		doctype.SaleReturn.Key: {
			Type: doctype.SaleReturn,
			page: func(c *gin.Context) {
				var req dto.SaleReturnPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListSaleReturns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetSaleReturn(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.SaleReturnCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateSaleReturn(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.SaleReturnUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateSaleReturn(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateSaleReturnStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteSaleReturn(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.SaleReturnPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportSaleReturns(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.SaleReturn.ExcelFile, doctype.SaleReturn.ExcelTitle, data)
			},
		},
		doctype.FinanceReceipt.Key: {
			Type: doctype.FinanceReceipt,
			page: func(c *gin.Context) {
				var req dto.FinanceReceiptPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListFinanceReceipts(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetFinanceReceipt(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.FinanceReceiptCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateFinanceReceipt(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.FinanceReceiptUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateFinanceReceipt(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateFinanceReceiptStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteFinanceReceipt(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.FinanceReceiptPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportFinanceReceipts(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.FinanceReceipt.ExcelFile, doctype.FinanceReceipt.ExcelTitle, data)
			},
		},
		doctype.FinancePayment.Key: {
			Type: doctype.FinancePayment,
			page: func(c *gin.Context) {
				var req dto.FinancePaymentPageReq
				if !bindQuery(c, &req) {
					return
				}
				list, total, err := h.svc.ListFinancePayments(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.Page(c, list, total)
			},
			get: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				row, err := h.svc.GetFinancePayment(c.Request.Context(), id)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			create: func(c *gin.Context) {
				var req dto.FinancePaymentCreateReq
				if !bindJSON(c, &req) {
					return
				}
				row, err := h.svc.CreateFinancePayment(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, row)
			},
			update: func(c *gin.Context) {
				var req dto.FinancePaymentUpdateReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateFinancePayment(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			status: func(c *gin.Context) {
				var req dto.DocUpdateStatusReq
				if !bindJSON(c, &req) {
					return
				}
				if err := h.svc.UpdateFinancePaymentStatus(c.Request.Context(), &req); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			del: func(c *gin.Context) {
				id, ok := pathID(c)
				if !ok {
					return
				}
				if err := h.svc.DeleteFinancePayment(c.Request.Context(), id); err != nil {
					response.Fail(c, err)
					return
				}
				response.OK(c, true)
			},
			export: func(c *gin.Context) {
				var req dto.FinancePaymentPageReq
				if !bindQuery(c, &req) {
					return
				}
				data, err := h.svc.ExportFinancePayments(c.Request.Context(), &req)
				if err != nil {
					response.Fail(c, err)
					return
				}
				h.sendExcel(c, doctype.FinancePayment.ExcelFile, doctype.FinancePayment.ExcelTitle, data)
			},
		},
	}
}

// registerDocRoutes 循环注册 12 类单据的路由：每类 7 条，组内静态路由
// （/page、/export-excel、/update-status）先于 /:id。权限码由注册表条目的
// PermPrefix 派生（ADR 0020），不再逐条手写。
func (h *Handler) registerDocRoutes(rg *gin.RouterGroup) {
	specs := h.docSpecs()
	for _, entry := range doctype.All() {
		spec, ok := specs[entry.Key]
		if !ok {
			// 完备性由 S2 路由树单测断言，缺规格不 panic。
			continue
		}
		doc := rg.Group("/erp/" + string(spec.Key))
		doc.GET("/page", h.requirePermission(spec.Perm(doctype.ActionQuery)), spec.page)
		doc.GET("/export-excel", h.requirePermission(spec.Perm(doctype.ActionExport)), spec.export)
		doc.PUT("/update-status", h.requirePermission(spec.Perm(doctype.ActionApprove)), spec.status)
		doc.GET("/:id", h.requirePermission(spec.Perm(doctype.ActionQuery)), spec.get)
		doc.POST("", h.requirePermission(spec.Perm(doctype.ActionCreate)), spec.create)
		doc.PUT("", h.requirePermission(spec.Perm(doctype.ActionUpdate)), spec.update)
		doc.DELETE("/:id", h.requirePermission(spec.Perm(doctype.ActionDelete)), spec.del)
	}
}
