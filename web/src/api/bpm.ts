import request from '@/utils/request'

// ---------- bpm 流程定义（issue #44，spec #42 §5 bpm 节） ----------
// 权限码 bpm:definition:*（菜单种子 510/511/5111+）；发布与停用共用
// bpm:definition:publish。实例/任务接口由 #46 追加。

export function pageDefinitions(params: any) {
  return request.get('/bpm/definition/page', { params }) as Promise<any>
}
export function getDefinition(id: number) {
  return request.get(`/bpm/definition/${id}`) as Promise<any>
}
// 新建草稿；同 key 再建即「发新版本」（版本号由服务端发放）。
export function createDefinition(data: any) {
  return request.post('/bpm/definition', data) as Promise<any>
}
// 编辑草稿（key/businessType 不可变；仅草稿可编辑）。
export function updateDefinition(data: any) {
  return request.put('/bpm/definition', data) as Promise<any>
}
// 删除草稿（仅草稿且无实例；已发布/停用只能停用）。
export function deleteDefinition(id: number) {
  return request.delete(`/bpm/definition/${id}`) as Promise<any>
}
// 发布草稿（同 key 旧发布版本自动停用）。
export function publishDefinition(id: number) {
  return request.post(`/bpm/definition/${id}/publish`) as Promise<any>
}
// 停用发布版本（停用后不可发起新实例，在途实例继续）。
export function disableDefinition(id: number) {
  return request.post(`/bpm/definition/${id}/disable`) as Promise<any>
}

// ---------- bpm 运行：实例/任务（issue #46，spec #42 §5 bpm 节） ----------
// 通用待办页三 tab（待办/已办/我发起）共用 /bpm/task/page?tab=...；
// 审批动作以「当前办理人/发起人」身份收口（后端判定，无权限码门槛）。

export interface BpmTaskItem {
  id: number
  instanceId: number
  nodeIndex: number
  nodeName: string
  status: number
  handledAt: string | null
  createTime: string
  instanceTitle: string
  instanceSummary: string
  businessType: string
  businessId: number
  instanceStatus: number
  initiatorId: number
  definitionKey: string
  definitionVersion: number
}

export function pageTasks(params: { pageNo: number; pageSize: number; tab: string }) {
  return request.get('/bpm/task/page', { params }) as Promise<{
    list: BpmTaskItem[]
    total: number
  }>
}

export function pageInstances(params: {
  pageNo: number
  pageSize: number
  businessType?: string
  status?: number
}) {
  return request.get('/bpm/instance/page', { params }) as Promise<any>
}

export interface BpmRecordItem {
  id: number
  instanceId: number
  taskId: number
  nodeIndex: number
  nodeName: string
  operatorId: number
  operatorName: string
  action: number
  comment: string
  transferToUserId: number
  transferToUserName?: string
  createTime: string
}

export interface BpmInstanceDetail {
  instance: any
  records: BpmRecordItem[]
  tasks: any[]
}

export function getInstanceDetail(id: number) {
  return request.get(`/bpm/instance/${id}`) as Promise<BpmInstanceDetail>
}

export function approveTask(id: number, comment: string) {
  return request.post(`/bpm/task/${id}/approve`, { comment }) as Promise<any>
}

export function rejectTask(id: number, comment: string) {
  return request.post(`/bpm/task/${id}/reject`, { comment }) as Promise<any>
}

export function transferTask(id: number, toUserId: number, comment: string) {
  return request.post(`/bpm/task/${id}/transfer`, { toUserId, comment }) as Promise<any>
}

export function withdrawInstance(id: number, comment: string) {
  return request.post(`/bpm/instance/${id}/withdraw`, { comment }) as Promise<any>
}

export function listSimpleUsers() {
  return request.get('/system/user/simple-list') as Promise<any[]>
}
