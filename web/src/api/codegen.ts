import request from '@/utils/request'

export function listCodegenTables(keyword?: string) {
  return request.get('/infra/codegen/tables', { params: { keyword } }) as Promise<any>
}

export function getCodegenTable(tableName: string) {
  return request.get(`/infra/codegen/tables/${tableName}/columns`) as Promise<any>
}

export function generateCode(data: any) {
  return request.post('/infra/codegen/generate', data, { responseType: 'blob' }) as Promise<any>
}
