import request from '@/utils/request'

// ---------- ERP 产品分类（issue #88，docs/erp-mvp-spec.md §5） ----------
// GET list 全量返回扁平列表，树组装在前端（spec §5，与 CRM 的 /tree 端点刻意不同）。
export function listProductCategories() {
  return request.get('/erp/product-category/list') as Promise<any>
}
export function getProductCategory(id: number) {
  return request.get(`/erp/product-category/${id}`) as Promise<any>
}
export function createProductCategory(data: any) {
  return request.post('/erp/product-category', data) as Promise<any>
}
export function updateProductCategory(data: any) {
  return request.put('/erp/product-category', data) as Promise<any>
}
export function deleteProductCategory(id: number) {
  return request.delete(`/erp/product-category/${id}`) as Promise<any>
}

// ---------- ERP 产品单位（仅名称字典，无换算率，spec §3.1） ----------
export function pageProductUnits(params: any) {
  return request.get('/erp/product-unit/page', { params }) as Promise<any>
}
export function getProductUnit(id: number) {
  return request.get(`/erp/product-unit/${id}`) as Promise<any>
}
export function createProductUnit(data: any) {
  return request.post('/erp/product-unit', data) as Promise<any>
}
export function updateProductUnit(data: any) {
  return request.put('/erp/product-unit', data) as Promise<any>
}
export function deleteProductUnit(id: number) {
  return request.delete(`/erp/product-unit/${id}`) as Promise<any>
}

// ---------- ERP 产品（条码/规格/三个 Money 参考价/保质期/重量展示字段） ----------
// 金额一律十进制字符串（如 "59.90"），重量毫单位定点十进制字符串（如 "0.150"，ADR 0017）；
// 分类/单位被产品引用时后端 409 拒绝删除。
export function pageProducts(params: any) {
  return request.get('/erp/product/page', { params }) as Promise<any>
}
export function getProduct(id: number) {
  return request.get(`/erp/product/${id}`) as Promise<any>
}
export function createProduct(data: any) {
  return request.post('/erp/product', data) as Promise<any>
}
export function updateProduct(data: any) {
  return request.put('/erp/product', data) as Promise<any>
}
export function deleteProduct(id: number) {
  return request.delete(`/erp/product/${id}`) as Promise<any>
}

// ---------- Excel 导出（issue #88，spec §4.11：ERP 只导出不导入） ----------
// 导出返回 xlsx 二进制（responseType: blob），复用列表筛选与数据权限口径。
function downloadExcel(path: string, params?: Record<string, any>) {
  return request.get(path, { params, responseType: 'blob' }) as Promise<any>
}
export function exportProductCategories() {
  return downloadExcel('/erp/product-category/export-excel')
}
export function exportProductUnits(params?: Record<string, any>) {
  return downloadExcel('/erp/product-unit/export-excel', params)
}
export function exportProducts(params?: Record<string, any>) {
  return downloadExcel('/erp/product/export-excel', params)
}

// ---------- ERP 仓库（issue #89，spec §3.1：address/principal 纯展示，status 0 启用/1 停用） ----------
export function pageWarehouses(params: any) {
  return request.get('/erp/warehouse/page', { params }) as Promise<any>
}
export function getWarehouse(id: number) {
  return request.get(`/erp/warehouse/${id}`) as Promise<any>
}
export function createWarehouse(data: any) {
  return request.post('/erp/warehouse', data) as Promise<any>
}
export function updateWarehouse(data: any) {
  return request.put('/erp/warehouse', data) as Promise<any>
}
export function deleteWarehouse(id: number) {
  return request.delete(`/erp/warehouse/${id}`) as Promise<any>
}

// ---------- ERP 客户（issue #89，spec §3.1：与 CRM 客户完全独立，ADR 0015） ----------
// 税率以十进制字符串提交（如 "13.00"，两位小数，0–999.99），未提交保持 NULL。
export function pageCustomers(params: any) {
  return request.get('/erp/customer/page', { params }) as Promise<any>
}
export function getCustomer(id: number) {
  return request.get(`/erp/customer/${id}`) as Promise<any>
}
export function createCustomer(data: any) {
  return request.post('/erp/customer', data) as Promise<any>
}
export function updateCustomer(data: any) {
  return request.put('/erp/customer', data) as Promise<any>
}
export function deleteCustomer(id: number) {
  return request.delete(`/erp/customer/${id}`) as Promise<any>
}

