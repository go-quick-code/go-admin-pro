// ---------- crm 产品（本文件由 codegen 生成（issue #10 样板）；把以下片段追加到 web/src/api/crm.ts 末尾） ----------
export function pageProducts(params: any) {
  return request.get('/crm/product/page', { params }) as Promise<any>
}
export function getProduct(id: number) {
  return request.get(`/crm/product/${id}`) as Promise<any>
}
export function createProduct(data: any) {
  return request.post('/crm/product', data) as Promise<any>
}
export function updateProduct(data: any) {
  return request.put('/crm/product', data) as Promise<any>
}
export function deleteProduct(id: number) {
  return request.delete(`/crm/product/${id}`) as Promise<any>
}
