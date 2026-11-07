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
