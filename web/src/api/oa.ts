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

// ---------- OA 公告（issue #48，spec #42 §3.4/§4.7/§5） ----------
// 权限模型：管理列表/草稿预览挂 oa:announcement:query（菜单 521），创建/编辑/
// 删除/发布撤回逐码（5211–5214）；员工侧「我的公告」（菜单 529）与已发布详情
// 登录即可，可见性由受众判定收口。

export interface AnnouncementTargetPayload {
  targetType: number
  targetId: number
}

export interface AnnouncementPayload {
  id?: number
  title: string
  content: string
  audienceType: number
  targets: AnnouncementTargetPayload[]
  pinned: boolean
}

// 管理端分页（全部状态，置顶在前；status/audienceType/pinned 精确，keyword 匹配标题）。
export function pageAnnouncements(params?: {
  pageNo?: number
  pageSize?: number
  status?: number
  audienceType?: number
  pinned?: boolean
  keyword?: string
}) {
  return request.get('/oa/announcement/page', { params }) as Promise<any>
}
// 我的公告（已发布且命中受众；置顶在前）。
export function pageMyAnnouncements(params?: { pageNo?: number; pageSize?: number; keyword?: string }) {
  return request.get('/oa/announcement/my/page', { params }) as Promise<any>
}
// 公告详情（已发布按受众可见；草稿/撤回仅管理查询码可预览）。
export function getAnnouncement(id: number) {
  return request.get(`/oa/announcement/${id}`) as Promise<any>
}
export function createAnnouncement(data: AnnouncementPayload) {
  return request.post('/oa/announcement', data) as Promise<any>
}
export function updateAnnouncement(data: AnnouncementPayload) {
  return request.put('/oa/announcement', data) as Promise<any>
}
// 删除仅草稿（发布后不可删除，只能撤回）。
export function deleteAnnouncement(id: number) {
  return request.delete(`/oa/announcement/${id}`) as Promise<any>
}
// 发布/重新发布：事务内为受众批量写 announcement 通知。
export function publishAnnouncement(id: number) {
  return request.post(`/oa/announcement/${id}/publish`) as Promise<any>
}
// 撤回：不再出现在员工列表，已写通知保留并在详情显示已撤回。
export function withdrawAnnouncement(id: number) {
  return request.post(`/oa/announcement/${id}/withdraw`) as Promise<any>
}

// 附件（infra_file，biz_type=oa_announcement）：查看跟随公告可见性，
// 上传/删除挂 oa:announcement:update 且仅草稿。
export function listAnnouncementFiles(id: number) {
  return request.get(`/oa/announcement/${id}/files`) as Promise<any>
}
export function uploadAnnouncementFile(id: number, file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(`/oa/announcement/${id}/files`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }) as Promise<any>
}
export function deleteAnnouncementFile(id: number, fileId: number) {
  return request.delete(`/oa/announcement/${id}/files/${fileId}`) as Promise<any>
}
// 鉴权下载（后端返回的 url 已含 /admin-api 前缀，baseURL 置空原样请求）。
export function downloadAnnouncementFile(url: string) {
  return request.get(url, { baseURL: '', responseType: 'blob' }) as Promise<any>
}
