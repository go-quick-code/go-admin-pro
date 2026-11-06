<template>
  <div class="app-container">
    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <el-select v-model="query.status" clearable placeholder="全部状态" style="width: 120px" @change="loadAnnouncements">
            <el-option label="草稿" :value="0" />
            <el-option label="已发布" :value="1" />
            <el-option label="已撤回" :value="2" />
          </el-select>
          <el-input
            v-model="query.keyword"
            clearable
            placeholder="标题关键字"
            style="width: 220px"
            @clear="loadAnnouncements"
            @keyup.enter="loadAnnouncements"
          />
          <el-button :icon="Search" circle @click="loadAnnouncements" />
        </div>
        <div class="toolbar-right">
          <el-button v-if="canCreate" type="primary" @click="openCreate">新增公告</el-button>
        </div>
      </div>

      <el-table v-loading="loading" :data="rows" row-key="id">
        <el-table-column label="标题" min-width="260">
          <template #default="{ row }">
            <el-tag v-if="row.pinned" type="danger" size="small" effect="plain" class="pin-tag">置顶</el-tag>
            <el-button link type="primary" @click="openPreview(row)">{{ row.title }}</el-button>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="受众" width="100">
          <template #default="{ row }">{{ row.audienceType === 0 ? '全员' : '自定义' }}</template>
        </el-table-column>
        <el-table-column label="发布时间" width="170">
          <template #default="{ row }">{{ formatTime(row.publishedAt) }}</template>
        </el-table-column>
        <el-table-column label="更新时间" width="170">
          <template #default="{ row }">{{ formatTime(row.updateTime) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="250" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openPreview(row)">查看</el-button>
            <el-button v-if="row.status === 0 && canUpdate" link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button v-if="row.status !== 1 && canPublish" link type="success" @click="doPublish(row)">
              {{ row.status === 2 ? '重新发布' : '发布' }}
            </el-button>
            <el-button v-if="row.status === 1 && canPublish" link type="warning" @click="doWithdraw(row)">撤回</el-button>
            <el-button v-if="row.status === 0 && canDelete" link type="danger" @click="doDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pager">
        <el-pagination
          v-model:current-page="query.pageNo"
          :page-size="query.pageSize"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="loadAnnouncements"
        />
      </div>
    </el-card>

    <!-- 编辑弹窗（仅草稿）：富文本 + 受众 + 置顶 + 附件 -->
    <el-dialog
      v-model="editor.visible"
      :title="editor.id ? '编辑公告' : '新增公告'"
      width="860px"
      append-to-body
      @opened="syncEditorContent"
    >
      <el-form label-width="90px">
        <el-form-item label="标题">
          <el-input v-model="editor.title" maxlength="200" show-word-limit placeholder="公告标题" />
        </el-form-item>
        <el-form-item label="正文">
          <div class="rich-editor-wrap">
            <div class="rich-toolbar">
              <el-button size="small" @mousedown.prevent="execCommand('bold')"><b>B</b></el-button>
              <el-button size="small" @mousedown.prevent="execCommand('italic')"><i>I</i></el-button>
              <el-button size="small" @mousedown.prevent="execCommand('underline')"><u>U</u></el-button>
              <el-button size="small" @mousedown.prevent="execCommand('insertUnorderedList')">• 列表</el-button>
              <el-button size="small" @mousedown.prevent="execCommand('insertOrderedList')">1. 列表</el-button>
              <el-button size="small" @mousedown.prevent="execCommand('formatBlock', 'h3')">小标题</el-button>
              <el-button size="small" @mousedown.prevent="execCommand('formatBlock', 'p')">正文段</el-button>
              <el-button size="small" @mousedown.prevent="insertLink">链接</el-button>
            </div>
            <div ref="editorRef" class="rich-editor" contenteditable="true" />
            <div class="field-hint">服务端会用 bluemonday 白名单清洗 HTML（脚本/事件等会被移除）。</div>
          </div>
        </el-form-item>
        <el-form-item label="受众">
          <el-radio-group v-model="editor.audienceType">
            <el-radio :value="0">全员</el-radio>
            <el-radio :value="1">自定义</el-radio>
          </el-radio-group>
        </el-form-item>
        <template v-if="editor.audienceType === 1">
          <el-form-item label="部门">
            <el-tree-select
              v-model="editor.deptIds"
              :data="deptTree"
              :props="{ label: 'name', value: 'id', children: 'children' }"
              node-key="id"
              multiple
              show-checkbox
              check-strictly
              clearable
              default-expand-all
              style="width: 100%"
              placeholder="选择部门（含子树）"
            />
          </el-form-item>
          <el-form-item label="角色">
            <el-select v-model="editor.roleIds" multiple filterable style="width: 100%" placeholder="选择角色">
              <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
            </el-select>
          </el-form-item>
        </template>
        <el-form-item label="置顶">
          <el-switch v-model="editor.pinned" />
          <span class="field-hint-inline">置顶公告在员工列表最前</span>
        </el-form-item>
        <el-form-item label="附件">
          <div class="files-block">
            <el-upload
              v-if="editor.id && editor.status === 0"
              :show-file-list="false"
              :http-request="handleFileUpload"
              :accept="fileAccept"
            >
              <el-button type="primary" plain>上传附件</el-button>
            </el-upload>
            <span v-if="!editor.id" class="field-hint">保存草稿后可上传附件。</span>
            <el-table v-if="editorFiles.length" :data="editorFiles" border size="small">
              <el-table-column label="文件名" min-width="220">
                <template #default="{ row }">
                  <el-button link type="primary" @click="downloadAttachment(row)">{{ row.name }}</el-button>
                </template>
              </el-table-column>
              <el-table-column label="大小" width="110">
                <template #default="{ row }">{{ formatSize(row.size) }}</template>
              </el-table-column>
              <el-table-column v-if="editor.status === 0" label="操作" width="90">
                <template #default="{ row }">
                  <el-button link type="danger" @click="removeAttachment(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editor.visible = false">取消</el-button>
        <el-button type="primary" :loading="editor.saving" @click="saveAnnouncement">保存草稿</el-button>
      </template>
    </el-dialog>

    <!-- 详情预览 -->
    <el-dialog v-model="preview.visible" :title="preview.title" width="820px" append-to-body>
      <div class="preview-meta">
        <el-tag :type="statusTag(preview.status)" size="small">{{ statusLabel(preview.status) }}</el-tag>
        <span>{{ preview.audienceType === 0 ? '全员' : '自定义受众' }}</span>
        <span v-if="preview.pinned" class="pin-text">置顶</span>
        <span>发布时间：{{ formatTime(preview.publishedAt) }}</span>
        <span v-if="preview.status === 2" class="withdrawn">已撤回</span>
      </div>
      <div v-if="preview.targets?.length" class="preview-targets">
        受众：
        <el-tag v-for="t in preview.targets" :key="`${t.targetType}-${t.targetId}`" size="small" class="target-tag">
          {{ t.targetType === 1 ? '部门' : '角色' }} · {{ t.targetName || t.targetId }}
        </el-tag>
      </div>
      <div class="preview-content" v-html="preview.content" />
      <div v-if="previewFiles.length" class="preview-files">
        <div class="files-title">附件</div>
        <el-button v-for="f in previewFiles" :key="f.id" link type="primary" @click="downloadAttachment(f)">
          {{ f.name }}
        </el-button>
      </div>
      <template #footer>
        <el-button @click="preview.visible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { listSimpleRoles, treeDepts } from '@/api/system'
