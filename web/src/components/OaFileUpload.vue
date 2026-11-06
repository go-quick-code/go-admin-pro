<template>
  <div v-loading="loading" class="oa-file-upload">
    <div v-if="canUpload || hint" class="oa-file-upload__toolbar">
      <el-upload
        v-if="canUpload"
        :show-file-list="false"
        :http-request="handleUpload"
        :accept="accept"
        :disabled="uploading"
      >
        <el-button type="primary" plain :loading="uploading">{{ uploadText }}</el-button>
      </el-upload>
      <span v-if="hint" class="oa-file-upload__hint">{{ hint }}</span>
    </div>

    <el-table v-if="files.length" :data="files" border size="small" class="oa-file-upload__table">
      <el-table-column label="文件名" min-width="200">
        <template #default="{ row }">
          <el-button link type="primary" @click="download(row)">{{ row.name }}</el-button>
        </template>
      </el-table-column>
      <el-table-column label="大小" width="110">
        <template #default="{ row }">{{ formatSize(row.size) }}</template>
      </el-table-column>
      <el-table-column v-if="showUploader" label="上传人" width="140">
        <template #default="{ row }">{{ row.uploaderName || row.uploaderId || '-' }}</template>
      </el-table-column>
      <el-table-column v-if="showCreateTime" label="上传时间" width="170">
        <template #default="{ row }">{{ row.createTime || '-' }}</template>
      </el-table-column>
      <el-table-column v-if="canDelete" label="操作" width="80">
        <template #default="{ row }">
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-else-if="emptyText && !loading" :description="emptyText" :image-size="50" />
  </div>
</template>

<script lang="ts">
// OaFileItem 是 OA 各业务附件/文件的通用展示行（id 为业务归属行 ID，
// url 为后端返回的鉴权下载地址）。公告/请假/报销/文档都据此接入。
export interface OaFileItem {
  id: number
  name: string
  size: number
  type?: string
  url?: string
  uploaderId?: number
  uploaderName?: string
  createTime?: string
}
</script>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { formatSize } from '@/utils/format'
import { saveBlobAs } from '@/utils/download'

// OA 共用文件上传组件（issue #59）：把「上传按钮 + 文件表格 + 鉴权下载 +
// 删除确认」收在一处，业务页面只提供三个 API 适配函数：
//   - uploadHandler(file)          → 调各自的 multipart 上传接口；
//   - downloadHandler(row)         → 用 row.url 拉 blob（responseType=blob）；
//   - deleteHandler(row)           → 调各自的删除接口。
// 上传/删除成功后组件只发事件，列表刷新由调用方决定（保持组件无数据权）。
// 现有公告/请假/报销页面可按同一 props 接入，无需改后端。

interface Props {
  files: OaFileItem[]
  canUpload?: boolean
  canDelete?: boolean
  uploadHandler?: (file: File) => Promise<unknown>
  downloadHandler?: (file: OaFileItem) => Promise<unknown>
  deleteHandler?: (file: OaFileItem) => Promise<void>
  accept?: string
  uploadText?: string
  hint?: string
  emptyText?: string
  showUploader?: boolean
  showCreateTime?: boolean
  loading?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  canUpload: false,
  canDelete: false,
  uploadHandler: undefined,
  downloadHandler: undefined,
  deleteHandler: undefined,
  accept:
    '.jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.json,.zip,.rar,.7z,.mp4,.mp3',
  uploadText: '上传文件',
  hint: '',
  emptyText: '',
  showUploader: false,
  showCreateTime: false,
  loading: false,
})

const emit = defineEmits<{
  (e: 'uploaded', file: unknown): void
  (e: 'deleted', file: OaFileItem): void
}>()

const uploading = ref(false)

async function handleUpload(options: any) {
  if (!props.uploadHandler) return
  uploading.value = true
  try {
    const uploaded = await props.uploadHandler(options.file as File)
    ElMessage.success('上传成功')
    emit('uploaded', uploaded)
  } catch {
    // 业务错误已由请求拦截器统一提示，这里只结束 loading。
  } finally {
    uploading.value = false
  }
}

async function download(row: OaFileItem) {
  if (!props.downloadHandler) return
  try {
    const response = await props.downloadHandler(row)
    saveBlobAs(response, row.name)
  } catch {
    // 请求拦截器已提示。
  }
}

async function remove(row: OaFileItem) {
  if (!props.deleteHandler) return
  try {
    await ElMessageBox.confirm(`确认删除文件「${row.name}」？`, '删除文件', { type: 'warning' })
  } catch {
    return
  }
  try {
    await props.deleteHandler(row)
    ElMessage.success('文件已删除')
    emit('deleted', row)
  } catch {
    // 请求拦截器已提示。
  }
}
</script>

<style scoped>
.oa-file-upload__toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.oa-file-upload__hint {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.oa-file-upload__table {
  width: 100%;
}
</style>
