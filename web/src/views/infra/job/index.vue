<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="infra:job"
      title="定时任务"
      dialog-width="680px"
      :action-width="240"
    >
      <template #cell-handlerName="{ row }">
        <el-tag size="small" type="info" effect="plain">{{ row.handlerName }}</el-tag>
      </template>
      <template #cell-cronExpression="{ row }">
        <span class="mono">{{ row.cronExpression }}</span>
      </template>
      <template #cell-status="{ row }">
        <el-switch :model-value="row.status === 0" @change="toggleStatus(row)" />
      </template>
      <template #cell-lastRunAt="{ row }">{{ formatTime(row.lastRunAt) }}</template>
      <template #cell-nextRunAt="{ row }">{{ formatTime(row.nextRunAt) }}</template>
      <template #actions="{ row }">
        <el-button link type="success" @click="handleRun(row)">执行</el-button>
        <el-button link type="primary" @click="openLogs(row)">日志</el-button>
      </template>
    </ProTable>

    <el-drawer v-model="logDrawer" :title="`执行日志 - ${currentJob?.name || ''}`" size="62%">
      <el-table v-loading="logLoading" :data="logs" border stripe>
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="beginTime" label="开始时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.beginTime) }}</template>
        </el-table-column>
        <el-table-column prop="duration" label="耗时(ms)" width="100" />
        <el-table-column label="结果" width="80">
          <template #default="{ row }">
            <el-tag :type="row.result === 0 ? 'success' : 'danger'" size="small">
              {{ row.result === 0 ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="message" label="执行信息" min-width="220" show-overflow-tooltip />
      </el-table>
      <el-pagination
        v-model:current-page="logQuery.pageNo"
        v-model:page-size="logQuery.pageSize"
        :total="logTotal"
        layout="total, prev, pager, next"
        class="pager"
        @current-change="loadLogs"
      />
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageJobs,
  createJob,
  updateJob,
  updateJobStatus,
  deleteJob,
  runJob,
  listJobHandlers,
  pageJobLogs,
} from '@/api/infra'

const tableRef = ref()
const handlerOptions = ref<{ label: string; value: string }[]>([])
const logDrawer = ref(false)
const currentJob = ref<any>(null)
const logs = ref<any[]>([])
const logTotal = ref(0)
const logLoading = ref(false)
const logQuery = reactive({ pageNo: 1, pageSize: 10 })

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '任务名称', minWidth: 140 },
  { prop: 'handlerName', label: '处理器', minWidth: 130, slot: true },
  { prop: 'cronExpression', label: 'Cron 表达式', minWidth: 150, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'lastRunAt', label: '上次执行', minWidth: 170, slot: true },
  { prop: 'nextRunAt', label: '下次执行', minWidth: 170, slot: true },
]

const searchItems = [
  { prop: 'name', label: '任务名称' },
  { prop: 'handlerName', label: '处理器' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = computed(() => [
  { prop: 'name', label: '任务名称', required: true },
  { prop: 'handlerName', label: '任务处理器', type: 'select', options: handlerOptions.value, required: true },
  { prop: 'handlerParam', label: '任务参数' },
  {
    prop: 'cronExpression',
    label: 'Cron 表达式',
    required: true,
    placeholder: '6 段，含秒，如 0 */5 * * * *',
  },
  { prop: 'retryCount', label: '重试次数', type: 'number' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
])

const defaultForm = { retryCount: 0, status: 1, handlerParam: '' }

const api = {
  page: pageJobs,
  create: createJob,
  update: updateJob,
  remove: deleteJob,
}

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await updateJobStatus({ id: row.id, status: next })
  row.status = next
  ElMessage.success(next === 0 ? '任务已启动' : '任务已暂停')
  tableRef.value?.reload()
}

async function handleRun(row: any) {
  await runJob(row.id)
  ElMessage.success('已触发执行，请稍后查看日志')
}

async function openLogs(row: any) {
  currentJob.value = row
  logQuery.pageNo = 1
  logDrawer.value = true
  await loadLogs()
}

async function loadLogs() {
  if (!currentJob.value) return
  logLoading.value = true
  try {
    const data = await pageJobLogs({
      jobId: currentJob.value.id,
      pageNo: logQuery.pageNo,
      pageSize: logQuery.pageSize,
    })
    logs.value = data.list || []
    logTotal.value = data.total || 0
  } finally {
    logLoading.value = false
  }
}

onMounted(async () => {
  const handlers: string[] = await listJobHandlers()
  handlerOptions.value = handlers.map((name) => ({ label: name, value: name }))
})
</script>
