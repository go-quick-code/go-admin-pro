import type { RouteRecordRaw } from 'vue-router'
import type { MenuItem } from '@/stores/user'

const modules = import.meta.glob('../views/**/*.vue')

function joinPath(parent: string, child: string) {
  if (!child) return parent || '/'
  if (child.startsWith('/')) return child
  const base = parent.endsWith('/') ? parent.slice(0, -1) : parent
  return `${base}/${child}`.replace(/\/+/g, '/')
}

// buildRoutes 把后端菜单树转换成 vue-router 路由。
export function buildRoutes(menus: MenuItem[]): RouteRecordRaw[] {
  const routes: RouteRecordRaw[] = []
  const walk = (items: MenuItem[], parentPath = '') => {
    for (const item of items) {
      if (item.type === 3) continue
      const path = joinPath(parentPath, item.path || '')
      if (item.type === 2 && item.component) {
        const key = `../views/${item.component}.vue`
        const loader = modules[key]
        if (loader) {
          routes.push({
            path,
            name: item.componentName || `menu-${item.id}`,
            component: loader,
            meta: { title: item.name, icon: item.icon },
          })
        } else {
          console.warn('未找到菜单组件：', key)
        }
      }
      if (item.children?.length) {
        walk(item.children, path)
      }
    }
  }
  walk(menus)
  return routes
}
