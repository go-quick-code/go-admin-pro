<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:stock-check"
      title="库存盘点"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="库存盘点单"
          export-perm="erp:stock-check:export"
          :export-action="exportStockChecks"
          :export-params="exportParams"
        />
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 20 ? 'success' : 'info'" size="small">{{ row.statusName }}</el-tag>
      </template>
      <template #actions="{ row }">
        <el-button link type="info" @click.stop="openDetail(row.id)">详情</el-button>
        <el-button v-if="row.status === 10 && canUpdate" link type="primary" @click.stop="openEdit(row.id)">
          编辑
        </el-button>
        <!-- 审核/反审核：独立权限码 erp:stock-check:approve（spec §4.1）；反审核带确认。 -->
        <el-button v-if="row.status === 10 && canApprove" link type="success" @click.stop="doApprove(row)">
          审核
        </el-button>
        <el-button v-if="row.status === 20 && canApprove" link type="warning" @click.stop="doUnapprove(row)">
          反审核
        </el-button>
        <el-button v-if="row.status === 10 && canDelete" link type="danger" @click.stop="doDelete(row)">
          删除
        </el-button>
      </template>
    </ProTable>

    <!-- 新增/编辑：明细只填实际清点数（actualCount ≥ 0）；baseCount 为服务端
         创建时账面快照（只读展示，审核不使用，spec §4.6）。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="880px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="92px">
        <el-form-item label="仓库" prop="warehouseId">
          <el-select v-model="form.warehouseId" style="width: 100%" placeholder="请选择盘点仓库">
            <el-option v-for="w in warehouseList" :key="w.id" :label="w.name" :value="w.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="盘点时间" prop="checkTime">
          <el-date-picker
            v-model="form.checkTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="选择盘点时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="form.remark" type="textarea" :rows="2" maxlength="500" placeholder="可选" />
        </el-form-item>
      </el-form>
      <div class="section-head">
        <span>明细</span>
        <el-button size="small" type="primary" plain @click="addItem">添加明细</el-button>
      </div>
      <el-table :data="form.items" border size="small" empty-text="请至少添加一条明细">
        <el-table-column label="#" width="48">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column label="产品" min-width="200">
          <template #default="{ row }">
            <el-select v-model="row.productId" filterable placeholder="请选择产品" style="width: 100%">
              <el-option v-for="p in productList" :key="p.id" :label="p.name" :value="p.id" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="实际清点数" width="170">
          <template #default="{ row }">
            <el-input v-model="row.actualCount" placeholder="如 10.000" inputmode="decimal" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="70">
          <template #default="{ $index }">
            <el-button link type="danger" @click="removeItem($index)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>

    <!-- 详情 drawer：头 descriptions + 明细（含创建时账面快照 baseCount）。 -->
    <el-drawer v-model="detailVisible" title="库存盘点单详情" size="640px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="仓库">{{ detail.warehouseName }}</el-descriptions-item>
        <el-descriptions-item label="盘点时间">{{ formatTime(detail.checkTime) }}</el-descriptions-item>
        <el-descriptions-item label="备注" :span="2">{{ detail.remark || '—' }}</el-descriptions-item>
      </el-descriptions>
      <el-table v-if="detail" :data="detail.items" border size="small" class="detail-items">
        <el-table-column label="#" width="48">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="productName" label="产品" min-width="160" />
        <el-table-column prop="baseCount" label="创建时账面" width="120" />
        <el-table-column prop="actualCount" label="实际清点" width="120" />
      </el-table>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime } from '@/utils/format'
import { useUserStore } from '@/stores/user'
import { useDictOptions } from '@/composables/useDict'
import {
  pageStockChecks,
  getStockCheck,
  createStockCheck,
  updateStockCheck,
  deleteStockCheck,
  updateStockCheckStatus,
  exportStockChecks,
  pageProducts,
  pageWarehouses,
} from '@/api/erp'

// 库存盘点页（issue #93，spec §3.5/§4.1/§4.6）：未审核可改可删；审核按
//「差异 = 实际清点数 − 审核时账面数」调整库存（差正盘盈、差负盘亏），审核后
// 账面 = 实际数；反审核按已入账流水净额反向（均带独立权限码 erp:stock-check:approve）。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:stock-check:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:stock-check:update'))
const canDelete = computed(() => userStore.hasPerm('erp:stock-check:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:stock-check:approve'))

