<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :show-actions="false"
      title="我的请假"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">发起请假</el-button>
        <el-button @click="router.push('/oa/leave/mine')">我的额度</el-button>
      </template>
      <template #cell-typeName="{ row }">
        <span>{{ row.typeName || row.typeCode || '-' }}</span>
      </template>
      <template #cell-start="{ row }">
        <span>{{ row.startDate }} {{ halfText(row.startHalf) }}</span>
      </template>
      <template #cell-end="{ row }">
        <span>{{ row.endDate }} {{ halfText(row.endHalf) }}</span>
      </template>
      <template #cell-durationDays="{ row }">{{ row.durationDays }} 天</template>
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
          @click.stop="openEdit(row)"
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

    <!-- 创建/编辑草稿：时长由服务端按工作日历计算（非工作日不计）。 -->
    <el-dialog v-model="formVisible" :title="formTitle" width="560px" append-to-body destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="96px">
        <el-form-item label="请假类型" prop="leaveTypeId">
          <el-select v-model="form.leaveTypeId" placeholder="请选择请假类型" style="width: 100%">
            <el-option
              v-for="opt in typeOptions"
              :key="opt.id"
              :label="opt.balanceManaged ? `${opt.name}（占用额度）` : opt.name"
              :value="opt.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="开始" prop="startDate">
          <div class="span-row">
            <el-date-picker
              v-model="form.startDate"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="开始日期"
              style="flex: 1"
            />
            <el-select v-model="form.startHalf" style="width: 100px">
              <el-option label="上午" :value="0" />
              <el-option label="下午" :value="1" />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item label="结束" prop="endDate">
          <div class="span-row">
            <el-date-picker
              v-model="form.endDate"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="结束日期"
              style="flex: 1"
            />
            <el-select v-model="form.endHalf" style="width: 100px">
              <el-option label="上午" :value="0" />
              <el-option label="下午" :value="1" />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item label="请假事由" prop="reason">
          <el-input
            v-model="form.reason"
            type="textarea"
            :rows="3"
            maxlength="500"
            show-word-limit
            placeholder="请填写请假事由"
          />
        </el-form-item>
      </el-form>
      <div class="hint">保存为草稿后可上传附件并提交审批；时长按工作日历计算，非工作日不计。</div>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveDraft">保存</el-button>
      </template>
    </el-dialog>

    <!-- 详情：申请信息 + 审批时间线 + 附件。审批动作走通用待办页。 -->
    <el-drawer v-model="detailVisible" title="请假详情" size="620px">
      <div v-loading="detailLoading">
        <template v-if="detail">
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="类型">{{ detail.typeName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="statusTag(detail.status)" size="small">
                {{ statusText(detail.status) }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="开始">
              {{ detail.startDate }} {{ halfText(detail.startHalf) }}
            </el-descriptions-item>
            <el-descriptions-item label="结束">
              {{ detail.endDate }} {{ halfText(detail.endHalf) }}
            </el-descriptions-item>
            <el-descriptions-item label="时长">{{ detail.durationDays }} 天</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
            <el-descriptions-item label="事由" :span="2">{{ detail.reason }}</el-descriptions-item>
            <el-descriptions-item v-if="detail.rejectReason" label="驳回原因" :span="2">
              {{ detail.rejectReason }}
            </el-descriptions-item>
          </el-descriptions>

          <div class="section-head">
            <span>审批时间线</span>
            <el-button
              v-if="isOwner(detail) && isEditable(detail.status) && canSubmit"
              type="primary"
              size="small"
              :loading="submitting"
              @click="doSubmit(detail)"
            >
              提交审批
            </el-button>
          </div>
          <el-timeline v-if="detail.approval?.records?.length" class="timeline">
            <el-timeline-item
              v-for="rec in detail.approval.records"
              :key="rec.id"
              :timestamp="formatTime(rec.createTime)"
              :type="recordTag(rec.action) === 'success' ? 'success' : recordTag(rec.action) === 'danger' ? 'danger' : 'primary'"
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
            :description="detail.approvalInstanceId ? '暂无审批记录' : '尚未提交审批（草稿）'"
          />

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
        </template>
      </div>
      <!-- 发起人动作：待审可撤回（首个审批前），已通过且开始前可取消。 -->
      <template #footer>
        <div class="drawer-footer">
          <el-button @click="detailVisible = false">关闭</el-button>
          <el-button
            v-if="canWithdrawDetail"
            type="warning"
            :loading="withdrawing"
            @click="doWithdraw"
          >
            撤回
          </el-button>
          <el-button v-if="canCancelDetail" type="danger" plain :loading="cancelling" @click="doCancel">
            取消请假
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
import {
  cancelLeaveRequest,
  createLeaveRequest,
  deleteLeaveRequest,
  deleteLeaveRequestFile,
  downloadLeaveRequestFile,
  getMyLeaveRequest,
  listLeaveTypes,
  pageMyLeaveRequests,
  submitLeaveRequest,
  updateLeaveRequest,
  uploadLeaveRequestFile,
  withdrawLeaveRequest,
} from '@/api/oa'

// 我的请假（issue #52/#53，菜单 529）：列表/详情/时间线 + 草稿表单（含附件）。
// 列表按可见范围返回（manage → 全租户、部门负责人 → 部门子树、否则本人），
// 但编辑/提交/删除/撤回/取消只对本人显示；审批在通用待办（/bpm/task）完成。
// 权限码 oa:leave:create/update/delete/submit/withdraw/cancel。
const userStore = useUserStore()
const route = useRoute()
const router = useRouter()
const tableRef = ref()
const canCreate = computed(() => userStore.hasPerm('oa:leave:create'))
const canUpdate = computed(() => userStore.hasPerm('oa:leave:update'))
const canWithdraw = computed(() => userStore.hasPerm('oa:leave:withdraw'))
const canCancel = computed(() => userStore.hasPerm('oa:leave:cancel'))
const currentUserId = computed(() => Number(userStore.userInfo?.id || 0))
const canDelete = computed(() => userStore.hasPerm('oa:leave:delete'))
const canSubmit = computed(() => userStore.hasPerm('oa:leave:submit'))

const pageApi = { page: pageMyLeaveRequests }
const columns = [
  { prop: 'typeName', label: '类型', minWidth: 120, slot: true },
  { prop: 'start', label: '开始', minWidth: 150, slot: true },
  { prop: 'end', label: '结束', minWidth: 150, slot: true },
  { prop: 'durationDays', label: '时长', width: 100, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'reason', label: '事由', minWidth: 200 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, slot: true },
  { prop: 'actions', label: '操作', width: 190, slot: true },
]
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
      { label: '已取消', value: 5 },
    ],
  },
]

