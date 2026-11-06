<template>
  <div class="app-container">
    <!-- 访问日志（issue #80，spec §7.2）：列表/过滤（路径/状态/时间段/用户）/
         批量删除/清空。日志按租户隔离（后端租户插件），跨租户 ID 删除 404。 -->
    <el-form :inline="true" :model="query" class="search-form">
      <el-form-item label="路径">
        <el-input v-model="query.path" clearable placeholder="请输入路径" style="width: 200px" @keyup.enter="handleSearch" />
      </el-form-item>
      <el-form-item label="状态码">
        <el-input v-model="statusInput" clearable placeholder="如 200" style="width: 120px" @keyup.enter="handleSearch" />
      </el-form-item>
      <el-form-item label="用户">
        <el-input v-model="query.username" clearable placeholder="请输入用户名" style="width: 160px" @keyup.enter="handleSearch" />
      </el-form-item>
      <el-form-item label="时间段">
        <el-date-picker
          v-model="timeRange"
          type="datetimerange"
          range-separator="至"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
          value-format="YYYY-MM-DD HH:mm:ss"
          style="width: 360px"
        />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="handleSearch">查询</el-button>
        <el-button @click="handleReset">重置</el-button>
      </el-form-item>
    </el-form>

    <div class="toolbar">
      <el-button v-if="canDelete" type="danger" plain :disabled="!selectedIds.length" @click="handleDeleteSelected">
        批量删除{{ selectedIds.length ? `（${selectedIds.length}）` : '' }}
      </el-button>
      <el-button v-if="canDelete" type="danger" plain @click="handleClear">清空日志</el-button>
    </div>

    <el-table v-loading="loading" :data="list" border stripe @selection-change="handleSelectionChange">
      <el-table-column v-if="canDelete" type="selection" width="45" />
      <el-table-column prop="id" label="ID" width="80" show-overflow-tooltip />
      <el-table-column prop="createTime" label="时间" :min-width="170" show-overflow-tooltip>
        <template #default="{ row }">{{ formatTime(row.createTime) }}</template>
      </el-table-column>
      <el-table-column prop="username" label="用户" :min-width="110" show-overflow-tooltip />
      <el-table-column prop="requestMethod" label="方法" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="methodType(row.requestMethod)">{{ row.requestMethod }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="requestPath" label="路径" :min-width="240" show-overflow-tooltip />
      <el-table-column prop="requestQuery" label="Query" :min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ row.requestQuery || '-' }}</template>
      </el-table-column>
      <el-table-column prop="responseStatus" label="状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="statusType(row.responseStatus)">{{ row.responseStatus }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="durationMs" label="耗时(ms)" width="100" />
      <el-table-column prop="clientIp" label="客户端 IP" :min-width="130" show-overflow-tooltip />
      <el-table-column prop="requestId" label="Request ID" :min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ row.requestId || '-' }}</template>
      </el-table-column>
    </el-table>

    <el-pagination
      v-model:current-page="pageNo"
      v-model:page-size="pageSize"
      :total="total"
      :page-sizes="[10, 20, 50, 100]"
      layout="total, sizes, prev, pager, next"
      class="pager"
      @size-change="load"
      @current-change="load"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { pageAPILogs, deleteAPILogs, clearAPILogs } from '@/api/infra'

const userStore = useUserStore()
const canDelete = computed(() => userStore.hasPerm('infra:api-log:delete'))

const loading = ref(false)
const list = ref<any[]>([])
const total = ref(0)
const pageNo = ref(1)
const pageSize = ref(20)
const selectedIds = ref<number[]>([])

const query = ref<{ path: string; username: string }>({ path: '', username: '' })
const statusInput = ref('')
const timeRange = ref<[string, string] | null>(null)

function methodType(method: string) {
  if (method === 'POST') return 'success'
  if (method === 'PUT' || method === 'PATCH') return 'warning'
  if (method === 'DELETE') return 'danger'
  return 'info'
}

// 状态码着色：2xx 成功、4xx 警告、5xx/其他 危险。
function statusType(code: number) {
  if (code >= 200 && code < 400) return 'success'
  if (code >= 400 && code < 500) return 'warning'
  return 'danger'
}

async function load() {
  loading.value = true
  try {
    const params: any = {
      pageNo: pageNo.value,
      pageSize: pageSize.value,
      path: query.value.path || undefined,
      username: query.value.username || undefined,
    }
    const status = Number(statusInput.value)
    if (statusInput.value !== '' && Number.isFinite(status)) params.status = status
    if (timeRange.value) {
      params.beginTime = timeRange.value[0]
      params.endTime = timeRange.value[1]
    }
    const res = await pageAPILogs(params)
    list.value = res?.list || []
    total.value = res?.total || 0
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pageNo.value = 1
  load()
}

function handleReset() {
  query.value = { path: '', username: '' }
  statusInput.value = ''
  timeRange.value = null
  handleSearch()
}

function handleSelectionChange(rows: any[]) {
  selectedIds.value = rows.map((r) => r.id)
}

async function handleDeleteSelected() {
  await ElMessageBox.confirm(`确认删除选中的 ${selectedIds.value.length} 条访问日志吗？`, '提示', { type: 'warning' })
  await deleteAPILogs(selectedIds.value)
  ElMessage.success('删除成功')
  selectedIds.value = []
  load()
}

async function handleClear() {
  await ElMessageBox.confirm('确认清空当前租户的全部访问日志吗？', '提示', { type: 'warning' })
  await clearAPILogs()
  ElMessage.success('访问日志已清空')
  load()
}

onMounted(load)
</script>

<style scoped>
.toolbar {
  margin-bottom: 12px;
}
.pager {
  margin-top: 12px;
  justify-content: flex-end;
}
</style>
