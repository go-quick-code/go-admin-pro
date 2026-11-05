<template>
  <span class="excel-actions">
    <template v-if="canImport">
      <el-button :loading="importing" @click="pickFile">导入</el-button>
      <el-button v-if="templateAction" link type="primary" :loading="templating" @click="downloadTemplate">
        下载模板
      </el-button>
    </template>
    <el-button v-if="canExport" type="success" plain :loading="exporting" @click="handleExport">
      导出
    </el-button>
    <input
      ref="fileInput"
      type="file"
      accept=".xlsx"
      class="excel-file-input"
      @change="onFileChange"
    />

    <el-dialog v-model="errorVisible" title="导入失败" width="720px" append-to-body>
      <el-alert
        :title="errorSummary"
        type="error"
        :closable="false"
        show-icon
        class="import-alert"
      />
      <el-table :data="errors" border stripe size="small" max-height="420">
        <el-table-column prop="row" label="行号" width="80" />
        <el-table-column prop="column" label="列名" width="140">
          <template #default="{ row }">{{ row.column || '-' }}</template>
        </el-table-column>
        <el-table-column prop="msg" label="原因" min-width="260" />
      </el-table>
      <template #footer>
        <el-button type="primary" @click="errorVisible = false">知道了</el-button>
      </template>
    </el-dialog>
  </span>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { saveBlobResponse } from '@/utils/download'

// Excel 导入/模板/导出按钮组（issue #20）：客户、联系人、产品复用同一交互。
// - 导入按钮与模板受 :import 权限码控制，导出受 :export 控制；
// - 导入失败的行级明细（code=4201）在对话框里按 行号/列名/原因 展示；
// - 导出把当前列表筛选作为查询参数（由父页面通过 export-params 提供），
//   后端仍会再套一遍数据范围谓词。

export interface ImportCellError {
  row: number
  column?: string
  msg: string
}

const props = defineProps<{
  // title 用于成功文案与兜底文件名（如「客户」）。
  title: string
  importPerm?: string
  exportPerm?: string
  importAction?: (file: File) => Promise<any>
  templateAction?: () => Promise<any>
  exportAction?: (params: Record<string, any>) => Promise<any>
  // exportParams 返回当前列表筛选（pageNo/pageSize 与空值会被剔除）。
  exportParams?: () => Record<string, any>
}>()

const emit = defineEmits<{ success: [] }>()

const userStore = useUserStore()
const fileInput = ref<HTMLInputElement>()
const importing = ref(false)
const templating = ref(false)
const exporting = ref(false)
const errorVisible = ref(false)
const errors = ref<ImportCellError[]>([])
const errorTotal = ref(0)
const errorTruncated = ref(false)

const canImport = computed(() =>
  Boolean(props.importPerm && props.importAction && userStore.hasPerm(props.importPerm)),
)
const canExport = computed(() =>
  Boolean(props.exportPerm && props.exportAction && userStore.hasPerm(props.exportPerm)),
)
const errorSummary = computed(() => {
  if (errorTruncated.value) {
    return `共 ${errorTotal.value} 处错误，仅显示前 ${errors.value.length} 条；整批未导入，请修正后重新上传。`
  }
  return `共 ${errorTotal.value} 处错误；整批未导入，请修正后重新上传。`
})

function pickFile() {
  fileInput.value?.click()
}

function onFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !props.importAction) return
  if (!/\.xlsx$/i.test(file.name)) {
    ElMessage.error('请选择 .xlsx 格式的 Excel 文件')
    return
  }
  if (file.size > 10 * 1024 * 1024) {
    ElMessage.error('文件超过大小上限：最大 10 MB')
    return
  }
  void doImport(file)
}

async function doImport(file: File) {
  importing.value = true
  try {
    const result = await props.importAction!(file)
    ElMessage.success(`导入成功：${result?.count ?? 0} 条`)
    emit('success')
  } catch (err: any) {
    const detail = extractValidation(err)
    if (detail) {
      errors.value = detail.errors
      errorTotal.value = detail.total
      errorTruncated.value = detail.truncated
      errorVisible.value = true
    }
    // 限额/表头等错误由 request 拦截器弹出后端 msg。
  } finally {
    importing.value = false
  }
}

// extractValidation 从业务错误（HTTP 200 code=4201）中取出行级明细；
// 其他错误（400/500）没有明细，返回 undefined 交给拦截器提示。
function extractValidation(err: any) {
  const body = err?.responseBody ?? err?.response?.data
  const data = body?.data
  if (!data || !Array.isArray(data.errors) || data.errors.length === 0) return undefined
  return {
    errors: data.errors as ImportCellError[],
    total: Number(data.total ?? data.errors.length),
    truncated: Boolean(data.truncated),
  }
}

async function downloadTemplate() {
  if (!props.templateAction) return
  templating.value = true
  try {
    const response = await props.templateAction()
    saveBlobResponse(response, `${props.title}导入模板`)
  } catch {
    // 错误提示由 request 拦截器统一处理。
  } finally {
    templating.value = false
  }
}

async function handleExport() {
  if (!props.exportAction) return
  exporting.value = true
  try {
    const response = await props.exportAction(filteredParams())
    saveBlobResponse(response, `${props.title}列表`)
    ElMessage.success('导出成功')
  } catch {
    // 错误提示由 request 拦截器统一处理。
  } finally {
    exporting.value = false
  }
}

// filteredParams 剔除分页参数与空值：空字符串传给 Go 的 int 指针字段会 400。
function filteredParams() {
  const source = props.exportParams?.() || {}
  const out: Record<string, any> = {}
  for (const [key, value] of Object.entries(source)) {
    if (key === 'pageNo' || key === 'pageSize') continue
    if (value === '' || value === null || value === undefined) continue
    out[key] = value
  }
  return out
}
</script>

<style scoped>
.excel-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.excel-file-input {
  display: none;
}
.import-alert {
  margin-bottom: 12px;
}
</style>
