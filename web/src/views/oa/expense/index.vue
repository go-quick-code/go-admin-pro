<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :show-actions="false"
      title="报销"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新建报销</el-button>
      </template>
      <template #cell-totalAmount="{ row }">
        <span class="amount">{{ row.totalAmount }}</span>
      </template>
      <template #cell-itemCount="{ row }">{{ row.itemCount }} 条</template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTag(row.status)" size="small">{{ statusText(row.status) }}</el-tag>
      </template>
      <template #cell-createTime="{ row }">{{ formatTime(row.createTime) }}</template>
      <template #cell-actions="{ row }">
        <el-button link type="info" @click.stop="openDetail(row.id)">详情</el-button>
        <el-button
          v-if="isOwner(row) && isEditable(row.status) && canUpdate"
          link
          type="primary"
          @click.stop="openEdit(row.id)"
        >
          编辑
        </el-button>
        <el-button
          v-if="isOwner(row) && isEditable(row.status) && canSubmit"
          link
          type="success"
          @click.stop="doSubmit(row)"
        >
          提交
        </el-button>
        <el-button
          v-if="isOwner(row) && isEditable(row.status) && canDelete"
          link
          type="danger"
          @click.stop="doDelete(row)"
        >
          删除
        </el-button>
      </template>
    </ProTable>

    <!-- 草稿编辑：表头 + 明细行（增删改排序），合计由服务端计算；此处仅预览。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="880px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="88px">
        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" maxlength="200" placeholder="如：十月客户拜访差旅" />
        </el-form-item>
        <el-form-item label="事由" prop="reason">
          <el-input
            v-model="form.reason"
            type="textarea"
            :rows="2"
            maxlength="500"
            show-word-limit
            placeholder="请填写报销事由"
          />
        </el-form-item>
      </el-form>

      <div class="section-head">
        <span>明细</span>
        <el-button size="small" type="primary" plain @click="addItem">添加明细</el-button>
      </div>
      <el-table :data="form.items" border size="small" empty-text="请至少添加一条明细">
        <el-table-column label="#" width="50">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column label="类别" min-width="120">
          <template #default="{ row }">
            <el-select v-model="row.category" placeholder="请选择" style="width: 100%">
              <el-option v-for="opt in categoryOptions" :key="opt" :label="opt" :value="opt" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="金额（元）" width="140">
          <template #default="{ row }">
            <el-input v-model="row.amount" placeholder="0.00" inputmode="decimal" />
          </template>
        </el-table-column>
        <el-table-column label="发生日期" width="150">
          <template #default="{ row }">
            <el-date-picker
              v-model="row.expenseDate"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="选择日期"
              style="width: 100%"
            />
          </template>
        </el-table-column>
        <el-table-column label="备注" min-width="150">
          <template #default="{ row }">
            <el-input v-model="row.remark" maxlength="200" placeholder="可选" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150">
          <template #default="{ $index }">
            <el-button link type="primary" :disabled="$index === 0" @click="moveItem($index, -1)">
              上移
            </el-button>
            <el-button
              link
              type="primary"
              :disabled="$index === form.items.length - 1"
              @click="moveItem($index, 1)"
            >
              下移
            </el-button>
            <el-button link type="danger" @click="removeItem($index)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="form-total">
        <span class="hint">合计（服务端按明细重算，手填金额无效）</span>
        <span class="amount">￥{{ formTotal }}</span>
      </div>
      <div class="hint">
        每条明细金额必须大于 0，类别取自系统字典 oa_expense_category；保存为草稿后可上传附件。
      </div>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveDraft">保存草稿</el-button>
      </template>
    </el-dialog>

    <!-- 详情：表头 +「明细 / 审批记录」两个 tab（issue #55：时间线 + 当前待办 +
         通用待办中心入口）。 -->
    <el-drawer v-model="detailVisible" title="报销详情" size="720px">
      <div v-loading="detailLoading">
        <template v-if="detail">
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="标题" :span="2">{{ detail.title }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="statusTag(detail.status)" size="small">
                {{ statusText(detail.status) }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="合计">
              <span class="amount">{{ detail.totalAmount }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="报销人">用户#{{ detail.userId }}</el-descriptions-item>
            <el-descriptions-item label="明细">{{ detail.itemCount }} 条</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
            <el-descriptions-item label="事由" :span="2">{{ detail.reason }}</el-descriptions-item>
            <el-descriptions-item v-if="detail.rejectReason" label="驳回原因" :span="2">
              {{ detail.rejectReason }}
            </el-descriptions-item>
          </el-descriptions>

          <el-tabs v-model="detailTab" class="detail-tabs">
            <el-tab-pane label="明细" name="detail">
              <div class="section-head"><span>明细</span></div>
              <el-table :data="detail.items" border size="small">
                <el-table-column type="index" label="#" width="50" />
                <el-table-column prop="category" label="类别" min-width="100" />
                <el-table-column label="金额（元）" width="120">
                  <template #default="{ row }">{{ row.amount }}</template>
                </el-table-column>
                <el-table-column prop="expenseDate" label="发生日期" width="120" />
                <el-table-column prop="remark" label="备注" min-width="150" />
              </el-table>
              <div class="form-total">
                <span class="hint">合计</span>
                <span class="amount">￥{{ detail.totalAmount }}</span>
              </div>

              <div class="section-head">
                <span>附件</span>
                <el-upload
                  v-if="isOwner(detail) && isEditable(detail.status) && canUpdate"
                  :show-file-list="false"
                  :http-request="handleUpload"
                  :accept="fileAccept"
                >
                  <el-button size="small">上传附件</el-button>
                </el-upload>
              </div>
              <el-table v-if="detail.files?.length" :data="detail.files" border size="small">
                <el-table-column label="文件名" min-width="220">
                  <template #default="{ row }">
                    <el-button link type="primary" class="file-link" @click="downloadFile(row)">
                      {{ row.name }}
                    </el-button>
                  </template>
                </el-table-column>
                <el-table-column label="大小" width="100">
                  <template #default="{ row }">{{ formatSize(row.size) }}</template>
                </el-table-column>
                <el-table-column
                  v-if="isOwner(detail) && isEditable(detail.status) && canUpdate"
                  label="操作"
                  width="80"
                >
                  <template #default="{ row }">
                    <el-button link type="danger" @click="removeFile(row)">删除</el-button>
                  </template>
                </el-table-column>
              </el-table>
              <div v-else class="hint">暂无附件；草稿/已驳回状态可上传。</div>
            </el-tab-pane>

            <!-- 审批记录（issue #55）：时间线 + 当前待办 + 通用待办中心入口。 -->
            <el-tab-pane label="审批记录" name="approval">
              <div class="section-head"><span>审批进度</span></div>
              <el-timeline v-if="detail.approval?.records?.length" class="timeline">
                <el-timeline-item
                  v-for="rec in detail.approval.records"
                  :key="rec.id"
                  :timestamp="formatTime(rec.createTime)"
                >
                  <div>
                    <el-tag :type="recordTag(rec.action)" size="small">{{ recordText(rec.action) }}</el-tag>
                    <span class="timeline-operator">{{ rec.operatorName || `用户#${rec.operatorId}` }}</span>
                  </div>
                  <div class="timeline-node">{{ rec.nodeName || '（实例级动作）' }}</div>
                  <div v-if="rec.comment" class="timeline-comment">{{ rec.comment }}</div>
                </el-timeline-item>
              </el-timeline>
              <el-empty
                v-else
                :description="detail.approvalInstanceId ? '暂无审批记录' : '尚未提交审批（草稿/已驳回可提交）'"
              />

              <div class="section-head"><span>当前待办</span></div>
              <el-table v-if="pendingTasks.length" :data="pendingTasks" border size="small">
                <el-table-column prop="nodeName" label="节点" min-width="120" />
                <el-table-column label="办理人" min-width="120">
                  <template #default="{ row }">
                    {{ row.assigneeName || `用户#${row.assigneeId}` }}
                  </template>
                </el-table-column>
                <el-table-column label="状态" width="80">
                  <template #default>待办</template>
                </el-table-column>
              </el-table>
              <div v-else class="hint">
                {{ detail.approvalInstanceId ? '当前没有待办任务（流程已结束）' : '尚未提交审批' }}
              </div>
              <div v-if="detail.approvalInstanceId" class="todo-link">
                <el-button type="primary" plain @click="goTodoCenter">前往待办中心</el-button>
              </div>
            </el-tab-pane>
          </el-tabs>
        </template>
      </div>
      <template #footer>
        <div class="drawer-footer">
          <el-button @click="detailVisible = false">关闭</el-button>
          <el-button
            v-if="detail && isOwner(detail) && isEditable(detail.status) && canUpdate"
            type="primary"
            @click="openEdit(detail.id)"
          >
            编辑
          </el-button>
          <el-button
            v-if="detail && isOwner(detail) && isEditable(detail.status) && canSubmit"
            type="success"
            :loading="submitting"
            @click="doSubmit(detail)"
          >
            提交审批
          </el-button>
          <el-button
            v-if="detail && isOwner(detail) && isEditable(detail.status) && canDelete"
            type="danger"
            plain
            @click="doDelete(detail)"
          >
            删除
          </el-button>
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatSize, formatTime } from '@/utils/format'
import { saveBlobAs } from '@/utils/download'
import { useUserStore } from '@/stores/user'
import { listDictDataByType } from '@/api/system'
import {
  createExpenseClaim,
  deleteExpenseClaim,
  deleteExpenseClaimFile,
  downloadExpenseClaimFile,
  getExpenseClaim,
  pageExpenseClaims,
  submitExpenseClaim,
  updateExpenseClaim,
  uploadExpenseClaimFile,
} from '@/api/oa'

// 报销（issue #54/#55，菜单 523 报销 / 535 我的报销）：列表 + 草稿编辑抽屉
// （表头 + 明细行增删改排序 + 合计预览）+ 详情（明细 / 审批记录两个 tab，
// 审批记录含时间线、当前待办与待办中心入口）。合计由服务端按明细重算（请求里
// 的 totalAmount 被忽略）；提交（POST /:id/submit）在事务内生成审批实例，
// 审批动作统一在通用待办完成。权限码 oa:expense:query/create/update/delete/submit。
const userStore = useUserStore()
const route = useRoute()
const router = useRouter()
const tableRef = ref()
const canQuery = computed(() => userStore.hasPerm('oa:expense:query'))
const canCreate = computed(() => userStore.hasPerm('oa:expense:create'))
const canUpdate = computed(() => userStore.hasPerm('oa:expense:update'))
const canDelete = computed(() => userStore.hasPerm('oa:expense:delete'))
const canSubmit = computed(() => userStore.hasPerm('oa:expense:submit'))
const currentUserId = computed(() => Number(userStore.userInfo?.id || 0))

const pageApi = { page: pageExpenseClaims }
const columns = computed(() => {
  const cols: any[] = [
    { prop: 'title', label: '标题', minWidth: 180 },
  ]
  // 管理/财务视角（有查询码）额外展示申请人；员工侧只看本人，无需该列。
  if (canQuery.value) {
    cols.push({ prop: 'userId', label: '申请人', width: 110 })
  }
  cols.push(
    { prop: 'totalAmount', label: '金额（元）', width: 130, slot: true },
    { prop: 'itemCount', label: '明细', width: 90, slot: true },
    { prop: 'status', label: '状态', width: 90, slot: true },
    { prop: 'createTime', label: '创建时间', minWidth: 170, slot: true },
    { prop: 'actions', label: '操作', width: 240, slot: true },
  )
  return cols
})
const searchItems = [
  {
    prop: 'status',
    label: '状态',
    type: 'select',
    options: [
      { label: '草稿', value: 0 },
      { label: '待审', value: 1 },
      { label: '通过', value: 2 },
      { label: '驳回', value: 3 },
      { label: '撤回', value: 4 },
    ],
  },
]

const statusTexts = ['草稿', '待审', '通过', '驳回', '撤回']
function statusText(status: number): string {
  return statusTexts[status] ?? `未知(${status})`
}
function statusTag(status: number): string {
  if (status === 2) return 'success'
  if (status === 3) return 'danger'
  if (status === 1) return 'warning'
  return 'info'
}
function isEditable(status: number): boolean {
  return status === 0 || status === 3
}
// 写动作只对发起人显示：管理/财务可在列表看到他人单据。
function isOwner(row: any): boolean {
  return currentUserId.value > 0 && Number(row?.userId) === currentUserId.value
}
function recordText(action: number): string {
  return ['通过', '驳回', '撤回', '转办'][action - 1] ?? `未知(${action})`
}
function recordTag(action: number): string {
  if (action === 1) return 'success'
  if (action === 2) return 'danger'
  return 'warning'
}

// ---------- 金额（分）工具：全程整数分，避免浮点误差 ----------
const MAX_AMOUNT_CENTS = 999999999999

// parseCents 把十进制字符串解析为分；非法/零/负/越界返回 null。
function parseCents(text: any): number | null {
  const s = String(text ?? '').trim()
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null
  const [intPart, fracPart = ''] = s.split('.')
  const cents = Number(intPart) * 100 + Number((fracPart + '00').slice(0, 2))
  if (!Number.isSafeInteger(cents) || cents <= 0 || cents > MAX_AMOUNT_CENTS) return null
  return cents
}
function centsToMoney(cents: number): string {
  return `${Math.floor(cents / 100)}.${String(cents % 100).padStart(2, '0')}`
}

// ---------- 类别字典 ----------
const categoryOptions = ref<string[]>([])
async function loadCategories() {
  const list = await listDictDataByType('oa_expense_category')
  const rows = Array.isArray(list) ? list : []
  categoryOptions.value = rows
    .map((row: any) => String(row?.value ?? ''))
    .filter((value: string) => value !== '')
}

// ---------- 表单 ----------
interface ItemRow {
  category: string
  amount: string
  expenseDate: string
  remark: string
}

const formVisible = ref(false)
const formTitle = ref('新建报销')
const saving = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({
  id: undefined as number | undefined,
  title: '',
  reason: '',
  items: [] as ItemRow[],
})
const rules = {
  title: [{ required: true, message: '请填写报销标题', trigger: 'blur' }],
  reason: [{ required: true, message: '请填写报销事由', trigger: 'blur' }],
}

const formTotal = computed(() => {
  let total = 0
  for (const row of form.items) {
    const cents = parseCents(row.amount)
    if (cents !== null) total += cents
  }
  return centsToMoney(total)
})

function emptyItem(): ItemRow {
  return { category: categoryOptions.value[0] ?? '', amount: '', expenseDate: '', remark: '' }
}

function resetForm() {
  Object.assign(form, { id: undefined, title: '', reason: '', items: [emptyItem()] })
}

function addItem() {
  form.items.push(emptyItem())
}
function removeItem(index: number) {
  if (form.items.length <= 1) {
    ElMessage.warning('至少保留一条明细')
    return
  }
  form.items.splice(index, 1)
}
function moveItem(index: number, delta: number) {
  const target = index + delta
  if (target < 0 || target >= form.items.length) return
  const [row] = form.items.splice(index, 1)
  form.items.splice(target, 0, row)
}

async function openCreate() {
  await loadCategories()
  resetForm()
  formTitle.value = '新建报销'
  formVisible.value = true
}

async function openEdit(id: number) {
  await loadCategories()
  const detail = await getExpenseClaim(id)
  Object.assign(form, {
    id: detail.id,
    title: detail.title,
    reason: detail.reason,
    items: (detail.items ?? []).map((item: any) => ({
      category: item.category,
      amount: String(item.amount ?? ''),
      expenseDate: item.expenseDate,
      remark: item.remark ?? '',
    })),
  })
  if (form.items.length === 0) form.items = [emptyItem()]
  formTitle.value = '编辑报销单'
  detailVisible.value = false
  formVisible.value = true
}

// validateItems 在提交前校验明细（后端同样校验，这里只为可读提示）。
function validateItems(): string | null {
  if (form.items.length === 0) return '请至少添加一条明细'
  for (let i = 0; i < form.items.length; i++) {
    const row = form.items[i]
    if (!row.category) return `第 ${i + 1} 条明细请选择类别`
    if (parseCents(row.amount) === null) return `第 ${i + 1} 条明细的金额必须大于 0（最多两位小数）`
    if (!row.expenseDate) return `第 ${i + 1} 条明细请选择发生日期`
    if (String(row.remark ?? '').length > 200) return `第 ${i + 1} 条明细的备注不能超过 200 字`
  }
  return null
}

async function saveDraft() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  const itemError = validateItems()
  if (itemError) {
    ElMessage.warning(itemError)
    return
  }
  saving.value = true
  try {
    const payload = {
      id: form.id,
      title: form.title.trim(),
      reason: form.reason.trim(),
      items: form.items.map((row) => ({
        category: row.category,
        amount: String(row.amount).trim(),
        expenseDate: row.expenseDate,
        remark: String(row.remark ?? '').trim(),
      })),
    }
    const saved = form.id ? await updateExpenseClaim(payload) : await createExpenseClaim(payload)
    formVisible.value = false
    ElMessage.success('已保存草稿')
    tableRef.value?.reload()
    if (saved?.id) {
      await openDetail(saved.id)
    }
  } finally {
    saving.value = false
  }
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<any>(null)
const detailTab = ref('detail')

// pendingTasks 是实例当前未办理的任务（bpm 候选人池物化为每人一行）。
const pendingTasks = computed(() =>
  (detail.value?.approval?.tasks ?? []).filter((task: any) => task?.status === 0),
)

async function openDetail(id: number) {
  detailVisible.value = true
  detailTab.value = 'detail'
  detailLoading.value = true
  try {
    detail.value = await getExpenseClaim(id)
  } finally {
    detailLoading.value = false
  }
}

async function reloadDetail() {
  if (detail.value?.id) {
    detail.value = await getExpenseClaim(detail.value.id)
  }
}

// ---------- 动作 ----------
const submitting = ref(false)

// doSubmit 提交审批：服务端校验仅草稿/已驳回可提交，并在同一事务内生成
// oa_expense_approval 实例（部门负责人 → oa:expense:manage）。
async function doSubmit(row: any) {
  await ElMessageBox.confirm(
    '提交后将进入审批流程（部门负责人 → 财务），审批期间不能修改明细，确认提交吗？',
    '提示',
    { type: 'warning' },
  )
  submitting.value = true
  try {
    const saved = await submitExpenseClaim(row.id)
    ElMessage.success('已提交审批')
    tableRef.value?.reload()
    if (detail.value?.id === row.id && saved?.id) {
      detail.value = saved
      detailTab.value = 'approval'
    }
  } finally {
    submitting.value = false
  }
}

// goTodoCenter 跳到通用待办页（审批动作统一在待办完成）。
function goTodoCenter() {
  router.push('/bpm/task')
}

async function doDelete(row: any) {
  await ElMessageBox.confirm('删除后明细将一并移除，确认删除该报销单吗？', '提示', {
    type: 'warning',
  })
  await deleteExpenseClaim(row.id)
  ElMessage.success('删除成功')
  if (detail.value?.id === row.id) {
    detailVisible.value = false
    detail.value = null
  }
  tableRef.value?.reload()
}

// ---------- 附件 ----------
const fileAccept =
  '.jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.json,.zip,.rar,.7z,.mp4,.mp3'

async function handleUpload(options: any) {
  if (!detail.value) return
  await uploadExpenseClaimFile(detail.value.id, options.file)
  ElMessage.success('附件上传成功')
  await reloadDetail()
}

async function removeFile(row: any) {
  if (!detail.value) return
  await ElMessageBox.confirm('确认删除该附件吗？', '提示', { type: 'warning' })
  await deleteExpenseClaimFile(detail.value.id, row.id)
  ElMessage.success('附件已删除')
  await reloadDetail()
}

async function downloadFile(row: any) {
  const response = await downloadExpenseClaimFile(row.url)
  saveBlobAs(response, row.name)
}

onMounted(async () => {
  const id = Number(route.query.id)
  if (Number.isInteger(id) && id > 0) {
    await openDetail(id)
  }
})
</script>

<style scoped>
.amount {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 20px 0 10px;
  font-weight: 600;
}
.form-total {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 10px;
}
.drawer-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.timeline {
  padding-left: 4px;
}
.timeline-operator {
  margin-left: 8px;
}
.timeline-node {
  color: #909399;
  font-size: 12px;
  margin-top: 2px;
}
.timeline-comment {
  margin-top: 4px;
  white-space: pre-wrap;
}
.hint {
  color: #909399;
  font-size: 12px;
  margin-top: 8px;
}
.detail-tabs {
  margin-top: 12px;
}
.todo-link {
  margin-top: 12px;
}
.file-link {
  padding: 0;
}
</style>
