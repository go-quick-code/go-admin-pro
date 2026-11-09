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

// ---------- ERP 单据客户端工厂（issue #125，ADR 0020 单据类型注册表的前端侧） ----------
// 12 类单据（其它出入库/调拨/盘点、采购三单、销售三单、收付款单）的 7 个端点
// 完全同构，仅 URL 段不同：本工厂是这 12 类单据 API 的唯一实现，行为与重构前的
// 手写函数逐字一致。下方每类单据先在模块级建一个 client 单例（resource 字面量
// 全文件仅出现 12 次，编译器可拦住单组拼错），再从单例上解构出 84 个具名导出；
// 这 84 个具名导出是本段唯一对外面，导出名与参数形态不变，视图零改动。
function createDocClient(resource: string) {
  return {
    page: (params: any) => request.get(`/erp/${resource}/page`, { params }) as Promise<any>,
    get: (id: number) => request.get(`/erp/${resource}/${id}`) as Promise<any>,
    create: (data: any) => request.post(`/erp/${resource}`, data) as Promise<any>,
    update: (data: any) => request.put(`/erp/${resource}`, data) as Promise<any>,
    delete: (id: number) => request.delete(`/erp/${resource}/${id}`) as Promise<any>,
    updateStatus: (data: { id: number; status: number }) => request.put(`/erp/${resource}/update-status`, data) as Promise<any>,
    export: (params?: Record<string, any>) => downloadExcel(`/erp/${resource}/export-excel`, params),
  }
}

// ---------- ERP 其它入库单（QR）/其它出库单（QC，issue #90，spec §3.5/§4.1） ----------
// 单号由服务端经 erp_sequence 发放（入参无单号字段，伪造不可采纳）；行金额
// 服务端按 单价×数量 计算（入参无 totalPrice 字段）。审核/反审核走
// update-status（幂等目标态：20 审核 / 10 反审核），approve 为独立权限码。
const stockIn = createDocClient('stock-in')
export const pageStockIns = stockIn.page
export const getStockIn = stockIn.get
export const createStockIn = stockIn.create
export const updateStockIn = stockIn.update
export const deleteStockIn = stockIn.delete
export const updateStockInStatus = stockIn.updateStatus
export const exportStockIns = stockIn.export

const stockOut = createDocClient('stock-out')
export const pageStockOuts = stockOut.page
export const getStockOut = stockOut.get
export const createStockOut = stockOut.create
export const updateStockOut = stockOut.update
export const deleteStockOut = stockOut.delete
export const updateStockOutStatus = stockOut.updateStatus
export const exportStockOuts = stockOut.export

// ---------- ERP 销售三单（issue #92，spec §3.4/§4.1/§4.5） ----------
// 销售订单（XS）/ 销售出库（CK）/ 销售退货（ST）：单号由服务端经 erp_sequence
// 发放（入参无单号/行金额字段，伪造不可采纳）；出库必挂已审核销售订单（orderId），
// 退货必挂已审核销售出库（outId，仓库服务端取自出库单，入参不传）；审核/反审核
// 走 update-status（幂等目标态：20 审核 / 10 反审核），approve 为独立权限码；
// 进度（outCount/returnCount）与核销金额（receiptPrice/refundPrice）服务端回写。
const saleOrder = createDocClient('sale-order')
export const pageSaleOrders = saleOrder.page
export const getSaleOrder = saleOrder.get
export const createSaleOrder = saleOrder.create
export const updateSaleOrder = saleOrder.update
export const deleteSaleOrder = saleOrder.delete
export const updateSaleOrderStatus = saleOrder.updateStatus
export const exportSaleOrders = saleOrder.export

const saleOut = createDocClient('sale-out')
export const pageSaleOuts = saleOut.page
export const getSaleOut = saleOut.get
export const createSaleOut = saleOut.create
export const updateSaleOut = saleOut.update
export const deleteSaleOut = saleOut.delete
export const updateSaleOutStatus = saleOut.updateStatus
export const exportSaleOuts = saleOut.export

