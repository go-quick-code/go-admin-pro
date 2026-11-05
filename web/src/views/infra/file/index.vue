<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="infra:file"
      title="文件"
      :action-width="150"
    >
      <template #toolbar>
        <el-upload
          v-if="userStore.hasPerm('infra:file:upload')"
          :show-file-list="false"
          :http-request="handleUpload"
          :accept="accept"
        >
          <el-button type="success">上传文件</el-button>
        </el-upload>
        <span class="tip">支持图片、文档、压缩包等，单文件不超过 20MB</span>
      </template>
      <template #cell-name="{ row }">
        <el-link type="primary" @click="handleDownload(row)">{{ row.name }}</el-link>
      </template>
      <template #cell-size="{ row }">{{ formatSize(row.size) }}</template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="handleDownload(row)">下载</el-button>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatSize, formatTime } from '@/utils/format'
import { saveBlobAs } from '@/utils/download'
import { pageFiles, uploadFile, deleteFile, downloadFile } from '@/api/infra'

const userStore = useUserStore()
const tableRef = ref()
const accept = '.jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.json,.zip,.rar,.7z,.mp4,.mp3'

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '文件名', minWidth: 220, slot: true },
  { prop: 'size', label: '大小', width: 110, slot: true },
  { prop: 'type', label: '类型', minWidth: 140 },
  { prop: 'provider', label: '存储', width: 100 },
  { prop: 'createTime', label: '上传时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [{ prop: 'name', label: '文件名' }]

const api = {
  page: pageFiles,
  remove: deleteFile,
}

async function handleUpload(options: any) {
  const data = await uploadFile(options.file)
  ElMessage.success('上传成功')
  tableRef.value?.reload()
  return data
}

// 文件读取统一走鉴权下载端点（不再使用 row.url 直链 / window.open）。
async function handleDownload(row: any) {
  const response = await downloadFile(row.id)
  saveBlobAs(response, row.name)
}
</script>

<style scoped>
.tip {
  color: #909399;
  font-size: 12px;
  margin-left: 10px;
}
</style>
