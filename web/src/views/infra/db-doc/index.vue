<template>
  <div class="app-container">
    <el-card shadow="never">
      <template #header>
        <div class="header">
          <span class="title">数据库文档</span>
          <div class="actions">
            <el-input
              v-model="keyword"
              placeholder="搜索表名 / 注释"
              clearable
              style="width: 240px"
              @keyup.enter="loadTables"
            />
            <el-button type="primary" @click="loadTables">查询</el-button>
            <el-button type="success" :loading="exporting" @click="handleExport">导出 Markdown</el-button>
          </div>
        </div>
      </template>

      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="notice"
        title="读取 information_schema 生成全库数据文档（含 system_ / infra_ 业务表，排除迁移账本表）。仅平台租户可访问。"
      />

      <el-table v-loading="loading" :data="tables" border stripe>
        <el-table-column prop="name" label="表名" min-width="180" />
        <el-table-column prop="comment" label="表注释" min-width="220" />
        <el-table-column prop="createTime" label="创建时间" min-width="170" />
        <el-table-column prop="updateTime" label="更新时间" min-width="170" />
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row)">列详情</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty
        v-if="!loading && !tables.length"
        description="没有匹配的表"
        :image-size="90"
      />
    </el-card>

    <el-dialog v-model="dialogVisible" :title="`列详情：${detail.name}`" width="960px" destroy-on-close>
      <p class="detail-comment">{{ detail.comment || '（无表注释）' }}</p>
      <el-table v-loading="detailLoading" :data="detail.columns" size="small" border max-height="480">
        <el-table-column prop="ordinal" label="#" width="55" />
        <el-table-column prop="name" label="列名" min-width="130" />
        <el-table-column prop="columnType" label="类型" min-width="130" />
        <el-table-column label="可空" width="60">
          <template #default="{ row }">{{ row.nullable ? '是' : '否' }}</template>
        </el-table-column>
        <el-table-column label="主键" width="60">
          <template #default="{ row }">{{ row.primaryKey ? '是' : '' }}</template>
        </el-table-column>
        <el-table-column label="自增" width="60">
          <template #default="{ row }">{{ row.autoIncrement ? '是' : '' }}</template>
        </el-table-column>
        <el-table-column label="默认值" width="110">
          <template #default="{ row }">{{ row.defaultValue ?? '' }}</template>
        </el-table-column>
        <el-table-column prop="comment" label="注释" min-width="150" />
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { listDBTables, getDBTable, exportDBDoc } from '@/api/infra'

const loading = ref(false)
const exporting = ref(false)
const keyword = ref('')
const tables = ref<any[]>([])

const dialogVisible = ref(false)
const detailLoading = ref(false)
const detail = reactive({ name: '', comment: '', columns: [] as any[] })

async function loadTables() {
  loading.value = true
  try {
    tables.value = await listDBTables(keyword.value)
  } finally {
    loading.value = false
  }
}

async function openDetail(row: any) {
  detail.name = row.name
  detail.comment = row.comment || ''
  detail.columns = []
  dialogVisible.value = true
  detailLoading.value = true
  try {
    const data = await getDBTable(row.name)
    detail.comment = data.comment || ''
    detail.columns = data.columns || []
  } finally {
    detailLoading.value = false
  }
}

async function handleExport() {
  exporting.value = true
  try {
    const response: any = await exportDBDoc()
    const blob = response.data instanceof Blob ? response.data : new Blob([response.data])
    const disposition = response.headers?.['content-disposition'] || ''
    const match = /filename="?([^";]+)"?/.exec(disposition)
    const filename = match ? match[1] : 'db-doc.md'
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
    ElMessage.success('数据库文档导出已开始')
  } finally {
    exporting.value = false
  }
}

onMounted(loadTables)
</script>

<style scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.title {
  font-weight: 600;
}

.actions {
  display: flex;
  gap: 10px;
}

.notice {
  margin-bottom: 16px;
}

.detail-comment {
  margin: 0 0 12px;
  color: var(--el-text-color-secondary);
}
</style>