const saleReturn = createDocClient('sale-return')
export const pageSaleReturns = saleReturn.page
export const getSaleReturn = saleReturn.get
export const createSaleReturn = saleReturn.create
export const updateSaleReturn = saleReturn.update
export const deleteSaleReturn = saleReturn.delete
export const updateSaleReturnStatus = saleReturn.updateStatus
export const exportSaleReturns = saleReturn.export

// ---------- ERP 采购订单（CG，issue #91，spec §3.3/§4.4） ----------
// 单号由服务端经 erp_sequence 发放（入参无单号字段，伪造不可采纳）；行金额服务端
// 按 单价×数量 计算（入参无 totalPrice 字段）；明细 inCount/returnCount 为服务端
// 重算的进度回写（只计已审核单）。审核/反审核走 update-status（幂等目标态：
// 20 审核 / 10 反审核），approve 为独立权限码；存在已审核入库/退货时订单不可反审核（409）。
const purchaseOrder = createDocClient('purchase-order')
export const pagePurchaseOrders = purchaseOrder.page
export const getPurchaseOrder = purchaseOrder.get
export const createPurchaseOrder = purchaseOrder.create
export const updatePurchaseOrder = purchaseOrder.update
export const deletePurchaseOrder = purchaseOrder.delete
export const updatePurchaseOrderStatus = purchaseOrder.updateStatus
export const exportPurchaseOrders = purchaseOrder.export

// ---------- ERP 采购入库单（RK，issue #91，spec §3.3/§4.4） ----------
// 必挂已审核采购订单（orderId 必填，前端挂单选择只出已审核单 status=20）；审核后
// 增库存并回写订单 in_count；反审核生成反向流水回滚（存在已审核退货时 409）。
// paymentPrice 为核销回写列（#94），只读展示。
const purchaseIn = createDocClient('purchase-in')
export const pagePurchaseIns = purchaseIn.page
export const getPurchaseIn = purchaseIn.get
export const createPurchaseIn = purchaseIn.create
export const updatePurchaseIn = purchaseIn.update
export const deletePurchaseIn = purchaseIn.delete
export const updatePurchaseInStatus = purchaseIn.updateStatus
export const exportPurchaseIns = purchaseIn.export

// ---------- ERP 采购退货单（RT，issue #91，spec §3.3/§4.4） ----------
// 必挂已审核采购入库单（inId 必填，前端挂单选择只出已审核单 status=20）；仓库取自
// 入库单（入参无仓库字段）；审核后减库存并回写订单 return_count（经入库单关联）。
// refundPrice 为核销回写列（#94），只读展示。
const purchaseReturn = createDocClient('purchase-return')
export const pagePurchaseReturns = purchaseReturn.page
export const getPurchaseReturn = purchaseReturn.get
export const createPurchaseReturn = purchaseReturn.create
export const updatePurchaseReturn = purchaseReturn.update
export const deletePurchaseReturn = purchaseReturn.delete
export const updatePurchaseReturnStatus = purchaseReturn.updateStatus
export const exportPurchaseReturns = purchaseReturn.export

// ---------- ERP 结算账户（issue #94，spec §3.6/§4.7） ----------
// 无余额入参：列表/详情返回「已审核收付款净额」实时余额列（不落库）；默认账户
// 租户内唯一互斥（置默认取消原默认）；被收付款单引用时删除 409。
export function pageFinanceAccounts(params: any) {
  return request.get('/erp/finance-account/page', { params }) as Promise<any>
}
export function getFinanceAccount(id: number) {
  return request.get(`/erp/finance-account/${id}`) as Promise<any>
}
export function createFinanceAccount(data: any) {
  return request.post('/erp/finance-account', data) as Promise<any>
}
export function updateFinanceAccount(data: any) {
  return request.put('/erp/finance-account', data) as Promise<any>
}
export function deleteFinanceAccount(id: number) {
  return request.delete(`/erp/finance-account/${id}`) as Promise<any>
}

