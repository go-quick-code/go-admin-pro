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
