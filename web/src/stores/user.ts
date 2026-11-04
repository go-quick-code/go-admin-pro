import { defineStore } from 'pinia'
import { getToken, setToken, removeToken } from '@/utils/auth'
import { login as loginApi, getUserInfo as getUserInfoApi, logout as logoutApi } from '@/api/auth'

export interface MenuItem {
  id: number
  name: string
  path: string
  component: string
  componentName: string
  icon?: string
  type: number
  visible?: boolean
  children?: MenuItem[]
}

export const useUserStore = defineStore('user', {
  state: () => ({
    token: getToken(),
    userInfo: null as any,
    roles: [] as string[],
    permissions: [] as string[],
    menus: [] as MenuItem[],
    loaded: false,
  }),
  actions: {
    async login(payload: any) {
      const data = await loginApi(payload)
      this.token = data.accessToken
      setToken(data.accessToken)
    },
    async fetchUserInfo() {
      const data = await getUserInfoApi()
      this.userInfo = data.user
      this.roles = data.roles || []
      this.permissions = data.permissions || []
      this.menus = data.menus || []
      this.loaded = true
    },
    hasPerm(code: string) {
      return this.permissions.includes('*') || this.permissions.includes(code)
    },
    async logout() {
      try {
        await logoutApi()
      } catch {
        // 忽略退出接口异常，客户端仍然清理登录态
      }
      this.reset()
    },
    reset() {
      this.token = ''
      this.userInfo = null
      this.roles = []
      this.permissions = []
      this.menus = []
      this.loaded = false
      removeToken()
    },
  },
})
