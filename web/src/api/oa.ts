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
