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

// ---------- OA 请假类型与额度（issue #51，spec #42 §3.5/§4.8） ----------
// 权限模型：类型与额度的全部管理动作挂 oa:leave:manage（菜单 522）；
// 类型列表与本人余额登录即可（员工发起请假的前置）。

export interface LeaveTypePayload {
  id?: number
  name: string
  balanceManaged: boolean
  annualQuota?: string
  sort?: number
  remark?: string
}

// 类型列表：status 精确（0 启用/1 停用，缺省全部）、keyword 名称/编码模糊。
export function listLeaveTypes(params?: { status?: number; keyword?: string }) {
  return request.get('/oa/leave/type/list', { params }) as Promise<any>
}
// 创建类型（code 创建后不可变；名称/编码租户内唯一）。
export function createLeaveType(data: LeaveTypePayload & { code: string }) {
  return request.post('/oa/leave/type', data) as Promise<any>
}
// 更新类型（不含 code）。
export function updateLeaveType(data: LeaveTypePayload) {
  return request.put('/oa/leave/type', data) as Promise<any>
}
// 启停类型（status 0 启用 / 1 停用）。
export function setLeaveTypeStatus(id: number, status: number) {
  return request.put(`/oa/leave/type/${id}/status`, { status }) as Promise<any>
}
// 删除类型（被请假申请引用时后端 409，只能停用）。
export function deleteLeaveType(id: number) {
  return request.delete(`/oa/leave/type/${id}`) as Promise<any>
}

// 额度管理分页（userId/leaveTypeId/year 精确过滤）。
export function pageLeaveBalances(params?: {
  pageNo?: number
  pageSize?: number
  userId?: number
  leaveTypeId?: number
  year?: number
}) {
  return request.get('/oa/leave/balance/page', { params }) as Promise<any>
}
// 额度初始化（幂等：已有 (用户, 类型, 年) 行不重复建、不覆盖）。
export function initLeaveBalances(data: { year: number; userIds: number[]; typeIds?: number[] }) {
  return request.post('/oa/leave/balance/init', data) as Promise<any>
}
// 管理员调整额度（delta 可负；原因必填并留痕）。
export function adjustLeaveBalance(data: {
  userId: number
  leaveTypeId: number
  year: number
  delta: string
  reason: string
}) {
  return request.post('/oa/leave/balance/adjust', data) as Promise<any>
}
// 我的额度（year 缺省当前自然年；覆盖全部启用中类型）。
export function getMyLeaveBalances(year?: number) {
  return request.get('/oa/leave/balance/mine', { params: year ? { year } : {} }) as Promise<any>
}

// ---------- OA 请假申请（issue #52，spec #42 §4.8/§5） ----------
// 权限模型：本人列表/详情/附件查看登录即可（服务端按本人收口）；
// 写动作逐码 oa:leave:create/update/delete/submit；审批走通用待办 /bpm/task/*。

export interface LeaveRequestPayload {
  id?: number
  leaveTypeId: number
  startDate: string
  startHalf: number
  endDate: string
  endHalf: number
  reason: string
}

// 本人请假分页：status 精确过滤（0 草稿/1 待审/2 通过/3 驳回/4 撤回/5 已取消）。
export function pageMyLeaveRequests(params?: { pageNo?: number; pageSize?: number; status?: number }) {
  return request.get('/oa/leave/request/page', { params }) as Promise<any>
}
// 本人请假详情：申请行 + 附件 + bpm 审批时间线（草稿 approval 为空）。
export function getMyLeaveRequest(id: number) {
  return request.get(`/oa/leave/request/${id}`) as Promise<any>
}
// 创建草稿（时长由服务端按工作日历计算，非工作日不计）。
export function createLeaveRequest(data: LeaveRequestPayload) {
  return request.post('/oa/leave/request', data) as Promise<any>
}
// 更新草稿/已驳回申请。
export function updateLeaveRequest(data: LeaveRequestPayload) {
  return request.put('/oa/leave/request', data) as Promise<any>
}
// 删除草稿/已驳回申请（其他状态 409）。
export function deleteLeaveRequest(id: number) {
  return request.delete(`/oa/leave/request/${id}`) as Promise<any>
}
// 提交审批：事务内创建 oa_leave_approval 实例并置待审。
export function submitLeaveRequest(id: number) {
  return request.post(`/oa/leave/request/${id}/submit`) as Promise<any>
}
// 附件列表（新上传的在前）。
export function listLeaveRequestFiles(id: number) {
  return request.get(`/oa/leave/request/${id}/files`) as Promise<any>
}
// 上传附件（multipart 字段名 file；仅草稿/已驳回）。
export function uploadLeaveRequestFile(id: number, file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(`/oa/leave/request/${id}/files`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }) as Promise<any>
}
// 删除附件（仅草稿/已驳回）。
export function deleteLeaveRequestFile(id: number, fileId: number) {
  return request.delete(`/oa/leave/request/${id}/files/${fileId}`) as Promise<any>
}
// 鉴权下载（后端返回的 url 已含 /admin-api 前缀，baseURL: '' 原样请求）。
export function downloadLeaveRequestFile(url: string) {
  return request.get(url, { baseURL: '', responseType: 'blob' }) as Promise<any>
}

