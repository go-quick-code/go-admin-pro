import request from '@/utils/request'

// ---------- CRM 客户（issue #8 样板：后续 CRM 实体照此复制） ----------
export function pageCustomers(params: any) {
  return request.get('/crm/customer/page', { params }) as Promise<any>
}
export function getCustomer(id: number) {
  return request.get(`/crm/customer/${id}`) as Promise<any>
}
export function createCustomer(data: any) {
  return request.post('/crm/customer', data) as Promise<any>
}
export function updateCustomer(data: any) {
  return request.put('/crm/customer', data) as Promise<any>
}
export function deleteCustomer(id: number) {
  return request.delete(`/crm/customer/${id}`) as Promise<any>
}

// ---------- 公海（issue #13：手动释放/认领 + 负责人历史） ----------
// 公海分页：owner 为空的客户，不受数据范围过滤（crm:customer:pool:view）。
export function pagePoolCustomers(params: any) {
  return request.get('/crm/customer/pool/page', { params }) as Promise<any>
}
// 认领公海客户到自己名下（crm:customer:claim，受认领上限约束）。
export function claimCustomer(id: number) {
  return request.put('/crm/customer/claim', { id }) as Promise<any>
}
// 释放客户到公海（负责人本人或 crm:customer:release）。
export function releaseCustomer(id: number, reason: string) {
  return request.put('/crm/customer/release', { id, reason }) as Promise<any>
}
// 转移客户给同租户其他用户（负责人本人或 crm:customer:transfer）。
export function transferCustomer(id: number, toOwnerUserId: number) {
  return request.put('/crm/customer/transfer', { id, toOwnerUserId }) as Promise<any>
}
// 负责人历史分页（新→旧）。
export function pageCustomerOwnerHistory(id: number, params: any) {
  return request.get(`/crm/customer/${id}/owner-history`, { params }) as Promise<any>
}

// ---------- 公海租户配置（issue #14：回收天数/认领上限按租户生效） ----------
// 读挂 crm:customer:pool:view（能看公海即可读）；写挂 crm:pool-config:update。
export function getPoolConfig() {
  return request.get('/crm/pool-config') as Promise<any>
}
export function updatePoolConfig(data: { recycleDays: number; claimLimit: number }) {
  return request.put('/crm/pool-config', data) as Promise<any>
}

// ---------- CRM 联系人（issue #9：隶属客户，可见性跟随客户） ----------
export function pageContacts(params: any) {
  return request.get('/crm/contact/page', { params }) as Promise<any>
}
export function getContact(id: number) {
  return request.get(`/crm/contact/${id}`) as Promise<any>
}
export function createContact(data: any) {
  return request.post('/crm/contact', data) as Promise<any>
}
export function updateContact(data: any) {
  return request.put('/crm/contact', data) as Promise<any>
}
export function deleteContact(id: number) {
  return request.delete(`/crm/contact/${id}`) as Promise<any>
}

// ---------- CRM 跟进记录（issue #12：客户维度，可见性跟随客户） ----------
// 新增跟进后端会同事务刷新客户 lastFollowTime；修改/删除不回退。
export function pageFollowUps(params: any) {
  return request.get('/crm/follow-up/page', { params }) as Promise<any>
}
export function getFollowUp(id: number) {
  return request.get(`/crm/follow-up/${id}`) as Promise<any>
}
export function createFollowUp(data: any) {
  return request.post('/crm/follow-up', data) as Promise<any>
}
// 修改/删除：创建人本人或持 crm:follow-up:update/delete 按钮码。
export function updateFollowUp(data: any) {
  return request.put('/crm/follow-up', data) as Promise<any>
}
export function deleteFollowUp(id: number) {
  return request.delete(`/crm/follow-up/${id}`) as Promise<any>
}

