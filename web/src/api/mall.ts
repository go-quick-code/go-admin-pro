import request from '@/utils/request'

// mall 会员域 API（issue #174，docs/mall-mvp-spec.md §4/§5）：管理端会员/等级/
// 地址。商品/交易域与 C 端端点随 T2/T3/T5 追加。
// 积分无管理端入口（余额只经后端唯一入账入口变动，T2）——本文件刻意无
// 积分写接口。

// ---------- 会员（mall:member:*） ----------
// mobile 租户内唯一（后端撞键 409）；password 选填（空 = 不设/不改密码，
// 服务端 bcrypt 散列）；会员无删除端点（停启用覆盖生命周期，spec §4）。
export function pageMembers(params: any) {
  return request.get('/mall/member/page', { params }) as Promise<any>
}
export function getMember(id: number) {
  return request.get(`/mall/member/${id}`) as Promise<any>
}
export function createMember(data: {
  mobile: string
  password?: string
  nickname?: string
  avatarUrl?: string
  levelId?: number
  status?: number
  remark?: string
}) {
  return request.post('/mall/member', data) as Promise<any>
}
export function updateMember(data: {
  id: number
  mobile: string
  password?: string
  nickname?: string
  avatarUrl?: string
  levelId?: number
  status?: number
  remark?: string
}) {
  return request.put('/mall/member', data) as Promise<any>
}
// 停启用（PUT /mall/member/:id/status）：status 0 启用 / 1 停用。
export function updateMemberStatus(id: number, status: number) {
  return request.put(`/mall/member/${id}/status`, { status }) as Promise<any>
}

// ---------- 会员等级（mall:level:*） ----------
// name/level 租户内唯一（后端撞键 409）；被会员引用禁删（后端 409）。
export function pageLevels(params: any) {
  return request.get('/mall/level/page', { params }) as Promise<any>
}
// 启用等级清单（登录 + mall:member:query）：会员表单等级下拉数据源。
export function listLevelOptions() {
  return request.get('/mall/level/list') as Promise<any>
}
export function getLevel(id: number) {
  return request.get(`/mall/level/${id}`) as Promise<any>
}
export function createLevel(data: { name: string; level: number; experience?: number; status?: number; remark?: string }) {
  return request.post('/mall/level', data) as Promise<any>
}
export function updateLevel(data: { id: number; name: string; level: number; experience?: number; status?: number; remark?: string }) {
  return request.put('/mall/level', data) as Promise<any>
}
export function deleteLevel(id: number) {
  return request.delete(`/mall/level/${id}`) as Promise<any>
}

// ---------- 收货地址（随 mall:member:query，只读） ----------
// 地址维护随 C 端自助（T2/T5），管理端只读：按会员查 + 管理视图分页。
export function pageAddresses(params: any) {
  return request.get('/mall/address/page', { params }) as Promise<any>
}
// 按会员查地址（不分页，会员详情抽屉数据源；member 详情已含 addresses，
// 单独刷新地址 tab 时使用）。
export function listMemberAddresses(userId: number) {
  return request.get(`/mall/address/member/${userId}`) as Promise<any>
}
