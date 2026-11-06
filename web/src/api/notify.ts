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