// ---------- ERP 供应商（issue #89，spec §3.1：字段同 ERP 客户） ----------
export function pageSuppliers(params: any) {
  return request.get('/erp/supplier/page', { params }) as Promise<any>
}
export function getSupplier(id: number) {
  return request.get(`/erp/supplier/${id}`) as Promise<any>
}
export function createSupplier(data: any) {
  return request.post('/erp/supplier', data) as Promise<any>
}
export function updateSupplier(data: any) {
  return request.put('/erp/supplier', data) as Promise<any>
}
export function deleteSupplier(id: number) {
  return request.delete(`/erp/supplier/${id}`) as Promise<any>
}

// ---------- Excel 导出（issue #89，spec §4.11：仓库/客户/供应商） ----------
export function exportWarehouses(params?: Record<string, any>) {
  return downloadExcel('/erp/warehouse/export-excel', params)
}
export function exportCustomers(params?: Record<string, any>) {
  return downloadExcel('/erp/customer/export-excel', params)
}
export function exportSuppliers(params?: Record<string, any>) {
  return downloadExcel('/erp/supplier/export-excel', params)
}

// ---------- ERP 产品库存（issue #90，spec §5：仓库×产品余额 + 全仓合计） ----------
// count 为毫单位定点十进制字符串（如 "12.500"，ADR 0017）；库存行无 owner 语义
//（仓库不做独立权限），租户内全量可见。get-count：带 warehouseId 返回单仓余额，
// 不带返回产品全仓合计。
export function pageStocks(params: any) {
  return request.get('/erp/stock/page', { params }) as Promise<any>
}
export function getStockCount(params: { productId: number; warehouseId?: number }) {
  return request.get('/erp/stock/get-count', { params }) as Promise<any>
}
export function exportStocks(params?: Record<string, any>) {
  return downloadExcel('/erp/stock/export-excel', params)
}

// ---------- ERP 出入库流水（append-only，只读查询页） ----------
// bizType 与单据表名一致 + _CANCEL 对（如 stock_in / stock_in_CANCEL）。
export function pageStockRecords(params: any) {
  return request.get('/erp/stock-record/page', { params }) as Promise<any>
}
export function exportStockRecords(params?: Record<string, any>) {
  return downloadExcel('/erp/stock-record/export-excel', params)
}

// ---------- ERP 其它入库单（QR）/其它出库单（QC，issue #90，spec §3.5/§4.1） ----------
// 单号由服务端经 erp_sequence 发放（入参无单号字段，伪造不可采纳）；行金额
// 服务端按 单价×数量 计算（入参无 totalPrice 字段）。审核/反审核走
// update-status（幂等目标态：20 审核 / 10 反审核），approve 为独立权限码。
export function pageStockIns(params: any) {
  return request.get('/erp/stock-in/page', { params }) as Promise<any>
}
export function getStockIn(id: number) {
  return request.get(`/erp/stock-in/${id}`) as Promise<any>
}
export function createStockIn(data: any) {
  return request.post('/erp/stock-in', data) as Promise<any>
}
export function updateStockIn(data: any) {
  return request.put('/erp/stock-in', data) as Promise<any>
}
export function deleteStockIn(id: number) {
  return request.delete(`/erp/stock-in/${id}`) as Promise<any>
}
export function updateStockInStatus(data: { id: number; status: number }) {
  return request.put('/erp/stock-in/update-status', data) as Promise<any>
}
export function exportStockIns(params?: Record<string, any>) {
  return downloadExcel('/erp/stock-in/export-excel', params)
}

export function pageStockOuts(params: any) {
  return request.get('/erp/stock-out/page', { params }) as Promise<any>
}
export function getStockOut(id: number) {
  return request.get(`/erp/stock-out/${id}`) as Promise<any>
}
export function createStockOut(data: any) {
  return request.post('/erp/stock-out', data) as Promise<any>
}
export function updateStockOut(data: any) {
  return request.put('/erp/stock-out', data) as Promise<any>
}
export function deleteStockOut(id: number) {
  return request.delete(`/erp/stock-out/${id}`) as Promise<any>
}
export function updateStockOutStatus(data: { id: number; status: number }) {
  return request.put('/erp/stock-out/update-status', data) as Promise<any>
}
export function exportStockOuts(params?: Record<string, any>) {
  return downloadExcel('/erp/stock-out/export-excel', params)
}

