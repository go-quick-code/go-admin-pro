<template>
  <div class="app-container">
    <el-tabs v-model="tab" @tab-change="reloadCurrent">
      <el-tab-pane label="待办" name="todo">
        <ProTable
          ref="todoTableRef"
          :columns="taskColumns"
          :api="todoApi"
          :extra-query="{ tab: 'todo' }"
          :show-toolbar="false"
          :show-actions="false"
          title=""
        >
          <template #cell-actions="{ row }">
            <el-button link type="success" @click.stop="openApprove(row)">通过</el-button>
            <el-button link type="danger" @click.stop="openReject(row)">驳回</el-button>
            <el-button link type="warning" @click.stop="openTransfer(row)">转办</el-button>
            <el-button link type="info" @click.stop="openDetail(row.instanceId)">审批详情</el-button>
          </template>
        </ProTable>
      </el-tab-pane>

      <el-tab-pane label="已办" name="done">
        <ProTable
          ref="doneTableRef"
          :columns="doneColumns"
          :api="doneApi"
          :extra-query="{ tab: 'done' }"
          :show-toolbar="false"
          :show-actions="false"
          title=""
        >
          <template #cell-actions="{ row }">
            <el-button link type="info" @click.stop="openDetail(row.instanceId)">审批详情</el-button>
          </template>
          <template #cell-status="{ row }">
            <el-tag :type="taskStatusTag(row.status)" size="small">{{ taskStatusText(row.status) }}</el-tag>
          </template>
        </ProTable>
      </el-tab-pane>

      <el-tab-pane label="我发起" name="initiated">
        <ProTable
          ref="initiatedTableRef"
          :columns="instanceColumns"
          :api="initiatedApi"
          :extra-query="{ tab: 'initiated' }"
          :show-toolbar="false"
          :show-actions="false"
          title=""
        >
          <template #cell-actions="{ row }">
            <el-button
              v-if="row.status === 0"
              link
              type="danger"
              @click.stop="doWithdraw(row)"
            >
              撤回
            </el-button>
            <el-button link type="info" @click.stop="openDetail(row.id)">审批详情</el-button>
          </template>
          <template #cell-status="{ row }">
            <el-tag :type="instanceStatusTag(row.status)" size="small">
              {{ instanceStatusOptions[row.status] ?? `未知(${row.status})` }}
            </el-tag>
          </template>
        </ProTable>
      </el-tab-pane>
    </el-tabs>

    <!-- 审批时间线（spec：业务摘要、审批时间线、业务详情入口）。 -->
    <el-drawer v-model="detailVisible" title="审批详情" size="560px">
      <div v-loading="detailLoading">
        <template v-if="detail">
          <h4 class="detail-title">{{ detail.instance.title }}</h4>
          <p class="detail-summary">{{ detail.instance.summary || '（无摘要）' }}</p>
          <p class="detail-meta">
            业务类型：{{ detail.instance.businessType }} · 业务 ID：{{ detail.instance.businessId }}
            <el-link
              v-if="businessRouteOf(detail.instance.businessType, detail.instance.businessId)"
              type="primary"
              :href="businessRouteOf(detail.instance.businessType, detail.instance.businessId)"
              target="_blank"
            >
              查看业务
            </el-link>
          </p>
          <el-timeline v-if="detail.records.length" class="timeline">
            <el-timeline-item
              v-for="rec in detail.records"
              :key="rec.id"
              :timestamp="formatTime(rec.createTime)"
              :type="recordTag(rec.action) === 'success' ? 'success' : recordTag(rec.action) === 'danger' ? 'danger' : 'primary'"
            >
              <div>
                <el-tag :type="recordTag(rec.action)" size="small">{{ recordText(rec.action) }}</el-tag>
                <span class="timeline-operator">{{ rec.operatorName || `用户#${rec.operatorId}` }}</span>
                <span v-if="rec.action === 4" class="timeline-operator">
                  → {{ rec.transferToUserName || `用户#${rec.transferToUserId}` }}
                </span>
              </div>
              <div class="timeline-node">{{ rec.nodeName || '（实例级动作）' }}</div>
              <div v-if="rec.comment" class="timeline-comment">{{ rec.comment }}</div>
            </el-timeline-item>
          </el-timeline>
          <el-empty v-else description="暂无审批记录（等待首个审批动作）" />
        </template>
      </div>
    </el-drawer>

    <!-- 通过 -->
    <el-dialog v-model="approveVisible" title="通过" width="480px" append-to-body>
      <el-form label-width="80px">
        <el-form-item label="审批意见">
          <el-input v-model="comment" type="textarea" :rows="3" maxlength="500" placeholder="可空" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="approveVisible = false">取消</el-button>
        <el-button type="primary" :loading="acting" @click="doApprove">确认通过</el-button>
      </template>
    </el-dialog>

    <!-- 驳回（原因必填，spec §4.3） -->
    <el-dialog v-model="rejectVisible" title="驳回" width="480px" append-to-body>
      <el-form label-width="80px">
        <el-form-item label="驳回原因" required>
          <el-input
            v-model="comment"
            type="textarea"
            :rows="3"
            maxlength="500"
            placeholder="1–500 字，必填"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="rejectVisible = false">取消</el-button>
        <el-button type="danger" :loading="acting" @click="doReject">确认驳回</el-button>
      </template>
    </el-dialog>

    <!-- 转办（目标为在职用户；无 simple-list 权限时降级手输用户 ID） -->
    <el-dialog v-model="transferVisible" title="转办" width="480px" append-to-body>
      <el-form label-width="80px">
        <el-form-item label="目标用户">
          <el-select
            v-if="userOptions.length"
            v-model="transferTo"
            filterable
            placeholder="选择转办目标"
            style="width: 100%"
          >
            <el-option
              v-for="u in userOptions"
              :key="u.id"
              :label="`${u.nickname} (#${u.id})`"
              :value="u.id"
            />
          </el-select>
          <el-input-number v-else v-model="transferTo" :min="1" style="width: 100%" />
        </el-form-item>
        <el-form-item label="转办说明">
          <el-input v-model="comment" type="textarea" :rows="2" maxlength="500" placeholder="可空" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="transferVisible = false">取消</el-button>
        <el-button type="warning" :loading="acting" @click="doTransfer">确认转办</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import {
  pageTasks,
  pageInstances,
  getInstanceDetail,
  approveTask,
  rejectTask,
  transferTask,
  withdrawInstance,
  type BpmTaskItem,
  type BpmInstanceDetail,
} from '@/api/bpm'
import { listSimpleUsers } from '@/api/system'
import { useRouter } from 'vue-router'

