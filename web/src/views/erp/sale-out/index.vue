<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:sale-out"
      title="销售出库"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="销售出库单"
          export-perm="erp:sale-out:export"
          :export-action="exportSaleOuts"
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
        <!-- 审核/反审核：独立权限码 erp:sale-out:approve（spec §4.1）；反审核带确认。 -->
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

    <!-- 新增/编辑：单号/行金额/总金额服务端权威；挂单字段只能从已审核订单中
         选择（spec §6），客户随挂单订单锁定（后端校验一致）。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="880px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="92px">
        <el-form-item label="销售订单" prop="orderId">
          <el-select
            v-model="form.orderId"
            filterable
            style="width: 100%"
            placeholder="请选择已审核销售订单"
            @change="onOrderChange"
          >
            <el-option
              v-for="o in approvedOrders"
              :key="o.id"
              :label="`${o.orderNo}（${o.customerName}）`"
              :value="o.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="客户" prop="customerId">
          <el-select v-model="form.customerId" filterable style="width: 100%" placeholder="请选择客户（须与订单一致）">
            <el-option v-for="c in customerList" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="仓库" prop="warehouseId">
          <el-select v-model="form.warehouseId" style="width: 100%" placeholder="请选择出库仓库">
            <el-option v-for="w in warehouseList" :key="w.id" :label="w.name" :value="w.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="出库时间" prop="outTime">
          <el-date-picker
            v-model="form.outTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="选择出库时间"
            style="width: 100%"
          />
        </el-form-item>
      </el-form>
      <ErpDocItemsEditor v-model="form.items" :products="productList" editable />
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>

    <!-- 详情 drawer：头 descriptions + 明细（行金额/已收款服务端权威）。 -->
    <el-drawer v-model="detailVisible" title="销售出库单详情" size="680px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="出库单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="销售订单">{{ detail.orderNo }}</el-descriptions-item>
        <el-descriptions-item label="客户">{{ detail.customerName }}</el-descriptions-item>
        <el-descriptions-item label="仓库">{{ detail.warehouseName }}</el-descriptions-item>
        <el-descriptions-item label="出库时间">{{ formatTime(detail.outTime) }}</el-descriptions-item>
        <el-descriptions-item label="出库总金额">{{ detail.totalPrice }}</el-descriptions-item>
        <el-descriptions-item label="已收款">{{ detail.receiptPrice }}</el-descriptions-item>
      </el-descriptions>
      <el-table v-if="detail" :data="detail.items" border size="small" class="detail-items">
        <el-table-column label="#" width="48">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="productName" label="产品" min-width="150" />
        <el-table-column prop="count" label="数量" width="100" />
        <el-table-column prop="productPrice" label="单价（元）" width="110" />
        <el-table-column prop="totalPrice" label="行金额（元）" width="110" />
      </el-table>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import ErpDocItemsEditor, { type DocItemRow } from '@/components/ErpDocItemsEditor.vue'
import { formatTime } from '@/utils/format'
import { useUserStore } from '@/stores/user'
import { useDictOptions } from '@/composables/useDict'
import {
  pageSaleOuts,
  getSaleOut,
  createSaleOut,
  updateSaleOut,
  deleteSaleOut,
  updateSaleOutStatus,
  exportSaleOuts,
  pageSaleOrders,
  pageCustomers,
  pageWarehouses,
  pageProducts,
} from '@/api/erp'

// 销售出库单页（issue #92，spec §3.4/§4.1/§4.5/§6）：必挂已审核销售订单，审核
// 减库存并回写订单 out_count；反审核生成反向流水（存在已审核退货时 409）。
// 单号/行金额/总金额服务端权威。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:sale-out:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:sale-out:update'))
const canDelete = computed(() => userStore.hasPerm('erp:sale-out:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:sale-out:approve'))

const approvedOrders = ref<any[]>([])
const customerList = ref<any[]>([])
const warehouseList = ref<any[]>([])
const productList = ref<any[]>([])
const statusOptions = useDictOptions('erp_audit_status')

