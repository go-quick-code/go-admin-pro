<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      :before-submit="normalizeSubmit"
      perm-prefix="crm:receivable"
      title="回款计划"
      :action-width="190"
    >
      <template #toolbar>
        <!-- Excel 导出（issue #20）：挂 crm:receivable:export，复用当前列表筛选；
             导出含已收金额与派生状态/逾期，数据权限 join 合同。 -->
        <ExcelActions
          title="回款计划"
          export-perm="crm:receivable:export"
          :export-action="exportReceivablePlans"
          :export-params="receivableExportParams"
        />
      </template>
      <template #cell-amount="{ row }">{{ row.amount ?? '0.00' }}</template>
      <template #cell-receivedAmount="{ row }">{{ row.receivedAmount ?? '0.00' }}</template>
      <template #cell-contractAmount="{ row }">{{ row.contractAmount ?? '0.00' }}</template>
      <template #cell-dueDate="{ row }">{{ formatDate(row.dueDate) }}</template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status)" size="small">{{ row.statusName || statusLabel(row.status) }}</el-tag>
        <el-tag v-if="row.overdue" type="danger" size="small" effect="plain" class="overdue-tag">逾期</el-tag>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click.stop="openRecords(row)">记录</el-button>
      </template>

      <!-- 新建：合同必选（可见合同范围由后端数据权限决定）；编辑：合同不可改。 -->
      <template #form-contractId="{ form }">
        <el-select v-model="form.contractId" filterable style="width: 100%" placeholder="请选择所属合同">
          <el-option
            v-for="c in contracts"
            :key="c.id"
            :label="`${c.contractNo} ${c.name}`"
            :value="c.id"
          />
        </el-select>
      </template>
      <template #form-contractName="{ form }">
        <span>{{ form.contractNo ? `${form.contractNo} ${form.contractName || ''}` : '-' }}</span>
      </template>
      <template #form-amount="{ form }">
        <el-input v-model="form.amount" clearable placeholder="0.00（元）" />
      </template>
      <template #form-dueDate="{ form }">
        <el-date-picker
          v-model="form.dueDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择到期日"
        />
      </template>
    </ProTable>

    <!-- 回款记录抽屉：计划信息 + 记录列表 + 录入金额/日期/方式 + 删除。
         可见性跟随合同（范围外计划在列表里已不可见）。 -->
    <el-drawer v-model="recordsVisible" :title="drawerTitle" size="720px" destroy-on-close>
      <div v-if="planRow" class="plan-summary">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="合同">
            {{ planRow.contractNo }} {{ planRow.contractName }}
          </el-descriptions-item>
          <el-descriptions-item label="期次">第 {{ planRow.periodNo }} 期</el-descriptions-item>
          <el-descriptions-item label="计划金额">{{ planRow.amount ?? '0.00' }} 元</el-descriptions-item>
          <el-descriptions-item label="已收金额">{{ planRow.receivedAmount ?? '0.00' }} 元</el-descriptions-item>
          <el-descriptions-item label="到期日">{{ formatDate(planRow.dueDate) }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="statusTagType(planRow.status)" size="small">
              {{ planRow.statusName || statusLabel(planRow.status) }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>
      </div>

      <el-form v-if="canCreate" :inline="true" class="record-form" @submit.prevent>
        <el-form-item label="金额">
          <el-input v-model="recordForm.amount" placeholder="0.00" style="width: 120px" />
        </el-form-item>
        <el-form-item label="到账日期">
          <el-date-picker
            v-model="recordForm.receiptDate"
            type="date"
            value-format="YYYY-MM-DD"
            style="width: 150px"
            placeholder="请选择"
          />
        </el-form-item>
        <el-form-item label="方式">
          <el-select v-model="recordForm.method" style="width: 120px" placeholder="请选择">
            <el-option v-for="m in methodOptions" :key="m" :label="m" :value="m" />
          </el-select>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="recordForm.remark" placeholder="可留空" style="width: 140px" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="recordSubmitting" @click="submitRecord">登记回款</el-button>
        </el-form-item>
      </el-form>

      <el-table v-loading="recordsLoading" :data="records" border stripe size="small">
        <el-table-column prop="recordNo" label="记录编号" width="150" />
        <el-table-column prop="amount" label="回款金额（元）" width="130" />
        <el-table-column label="到账日期" width="120">
          <template #default="{ row }">{{ formatDate(row.receiptDate) }}</template>
        </el-table-column>
        <el-table-column prop="method" label="方式" width="110" />
        <el-table-column prop="remark" label="备注" min-width="120" />
        <el-table-column label="操作" width="80" fixed="right">
          <template #default="{ row }">
            <el-button v-if="canDelete" link type="danger" @click="removeRecord(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!recordsLoading && !records.length" description="暂无回款记录" :image-size="60" />
      <div class="record-total">
        已登记合计：{{ receivedTotal }} 元（计划 {{ planRow?.amount ?? '0.00' }} 元）
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime, dictLabel } from '@/utils/format'
import {
  pageReceivablePlans,
  createReceivablePlan,
  updateReceivablePlan,
  deleteReceivablePlan,
  pageReceivableRecords,
  createReceivableRecord,
  deleteReceivableRecord,
  exportReceivablePlans,
  pageContracts,
} from '@/api/crm'
import { useUserStore } from '@/stores/user'

// 派生状态对齐后端 service.DeriveReceivableStatus（spec §3：状态不落库）：
// 已回款优先，其次逾期，再次部分回款，否则待回款。
const statusOptions = [
  { label: '待回款', value: 1 },
  { label: '部分回款', value: 2 },
  { label: '已回款', value: 3 },
  { label: '逾期', value: 4 },
]

const methodOptions = ['银行转账', '现金', '支票', '其他']

function statusLabel(status?: number) {
  return dictLabel(statusOptions, status)
}

function statusTagType(status?: number) {
  if (status === 2) return 'warning'
  if (status === 3) return 'success'
  if (status === 4) return 'danger'
  return 'info'
}

function formatDate(value?: string) {
  return value ? String(value).slice(0, 10) : '-'
}

// 本地日历日（与后端 DateKey/逾期口径一致，避免 toISOString 的 UTC 偏移）。
function todayString() {
  const now = new Date()
  const month = String(now.getMonth() + 1).padStart(2, '0')
  const day = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${month}-${day}`
}

const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('crm:receivable:create'))
const canDelete = computed(() => userStore.hasPerm('crm:receivable:delete'))

const tableRef = ref()
const contracts = ref<any[]>([])

// 回款计划导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function receivableExportParams() {
  const q = tableRef.value?.query || {}
  return {
    contractId: q.contractId,
    status: q.status,
    overdue: q.overdue,
  }
}

const searchItems = computed(() => [
  {
    prop: 'contractId',
    label: '合同',
    type: 'select',
    options: contracts.value.map((c) => ({ label: `${c.contractNo} ${c.name}`, value: c.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
  {
    prop: 'overdue',
    label: '逾期',
    type: 'select',
    options: [
      { label: '是', value: true },
      { label: '否', value: false },
    ],
  },
])

const columns = [
  { prop: 'contractNo', label: '合同编号', width: 150 },
  { prop: 'contractName', label: '合同名称', minWidth: 160 },
  { prop: 'periodNo', label: '期次', width: 80 },
  { prop: 'contractAmount', label: '合同金额（元）', width: 140, slot: true },
  { prop: 'amount', label: '计划金额（元）', width: 140, slot: true },
  { prop: 'receivedAmount', label: '已收金额（元）', width: 140, slot: true },
  { prop: 'dueDate', label: '到期日', width: 120, slot: true },
  { prop: 'status', label: '状态', width: 140, slot: true },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'contractId', label: '所属合同', slot: true, requiredOn: 'create', visibleOn: 'create' },
  { prop: 'contractName', label: '所属合同', slot: true, visibleOn: 'update' },
  { prop: 'periodNo', label: '期次', type: 'number', required: true },
  { prop: 'amount', label: '计划金额', slot: true },
  { prop: 'dueDate', label: '到期日', slot: true, required: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = { periodNo: 1, amount: '0.00' }

const api = {
  page: pageReceivablePlans,
  create: createReceivablePlan,
  update: updateReceivablePlan,
  remove: deleteReceivablePlan,
}

// 提交前规整：金额空串省略（后端 money 只收十进制字符串）；更新只提交可改字段
//（合同归属不可改，行里带出的派生/展示字段一并剔除）。
function normalizeSubmit(payload: any, mode: 'create' | 'update') {
  const out: any = {
    periodNo: Number(payload.periodNo || 1),
    dueDate: payload.dueDate,
    remark: payload.remark ?? '',
  }
  if (payload.amount !== '' && payload.amount != null) out.amount = payload.amount
  if (mode === 'create') out.contractId = payload.contractId
  else out.id = payload.id
  return out
}

// ---------- 记录抽屉 ----------
const recordsVisible = ref(false)
const planRow = ref<any>(null)
const records = ref<any[]>([])
const recordsLoading = ref(false)
const recordSubmitting = ref(false)
const recordForm = reactive<any>({ amount: '', receiptDate: '', method: '银行转账', remark: '' })

const drawerTitle = computed(() =>
  planRow.value ? `第 ${planRow.value.periodNo} 期回款记录` : '回款记录',
)

const receivedTotal = computed(() => {
  const cents = records.value.reduce((sum, row) => {
    const value = Number.parseFloat(String(row.amount ?? '0'))
    return Number.isFinite(value) ? sum + Math.round(value * 100) : sum
  }, 0)
  return (cents / 100).toFixed(2)
})

async function openRecords(row: any) {
  planRow.value = row
  recordForm.amount = ''
  recordForm.receiptDate = todayString()
  recordForm.method = '银行转账'
  recordForm.remark = ''
  recordsVisible.value = true
  await loadRecords()
}

async function loadRecords() {
  if (!planRow.value) return
  recordsLoading.value = true
  try {
    const page = await pageReceivableRecords({
      planId: planRow.value.id,
      pageNo: 1,
      pageSize: 200,
    })
    records.value = page.list || []
  } finally {
    recordsLoading.value = false
  }
}

async function submitRecord() {
  if (!planRow.value) return
  if (!recordForm.amount || !recordForm.receiptDate) {
    ElMessage.warning('请填写回款金额与到账日期')
    return
  }
  recordSubmitting.value = true
  try {
    await createReceivableRecord({
      planId: planRow.value.id,
      amount: recordForm.amount,
      receiptDate: recordForm.receiptDate,
      method: recordForm.method,
      remark: recordForm.remark,
    })
    ElMessage.success('回款登记成功')
    recordForm.amount = ''
    recordForm.remark = ''
    await loadRecords()
    await refreshPlanRow()
  } finally {
    recordSubmitting.value = false
  }
}

async function removeRecord(row: any) {
  await ElMessageBox.confirm(`确认删除回款记录 ${row.recordNo} 吗？`, '提示', { type: 'warning' })
  await deleteReceivableRecord(row.id)
  ElMessage.success('删除成功')
  await loadRecords()
  await refreshPlanRow()
}

// 登记/删除后刷新抽屉里的计划行与主列表（已收金额与派生状态由后端重算）。
async function refreshPlanRow() {
  if (!planRow.value) return
  const page = await pageReceivablePlans({
    contractId: planRow.value.contractId,
    pageNo: 1,
    pageSize: 200,
  })
  const fresh = (page.list || []).find((p: any) => p.id === planRow.value.id)
  if (fresh) planRow.value = fresh
  tableRef.value?.reload()
}

onMounted(async () => {
  // 合同下拉：当前用户可见范围内的前 200 条（v1 规模可控）。
  const page = await pageContracts({ pageNo: 1, pageSize: 200 })
  contracts.value = page.list || []
})
</script>

<style scoped>
.overdue-tag {
  margin-left: 4px;
}
.plan-summary {
  margin-bottom: 16px;
}
.record-form {
  margin-bottom: 8px;
}
.record-total {
  margin-top: 10px;
  text-align: right;
  font-weight: 600;
  color: var(--el-color-danger);
}
</style>
