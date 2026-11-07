<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="message:log"
      title="发送日志"
      :action-width="170"
    >
      <template #toolbar>
        <el-button v-if="canDelete" type="danger" @click="handleClear">清空日志</el-button>
      </template>
      <template #cell-channelType="{ row }">
        <el-tag size="small" effect="plain">{{ row.channelType === 'mail' ? '邮件' : '短信' }}</el-tag>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '成功' : '失败' }}
        </el-tag>
      </template>
      <template #cell-errorMessage="{ row }">
        <span class="error-message">{{ row.errorMessage || '-' }}</span>
      </template>
      <template #cell-sentAt="{ row }">{{ formatTime(row.sentAt) }}</template>
      <template #cell-createTime="{ row }">{{ formatTime(row.createTime) }}</template>
      <template #actions="{ row }">
        <el-button v-if="row.status === 1 && canResend" link type="primary" @click="handleResend(row)">
          重发
        </el-button>
        <el-button v-if="canDelete" link type="danger" @click="handleDelete(row)">删除</el-button>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { pageMessageLogs, resendMessageLog, deleteMessageLog, clearMessageLogs } from '@/api/message'

// 发送日志（issue #72，spec §6）：列表 + 失败重发 + 删除/清空。
// 日志按租户隔离（后端租户插件），跨租户 ID 一律 404。

const userStore = useUserStore()
const tableRef = ref()

const canResend = computed(() => userStore.hasPerm('message:log:resend'))
const canDelete = computed(() => userStore.hasPerm('message:log:delete'))

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'channelType', label: '类型', width: 80, slot: true },
  { prop: 'templateCode', label: '模板编码', minWidth: 130 },
  { prop: 'recipient', label: '收件人', minWidth: 160 },
  { prop: 'title', label: '标题', minWidth: 160 },
  { prop: 'status', label: '结果', width: 80, slot: true },
  { prop: 'errorMessage', label: '失败原因', minWidth: 180, slot: true },
  { prop: 'retryCount', label: '重发次数', width: 100 },
  { prop: 'sentAt', label: '发送时间', minWidth: 170, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, slot: true },
]

const searchItems = [
  {
    prop: 'channelType',
    label: '类型',
    type: 'select',
    options: [
      { label: '短信', value: 'sms' },
      { label: '邮件', value: 'mail' },
    ],
  },
  { prop: 'templateCode', label: '模板编码' },
  { prop: 'recipient', label: '收件人' },
  {
    prop: 'status',
    label: '结果',
    type: 'select',
    options: [
      { label: '成功', value: 0 },
      { label: '失败', value: 1 },
    ],
  },
]

const api = { page: pageMessageLogs }

function reload() {
  tableRef.value?.reload()
}

async function handleResend(row: any) {
  await resendMessageLog(row.id)
  ElMessage.success('已重新发送')
  reload()
}

async function handleDelete(row: any) {
  await ElMessageBox.confirm('确认删除该条发送日志吗？', '提示', { type: 'warning' })
  await deleteMessageLog(row.id)
  ElMessage.success('删除成功')
  reload()
}

async function handleClear() {
  await ElMessageBox.confirm('确认清空当前租户的全部发送日志吗？', '提示', { type: 'warning' })
  await clearMessageLogs()
  ElMessage.success('已清空')
  reload()
}
</script>

<style scoped>
.error-message {
  color: var(--el-color-danger);
}
</style>
