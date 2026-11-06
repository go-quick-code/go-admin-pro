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

// ---------- OA 考勤结果与校正（issue #61，spec #42 §4.10/§5） ----------
// 管理端结果查询挂 oa:attendance:result:query（菜单 533）；校正/撤销校正/
// 手工重算挂 oa:attendance:result:correct（按钮 5331）；员工「我的考勤」
// 结果列表登录即可（服务端强制本人，菜单 531 页面内展示）。

// 管理端结果分页：userId/日期区间/生效状态过滤（可见范围由后端收口）。
export function pageAttendanceResults(params: any) {
  return request.get('/oa/attendance/result/page', { params }) as Promise<any>
}
// 我的考勤结果分页（强制本人）。
export function pageMyAttendanceResults(params: any) {
  return request.get('/oa/attendance/result/mine', { params }) as Promise<any>
}
// 手工重算：按用户 + 日期范围（含两端，最多 366 天），幂等覆盖。
export function recomputeAttendanceResults(data: any) {
  return request.post('/oa/attendance/result/recompute', data) as Promise<any>
}
// 管理员校正：overrideStatus 0–6 + 备注（1–200 字），不覆盖原始打卡。
export function correctAttendanceResult(id: number, data: any) {
  return request.put(`/oa/attendance/result/${id}/correct`, data) as Promise<any>
}
// 撤销校正：结果回落到派生状态。
export function clearAttendanceResultCorrection(id: number) {
  return request.delete(`/oa/attendance/result/${id}/correct`) as Promise<any>
}
// ---------- OA 考勤月度汇总（issue #62，spec #42 §4.10） ----------
// 管理端汇总分页/单用户查询挂 oa:attendance:summary:query（菜单 534），
// 可见范围复用 oa:attendance:manage（5332）：全租户 / 部门负责人子树 / 本人；
// 员工「我的考勤」本月汇总卡片登录即可（强制本人）。

