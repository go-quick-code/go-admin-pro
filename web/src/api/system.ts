import request from '@/utils/request'

// ---------- 用户 ----------
export function pageUsers(params: any) {
  return request.get('/system/user/page', { params }) as Promise<any>
}
export function getUser(id: number) {
  return request.get(`/system/user/${id}`) as Promise<any>
}
export function createUser(data: any) {
  return request.post('/system/user', data) as Promise<any>
}
export function updateUser(data: any) {
  return request.put('/system/user', data) as Promise<any>
}
export function updateUserStatus(data: { id: number; status: number }) {
  return request.put('/system/user/status', data) as Promise<any>
}
export function resetUserPassword(data: { id: number; password: string }) {
  return request.put('/system/user/password', data) as Promise<any>
}
export function deleteUser(id: number) {
  return request.delete(`/system/user/${id}`) as Promise<any>
}

export function listSimpleUsers() {
  return request.get('/system/user/simple-list') as Promise<any>
}

// ---------- 角色 ----------
export function pageRoles(params: any) {
  return request.get('/system/role/page', { params }) as Promise<any>
}
export function listSimpleRoles() {
  return request.get('/system/role/simple-list') as Promise<any>
}
export function getRole(id: number) {
  return request.get(`/system/role/${id}`) as Promise<any>
}
export function createRole(data: any) {
  return request.post('/system/role', data) as Promise<any>
}
export function updateRole(data: any) {
  return request.put('/system/role', data) as Promise<any>
}
export function deleteRole(id: number) {
  return request.delete(`/system/role/${id}`) as Promise<any>
}

// ---------- 菜单 ----------
export function listMenus() {
  return request.get('/system/menu/list') as Promise<any>
}
export function getMenu(id: number) {
  return request.get(`/system/menu/${id}`) as Promise<any>
}
export function createMenu(data: any) {
  return request.post('/system/menu', data) as Promise<any>
}
export function updateMenu(data: any) {
  return request.put('/system/menu', data) as Promise<any>
}
export function deleteMenu(id: number) {
  return request.delete(`/system/menu/${id}`) as Promise<any>
}

// ---------- 部门 ----------
export function treeDepts() {
  return request.get('/system/dept/tree') as Promise<any>
}
export function getDept(id: number) {
  return request.get(`/system/dept/${id}`) as Promise<any>
}
export function createDept(data: any) {
  return request.post('/system/dept', data) as Promise<any>
}
export function updateDept(data: any) {
  return request.put('/system/dept', data) as Promise<any>
}
export function deleteDept(id: number) {
  return request.delete(`/system/dept/${id}`) as Promise<any>
}

// ---------- 岗位 ----------
export function pagePosts(params: any) {
  return request.get('/system/post/page', { params }) as Promise<any>
}
export function listSimplePosts() {
  return request.get('/system/post/simple-list') as Promise<any>
}
export function createPost(data: any) {
  return request.post('/system/post', data) as Promise<any>
}
export function updatePost(data: any) {
  return request.put('/system/post', data) as Promise<any>
}
export function deletePost(id: number) {
  return request.delete(`/system/post/${id}`) as Promise<any>
}

// ---------- 字典 ----------
export function pageDictTypes(params: any) {
  return request.get('/system/dict-type/page', { params }) as Promise<any>
}
export function createDictType(data: any) {
  return request.post('/system/dict-type', data) as Promise<any>
}
export function updateDictType(data: any) {
  return request.put('/system/dict-type', data) as Promise<any>
}
export function deleteDictType(id: number) {
  return request.delete(`/system/dict-type/${id}`) as Promise<any>
}
export function listDictData(params: any) {
  return request.get('/system/dict-data/list', { params }) as Promise<any>
}
export function listDictDataByType(dictType: string) {
  return request.get(`/system/dict-data/type/${dictType}`) as Promise<any>
}
export function createDictData(data: any) {
  return request.post('/system/dict-data', data) as Promise<any>
}
export function updateDictData(data: any) {
  return request.put('/system/dict-data', data) as Promise<any>
}
export function deleteDictData(id: number) {
  return request.delete(`/system/dict-data/${id}`) as Promise<any>
}

// ---------- 租户 ----------
export function pageTenants(params: any) {
  return request.get('/system/tenant/page', { params }) as Promise<any>
}
export function createTenant(data: any) {
  return request.post('/system/tenant', data) as Promise<any>
}
export function updateTenant(data: any) {
  return request.put('/system/tenant', data) as Promise<any>
}
export function updateTenantStatus(data: { id: number; status: number }) {
  return request.put('/system/tenant/status', data) as Promise<any>
}
export function deleteTenant(id: number) {
  return request.delete(`/system/tenant/${id}`) as Promise<any>
}

// ---------- 租户套餐 ----------
export function pageTenantPackages(params: any) {
  return request.get('/system/tenant-package/page', { params }) as Promise<any>
}
export function listSimpleTenantPackages() {
  return request.get('/system/tenant-package/simple-list') as Promise<any>
}
export function getTenantPackage(id: number) {
  return request.get(`/system/tenant-package/${id}`) as Promise<any>
}
export function createTenantPackage(data: any) {
  return request.post('/system/tenant-package', data) as Promise<any>
}
export function updateTenantPackage(data: any) {
  return request.put('/system/tenant-package', data) as Promise<any>
}
export function updateTenantPackageStatus(data: { id: number; status: number }) {
  return request.put('/system/tenant-package/status', data) as Promise<any>
}
export function deleteTenantPackage(id: number) {
  return request.delete(`/system/tenant-package/${id}`) as Promise<any>
}

// ---------- 日志 ----------
export function pageLoginLogs(params: any) {
  return request.get('/system/login-log/page', { params }) as Promise<any>
}
export function clearLoginLogs() {
  return request.delete('/system/login-log/clear') as Promise<any>
}
export function pageOperateLogs(params: any) {
  return request.get('/system/operate-log/page', { params }) as Promise<any>
}
export function deleteOperateLogs(ids: number[]) {
  return request.delete('/system/operate-log', { data: { ids } }) as Promise<any>
}
export function clearOperateLogs() {
  return request.delete('/system/operate-log/clear') as Promise<any>
}

// ---------- 仪表盘 ----------
export function getDashboardSummary() {
  return request.get('/system/dashboard/summary') as Promise<any>
}