// ---------- 产品分类（issue #11：树形结构，删除约束在 service 层） ----------
export function treeProductCategories() {
  return request.get('/crm/product-category/tree') as Promise<any>
}
export function createProductCategory(data: any) {
  return request.post('/crm/product-category', data) as Promise<any>
}
export function updateProductCategory(data: any) {
  return request.put('/crm/product-category', data) as Promise<any>
}
export function deleteProductCategory(id: number) {
  return request.delete(`/crm/product-category/${id}`) as Promise<any>
}

// ---------- 产品（issue #11：租户级共享资源，编码租户内唯一） ----------
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
// 状态切换（spec §5 PUT /product/status，crm:product:update）：只改 status，
// 0 下架 / 1 上架，不触碰其他字段。
export function updateProductStatus(id: number, status: number) {
  return request.put('/crm/product/status', { id, status }) as Promise<any>
}
export function deleteProduct(id: number) {
  return request.delete(`/crm/product/${id}`) as Promise<any>
}

// ---------- 商机（issue #15：owner-scoped，阶段推进走独立端点） ----------
// 金额一律十进制字符串（如 "5000.00"）；行项目金额由后端按 数量×单价 计算，
// 前端只提交 { productId, quantity, price }。
export function pageOpportunities(params: any) {
  return request.get('/crm/opportunity/page', { params }) as Promise<any>
}
export function getOpportunity(id: number) {
  return request.get(`/crm/opportunity/${id}`) as Promise<any>
}
export function createOpportunity(data: any) {
  return request.post('/crm/opportunity', data) as Promise<any>
}
export function updateOpportunity(data: any) {
  return request.put('/crm/opportunity', data) as Promise<any>
}
export function deleteOpportunity(id: number) {
  return request.delete(`/crm/opportunity/${id}`) as Promise<any>
}
// 阶段推进（创建时可指定 1–4；此后阶段变更只走本入口，crm:opportunity:stage）：
// 5 赢单 / 6 输单为终态，终态后再推进后端返回 400。
export function advanceOpportunityStage(id: number, stage: number) {
  return request.put('/crm/opportunity/stage', { id, stage }) as Promise<any>
}

// ---------- 合同（issue #16：owner-scoped，状态流转走独立端点） ----------
// 金额一律十进制字符串（如 "5000.00"）；有行项目时后端按 SUM(行项目金额) 落库，
// 前端只提交 { productId, quantity, price }。status 唯一入口是 PUT /crm/contract/status：
// 1 草稿 → 2 待审（提交）→ 3 已签（审批通过）；待审可驳回回草稿；4 作废。
export function pageContracts(params: any) {
  return request.get('/crm/contract/page', { params }) as Promise<any>
}
export function getContract(id: number) {
  return request.get(`/crm/contract/${id}`) as Promise<any>
}
export function createContract(data: any) {
  return request.post('/crm/contract', data) as Promise<any>
}
export function updateContract(data: any) {
  return request.put('/crm/contract', data) as Promise<any>
}
export function deleteContract(id: number) {
  return request.delete(`/crm/contract/${id}`) as Promise<any>
}
// 状态流转（唯一状态入口，crm:contract:update）：提交/作废；审批通过与驳回
// 后端还会校验 crm:contract:approve（无码 403）。reason 仅驳回时使用。
export function changeContractStatus(id: number, status: number, reason = '') {
  return request.put('/crm/contract/status', { id, status, reason }) as Promise<any>
}

// ---------- 合同附件（issue #17：复用 infra_file，权限跟随合同） ----------
// 列表挂 crm:contract:query，上传/删除挂 crm:contract:update，不要求 infra:file:*。
// 上传为 multipart（字段名 file），20MB 与扩展名白名单由后端复用 infra 口径校验。
export function listContractFiles(contractId: number) {
  return request.get(`/crm/contract/${contractId}/files`) as Promise<any>
}
export function uploadContractFile(contractId: number, file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(`/crm/contract/${contractId}/files`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }) as Promise<any>
}
export function deleteContractFile(contractId: number, fileId: number) {
  return request.delete(`/crm/contract/${contractId}/files/${fileId}`) as Promise<any>
}