import { saveBlobAs } from '@/utils/download'
import { formatSize, formatTime } from '@/utils/format'
import {
  createAnnouncement,
  deleteAnnouncement,
  deleteAnnouncementFile,
  downloadAnnouncementFile,
  getAnnouncement,
  listAnnouncementFiles,
  pageAnnouncements,
  publishAnnouncement,
  updateAnnouncement,
  uploadAnnouncementFile,
  withdrawAnnouncement,
} from '@/api/oa'

// 公告管理页（issue #48，菜单 521）：列表、富文本编辑、受众选择、附件、置顶、
// 发布/撤回。富文本用轻量 contenteditable 编辑器（不新增依赖），服务端
// bluemonday 白名单清洗后入库并在详情以清洗后的 HTML 返回。

interface AnnouncementRow {
  id: number
  title: string
  status: number
  audienceType: number
  pinned: boolean
  publishedAt: string | null
  withdrawnAt: string | null
  createTime: string
  updateTime: string
}

interface AnnouncementTarget {
  targetType: number
  targetId: number
  targetName?: string
}

interface Attachment {
  id: number
  name: string
  url: string
  size: number
  type: string
}

const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('oa:announcement:create'))
const canUpdate = computed(() => userStore.hasPerm('oa:announcement:update'))
const canDelete = computed(() => userStore.hasPerm('oa:announcement:delete'))
const canPublish = computed(() => userStore.hasPerm('oa:announcement:publish'))