// 管理端月度汇总分页（按用户分页；month 缺省当月，可见范围由后端收口）。
export function pageAttendanceSummaries(params: any) {
  return request.get('/oa/attendance/summary/page', { params }) as Promise<any>
}
// 单用户月度汇总（范围外/跨租户后端 404）。
export function getAttendanceSummaryOfUser(params: { month?: string; userId: number }) {
  return request.get('/oa/attendance/summary/user', { params }) as Promise<any>
}
// 我的月度汇总（强制本人；month 缺省当月）。
export function getMyAttendanceSummary(params?: { month?: string }) {
  return request.get('/oa/attendance/summary/mine', { params }) as Promise<any>
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
// 删除/发布撤回逐码（5211–5214）；员工侧「我的公告」（菜单 532）与已发布详情
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
// 已读统计（issue #49，按钮 5215 oa:announcement:read-stats）：去重后的
// 已读人数 + 分页名单（userId/nickname/readAt）；撤回保留历史已读。
export function getAnnouncementReadStats(id: number, params?: { pageNo?: number; pageSize?: number }) {
  return request.get(`/oa/announcement/${id}/read-stats`, { params }) as Promise<any>
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
// ---------- OA 请假申请（issue #52/#53，spec #42 §4.8/§5） ----------
// 权限模型：列表/详情/附件查看登录即可（服务端按 §4.6 可见范围收口）；
// 写动作逐码 oa:leave:create/update/delete/submit/withdraw/cancel；
// 审批走通用待办 /bpm/task/*。

export interface LeaveRequestPayload {
  id?: number
  leaveTypeId: number
  startDate: string
  startHalf: number
  endDate: string
  endHalf: number
  reason: string
}

// 请假分页：status 精确过滤（0 草稿/1 待审/2 通过/3 驳回/4 撤回/5 已取消）；
// 服务端按可见范围返回（manage → 全租户、部门负责人 → 部门子树、否则本人）。
export function pageMyLeaveRequests(params?: { pageNo?: number; pageSize?: number; status?: number }) {
  return request.get('/oa/leave/request/page', { params }) as Promise<any>
}
// 请假详情：申请行 + 附件 + bpm 审批时间线（草稿 approval 为空）；范围外 404。
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
// 撤回：仅发起人、首个审批动作前（oa:leave:withdraw，button 5295）；额度不变。
export function withdrawLeaveRequest(id: number) {
  return request.post(`/oa/leave/request/${id}/withdraw`) as Promise<any>
}
// 取消：仅发起人、已通过且开始日期前（oa:leave:cancel，button 5296）；
// 事务内返还已扣额度，开始日期后返回可读 400。
export function cancelLeaveRequest(id: number) {
  return request.post(`/oa/leave/request/${id}/cancel`) as Promise<any>
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


// ---------- OA 报销（issue #54/#55，spec #42 §4.9/§5） ----------
// 权限模型：列表/详情/附件查看登录即可（服务端按 §4.6 可见范围收口）；
// 写动作逐码 oa:expense:create/update/delete/submit；审批动作统一在通用待办
// （/bpm/task/*）完成，通过/驳回/撤回由 bpm 回调写回报销单状态。金额一律
// 十进制字符串（分单位由后端 money.Money 解析，拒绝 JSON 数字以避免浮点丢精度）。

export interface ExpenseItemPayload {
  id?: number
  category: string
  amount: string
  expenseDate: string
  remark?: string
}

export interface ExpenseClaimPayload {
  id?: number
  title: string
  reason: string
  // totalAmount 仅回显用，服务端按明细求和覆盖（spec §4.9）。
  totalAmount?: string
  items: ExpenseItemPayload[]
}

// 报销分页：status 精确过滤（0 草稿/1 待审/2 通过/3 驳回/4 撤回）；
// 服务端按可见范围返回（manage → 全租户、部门负责人 → 部门子树、否则本人）。
export function pageExpenseClaims(params?: { pageNo?: number; pageSize?: number; status?: number }) {
  return request.get('/oa/expense/claim/page', { params }) as Promise<any>
}
// 报销详情：表头 + 明细 + 附件 + bpm 审批时间线（草稿 approval 为空）；范围外 404。
export function getExpenseClaim(id: number) {
  return request.get(`/oa/expense/claim/${id}`) as Promise<any>
}
// 创建草稿（明细至少一条，每条金额 > 0，类别取字典 oa_expense_category）。
export function createExpenseClaim(data: ExpenseClaimPayload) {
  return request.post('/oa/expense/claim', data) as Promise<any>
}
// 更新草稿/已驳回单据：明细行集整体替换，服务端重算合计。
export function updateExpenseClaim(data: ExpenseClaimPayload) {
  return request.put('/oa/expense/claim', data) as Promise<any>
}
// 删除草稿/已驳回单据（明细级联软删；其他状态 409）。
export function deleteExpenseClaim(id: number) {
  return request.delete(`/oa/expense/claim/${id}`) as Promise<any>
}
// 提交审批（issue #55，oa:expense:submit）：仅本人、仅草稿/已驳回（其余 409）；
// 服务端在同一事务内生成 oa_expense_approval 实例（部门负责人 → oa:expense:manage），
// 审批人解析不到返回 400 且不改变状态。
export function submitExpenseClaim(id: number) {
  return request.post(`/oa/expense/claim/${id}/submit`) as Promise<any>
}
// 附件列表（新上传的在前）。
export function listExpenseClaimFiles(id: number) {
  return request.get(`/oa/expense/claim/${id}/files`) as Promise<any>
}
// 上传附件（multipart 字段名 file；仅草稿/已驳回）。
export function uploadExpenseClaimFile(id: number, file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(`/oa/expense/claim/${id}/files`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }) as Promise<any>
}
// 删除附件（仅草稿/已驳回）。
export function deleteExpenseClaimFile(id: number, fileId: number) {
  return request.delete(`/oa/expense/claim/${id}/files/${fileId}`) as Promise<any>
}
// 鉴权下载（后端返回的 url 已含 /admin-api 前缀，baseURL: '' 原样请求）。
export function downloadExpenseClaimFile(url: string) {
  return request.get(url, { baseURL: '', responseType: 'blob' }) as Promise<any>
}


// ---------- OA 日程（issue #56，spec #42 §4.11/§5） ----------
// 权限模型：列表/详情挂 oa:calendar:query（菜单 524 管理入口 / 536 我的日程），
// 可见性由服务端三态谓词收口（私有仅创建人、部门按创建时部门快照及子树、
// 公开全租户）；创建/编辑/取消逐码 oa:calendar:create/update/delete；
// 公开日程（visibility=2）另需 oa:calendar:public（缺失 403）。
// 起止时间为业务时区墙钟 "YYYY-MM-DD HH:mm:ss"。

export interface CalendarEventPayload {
  id?: number
  title: string
  description?: string
  startTime: string
  endTime: string
  allDay?: boolean
  // 0 私有 / 1 部门 / 2 公开（公开需 oa:calendar:public）。
  visibility: number
}

// 日程列表：按可见性返回有效日程（start_time ASC）。月/周视图传时间窗口
// （区间语义为「与日程相交」），关键词匹配标题；已取消日程不再返回。
export function listCalendarEvents(params?: { startTime?: string; endTime?: string; keyword?: string }) {
  return request.get('/oa/calendar/event/list', { params }) as Promise<any>
}
// 日程详情（范围外/跨租户/已取消 404）。
export function getCalendarEvent(id: number) {
  return request.get(`/oa/calendar/event/${id}`) as Promise<any>
}
// 创建日程（部门可见性用当前用户部门快照；公开需 oa:calendar:public）。
export function createCalendarEvent(data: CalendarEventPayload) {
  return request.post('/oa/calendar/event', data) as Promise<any>
}
// 编辑日程（仅创建人、仅有效日程；dept_id 快照不刷新）。
export function updateCalendarEvent(data: CalendarEventPayload) {
  return request.put('/oa/calendar/event', data) as Promise<any>
}
// 取消日程（软取消，仅创建人；取消后不再展示）。
export function cancelCalendarEvent(id: number) {
  return request.delete(`/oa/calendar/event/${id}`) as Promise<any>
}


// ---------- OA 文档目录与 ACL（issue #58，spec #42 §3.8/§4.12/§5） ----------
// 权限模型：目录树/详情挂 oa:document:query（菜单 526 管理入口 / 537 我的文档），
// 不可见目录不出现在树里、详情 404；创建挂 oa:document:create（5261），
// 顶层目录必须带显式 ACL；改名/删除/ACL 设置只要求目录管理权（目录 owner 或
// oa:document:manage），服务层对「可见无管理权」返回 403、不可见返回 404；
// 有子目录或文件的目录删除返回 409。文件上传/下载由 #59 落地（码 5263）。

// ACL 主体：0 全员（targetId=0）/ 1 部门（含子树）/ 2 角色 / 3 用户。
export interface DocumentACLPayload {
  targetType: number
  targetId: number
}

export interface DocumentFolderPayload {
  id?: number
  parentId?: number
  name: string
  sort?: number
  acl?: DocumentACLPayload[]
}

// 可见目录树：只含当前用户有读权限的目录；父目录不可见时子目录提升为根节点。
export function getDocumentFolderTree() {
  return request.get('/oa/document/folder/tree') as Promise<any>
}
// 目录详情（范围外/跨租户/无 ACL 顶层的非 owner 404）。
export function getDocumentFolder(id: number) {
  return request.get(`/oa/document/folder/${id}`) as Promise<any>
}
// 创建目录：parentId=0 为顶层（必须带显式 ACL）；子目录 acl 留空表示继承。
export function createDocumentFolder(data: DocumentFolderPayload) {
  return request.post('/oa/document/folder', data) as Promise<any>
}
// 改名/排序（parentId 不可变，v1 不做跨目录移动）。
export function updateDocumentFolder(data: DocumentFolderPayload) {
  const { id, name, sort } = data
  return request.put('/oa/document/folder', { id, name, sort }) as Promise<any>
}
// 删除目录：有子目录或文件返回 409。
export function deleteDocumentFolder(id: number) {
  return request.delete(`/oa/document/folder/${id}`) as Promise<any>
}
// 读取目录 ACL（需要目录管理权）：own entries + 最近祖先的有效条目。
export function getDocumentFolderACL(id: number) {
  return request.get(`/oa/document/folder/${id}/acl`) as Promise<any>
}
// 整体替换目录 ACL：顶层目录不允许清空（400）；子目录清空表示继承父目录。
export function setDocumentFolderACL(id: number, entries: DocumentACLPayload[]) {
  return request.put(`/oa/document/folder/${id}/acl`, { entries }) as Promise<any>
}

// ---------- OA 会议室与预约（issue #57，spec #42 §4.11/§4.14/§5） ----------
// 权限模型：会议室查询/详情与预约列表挂 oa:room:query（菜单 525 管理入口 /
// 538 会议室预约 / 539 我的会议）；会议室 CRUD/启停/删除挂 oa:room:manage
// （有未来有效预约时删除返回 409，只能停用）；创建预约挂 oa:room:booking；
// 取消预约登录即可（后端校验预约人本人或 oa:room:manage）。
// 无 oa:room:manage 的查看者在预约列表里只会看到本人预约（服务端收口）。

export interface MeetingRoomPayload {
  name: string
  location?: string
  capacity?: number
  sort?: number
}

export interface RoomBookingPayload {
  roomId: number
  title: string
  // 业务时区墙钟 "YYYY-MM-DD HH:mm:ss"。
  startTime: string
  endTime: string
}

// 启用会议室列表（员工预约下拉）。
export function listMeetingRooms() {
  return request.get('/oa/room/list') as Promise<any>
}
// 会议室分页（名称模糊、状态精确）——管理端。
export function pageMeetingRooms(params: any) {
  return request.get('/oa/room/page', { params }) as Promise<any>
}
export function getMeetingRoom(id: number) {
  return request.get(`/oa/room/${id}`) as Promise<any>
}
export function createMeetingRoom(data: MeetingRoomPayload) {
  return request.post('/oa/room', data) as Promise<any>
}
export function updateMeetingRoom(id: number, data: MeetingRoomPayload) {
  return request.put(`/oa/room/${id}`, data) as Promise<any>
}
// 启停：0 启用 / 1 停用。
export function setMeetingRoomStatus(id: number, status: number) {
  return request.put(`/oa/room/${id}/status`, { status }) as Promise<any>
}
// 删除：有未来有效预约时 409（只能停用）。
export function deleteMeetingRoom(id: number) {
  return request.delete(`/oa/room/${id}`) as Promise<any>
}

// 预约分页：roomId/date/status 可选；管理端可按 userId 过滤，员工侧强制本人。
export function pageRoomBookings(params: any) {
  return request.get('/oa/room/booking/list', { params }) as Promise<any>
}
export function getRoomBooking(id: number) {
  return request.get(`/oa/room/booking/${id}`) as Promise<any>
}
// 创建预约：同事务生成部门可见日程（source=room_booking）；冲突 400。
export function createRoomBooking(data: RoomBookingPayload) {
  return request.post('/oa/room/booking', data) as Promise<any>
}
// 取消预约：同步软取消生成的日程；重复取消 409。
export function cancelRoomBooking(id: number) {
  return request.post(`/oa/room/booking/${id}/cancel`) as Promise<any>
}
