import request from '@/utils/request'

// ---------- 消息渠道（平台级，仅平台租户可管理） ----------
export function pageChannels(params: any) {
  return request.get('/message/channel/page', { params }) as Promise<any>
}
export function createChannel(data: any) {
  return request.post('/message/channel', data) as Promise<any>
}
export function updateChannel(data: any) {
  return request.put('/message/channel', data) as Promise<any>
}
export function updateChannelStatus(id: number, status: number) {
  return request.put(`/message/channel/${id}/status`, { status }) as Promise<any>
}
export function deleteChannel(id: number) {
  return request.delete(`/message/channel/${id}`) as Promise<any>
}

// ---------- 消息模板（平台级，仅平台租户可管理） ----------
export function pageTemplates(params: any) {
  return request.get('/message/template/page', { params }) as Promise<any>
}
export function createTemplate(data: any) {
  return request.post('/message/template', data) as Promise<any>
}
export function updateTemplate(data: any) {
  return request.put('/message/template', data) as Promise<any>
}
export function deleteTemplate(id: number) {
  return request.delete(`/message/template/${id}`) as Promise<any>
}

// ---------- 发送 ----------
export function sendSms(data: any) {
  return request.post('/message/sms/send', data) as Promise<any>
}
export function sendMail(data: any) {
  return request.post('/message/mail/send', data) as Promise<any>
}

// ---------- 发送日志（租户级） ----------
export function pageMessageLogs(params: any) {
  return request.get('/message/log/page', { params }) as Promise<any>
}
export function resendMessageLog(id: number) {
  return request.post(`/message/log/${id}/resend`) as Promise<any>
}
export function deleteMessageLog(id: number) {
  return request.delete(`/message/log/${id}`) as Promise<any>
}
export function clearMessageLogs() {
  return request.delete('/message/log/clear') as Promise<any>
}