const loading = ref(false)
const rows = ref<AnnouncementRow[]>([])
const total = ref(0)
const query = ref({ pageNo: 1, pageSize: 10, status: undefined as number | undefined, keyword: '' })
const fileAccept =
  '.jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.json,.zip,.rar,.7z,.mp4,.mp3'
const deptTree = ref<any[]>([])
const roles = ref<any[]>([])

function statusLabel(status: number) {
  return status === 0 ? '草稿' : status === 1 ? '已发布' : '已撤回'
}
function statusTag(status: number) {
  return status === 0 ? 'info' : status === 1 ? 'success' : 'warning'
}

async function loadAnnouncements() {
  loading.value = true
  try {
    const params: Record<string, number | string | boolean> = {
      pageNo: query.value.pageNo,
      pageSize: query.value.pageSize,
    }
    if (query.value.status !== undefined && query.value.status !== null) params.status = query.value.status
    if (query.value.keyword) params.keyword = query.value.keyword
    const data = await pageAnnouncements(params)
    rows.value = data.list || []
    total.value = data.total || 0
  } finally {
    loading.value = false
  }
}

async function loadOptions() {
  // 部门树登录即可；角色简表需要 system:role:simple-list（超管天然持有）。
  // 任一失败都降级为空选项，不阻断公告列表（受众选择可留空后由后端校验）。
  try {
    deptTree.value = (await treeDepts()) || []
  } catch {
    deptTree.value = []
  }
  try {
    roles.value = (await listSimpleRoles()) || []
  } catch {
    roles.value = []
  }
}

// ---- 编辑弹窗 ----
const editorRef = ref<HTMLElement>()
const editor = ref({
  visible: false,
  saving: false,
  id: 0,
  status: 0,
  title: '',
  content: '',
  audienceType: 0,
  deptIds: [] as number[],
  roleIds: [] as number[],
  pinned: false,
})
const editorFiles = ref<Attachment[]>([])
// pendingEditorContent 暂存待写入 contenteditable 的 HTML：弹窗渲染完成
// （@opened）后再赋值，避免 nextTick 早于 teleport/过渡挂载导致内容丢失。
const pendingEditorContent = ref('')

function syncEditorContent() {
  if (editorRef.value) editorRef.value.innerHTML = pendingEditorContent.value
}

function resetEditor() {
  editor.value = {
    visible: true,
    saving: false,
    id: 0,
    status: 0,
    title: '',
    content: '',
    audienceType: 0,
    deptIds: [],
    roleIds: [],
    pinned: false,
  }
  editorFiles.value = []
  pendingEditorContent.value = ''
  nextTick(syncEditorContent)
}

function openCreate() {
  if (!canCreate.value) return
  resetEditor()
}

async function openEdit(row: AnnouncementRow) {
  const detail = await getAnnouncement(row.id)
  editor.value = {
    visible: true,
    saving: false,
    id: detail.id,
    status: detail.status,
    title: detail.title,
    content: detail.content,
    audienceType: detail.audienceType,
    deptIds: (detail.targets || []).filter((t: AnnouncementTarget) => t.targetType === 1).map((t: AnnouncementTarget) => t.targetId),
    roleIds: (detail.targets || []).filter((t: AnnouncementTarget) => t.targetType === 2).map((t: AnnouncementTarget) => t.targetId),
    pinned: detail.pinned,
  }
  pendingEditorContent.value = detail.content || ''
  nextTick(syncEditorContent)
  await loadEditorFiles(detail.id)
}

async function loadEditorFiles(id: number) {
  editorFiles.value = (await listAnnouncementFiles(id)) || []
}

function execCommand(command: string, value?: string) {
  document.execCommand(command, false, value)
  editorRef.value?.focus()
}

function insertLink() {
  const url = window.prompt('请输入链接地址（https://…）')
  if (!url) return
  document.execCommand('createLink', false, url)
  editorRef.value?.focus()
}