// 通用待办页（issue #46，spec §6 通用待办）：待办/已办/我发起三 tab，
// 业务摘要、审批时间线（el-timeline）与业务详情入口。
// 动作接口以「当前办理人/发起人」身份收口；bpm:task:handle 仅控制办理按钮显隐。

const tab = ref('todo')
const router = useRouter()
const userStore = useUserStore()
const canHandle = userStore.hasPerm('bpm:task:handle')

const todoTableRef = ref()
const doneTableRef = ref()
const initiatedTableRef = ref()

function reloadCurrent() {
  if (tab.value === 'todo') todoTableRef.value?.reload()
  if (tab.value === 'done') doneTableRef.value?.reload()
  if (tab.value === 'initiated') initiatedTableRef.value?.reload()
}

const todoApi = { page: (params: any) => pageTasks({ ...params, tab: 'todo' }) }
const doneApi = { page: (params: any) => pageTasks({ ...params, tab: 'done' }) }
const initiatedApi = { page: (params: any) => pageInstances({ ...params }) }

const taskColumns = [
  { prop: 'instanceTitle', label: '业务标题', minWidth: 180 },
  { prop: 'nodeName', label: '当前节点', width: 120 },
  {
    prop: 'instanceSummary',
    label: '摘要',
    minWidth: 200,
    formatter: (row: any) => row.instanceSummary || '-',
  },
  {
    prop: 'businessType',
    label: '业务类型',
    width: 150,
    formatter: (row: any) => `${row.businessType} #${row.businessId}`,
  },
  {
    prop: 'createTime',
    label: '到达时间',
    minWidth: 160,
    formatter: (row: any) => formatTime(row.createTime),
  },
  ...(canHandle ? [{ prop: 'actions', label: '操作', width: 250, slot: true }] : []),
]

const doneColumns = [
  { prop: 'instanceTitle', label: '业务标题', minWidth: 180 },
  { prop: 'nodeName', label: '节点', width: 120 },
  { prop: 'status', label: '办理结果', width: 100, slot: true },
  {
    prop: 'handledAt',
    label: '办理时间',
    minWidth: 160,
    formatter: (row: any) => formatTime(row.handledAt) || '-',
  },
  { prop: 'actions', label: '操作', width: 110, slot: true },
]

const instanceStatusOptions = ['运行中', '通过', '驳回', '撤回']
const instanceColumns = [
  { prop: 'title', label: '标题', minWidth: 180 },
  {
    prop: 'businessType',
    label: '业务类型',
    width: 150,
    formatter: (row: any) => `${row.businessType} #${row.businessId}`,
  },
  { prop: 'status', label: '状态', width: 90, slot: true },
  {
    prop: 'startedAt',
    label: '发起时间',
    minWidth: 160,
    formatter: (row: any) => formatTime(row.startedAt),
  },
  {
    prop: 'endedAt',
    label: '结束时间',
    minWidth: 160,
    formatter: (row: any) => formatTime(row.endedAt) || '-',
  },
  { prop: 'actions', label: '操作', width: 150, slot: true },
]

