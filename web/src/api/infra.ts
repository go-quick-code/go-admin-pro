import request from '@/utils/request'

// ---------- 文件 ----------
export function pageFiles(params: any) {
  return request.get('/infra/file/page', { params }) as Promise<any>
}
export function uploadFile(file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post('/infra/file/upload', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  }) as Promise<any>
}
export function deleteFile(id: number) {
  return request.delete(`/infra/file/${id}`) as Promise<any>
}
// 鉴权下载（PR #39 残余风险 A）：文件不再有公开静态地址，下载走该端点并带
// Authorization；responseType=blob 由页面按 Content-Disposition 保存。
export function downloadFile(id: number) {
  return request.get(`/infra/file/${id}/download`, { responseType: 'blob' }) as Promise<any>
}

// ---------- 参数配置 ----------
export function pageConfigs(params: any) {
  return request.get('/infra/config/page', { params }) as Promise<any>
}
export function createConfig(data: any) {
  return request.post('/infra/config', data) as Promise<any>
}
export function updateConfig(data: any) {
  return request.put('/infra/config', data) as Promise<any>
}
export function deleteConfig(id: number) {
  return request.delete(`/infra/config/${id}`) as Promise<any>
}

// ---------- 定时任务 ----------
export function pageJobs(params: any) {
  return request.get('/infra/job/page', { params }) as Promise<any>
}
export function listJobHandlers() {
  return request.get('/infra/job/handlers') as Promise<any>
}
export function createJob(data: any) {
  return request.post('/infra/job', data) as Promise<any>
}
export function updateJob(data: any) {
  return request.put('/infra/job', data) as Promise<any>
}
export function updateJobStatus(data: { id: number; status: number }) {
  return request.put('/infra/job/status', data) as Promise<any>
}
export function runJob(id: number) {
  return request.put(`/infra/job/${id}/run`) as Promise<any>
}
export function deleteJob(id: number) {
  return request.delete(`/infra/job/${id}`) as Promise<any>
}
export function pageJobLogs(params: any) {
  return request.get('/infra/job/log/page', { params }) as Promise<any>
}

// ---------- 数据库文档（issue #83，仅平台租户） ----------
export function listDBTables(keyword?: string) {
  return request.get('/infra/db-doc/tables', { params: { keyword: keyword || undefined } }) as Promise<any>
}
export function getDBTable(name: string) {
  return request.get(`/infra/db-doc/table/${name}`) as Promise<any>
}
// 导出 Markdown：responseType=blob，页面按 Content-Disposition 保存。
export function exportDBDoc() {
  return request.get('/infra/db-doc/export', { responseType: 'blob' }) as Promise<any>
}