// ---------- 回款计划 + 回款记录（issue #18：可见性 join 合同套用合同数据范围） ----------
// 金额一律十进制字符串（如 "3000.00"）。计划状态不落库：列表返回派生状态
// status（1 待回款 / 2 部分回款 / 3 已回款 / 4 逾期）、statusName 与 overdue。
// 计划不单独编号（periodNo 标识期次）；记录编号 HK+yyyyMMdd+4 位序号由后端发放。
export function pageReceivablePlans(params: any) {
  return request.get('/crm/receivable/plan/page', { params }) as Promise<any>
}
export function createReceivablePlan(data: any) {
  return request.post('/crm/receivable/plan', data) as Promise<any>
}
export function updateReceivablePlan(data: any) {
  return request.put('/crm/receivable/plan', data) as Promise<any>
}
export function deleteReceivablePlan(id: number) {
  return request.delete(`/crm/receivable/plan/${id}`) as Promise<any>
}
export function pageReceivableRecords(params: any) {
  return request.get('/crm/receivable/record/page', { params }) as Promise<any>
}
export function createReceivableRecord(data: any) {
  return request.post('/crm/receivable/record', data) as Promise<any>
}
export function deleteReceivableRecord(id: number) {
  return request.delete(`/crm/receivable/record/${id}`) as Promise<any>
}

// ---------- CRM 仪表盘（issue #19：4 卡片 + 商机阶段漏斗 + 近 12 个月趋势） ----------
// 一次返回全部聚合，金额一律十进制字符串（如 "10500.50"）；
// 后端已按当前用户数据范围过滤，前端不再二次筛选。
export function getDashboardSummary() {
  return request.get('/crm/dashboard/summary') as Promise<any>
}

// ---------- Excel 导入导出（issue #20，docs/crm-mvp-spec.md §7） ----------
// 导入为 multipart（字段名 file），模板/导出返回 xlsx 二进制（responseType: blob）。
// 后端在进入解析前固定 10MB / 5000 行 / 100 条错误明细上限；导入先全量校验，
// 有错整批拒绝（HTTP 200 + code 4201，data 为 { errors, total, truncated }），
// 页面按行号 + 列名 + 原因展示（@/components/ExcelActions.vue）。
function uploadExcel(path: string, file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(path, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }) as Promise<any>
}

function downloadExcel(path: string, params?: Record<string, any>) {
  return request.get(path, { params, responseType: 'blob' }) as Promise<any>
}

// 客户：导入 + 模板 + 导出（导出复用列表筛选与数据权限）。
export function downloadCustomerImportTemplate() {
  return downloadExcel('/crm/customer/import-template')
}
export function importCustomers(file: File) {
  return uploadExcel('/crm/customer/import', file)
}
export function exportCustomers(params?: Record<string, any>) {
  return downloadExcel('/crm/customer/export-excel', params)
}

// 联系人：导入 + 模板（客户编号关联所属客户，须对当前用户可见）。
export function downloadContactImportTemplate() {
  return downloadExcel('/crm/contact/import-template')
}
export function importContacts(file: File) {
  return uploadExcel('/crm/contact/import', file)
}

// 产品：导入 + 模板 + 导出（分类按名称关联，可空）。
export function downloadProductImportTemplate() {
  return downloadExcel('/crm/product/import-template')
}
export function importProducts(file: File) {
  return uploadExcel('/crm/product/import', file)
}
export function exportProducts(params?: Record<string, any>) {
  return downloadExcel('/crm/product/export-excel', params)
}

// 合同导出（复用列表筛选与数据权限；日期为原生日期，金额为十进制字符串）。
export function exportContracts(params?: Record<string, any>) {
  return downloadExcel('/crm/contract/export-excel', params)
}

// 回款计划导出（含已收金额与派生状态/逾期）。
export function exportReceivablePlans(params?: Record<string, any>) {
  return downloadExcel('/crm/receivable/plan/export-excel', params)
}