function taskStatusText(status: number): string {
  return ['待办', '通过', '驳回', '转办', '取消'][status] ?? `未知(${status})`
}
function taskStatusTag(status: number): string {
  if (status === 1) return 'success'
  if (status === 2) return 'danger'
  if (status === 3) return 'warning'
  return 'info'
}
function recordText(action: number): string {
  return ['通过', '驳回', '撤回', '转办'][action - 1] ?? `未知(${action})`
}
function recordTag(action: number): string {
  if (action === 1) return 'success'
  if (action === 2) return 'danger'
  return 'warning'
}
function instanceStatusTag(status: number): string {
  if (status === 1) return 'success'
  if (status === 2 || status === 3) return 'danger'
  return 'primary'
}

// 业务详情入口：业务类型 → 前端路由（业务工单落地页面后在此登记）。
// 合同审批 / 报销审批直达业务详情抽屉（?id=<业务ID>，issue #47/#55）。
function businessRouteOf(businessType: string, businessId?: number): string {
  const routes: Record<string, string> = {
    crm_contract_approval: '/crm/contract',
    oa_leave_approval: '/oa/leave/request',
    oa_expense_approval: '/oa/expense',
  }
  const base = routes[businessType]
  if (!base) return ''
  // 请假页在列表内定位（无 ?id= 约定），合同与报销按 query.id 打开抽屉。
  if (businessType !== 'oa_leave_approval' && businessId) {
    return `${base}?id=${businessId}`
  }
  return base
}

// ---------- 审批时间线 ----------
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<BpmInstanceDetail | null>(null)

async function openDetail(instanceId: number) {
  detailVisible.value = true
  detailLoading.value = true
  try {
    detail.value = await getInstanceDetail(instanceId)
  } finally {
    detailLoading.value = false
  }
}

// ---------- 动作 ----------
const acting = ref(false)
const comment = ref('')
const approveVisible = ref(false)
const rejectVisible = ref(false)
const transferVisible = ref(false)
const transferTo = ref<number>()
const currentTask = ref<BpmTaskItem | null>(null)
const userOptions = ref<any[]>([])

function openApprove(row: BpmTaskItem) {
  currentTask.value = row
  comment.value = ''
  approveVisible.value = true
}
function openReject(row: BpmTaskItem) {
  currentTask.value = row
  comment.value = ''
  rejectVisible.value = true
}
function openTransfer(row: BpmTaskItem) {
  currentTask.value = row
  comment.value = ''
  transferTo.value = undefined
  transferVisible.value = true
  // 下拉数据缺失时降级为手输用户 ID（与 BpmNodeEditor 同款降级）。
  listSimpleUsers()
    .then((users) => {
      userOptions.value = users || []
    })
    .catch(() => {
      userOptions.value = []
    })
}

async function doApprove() {
  if (!currentTask.value) return
  acting.value = true
  try {
    await approveTask(currentTask.value.id, comment.value)
    ElMessage.success('已通过')
    approveVisible.value = false
    todoTableRef.value?.reload()
  } finally {
    acting.value = false
  }
}

async function doReject() {
  if (!currentTask.value) return
  if (!comment.value.trim()) {
    ElMessage.warning('驳回原因不能为空')
    return
  }
  acting.value = true
  try {
    await rejectTask(currentTask.value.id, comment.value)
    ElMessage.success('已驳回')
    rejectVisible.value = false
    todoTableRef.value?.reload()
  } finally {
    acting.value = false
  }
}

async function doTransfer() {
  if (!currentTask.value) return
  if (!transferTo.value) {
    ElMessage.warning('请选择或输入转办目标用户')
    return
  }
  acting.value = true
  try {
    await transferTask(currentTask.value.id, transferTo.value, comment.value)
    ElMessage.success('已转办')
    transferVisible.value = false
    todoTableRef.value?.reload()
  } finally {
    acting.value = false
  }
}

async function doWithdraw(row: any) {
  const { value } = await ElMessageBox.prompt(
    `确认撤回「${row.title}」吗？撤回后流程结束（仅限首个审批前）。可填写撤回说明。`,
    '撤回确认',
    { inputPlaceholder: '撤回说明（可空）', type: 'warning' },
  )
  await withdrawInstance(row.id, value || '')
  ElMessage.success('已撤回')
  initiatedTableRef.value?.reload()
}

onMounted(() => {
  // 路由 query 支持（通知跳转）：/bpm/task?tab=initiated
  const q = router.currentRoute.value.query.tab
  if (q === 'done' || q === 'initiated') tab.value = q as string
})
</script>

<style scoped>
.detail-title {
  margin: 0 0 8px;
}
.detail-summary {
  color: #606266;
  margin: 0 0 8px;
}
.detail-meta {
  color: #909399;
  font-size: 13px;
  margin: 0 0 16px;
}
.timeline {
  padding-left: 4px;
}
.timeline-operator {
  margin-left: 8px;
  font-weight: 600;
}
.timeline-node {
  color: #909399;
  font-size: 12px;
  margin-top: 2px;
}
.timeline-comment {
  margin-top: 4px;
  padding: 6px 8px;
  background: #f5f7fa;
  border-radius: 4px;
  font-size: 13px;
}
</style>
