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
        <a :href="row.url" target="_blank" class="file-link">{{ row.name }}</a>
      </template>
      <template #cell-size="{ row }">{{ formatSize(row.size) }}</template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="preview(row)">预览</el-button>
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
import { pageFiles, uploadFile, deleteFile } from '@/api/infra'

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

function preview(row: any) {
  window.open(row.url, '_blank')
}
</script>

<style scoped>
.file-link {
  color: #409eff;
}

.tip {
  color: #909399;
  font-size: 12px;
  margin-left: 10px;
}
</style>