const statusTexts = ['草稿', '待审', '通过', '驳回', '撤回', '已取消']
function statusText(status: number): string {
  return statusTexts[status] ?? `未知(${status})`
}
function statusTag(status: number): string {
  if (status === 2) return 'success'
  if (status === 3) return 'danger'
  if (status === 1) return 'warning'
  return 'info'
}
function halfText(half: number): string {
  return half === 1 ? '下午' : '上午'
}
function isEditable(status: number): boolean {
  return status === 0 || status === 3
}
// 写动作（编辑/提交/删除/撤回/取消）只对发起人显示：列表可能包含
// 部门负责人/HR 可见的他人申请。
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

// ---------- 表单 ----------
const formVisible = ref(false)
const formTitle = ref('发起请假')
const saving = ref(false)
const formRef = ref<FormInstance>()
const form = reactive<any>({ id: undefined, leaveTypeId: undefined, startDate: '', startHalf: 0, endDate: '', endHalf: 1, reason: '' })
const rules = {
  leaveTypeId: [{ required: true, message: '请选择请假类型', trigger: 'change' }],
  startDate: [{ required: true, message: '请选择开始日期', trigger: 'change' }],
  endDate: [{ required: true, message: '请选择结束日期', trigger: 'change' }],
  reason: [{ required: true, message: '请填写请假事由', trigger: 'blur' }],
}
const typeOptions = ref<any[]>([])

async function loadTypes() {
  const data = await listLeaveTypes({ status: 0 })
  typeOptions.value = Array.isArray(data) ? data : []
}

function resetForm() {
  Object.assign(form, {
    id: undefined,
    leaveTypeId: typeOptions.value[0]?.id,
    startDate: '',
    startHalf: 0,
    endDate: '',
    endHalf: 1,
    reason: '',
  })
}

async function openCreate() {
  await loadTypes()
  resetForm()
  formTitle.value = '发起请假'
  formVisible.value = true
}