const warehouseList = ref<any[]>([])
const productList = ref<any[]>([])
const statusOptions = useDictOptions('erp_audit_status')

const searchItems = computed(() => [
  { prop: 'no', label: '单号' },
  {
    prop: 'warehouseId',
    label: '仓库',
    type: 'select',
    options: warehouseList.value.map((w) => ({ label: w.name, value: w.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'no', label: '单号', minWidth: 160 },
  { prop: 'warehouseName', label: '仓库', minWidth: 120 },
  { prop: 'checkTime', label: '盘点时间', minWidth: 160, formatter: (row: any) => formatTime(row.checkTime) },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

const pageApi = { page: pageStockChecks }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, warehouseId: q.warehouseId, status: q.status }
}

// ---------- 新增/编辑 ----------
interface CheckItemRow {
  productId?: number
  actualCount: string
}

const formVisible = ref(false)
const formTitle = ref('新增库存盘点单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{
  warehouseId?: number
  checkTime: string
  remark: string
  items: CheckItemRow[]
}>({
  warehouseId: undefined,
  checkTime: '',
  remark: '',
  items: [],
})

const rules = {
  warehouseId: [{ required: true, message: '请选择仓库', trigger: 'change' }],
  checkTime: [{ required: true, message: '请选择盘点时间', trigger: 'change' }],
}

function addItem() {
  form.value.items.push({ productId: undefined, actualCount: '' })
}

function removeItem(index: number) {
  form.value.items.splice(index, 1)
}

function openCreate() {
  editingID.value = undefined
  form.value = { warehouseId: undefined, checkTime: '', remark: '', items: [] }
  formTitle.value = '新增库存盘点单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getStockCheck(id)) as any
  editingID.value = detail.id
  form.value = {
    warehouseId: detail.warehouseId,
    checkTime: toLocalInput(detail.checkTime),
    remark: detail.remark || '',
    items: (detail.items || []).map((it: any) => ({
      productId: it.productId,
      actualCount: it.actualCount,
    })),
  }
  formTitle.value = '编辑库存盘点单'
  formVisible.value = true
}

function toLocalInput(value: string) {
  if (!value) return ''
  return value.length > 19 ? value.slice(0, 19).replace('T', ' ') : value.replace('T', ' ')
}

async function submit() {
  await formRef.value?.validate()
  if (!form.value.items.length) {
    ElMessage.warning('请至少添加一条明细')
    return
  }
  const payload: any = {
    warehouseId: form.value.warehouseId,
    checkTime: form.value.checkTime,
    remark: form.value.remark,
    items: form.value.items.map((it) => ({
      productId: it.productId,
      actualCount: it.actualCount,
    })),
  }
  submitting.value = true
  try {
    if (editingID.value) {
      await updateStockCheck({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createStockCheck(payload)
      ElMessage.success('已创建')
    }
    formVisible.value = false
    tableRef.value?.reload?.()
  } finally {
    submitting.value = false
  }
}

// ---------- 审核 / 反审核 / 删除 ----------
async function doApprove(row: any) {
  await ElMessageBox.confirm(
    `确认审核盘点单 ${row.no}？将按「实际清点数 − 审核时账面数」生成盘盈/盘亏调整，审核后账面 = 实际数。`,
    '审核确认',
    { type: 'warning' },
  )
  await updateStockCheckStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核落账')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核盘点单 ${row.no}？将按已入账调整净额生成反向流水（原流水保留）。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updateStockCheckStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除盘点单 ${row.no}？`, '删除确认', { type: 'warning' })
  await deleteStockCheck(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getStockCheck(id)) as any
  detailVisible.value = true
}

onMounted(async () => {
  warehouseList.value = ((await pageWarehouses({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  productList.value = ((await pageProducts({ pageNo: 1, pageSize: 200 })) as any)?.list || []
})
</script>

<style scoped>
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 4px 0 8px;
  font-weight: 600;
}
.detail-items {
  margin-top: 12px;
}
</style>