async function saveAnnouncement() {
  const content = editorRef.value?.innerHTML || ''
  if (!editor.value.title.trim()) {
    ElMessage.warning('请填写公告标题')
    return
  }
  if (!content.trim()) {
    ElMessage.warning('请填写公告正文')
    return
  }
  if (editor.value.audienceType === 1 && !editor.value.deptIds.length && !editor.value.roleIds.length) {
    ElMessage.warning('自定义受众至少选择一个部门或角色')
    return
  }
  // 全员受众不携带目标（后端拒绝“全员 + 目标”的歧义输入）。
  const targets: AnnouncementTarget[] =
    editor.value.audienceType === 1
      ? [
          ...editor.value.deptIds.map((id) => ({ targetType: 1, targetId: id })),
          ...editor.value.roleIds.map((id) => ({ targetType: 2, targetId: id })),
        ]
      : []
  editor.value.saving = true
  try {
    const payload = {
      title: editor.value.title,
      content,
      audienceType: editor.value.audienceType,
      targets,
      pinned: editor.value.pinned,
    }
    if (editor.value.id) {
      await updateAnnouncement({ ...payload, id: editor.value.id })
      ElMessage.success('已保存')
    } else {
      const detail = await createAnnouncement(payload)
      editor.value.id = detail.id
      editor.value.status = detail.status
      ElMessage.success('草稿已创建，可继续上传附件')
    }
    await loadEditorFiles(editor.value.id)
    await loadAnnouncements()
  } finally {
    editor.value.saving = false
  }
}

async function handleFileUpload(options: any) {
  if (!editor.value.id) return
  await uploadAnnouncementFile(editor.value.id, options.file)
  ElMessage.success('附件上传成功')
  await loadEditorFiles(editor.value.id)
}

async function removeAttachment(row: Attachment) {
  if (!editor.value.id) return
  const confirmed = await ElMessageBox.confirm(`确认删除附件「${row.name}」？`, '删除附件', { type: 'warning' }).catch(
    () => false,
  )
  if (!confirmed) return
  await deleteAnnouncementFile(editor.value.id, row.id)
  ElMessage.success('附件已删除')
  await loadEditorFiles(editor.value.id)
}

async function downloadAttachment(row: Attachment) {
  const response = await downloadAnnouncementFile(row.url)
  saveBlobAs(response, row.name)
}

// ---- 状态流转 ----
async function doPublish(row: AnnouncementRow) {
  const confirmed = await ElMessageBox.confirm(
    `确认发布公告「${row.title}」？发布后将向受众发送站内通知，且不可删除。`,
    '发布公告',
    { type: 'warning' },
  ).catch(() => false)
  if (!confirmed) return
  await publishAnnouncement(row.id)
  ElMessage.success('已发布')
  await loadAnnouncements()
}

async function doWithdraw(row: AnnouncementRow) {
  const confirmed = await ElMessageBox.confirm(
    `确认撤回公告「${row.title}」？撤回后员工列表不再展示，详情显示已撤回。`,
    '撤回公告',
    { type: 'warning' },
  ).catch(() => false)
  if (!confirmed) return
  await withdrawAnnouncement(row.id)
  ElMessage.success('已撤回')
  await loadAnnouncements()
}

async function doDelete(row: AnnouncementRow) {
  const confirmed = await ElMessageBox.confirm(`确认删除草稿「${row.title}」？`, '删除公告', { type: 'warning' }).catch(
    () => false,
  )
  if (!confirmed) return
  await deleteAnnouncement(row.id)
  ElMessage.success('已删除')
  await loadAnnouncements()
}

// ---- 详情预览 ----
const preview = ref<any>({ visible: false, title: '', content: '', status: 0, audienceType: 0, pinned: false, targets: [] })
const previewFiles = ref<Attachment[]>([])

async function openPreview(row: AnnouncementRow) {
  const detail = await getAnnouncement(row.id)
  preview.value = { ...detail, visible: true }
  previewFiles.value = (await listAnnouncementFiles(row.id)) || []
}

onMounted(async () => {
  await loadOptions()
  await loadAnnouncements()
})
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}
.toolbar-left,
.toolbar-right {
  display: flex;
  gap: 8px;
  align-items: center;
}
.pin-tag {
  margin-right: 6px;
}
.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
.rich-editor-wrap {
  width: 100%;
}
.rich-toolbar {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
.rich-editor {
  min-height: 160px;
  max-height: 360px;
  overflow: auto;
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  padding: 8px 10px;
  line-height: 1.6;
}
.field-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}
.field-hint-inline {
  margin-left: 8px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.files-block {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.preview-meta {
  display: flex;
  gap: 12px;
  align-items: center;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  margin-bottom: 8px;
}
.preview-targets {
  margin-bottom: 12px;
  font-size: 13px;
}
.target-tag {
  margin-right: 6px;
}
.preview-content {
  border-top: 1px solid var(--el-border-color-lighter);
  padding: 12px 2px;
  line-height: 1.7;
  min-height: 80px;
}
.preview-files {
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: 10px;
}
.files-title {
  font-weight: 600;
  margin-bottom: 6px;
}
.pin-text {
  color: var(--el-color-danger);
}
.withdrawn {
  color: var(--el-color-warning);
  font-weight: 600;
}
</style>
