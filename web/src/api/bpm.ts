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
