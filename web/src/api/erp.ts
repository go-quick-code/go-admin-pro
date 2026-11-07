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
