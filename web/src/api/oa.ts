import request from '@/utils/request'

// ---------- OA 工作日历（issue #50，spec #42 §3.3/§5） ----------
// 权限码 oa:workday:*（菜单种子 520 段：520 查询、5201 单日、5202 批量、
// 5203 恢复默认）；请假时长与考勤派生在后端复用同一套纯函数。

// 按月查看：整月逐日三态（显式记录优先、无记录走默认规则）。
export function getWorkdayMonth(month: string) {
  return request.get('/oa/workday/month', { params: { month } }) as Promise<any>
}
// 单日设置（upsert）：type 0 工作日（含周末调休上班）/ 1 休息日 / 2 节假日。
export function setWorkday(data: { date: string; type: number; remark?: string }) {
  return request.put('/oa/workday', data) as Promise<any>
}
// 批量设置：dates 显式日期列表，或 startDate/endDate 区间 + weekdays 星期过滤。
export function batchWorkday(data: {
  dates?: string[]
  startDate?: string
  endDate?: string
  weekdays?: number[]
  type: number
  remark?: string
}) {
  return request.post('/oa/workday/batch', data) as Promise<any>
}
// 清除单日显式记录，恢复默认规则（周一至周五工作日、周六周日休息）。
export function clearWorkday(date: string) {
  return request.delete(`/oa/workday/${date}`) as Promise<any>
}

// ---------- OA 考勤基础（issue #60，spec #42 §3.9/§4.10/§5） ----------
// 权限码 oa:attendance:shift:manage（菜单 527 班次管理）、
// oa:attendance:group:manage（菜单 530 考勤组管理）；员工打卡接口以登录身份
// 收口（菜单 531 我的考勤，无权限码），无启用考勤组时打卡返回 400。

// 班次：分页/新增/修改/删除/启停（time-of-day HH:MM，不支持跨天）。
export function pageShifts(params: any) {
  return request.get('/oa/attendance/shift', { params }) as Promise<any>
}
export function createShift(data: any) {
  return request.post('/oa/attendance/shift', data) as Promise<any>
}
export function updateShift(data: any) {
  const { id, ...rest } = data || {}
  return request.put(`/oa/attendance/shift/${id}`, rest) as Promise<any>
}
export function deleteShift(id: number) {
  return request.delete(`/oa/attendance/shift/${id}`) as Promise<any>
}
export function setShiftStatus(id: number, status: number) {
  return request.put(`/oa/attendance/shift/${id}/status`, { status }) as Promise<any>
}

// 考勤组：分页/详情（含组员）/新增/修改/删除（有组员 409）/启停。
export function pageGroups(params: any) {
  return request.get('/oa/attendance/group', { params }) as Promise<any>
}
export function getGroup(id: number) {
  return request.get(`/oa/attendance/group/${id}`) as Promise<any>
}
export function createGroup(data: any) {
  return request.post('/oa/attendance/group', data) as Promise<any>
}
export function updateGroup(data: any) {
  const { id, ...rest } = data || {}
  return request.put(`/oa/attendance/group/${id}`, rest) as Promise<any>
}
export function deleteGroup(id: number) {
  return request.delete(`/oa/attendance/group/${id}`) as Promise<any>
}
export function setGroupStatus(id: number, status: number) {
  return request.put(`/oa/attendance/group/${id}/status`, { status }) as Promise<any>
}

// 组员物化：按用户批量添加（已在其他组会改绑，一个用户只属于一个组）、
// 按部门批量导入（取部门当前成员，后续不自动同步）、移出单个组员。
export function addGroupMembers(id: number, userIds: number[]) {
  return request.post(`/oa/attendance/group/${id}/members`, { userIds }) as Promise<any>
}
export function addGroupMembersByDept(id: number, deptIds: number[]) {
  return request.post(`/oa/attendance/group/${id}/members/by-dept`, { deptIds }) as Promise<any>
}
export function removeGroupMember(id: number, userId: number) {
  return request.delete(`/oa/attendance/group/${id}/members/${userId}`) as Promise<any>
}

// Web 打卡与我的考勤（登录即可用；同一天多次打卡均保留）。
export function punchIn() {
  return request.post('/oa/attendance/punch/in') as Promise<any>
}
export function punchOut() {
  return request.post('/oa/attendance/punch/out') as Promise<any>
}
export function getPunchToday() {
  return request.get('/oa/attendance/punch/today') as Promise<any>
}
export function pageMyPunches(params: any) {
  return request.get('/oa/attendance/punch/page', { params }) as Promise<any>
}
