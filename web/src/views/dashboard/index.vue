<template>
  <div class="app-container">
    <el-row :gutter="16">
      <el-col v-for="card in cards" :key="card.label" :xs="12" :sm="12" :md="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon" :class="card.color">
            <el-icon :size="24"><component :is="card.icon" /></el-icon>
          </div>
          <div class="stat-body">
            <div class="stat-value">{{ card.value }}</div>
            <div class="stat-label">{{ card.label }}</div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="row-gap">
      <el-col :md="16" :xs="24">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span>最近登录</span>
              <el-tag size="small" type="info">最近 5 条</el-tag>
            </div>
          </template>
          <el-table v-loading="loading" :data="summary.recentLogins" size="small">
            <el-table-column prop="username" label="用户名" min-width="110" />
            <el-table-column prop="loginIp" label="登录 IP" min-width="130" />
            <el-table-column label="结果" width="80">
              <template #default="{ row }">
                <el-tag :type="row.result === 0 ? 'success' : 'danger'" size="small">
                  {{ row.result === 0 ? '成功' : '失败' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="message" label="说明" min-width="130" show-overflow-tooltip />
            <el-table-column prop="loginTime" label="时间" min-width="170">
              <template #default="{ row }">{{ formatTime(row.loginTime) }}</template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>

      <el-col :md="8" :xs="24">
        <el-card shadow="never" class="quick-card">
          <template #header>
            <div class="card-header"><span>快速入口</span></div>
          </template>
          <div class="quick-links">
            <el-button v-if="userStore.hasPerm('system:user:create')" @click="router.push('/system/user')">
              新增用户
            </el-button>
            <el-button v-if="userStore.hasPerm('system:role:query')" @click="router.push('/system/role')">
              角色权限
            </el-button>
            <el-button v-if="userStore.hasPerm('system:tenant:query')" @click="router.push('/system/tenant')">
              租户管理
            </el-button>
            <el-button v-if="userStore.hasPerm('infra:job:query')" @click="router.push('/infra/job')">
              定时任务
            </el-button>
            <el-button v-if="userStore.hasPerm('infra:codegen:query')" @click="router.push('/codegen')">
              代码生成
            </el-button>
          </div>
          <el-divider />
          <div class="welcome">
            <div class="welcome-title">你好，{{ userStore.userInfo?.nickname || userStore.userInfo?.username }}</div>
            <div class="welcome-desc">
              当前角色：{{ userStore.roles.join('、') || '未分配角色' }}
            </div>
            <div class="welcome-desc">所属部门：{{ userStore.userInfo?.deptName || '未分配部门' }}</div>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { User, UserFilled, OfficeBuilding, Menu } from '@element-plus/icons-vue'
import { useUserStore } from '@/stores/user'
import { getDashboardSummary } from '@/api/system'

const router = useRouter()
const userStore = useUserStore()
const loading = ref(false)
const summary = reactive<any>({
  userCount: 0,
  roleCount: 0,
  deptCount: 0,
  menuCount: 0,
  recentLogins: [],
})

const cards = computed(() => [
  { label: '用户总数', value: summary.userCount, icon: User, color: 'blue' },
  { label: '角色数量', value: summary.roleCount, icon: UserFilled, color: 'purple' },
  { label: '部门数量', value: summary.deptCount, icon: OfficeBuilding, color: 'green' },
  { label: '菜单数量', value: summary.menuCount, icon: Menu, color: 'orange' },
])

function formatTime(value: string) {
  if (!value) return '-'
  return value.replace('T', ' ').slice(0, 19)
}

onMounted(async () => {
  loading.value = true
  try {
    const data = await getDashboardSummary()
    Object.assign(summary, data)
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
.stat-card :deep(.el-card__body) {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
}

.stat-icon {
  width: 52px;
  height: 52px;
  border-radius: 14px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.stat-icon.blue {
  background: rgba(64, 158, 255, 0.12);
  color: #409eff;
}

.stat-icon.purple {
  background: rgba(124, 108, 245, 0.12);
  color: #7c6cf5;
}

.stat-icon.green {
  background: rgba(103, 194, 58, 0.12);
  color: #67c23a;
}

.stat-icon.orange {
  background: rgba(230, 162, 60, 0.12);
  color: #e6a23c;
}

.stat-value {
  font-size: 26px;
  font-weight: 700;
  line-height: 1.2;
}

.stat-label {
  color: #909399;
  font-size: 13px;
  margin-top: 2px;
}

.row-gap {
  margin-top: 16px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}

.quick-links {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.quick-links .el-button {
  margin-left: 0;
}

.welcome-title {
  font-size: 16px;
  font-weight: 600;
  margin-bottom: 8px;
}

.welcome-desc {
  color: #909399;
  font-size: 13px;
  line-height: 1.9;
}
</style>