// ---------- ERP 收款单（SK，issue #94，spec §3.6/§4.7） ----------
// 明细只挂已审核销售出库（bizType=sale_out）/销售退货（sale_return），price 为
// 本次核销金额（审核时校验「本单合计 + 其它已审核核销 ≤ 单据总额」，超限 409）；
// 审核回写 sale_out.receiptPrice / sale_return.refundPrice，反审核重算释放额度；
// 单号服务端发放（入参无单号字段）。
const financeReceipt = createDocClient('finance-receipt')
export const pageFinanceReceipts = financeReceipt.page
export const getFinanceReceipt = financeReceipt.get
export const createFinanceReceipt = financeReceipt.create
export const updateFinanceReceipt = financeReceipt.update
export const deleteFinanceReceipt = financeReceipt.delete
export const updateFinanceReceiptStatus = financeReceipt.updateStatus
export const exportFinanceReceipts = financeReceipt.export

// ---------- ERP 付款单（FK，issue #94，spec §3.6/§4.7） ----------
// 收款单镜像：明细只挂已审核采购入库（bizType=purchase_in）/采购退货
// （purchase_return）；审核回写 purchase_in.paymentPrice /
// purchase_return.refundPrice，反审核重算释放额度。
const financePayment = createDocClient('finance-payment')
export const pageFinancePayments = financePayment.page
export const getFinancePayment = financePayment.get
export const createFinancePayment = financePayment.create
export const updateFinancePayment = financePayment.update
export const deleteFinancePayment = financePayment.delete
export const updateFinancePaymentStatus = financePayment.updateStatus
export const exportFinancePayments = financePayment.export

// ---------- ERP 首页统计（issue #95，spec §5/§6：菜单 701，权限码 erp:statistics:query） ----------
// 口径：销售额 = Σ已审核销售出库 totalPrice（按 outTime 落区间）− Σ已审核销售退货
// totalPrice（按 returnTime）；采购 = 采购入库 − 采购退货（inTime/returnTime）。
// summary 返回今日/昨日/本月/今年四值；time-summary 返回近 N 个月（默认 6）按月，
// 金额均为十进制字符串（ADR 0007），数据范围与租户隔离由后端保证。
export function getSaleStatisticsSummary() {
  return request.get('/erp/sale-statistics/summary') as Promise<any>
}
export function getSaleStatisticsTimeSummary(params?: { months?: number }) {
  return request.get('/erp/sale-statistics/time-summary', { params }) as Promise<any>
}
export function getPurchaseStatisticsSummary() {
  return request.get('/erp/purchase-statistics/summary') as Promise<any>
}
export function getPurchaseStatisticsTimeSummary(params?: { months?: number }) {
  return request.get('/erp/purchase-statistics/time-summary', { params }) as Promise<any>
}

// ---------- ERP 库存调拨单（DB，issue #93，spec §3.5/§4.6） ----------
// 单号由服务端经 erp_sequence 发放（入参无单号字段）；审核时同一事务内出仓
// 负、入仓正两笔流水同额成对（两仓必须不同、源仓不足 409）；反审核各生成
// 反向流水。审核/反审核走 update-status（幂等目标态：20 审核 / 10 反审核），
// approve 为独立权限码。
const stockMove = createDocClient('stock-move')
export const pageStockMoves = stockMove.page
export const getStockMove = stockMove.get
export const createStockMove = stockMove.create
export const updateStockMove = stockMove.update
export const deleteStockMove = stockMove.delete
export const updateStockMoveStatus = stockMove.updateStatus
export const exportStockMoves = stockMove.export

// ---------- ERP 库存盘点单（PD，issue #93，spec §3.5/§4.6） ----------
// 明细 actualCount 为实际清点数（≥ 0，十进制字符串）；baseCount 为创建时账面
// 数快照（服务端写入、只读展示，审核以当时账面为准，入参无该字段）。审核按
// 「差异 = actualCount − 当时账面数」调整库存（差正盘盈、差负盘亏），审核后
// 账面 = 实际数；反审核按已入账流水净额反向。
const stockCheck = createDocClient('stock-check')
export const pageStockChecks = stockCheck.page
export const getStockCheck = stockCheck.get
export const createStockCheck = stockCheck.create
export const updateStockCheck = stockCheck.update
export const deleteStockCheck = stockCheck.delete
export const updateStockCheckStatus = stockCheck.updateStatus
export const exportStockChecks = stockCheck.export
