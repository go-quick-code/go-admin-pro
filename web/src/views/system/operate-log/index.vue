<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="system:operate-log"
      title="操作日志"
      :show-actions="false"
    >
      <template #toolbar>
        <el-button v-if="userStore.hasPerm('system:operate-log:delete')" type="danger" plain @click="handleClear">
          清空日志
        </el-button>
      </template>
      <template #cell-type="{ row }">
        <el-tag size="small" effect="plain">{{ operateTypeLabel(row.type) }}</el-tag>
      </template>
      <template #cell-requestMethod="{ row }">
        <el-tag size="small" :type="methodType(row.requestMethod)">{{ row.requestMethod }}</el-tag>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '成功' : '失败' }}
        </el-tag>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, operateTypeOptions } from '@/utils/format'
import { pageOperateLogs, clearOperateLogs } from '@/api/system'

const userStore = useUserStore()

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'username', label: '操作人', minWidth: 110 },
  { prop: 'module', label: '模块', minWidth: 100 },
  { prop: 'name', label: '操作对象', minWidth: 110 },
  { prop: 'type', label: '类型', width: 90, slot: true },
  { prop: 'requestMethod', label: '方法', width: 90, slot: true },
  { prop: 'requestUrl', label: '请求地址', minWidth: 240 },
  { prop: 'status', label: '状态', width: 80, slot: true },
  { prop: 'duration', label: '耗时(ms)', width: 100 },
  { prop: 'operateTime', label: '操作时间', minWidth: 170, formatter: (row: any) => formatTime(row.operateTime) },
]

const searchItems = [
  { prop: 'username', label: '操作人' },
  { prop: 'module', label: '模块' },
  {
    prop: 'status',
    label: '状态',
    type: 'select',
    options: [
      { label: '成功', value: 0 },
      { label: '失败', value: 1 },
    ],
  },
]

const api = { page: pageOperateLogs }

function operateTypeLabel(type: number) {
  return operateTypeOptions.find((item) => item.value === type)?.label || '-'
}

function methodType(method: string) {
  if (method === 'POST') return 'success'
  if (method === 'PUT' || method === 'PATCH') return 'warning'
  if (method === 'DELETE') return 'danger'
  return 'info'
}

async function handleClear() {
  await ElMessageBox.confirm('确认清空当前租户的全部操作日志吗？', '提示', { type: 'warning' })
  await clearOperateLogs()
  ElMessage.success('操作日志已清空')
  window.location.reload()
}
</script>
