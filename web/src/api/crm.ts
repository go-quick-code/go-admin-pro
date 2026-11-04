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
