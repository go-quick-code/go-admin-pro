<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="pay:notify-task"
      title="回调通知"
      @row-click="openDetail"
    >
      <template #cell-type="{ row }">
        <el-tag size="small">{{ typeLabel(row.type) }}</el-tag>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-notifyTimes="{ row }">
        <span>{{ row.notifyTimes }} / {{ row.maxNotifyTimes }}</span>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openDetail(row)">日志</el-button>
      </template>
    </ProTable>

    <!-- 执行日志抽屉（spec §6：回调通知 = 任务列表 + 日志子表）：append-only
        逐次落行（spec §4.10），response 为响应摘要（后端截断 1KB）。 -->
    <el-drawer v-model="detailVisible" :title="detailTitle" size="860px">
      <el-descriptions :column="2" border size="small" class="task-summary">
        <el-descriptions-item label="任务 ID">{{ task?.id }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ task ? typeLabel(task.type) : '-' }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag v-if="task" :type="statusTagType(task.status)" size="small">{{ statusLabel(task.status) }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="已通知次数">{{ task ? `${task.notifyTimes} / ${task.maxNotifyTimes}` : '-' }}</el-descriptions-item>
        <el-descriptions-item label="消费方单号" :span="2">{{ task?.merchantOrderId || '-' }}</el-descriptions-item>
        <el-descriptions-item label="业务单据 ID">{{ task?.dataId }}</el-descriptions-item>
        <el-descriptions-item label="回调地址" :span="3">
          <span class="notify-url">{{ task?.notifyUrl || '-' }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="上次执行时间">{{ formatTime(task?.lastExecuteTime) || '-' }}</el-descriptions-item>
        <el-descriptions-item label="下次通知时间">{{ formatTime(task?.nextNotifyTime) || '-' }}</el-descriptions-item>
      </el-descriptions>

      <el-table v-loading="logLoading" :data="logs" size="small" border>
        <el-table-column prop="notifyTimes" label="第几次" width="80" />
        <el-table-column label="本次结果" width="140">
          <template #default="{ row }">
            <el-tag :type="logTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="response" label="响应摘要" min-width="220" show-overflow-tooltip />
        <el-table-column label="执行时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.createTime) }}</template>
        </el-table-column>
      </el-table>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { getNotifyTask, pageNotifyTasks } from '@/api/pay'

// 回调通知页（issue #134，spec §6）：商户回调任务列表 + 执行日志抽屉。
// 只读（交易单据 append-only，spec §4.10——无新增/编辑/删除按钮；导出随
// #138 补种）。type 1 支付/2 退款/3 转账；status 0 等待/10 成功/20 彻底失败/
// 21 请求成功但结果失败/22 请求失败（spec §3.3）。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('pay:notify-task:query'))

const typeLabels: Record<number, string> = { 1: '支付', 2: '退款', 3: '转账' }
function typeLabel(type: number) {
  return typeLabels[type] ?? String(type)
}

const statusLabels: Record<number, string> = {
  0: '等待',
  10: '成功',
  20: '彻底失败',
  21: '请求成功但结果失败',
  22: '请求失败',
}
function statusLabel(status: number) {
  return statusLabels[status] ?? String(status)
}
function statusTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 20) return 'danger'
  if (status === 0) return 'info'
  return 'warning' // 21/22 继续重试
}
function logTagType(status: number) {
  if (status === 10) return 'success'
  return 'danger' // 21/22 本次执行未成功
}

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'type', label: '类型', width: 90, slot: true },
  { prop: 'status', label: '状态', width: 160, slot: true },
  { prop: 'merchantOrderId', label: '消费方单号', minWidth: 160, showOverflowTooltip: true },
  { prop: 'dataId', label: '业务单据', width: 100 },
  { prop: 'notifyTimes', label: '已通知次数', width: 110, slot: true },
  {
    prop: 'lastExecuteTime',
    label: '上次执行时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.lastExecuteTime) || '-',
  },
  {
    prop: 'nextNotifyTime',
    label: '下次通知时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.nextNotifyTime) || '-',
  },
]

const searchItems = [
  {
    prop: 'type',
    label: '类型',
    type: 'select',
    options: [
      { label: '支付', value: 1 },
      { label: '退款', value: 2 },
      { label: '转账', value: 3 },
    ],
  },
  {
    prop: 'status',
    label: '状态',
    type: 'select',
    options: [
      { label: '等待', value: 0 },
      { label: '成功', value: 10 },
      { label: '彻底失败', value: 20 },
      { label: '请求成功但结果失败', value: 21 },
      { label: '请求失败', value: 22 },
    ],
  },
  { prop: 'merchantOrderId', label: '消费方单号' },
]

const api = { page: pageNotifyTasks }

// ---------- 执行日志抽屉 ----------

const task = ref<any>(null)
const detailVisible = ref(false)
const logLoading = ref(false)
const logs = ref<any[]>([])

const detailTitle = computed(() => (task.value ? `回调任务日志：#${task.value.id}` : '回调任务日志'))

async function openDetail(row: any) {
  task.value = row
  detailVisible.value = true
  logLoading.value = true
  try {
    const detail = await getNotifyTask(row.id)
    logs.value = detail.logs || []
  } finally {
    logLoading.value = false
  }
}
</script>

<style scoped>
.task-summary {
  margin-bottom: 12px;
}

.notify-url {
  word-break: break-all;
}
</style>
