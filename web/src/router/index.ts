import { createRouter, createWebHistory } from 'vue-router'
import { useUserStore } from '@/stores/user'
import { buildRoutes } from './dynamic'

const Layout = () => import('@/layout/index.vue')

export const constantRoutes = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/login/index.vue'),
    meta: { title: '登录' },
  },
  {
    // 社交登录回调页（issue #77）：三方 redirect_uri 指向这里，公开访问。
    path: '/social/callback',
    name: 'SocialCallback',
    component: () => import('@/views/social/callback.vue'),
    meta: { title: '社交登录回调', public: true },
  },
  {
    path: '/',
    name: 'Layout',
    component: Layout,
    redirect: '/dashboard',
    children: [
      {
        path: '/profile',
        name: 'Profile',
        component: () => import('@/views/profile/index.vue'),
        meta: { title: '个人中心' },
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: () => import('@/views/error/404.vue'),
    meta: { title: '404' },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes: constantRoutes,
})

router.beforeEach(async (to) => {
  const store = useUserStore()
  if (to.path === '/login') {
    return store.token ? '/' : true
  }
  // 社交登录回调页等公开路由：未登录也要可达（issue #77）。
  if (to.meta?.public) {
    return true
  }
  if (!store.token) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  if (!store.loaded) {
    try {
      await store.fetchUserInfo()
      buildRoutes(store.menus).forEach((route) => {
        router.addRoute('Layout', route)
      })
      // 仅按 path 重新匹配，避免沿用 NotFound 的 name 再次命中 404。
      return { path: to.path, query: to.query, hash: to.hash, replace: true }
    } catch {
      store.reset()
      return { path: '/login', query: { redirect: to.fullPath } }
    }
  }
  return true
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} - go-admin-pro` : 'go-admin-pro'
})

export default router
