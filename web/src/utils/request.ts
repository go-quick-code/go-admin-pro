import axios from 'axios'
import { ElMessage } from 'element-plus'
import { getToken, removeToken } from './auth'

const service = axios.create({
  baseURL: '/admin-api',
  timeout: 30000,
})

service.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

service.interceptors.response.use(
  (response) => {
    if (response.config.responseType === 'blob') {
      return response as any
    }
    const body = response.data
    if (body && typeof body === 'object' && 'code' in body) {
      if (body.code === 0) {
        return body.data
      }
      const err: any = new Error(body.msg || '请求失败')
      // 业务错误码与 data 一并挂到错误上：Excel 导入失败（code=4201）需要
      // 在页面上按行展示 data.errors，而不是只弹一条概要。
      err.responseBody = body
      if (!response.config.silent) {
        ElMessage.error(body.msg || '请求失败')
      }
      return Promise.reject(err)
    }
    return body
  },
  async (error) => {
    const status = error.response?.status
    let msg = error.response?.data?.msg
    // responseType=blob 时后端错误体也是 Blob，需要先读文本再解析信封。
    const raw = error.response?.data
    if (!msg && raw instanceof Blob) {
      try {
        const body = JSON.parse(await raw.text())
        msg = body?.msg
      } catch {
        // 非 JSON 错误体按状态码兜底
      }
    }
    msg = msg || error.message || '网络异常'
    if (status === 401) {
      removeToken()
      if (!window.location.pathname.startsWith('/login')) {
        const redirect = encodeURIComponent(window.location.pathname + window.location.search)
        window.location.href = `/login?redirect=${redirect}`
      }
    } else if (!error.config?.silent) {
      ElMessage.error(msg)
    }
    return Promise.reject(error)
  },
)

// silent 请求不弹全局错误提示：后台轮询/重连类调用（如推送换票）失败时
// 由调用方自行降级，不能每次重试都打扰用户。
declare module 'axios' {
  export interface AxiosRequestConfig {
    silent?: boolean
  }
}

export default service