async function openEdit(row: any) {
  await loadTypes()
  Object.assign(form, {
    id: row.id,
    leaveTypeId: row.leaveTypeId,
    startDate: row.startDate,
    startHalf: row.startHalf,
    endDate: row.endDate,
    endHalf: row.endHalf,
    reason: row.reason,
  })
  formTitle.value = '编辑请假申请'
  formVisible.value = true
}

async function saveDraft() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    const payload = {
      id: form.id,
      leaveTypeId: form.leaveTypeId,
      startDate: form.startDate,
      startHalf: form.startHalf,
      endDate: form.endDate,
      endHalf: form.endHalf,
      reason: form.reason,
    }
    const saved = form.id ? await updateLeaveRequest(payload) : await createLeaveRequest(payload)
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

// 发起人动作按状态显示：待审 → 撤回；已通过 → 取消（开始日期后由后端
// 返回可读 400）。列表可含他人申请，因此同时要求当前用户是发起人。
const canWithdrawDetail = computed(
  () => !!detail.value && isOwner(detail.value) && detail.value.status === 1 && canWithdraw.value,
)
const canCancelDetail = computed(
  () => !!detail.value && isOwner(detail.value) && detail.value.status === 2 && canCancel.value,
)

async function openDetail(id: number) {
  detailVisible.value = true
  detailLoading.value = true
  try {
    detail.value = await getMyLeaveRequest(id)
  } finally {
    detailLoading.value = false
  }
}

async function reloadDetail() {
  if (detail.value?.id) {
    detail.value = await getMyLeaveRequest(detail.value.id)
  }
}

// ---------- 动作 ----------
const submitting = ref(false)

async function doSubmit(row: any) {
  await ElMessageBox.confirm('提交后将进入审批流程，确认提交该请假申请吗？', '提示', {
    type: 'warning',
  })
  submitting.value = true
  try {
    await submitLeaveRequest(row.id)
    ElMessage.success('已提交审批')
    tableRef.value?.reload()
    if (detailVisible.value) {
      await reloadDetail()
    }
  } finally {
    submitting.value = false
  }
}

async function doDelete(row: any) {
  await ElMessageBox.confirm('确认删除该请假申请吗？', '提示', { type: 'warning' })
  await deleteLeaveRequest(row.id)
  ElMessage.success('删除成功')
  if (detail.value?.id === row.id) {
    detailVisible.value = false
    detail.value = null
  }
  tableRef.value?.reload()
}

const withdrawing = ref(false)

async function doWithdraw() {
  if (!detail.value) return
  await ElMessageBox.confirm('撤回后本次审批将终止，已扣额度不受影响。确认撤回吗？', '提示', {
    type: 'warning',
  })
  withdrawing.value = true
  try {
    await withdrawLeaveRequest(detail.value.id)
    ElMessage.success('已撤回')
    tableRef.value?.reload()
    await reloadDetail()
  } finally {
    withdrawing.value = false
  }
}

const cancelling = ref(false)

async function doCancel() {
  if (!detail.value) return
  await ElMessageBox.confirm('取消后已扣额度将返还，确认取消该请假吗？', '提示', {
    type: 'warning',
  })
  cancelling.value = true
  try {
    await cancelLeaveRequest(detail.value.id)
    ElMessage.success('已取消请假')
    tableRef.value?.reload()
    await reloadDetail()
  } finally {
    cancelling.value = false
  }
}

// ---------- 附件 ----------
const fileAccept =
  '.jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.json,.zip,.rar,.7z,.mp4,.mp3'

async function handleUpload(options: any) {
  if (!detail.value) return
  await uploadLeaveRequestFile(detail.value.id, options.file)
  ElMessage.success('附件上传成功')
  await reloadDetail()
}

async function removeFile(row: any) {
  if (!detail.value) return
  await ElMessageBox.confirm('确认删除该附件吗？', '提示', { type: 'warning' })
  await deleteLeaveRequestFile(detail.value.id, row.id)
  ElMessage.success('附件已删除')
  await reloadDetail()
}

async function downloadFile(row: any) {
  const response = await downloadLeaveRequestFile(row.url)
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
.span-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 20px 0 10px;
  font-weight: 600;
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
.file-link {
  padding: 0;
}
</style>