// ---------- ERP 销售三单（issue #92，spec §3.4/§4.1/§4.5） ----------
// 销售订单（XS）/ 销售出库（CK）/ 销售退货（ST）：单号由服务端经 erp_sequence
// 发放（入参无单号/行金额字段，伪造不可采纳）；出库必挂已审核销售订单（orderId），
// 退货必挂已审核销售出库（outId，仓库服务端取自出库单，入参不传）；审核/反审核
// 走 update-status（幂等目标态：20 审核 / 10 反审核），approve 为独立权限码；
// 进度（outCount/returnCount）与核销金额（receiptPrice/refundPrice）服务端回写。
export function pageSaleOrders(params: any) {
  return request.get('/erp/sale-order/page', { params }) as Promise<any>
}
export function getSaleOrder(id: number) {
  return request.get(`/erp/sale-order/${id}`) as Promise<any>
}
export function createSaleOrder(data: any) {
  return request.post('/erp/sale-order', data) as Promise<any>
}
export function updateSaleOrder(data: any) {
  return request.put('/erp/sale-order', data) as Promise<any>
}
export function deleteSaleOrder(id: number) {
  return request.delete(`/erp/sale-order/${id}`) as Promise<any>
}
export function updateSaleOrderStatus(data: { id: number; status: number }) {
  return request.put('/erp/sale-order/update-status', data) as Promise<any>
}
export function exportSaleOrders(params?: Record<string, any>) {
  return downloadExcel('/erp/sale-order/export-excel', params)
}

export function pageSaleOuts(params: any) {
  return request.get('/erp/sale-out/page', { params }) as Promise<any>
}
export function getSaleOut(id: number) {
  return request.get(`/erp/sale-out/${id}`) as Promise<any>
}
export function createSaleOut(data: any) {
  return request.post('/erp/sale-out', data) as Promise<any>
}
export function updateSaleOut(data: any) {
  return request.put('/erp/sale-out', data) as Promise<any>
}
export function deleteSaleOut(id: number) {
  return request.delete(`/erp/sale-out/${id}`) as Promise<any>
}
export function updateSaleOutStatus(data: { id: number; status: number }) {
  return request.put('/erp/sale-out/update-status', data) as Promise<any>
}
export function exportSaleOuts(params?: Record<string, any>) {
  return downloadExcel('/erp/sale-out/export-excel', params)
}

export function pageSaleReturns(params: any) {
  return request.get('/erp/sale-return/page', { params }) as Promise<any>
}
export function getSaleReturn(id: number) {
  return request.get(`/erp/sale-return/${id}`) as Promise<any>
}
export function createSaleReturn(data: any) {
  return request.post('/erp/sale-return', data) as Promise<any>
}
export function updateSaleReturn(data: any) {
  return request.put('/erp/sale-return', data) as Promise<any>
}
export function deleteSaleReturn(id: number) {
  return request.delete(`/erp/sale-return/${id}`) as Promise<any>
}
export function updateSaleReturnStatus(data: { id: number; status: number }) {
  return request.put('/erp/sale-return/update-status', data) as Promise<any>
}
export function exportSaleReturns(params?: Record<string, any>) {
  return downloadExcel('/erp/sale-return/export-excel', params)
}

// ---------- ERP 采购订单（CG，issue #91，spec §3.3/§4.4） ----------
// 单号由服务端经 erp_sequence 发放（入参无单号字段，伪造不可采纳）；行金额服务端
// 按 单价×数量 计算（入参无 totalPrice 字段）；明细 inCount/returnCount 为服务端
// 重算的进度回写（只计已审核单）。审核/反审核走 update-status（幂等目标态：
// 20 审核 / 10 反审核），approve 为独立权限码；存在已审核入库/退货时订单不可反审核（409）。
export function pagePurchaseOrders(params: any) {
  return request.get('/erp/purchase-order/page', { params }) as Promise<any>
}
export function getPurchaseOrder(id: number) {
  return request.get(`/erp/purchase-order/${id}`) as Promise<any>
}
export function createPurchaseOrder(data: any) {
  return request.post('/erp/purchase-order', data) as Promise<any>
}
export function updatePurchaseOrder(data: any) {
  return request.put('/erp/purchase-order', data) as Promise<any>
}
export function deletePurchaseOrder(id: number) {
  return request.delete(`/erp/purchase-order/${id}`) as Promise<any>
}
export function updatePurchaseOrderStatus(data: { id: number; status: number }) {
  return request.put('/erp/purchase-order/update-status', data) as Promise<any>
}
export function exportPurchaseOrders(params?: Record<string, any>) {
  return downloadExcel('/erp/purchase-order/export-excel', params)
}