const searchItems = computed(() => [
  { prop: 'no', label: '单号' },
  {
    prop: 'orderId',
    label: '销售订单',
    type: 'select',
    options: approvedOrders.value.map((o) => ({ label: o.orderNo, value: o.id })),
  },
  {
    prop: 'warehouseId',
    label: '仓库',
    type: 'select',
    options: warehouseList.value.map((w) => ({ label: w.name, value: w.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'no', label: '出库单号', minWidth: 160 },
  { prop: 'orderNo', label: '销售订单号', minWidth: 160 },
  { prop: 'customerName', label: '客户', minWidth: 110 },
  { prop: 'warehouseName', label: '仓库', minWidth: 110 },
  { prop: 'outTime', label: '出库时间', minWidth: 160, formatter: (row: any) => formatTime(row.outTime) },
  { prop: 'totalPrice', label: '出库总金额', width: 110 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

// 分页透传；删除/编辑走自定义动作（需要按状态显隐）。
const pageApi = { page: pageSaleOuts }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, orderId: q.orderId, warehouseId: q.warehouseId, status: q.status }
}

// ---------- 新增/编辑 ----------
const formVisible = ref(false)
const formTitle = ref('新增销售出库单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{
  orderId?: number
  customerId?: number
  warehouseId?: number
  outTime: string
  items: DocItemRow[]
}>({
  orderId: undefined,
  customerId: undefined,
  warehouseId: undefined,
  outTime: '',
  items: [],
})

const rules = {
  orderId: [{ required: true, message: '请选择已审核销售订单', trigger: 'change' }],
  customerId: [{ required: true, message: '请选择客户', trigger: 'change' }],
  warehouseId: [{ required: true, message: '请选择仓库', trigger: 'change' }],
  outTime: [{ required: true, message: '请选择出库时间', trigger: 'change' }],
}

// 挂单后客户随订单锁定（后端校验一致，前端联动减少误选）。
function onOrderChange(orderId?: number) {
  const order = approvedOrders.value.find((o) => o.id === orderId)
  if (order) form.value.customerId = order.customerId
}

function openCreate() {
  editingID.value = undefined
  form.value = { orderId: undefined, customerId: undefined, warehouseId: undefined, outTime: '', items: [] }
  formTitle.value = '新增销售出库单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getSaleOut(id)) as any
  editingID.value = detail.id
  form.value = {
    orderId: detail.orderId,
    customerId: detail.customerId,
    warehouseId: detail.warehouseId,
    outTime: toLocalInput(detail.outTime),
    items: (detail.items || []).map((it: any) => ({
      productId: it.productId,
      count: it.count,
      productPrice: it.productPrice,
    })),
  }
  formTitle.value = '编辑销售出库单'
  formVisible.value = true
}

// 服务端返回业务时区时间字符串（"2006-01-02 15:04:05"），datetime 控件按同格式回填。
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
    customerId: form.value.customerId,
    orderId: form.value.orderId,
    warehouseId: form.value.warehouseId,
    outTime: form.value.outTime,
    items: form.value.items.map((it) => ({
      productId: it.productId,
      count: it.count,
      productPrice: it.productPrice || '0.00',
    })),
  }
  submitting.value = true
  try {
    if (editingID.value) {
      await updateSaleOut({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createSaleOut(payload)
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
  await ElMessageBox.confirm(`确认审核出库单 ${row.no}？审核后将扣减库存并回写订单进度。`, '审核确认', {
    type: 'warning',
  })
  await updateSaleOutStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核落账')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核出库单 ${row.no}？将生成反向流水回补库存并回退订单进度（原流水保留）。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updateSaleOutStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除出库单 ${row.no}？`, '删除确认', { type: 'warning' })
  await deleteSaleOut(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getSaleOut(id)) as any
  detailVisible.value = true
}

onMounted(async () => {
  const [orders, customers, warehouses, products] = await Promise.all([
    pageSaleOrders({ pageNo: 1, pageSize: 200, status: 20 }),
    pageCustomers({ pageNo: 1, pageSize: 200 }),
    pageWarehouses({ pageNo: 1, pageSize: 200 }),
    pageProducts({ pageNo: 1, pageSize: 200 }),
  ])
  approvedOrders.value = (orders as any)?.list || []
  customerList.value = (customers as any)?.list || []
  warehouseList.value = (warehouses as any)?.list || []
  productList.value = (products as any)?.list || []
})
</script>

<style scoped>
.detail-items {
  margin-top: 12px;
}
</style>
