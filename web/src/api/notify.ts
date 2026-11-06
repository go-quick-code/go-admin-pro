import request from '@/utils/request'

// 站内通知（issue #45，顶栏铃铛 + 通知列表）。通知是个人数据：
// 后端按当前登录用户收口，这里不接受 userId 参数。

export interface NotifyMessage {
  id: number
  type: string
  title: string
  content: string
  bizType: string
  bizId: number
  route: string
  routeParams: string
  readAt: string | null
  createTime: string
}

export function pageMessages(params: {
  pageNo: number
  pageSize: number
  type?: string
  read?: boolean
}) {
  return request.get('/notify/message/page', { params }) as Promise<{
    list: NotifyMessage[]
    total: number
  }>
}

export function getUnreadCount() {
  return request.get('/notify/message/unread-count') as Promise<{ count: number }>
}

export function markMessageRead(id: number) {
  return request.put(`/notify/message/${id}/read`) as Promise<boolean>
}

export function markAllMessagesRead() {
  return request.put('/notify/message/read-all') as Promise<boolean>
}

// ---------- 实时推送（issue #78，spec §4.5）----------
// SSE/WebSocket 不能带 Authorization 头：先带 Bearer 换一次性票据，
// 再凭票据建立流，避免 JWT 进入 URL/日志（ADR 0013）。

export interface PushTicket {
  ticket: string
  expiresIn: number
  transports: string[]
}

// getPushTicket 换取一次性推送票据。silent：票据失败由 NotifyBell 自行
// 降级 60 秒轮询，不弹全局错误提示。
export function getPushTicket() {
  return request.post('/notify/push/ticket', null, { silent: true }) as Promise<PushTicket>
}
