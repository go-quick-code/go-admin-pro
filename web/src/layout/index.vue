<template>
  <el-container class="layout">
    <el-aside :width="collapsed ? '64px' : '220px'" class="aside">
      <div class="logo">
        <div class="logo-mark">G</div>
        <span v-show="!collapsed" class="logo-text">go-admin-pro</span>
      </div>
      <el-scrollbar>
        <el-menu
          :default-active="activeMenu"
          :collapse="collapsed"
          :collapse-transition="false"
          background-color="#1d2129"
          text-color="#bfcbd9"
          active-text-color="#ffffff"
          router
          unique-opened
        >
          <SidebarItem v-for="item in userStore.menus" :key="item.id" :item="item" />
        </el-menu>
      </el-scrollbar>
    </el-aside>

    <el-container class="main-wrap">
      <el-header class="navbar">
        <div class="navbar-left">
          <el-icon class="collapse-btn" @click="collapsed = !collapsed">
            <Fold v-if="!collapsed" />
            <Expand v-else />
          </el-icon>
          <el-breadcrumb separator="/">
            <el-breadcrumb-item :to="{ path: '/dashboard' }">首页</el-breadcrumb-item>
            <el-breadcrumb-item v-if="route.meta.title">{{ route.meta.title }}</el-breadcrumb-item>
          </el-breadcrumb>
        </div>
        <div class="navbar-right">
          <NotifyBell />
          <el-tag size="small" type="info" effect="plain">租户 {{ userStore.userInfo?.tenantId || 1 }}</el-tag>
          <el-dropdown trigger="click" @command="handleCommand">
            <div class="user-entry">
              <el-avatar :size="30" class="avatar">
                {{ (userStore.userInfo?.nickname || userStore.userInfo?.username || 'U').slice(0, 1) }}
              </el-avatar>
              <span class="username">{{ userStore.userInfo?.nickname || userStore.userInfo?.username }}</span>
              <el-icon><ArrowDown /></el-icon>
            </div>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="profile">个人中心</el-dropdown-item>
                <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import NotifyBell from '@/components/NotifyBell.vue'
import SidebarItem from './SidebarItem.vue'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()
const collapsed = ref(false)

const activeMenu = computed(() => route.path)

async function handleCommand(command: string) {
  if (command === 'profile') {
    router.push('/profile')
    return
  }
  if (command === 'logout') {
    await ElMessageBox.confirm('确认退出登录吗？', '提示', { type: 'warning' })
    await userStore.logout()
    // 重新加载以清理动态路由
    window.location.href = '/login'
  }
}
</script>

<style scoped>
.layout {
  height: 100vh;
}

.aside {
  background-color: #1d2129;
  transition: width 0.2s;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.logo {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 16px;
  color: #fff;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}

.logo-mark {
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: linear-gradient(135deg, #409eff, #6a5acd);
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  font-size: 18px;
  flex-shrink: 0;
}

.logo-text {
  font-weight: 600;
  font-size: 16px;
  white-space: nowrap;
}

.aside :deep(.el-menu) {
  border-right: none;
}

.aside :deep(.el-menu-item.is-active) {
  background-color: #409eff !important;
}

.aside :deep(.el-sub-menu .el-menu-item) {
  background-color: #151820 !important;
}

.aside :deep(.el-sub-menu .el-menu-item.is-active) {
  background-color: #409eff !important;
}

.main-wrap {
  min-width: 0;
}

.navbar {
  height: 56px;
  background: #fff;
  display: flex;
  align-items: center;
  justify-content: space-between;
  box-shadow: 0 1px 4px rgba(0, 21, 41, 0.08);
  z-index: 10;
}

.navbar-left {
  display: flex;
  align-items: center;
  gap: 16px;
}

.collapse-btn {
  font-size: 20px;
  cursor: pointer;
  color: #606266;
}

.navbar-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.user-entry {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  color: #303133;
}

.avatar {
  background: #409eff;
  color: #fff;
  font-size: 14px;
}

.username {
  font-size: 14px;
}

.main {
  padding: 0;
  overflow: auto;
  background: #f2f4f7;
}
</style>
