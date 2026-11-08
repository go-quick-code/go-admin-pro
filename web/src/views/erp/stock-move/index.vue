<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:stock-move"
      title="库存调拨"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="库存调拨单"
          export-perm="erp:stock-move:export"
          :export-action="exportStockMoves"
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
        <!-- 审核/反审核：独立权限码 erp:stock-move:approve（spec §4.1）；反审核带确认。 -->
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

    <!-- 新增/编辑：调拨只有产品 + 数量（无单价/行金额，spec §3.5）；单号服务端发放。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="880px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="92px">
        <el-form-item label="调出仓库" prop="fromWarehouseId">
          <el-select v-model="form.fromWarehouseId" style="width: 100%" placeholder="请选择调出仓库">
            <el-option v-for="w in warehouseList" :key="w.id" :label="w.name" :value="w.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="调入仓库" prop="toWarehouseId">
          <el-select v-model="form.toWarehouseId" style="width: 100%" placeholder="请选择调入仓库">
            <el-option v-for="w in warehouseList" :key="w.id" :label="w.name" :value="w.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="调拨时间" prop="moveTime">
          <el-date-picker
            v-model="form.moveTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="选择调拨时间"
            style="width: 100%"
          />
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
        <el-table-column label="调拨数量" width="160">
          <template #default="{ row }">
            <el-input v-model="row.count" placeholder="如 1.500" inputmode="decimal" />
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

    <!-- 详情 drawer：头 descriptions + 明细。 -->
    <el-drawer v-model="detailVisible" title="库存调拨单详情" size="640px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="调出仓库">{{ detail.fromWarehouseName }}</el-descriptions-item>
        <el-descriptions-item label="调入仓库">{{ detail.toWarehouseName }}</el-descriptions-item>
        <el-descriptions-item label="调拨时间">{{ formatTime(detail.moveTime) }}</el-descriptions-item>
      </el-descriptions>
      <el-table v-if="detail" :data="detail.items" border size="small" class="detail-items">
        <el-table-column label="#" width="48">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="productName" label="产品" min-width="180" />
        <el-table-column prop="count" label="调拨数量" width="120" />
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
  pageStockMoves,
  getStockMove,
  createStockMove,
  updateStockMove,
  deleteStockMove,
  updateStockMoveStatus,
  exportStockMoves,
  pageProducts,
  pageWarehouses,
} from '@/api/erp'

// 库存调拨页（issue #93，spec §3.5/§4.1/§4.6）：未审核可改可删；审核落账
//（出仓负、入仓正两笔流水成对），反审核各生成反向流水（均带独立权限码
// erp:stock-move:approve）；单号服务端权威。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:stock-move:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:stock-move:update'))
const canDelete = computed(() => userStore.hasPerm('erp:stock-move:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:stock-move:approve'))

const warehouseList = ref<any[]>([])
const productList = ref<any[]>([])
const statusOptions = useDictOptions('erp_audit_status')

const searchItems = computed(() => [
  { prop: 'no', label: '单号' },
  {
    prop: 'fromWarehouseId',
    label: '调出仓库',
    type: 'select',
    options: warehouseList.value.map((w) => ({ label: w.name, value: w.id })),
  },
  {
    prop: 'toWarehouseId',
    label: '调入仓库',
    type: 'select',
    options: warehouseList.value.map((w) => ({ label: w.name, value: w.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'no', label: '单号', minWidth: 160 },
  { prop: 'fromWarehouseName', label: '调出仓库', minWidth: 120 },
  { prop: 'toWarehouseName', label: '调入仓库', minWidth: 120 },
  { prop: 'moveTime', label: '调拨时间', minWidth: 160, formatter: (row: any) => formatTime(row.moveTime) },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

const pageApi = { page: pageStockMoves }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, fromWarehouseId: q.fromWarehouseId, toWarehouseId: q.toWarehouseId, status: q.status }
}

// ---------- 新增/编辑 ----------
interface MoveItemRow {
  productId?: number
  count: string
}

const formVisible = ref(false)
const formTitle = ref('新增库存调拨单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{
  fromWarehouseId?: number
  toWarehouseId?: number
  moveTime: string
  items: MoveItemRow[]
}>({
  fromWarehouseId: undefined,
  toWarehouseId: undefined,
  moveTime: '',
  items: [],
})

const rules = {
  fromWarehouseId: [{ required: true, message: '请选择调出仓库', trigger: 'change' }],
  toWarehouseId: [{ required: true, message: '请选择调入仓库', trigger: 'change' }],
  moveTime: [{ required: true, message: '请选择调拨时间', trigger: 'change' }],
}

function addItem() {
  form.value.items.push({ productId: undefined, count: '' })
}

function removeItem(index: number) {
  form.value.items.splice(index, 1)
}

function openCreate() {
  editingID.value = undefined
  form.value = { fromWarehouseId: undefined, toWarehouseId: undefined, moveTime: '', items: [] }
  formTitle.value = '新增库存调拨单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getStockMove(id)) as any
  editingID.value = detail.id
  form.value = {
    fromWarehouseId: detail.fromWarehouseId,
    toWarehouseId: detail.toWarehouseId,
    moveTime: toLocalInput(detail.moveTime),
    items: (detail.items || []).map((it: any) => ({
      productId: it.productId,
      count: it.count,
    })),
  }
  formTitle.value = '编辑库存调拨单'
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
    fromWarehouseId: form.value.fromWarehouseId,
    toWarehouseId: form.value.toWarehouseId,
    moveTime: form.value.moveTime,
    items: form.value.items.map((it) => ({
      productId: it.productId,
      count: it.count,
    })),
  }
  submitting.value = true
  try {
    if (editingID.value) {
      await updateStockMove({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createStockMove(payload)
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
    `确认审核调拨单 ${row.no}？将同事务内从「${row.fromWarehouseName}」出仓、「${row.toWarehouseName}」入仓。`,
    '审核确认',
    { type: 'warning' },
  )
  await updateStockMoveStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核落账')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核调拨单 ${row.no}？将生成成对反向流水并恢复两仓库存（原流水保留）。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updateStockMoveStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除调拨单 ${row.no}？`, '删除确认', { type: 'warning' })
  await deleteStockMove(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getStockMove(id)) as any
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
