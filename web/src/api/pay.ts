import request from '@/utils/request'

// ---------- 支付应用（租户级） ----------
export function pageApps(params: any) {
  return request.get('/pay/app/page', { params }) as Promise<any>
}
export function getApp(id: number) {
  return request.get(`/pay/app/${id}`) as Promise<any>
}
export function createApp(data: any) {
  return request.post('/pay/app', data) as Promise<any>
}
export function updateApp(data: any) {
  return request.put('/pay/app', data) as Promise<any>
}
export function deleteApp(id: number) {
  return request.delete(`/pay/app/${id}`) as Promise<any>
}
// 渠道绑定（全量替换，spec §5）：channelCodes 为绑定码数组。
export function getAppChannels(id: number) {
  return request.get(`/pay/app/${id}/channels`) as Promise<any>
}
export function updateAppChannels(id: number, channelCodes: string[]) {
  return request.put(`/pay/app/${id}/channels`, { channelCodes }) as Promise<any>
}

// ---------- 支付渠道（平台级，仅平台租户可管理） ----------
export function pageChannels(params: any) {
  return request.get('/pay/channel/page', { params }) as Promise<any>
}
// 渠道只启停 + 凭证合并（提交 ****** 保留原值），14 行种子常驻禁删（spec §4.10）。
export function updateChannel(id: number, data: { status?: number; config?: string; remark?: string }) {
  return request.put(`/pay/channel/${id}`, data) as Promise<any>
}
// 租户侧只读清单（code/名称/状态，无凭证）：应用详情绑定 tab 数据源。
export function listTenantChannels() {
  return request.get('/pay/channel/list') as Promise<any>
}
