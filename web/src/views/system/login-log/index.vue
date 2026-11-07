<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="system:login-log"
      title="登录日志"
      :show-actions="false"
    >
      <template #toolbar>
        <el-button v-if="userStore.hasPerm('system:login-log:delete')" type="danger" plain @click="handleClear">
          清空日志
        </el-button>
      </template>
      <template #cell-result="{ row }">
        <el-tag :type="row.result === 0 ? 'success' : 'danger'" size="small">
          {{ row.result === 0 ? '成功' : '失败' }}
        </el-tag>
      </template>
      <template #cell-userAgent="{ row }">
        <span :title="row.userAgent">{{ truncate(row.userAgent, 42) }}</span>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { pageLoginLogs, clearLoginLogs } from '@/api/system'

const userStore = useUserStore()

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'username', label: '用户名', minWidth: 110 },
  { prop: 'loginIp', label: '登录 IP', minWidth: 130 },
  { prop: 'loginLocation', label: '归属地', minWidth: 160 },
  { prop: 'result', label: '结果', width: 80, slot: true },
  { prop: 'message', label: '说明', minWidth: 140 },
  { prop: 'userAgent', label: 'User-Agent', minWidth: 240, slot: true },
  { prop: 'loginTime', label: '登录时间', minWidth: 170, formatter: (row: any) => formatTime(row.loginTime) },
]

const searchItems = [
  { prop: 'username', label: '用户名' },
  { prop: 'loginIp', label: '登录 IP' },
  {
    prop: 'result',
    label: '结果',
    type: 'select',
    options: [
      { label: '成功', value: 0 },
      { label: '失败', value: 1 },
    ],
  },
]

const api = { page: pageLoginLogs }

function truncate(value: string, max: number) {
  if (!value) return '-'
  return value.length > max ? `${value.slice(0, max)}...` : value
}

async function handleClear() {
  await ElMessageBox.confirm('确认清空当前租户的全部登录日志吗？', '提示', { type: 'warning' })
  await clearLoginLogs()
  ElMessage.success('登录日志已清空')
  window.location.reload()
}
</script>
