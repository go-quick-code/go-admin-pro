import request from '@/utils/request'

export function login(data: { username: string; password: string; tenantId?: number; captchaKey?: string; captchaCode?: string }) {
  return request.post('/system/auth/login', data) as Promise<any>
}

export function getCaptcha() {
  return request.get('/system/auth/captcha') as Promise<any>
}

export function getUserInfo() {
  return request.get('/system/auth/user-info') as Promise<any>
}

export function logout() {
  return request.post('/system/auth/logout') as Promise<any>
}

export function updateProfile(data: any) {
  return request.put('/system/auth/profile', data) as Promise<any>
}

export function changePassword(data: { oldPassword: string; newPassword: string }) {
  return request.put('/system/auth/password', data) as Promise<any>
}