// ---------- ERP 采购入库单（RK，issue #91，spec §3.3/§4.4） ----------
// 必挂已审核采购订单（orderId 必填，前端挂单选择只出已审核单 status=20）；审核后
// 增库存并回写订单 in_count；反审核生成反向流水回滚（存在已审核退货时 409）。
// paymentPrice 为核销回写列（#94），只读展示。
export function pagePurchaseIns(params: any) {
  return request.get('/erp/purchase-in/page', { params }) as Promise<any>
}
export function getPurchaseIn(id: number) {
  return request.get(`/erp/purchase-in/${id}`) as Promise<any>
}
export function createPurchaseIn(data: any) {
  return request.post('/erp/purchase-in', data) as Promise<any>
}
export function updatePurchaseIn(data: any) {
  return request.put('/erp/purchase-in', data) as Promise<any>
}
export function deletePurchaseIn(id: number) {
  return request.delete(`/erp/purchase-in/${id}`) as Promise<any>
}
export function updatePurchaseInStatus(data: { id: number; status: number }) {
  return request.put('/erp/purchase-in/update-status', data) as Promise<any>
}
export function exportPurchaseIns(params?: Record<string, any>) {
  return downloadExcel('/erp/purchase-in/export-excel', params)
}

// ---------- ERP 采购退货单（RT，issue #91，spec §3.3/§4.4） ----------
// 必挂已审核采购入库单（inId 必填，前端挂单选择只出已审核单 status=20）；仓库取自
// 入库单（入参无仓库字段）；审核后减库存并回写订单 return_count（经入库单关联）。
// refundPrice 为核销回写列（#94），只读展示。
export function pagePurchaseReturns(params: any) {
  return request.get('/erp/purchase-return/page', { params }) as Promise<any>
}
export function getPurchaseReturn(id: number) {
  return request.get(`/erp/purchase-return/${id}`) as Promise<any>
}
export function createPurchaseReturn(data: any) {
  return request.post('/erp/purchase-return', data) as Promise<any>
}
export function updatePurchaseReturn(data: any) {
  return request.put('/erp/purchase-return', data) as Promise<any>
}
export function deletePurchaseReturn(id: number) {
  return request.delete(`/erp/purchase-return/${id}`) as Promise<any>
}
export function updatePurchaseReturnStatus(data: { id: number; status: number }) {
  return request.put('/erp/purchase-return/update-status', data) as Promise<any>
}
export function exportPurchaseReturns(params?: Record<string, any>) {
  return downloadExcel('/erp/purchase-return/export-excel', params)
}

// ---------- ERP 库存调拨单（DB，issue #93，spec §3.5/§4.6） ----------
// 单号由服务端经 erp_sequence 发放（入参无单号字段）；审核时同一事务内出仓
// 负、入仓正两笔流水同额成对（两仓必须不同、源仓不足 409）；反审核各生成
// 反向流水。审核/反审核走 update-status（幂等目标态：20 审核 / 10 反审核），
// approve 为独立权限码。
export function pageStockMoves(params: any) {
  return request.get('/erp/stock-move/page', { params }) as Promise<any>
}
export function getStockMove(id: number) {
  return request.get(`/erp/stock-move/${id}`) as Promise<any>
}
export function createStockMove(data: any) {
  return request.post('/erp/stock-move', data) as Promise<any>
}
export function updateStockMove(data: any) {
  return request.put('/erp/stock-move', data) as Promise<any>
}
export function deleteStockMove(id: number) {
  return request.delete(`/erp/stock-move/${id}`) as Promise<any>
}
export function updateStockMoveStatus(data: { id: number; status: number }) {
  return request.put('/erp/stock-move/update-status', data) as Promise<any>
}
export function exportStockMoves(params?: Record<string, any>) {
  return downloadExcel('/erp/stock-move/export-excel', params)
}

// ---------- ERP 库存盘点单（PD，issue #93，spec §3.5/§4.6） ----------
// 明细 actualCount 为实际清点数（≥ 0，十进制字符串）；baseCount 为创建时账面
// 数快照（服务端写入、只读展示，审核以当时账面为准，入参无该字段）。审核按
// 「差异 = actualCount − 当时账面数」调整库存（差正盘盈、差负盘亏），审核后
// 账面 = 实际数；反审核按已入账流水净额反向。
export function pageStockChecks(params: any) {
  return request.get('/erp/stock-check/page', { params }) as Promise<any>
}
export function getStockCheck(id: number) {
  return request.get(`/erp/stock-check/${id}`) as Promise<any>
}
export function createStockCheck(data: any) {
  return request.post('/erp/stock-check', data) as Promise<any>
}
export function updateStockCheck(data: any) {
  return request.put('/erp/stock-check', data) as Promise<any>
}
export function deleteStockCheck(id: number) {
  return request.delete(`/erp/stock-check/${id}`) as Promise<any>
}
export function updateStockCheckStatus(data: { id: number; status: number }) {
  return request.put('/erp/stock-check/update-status', data) as Promise<any>
}
export function exportStockChecks(params?: Record<string, any>) {
  return downloadExcel('/erp/stock-check/export-excel', params)
}
