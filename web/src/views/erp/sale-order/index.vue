<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:sale-order"
      title="销售订单"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="销售订单"
          export-perm="erp:sale-order:export"
          :export-action="exportSaleOrders"
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
        <!-- 审核/反审核：独立权限码 erp:sale-order:approve（spec §4.1）；反审核带确认。 -->
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

    <!-- 新增/编辑：订单号由服务端发放（无单号输入），行金额服务端计算（无行金额输入）。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="880px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="92px">
        <el-form-item label="客户" prop="customerId">
          <el-select v-model="form.customerId" filterable style="width: 100%" placeholder="请选择 ERP 客户">
            <el-option v-for="c in customerList" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="销售员" prop="saleUserId">
          <el-select v-model="form.saleUserId" filterable clearable style="width: 100%" placeholder="可选（纯记录）">
            <el-option v-for="u in userList" :key="u.id" :label="u.nickname" :value="u.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="下单时间" prop="orderTime">
          <el-date-picker
            v-model="form.orderTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="选择下单时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="form.remark" type="textarea" :rows="2" maxlength="500" placeholder="可选" />
        </el-form-item>
      </el-form>
      <ErpDocItemsEditor v-model="form.items" :products="productList" editable />
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>

    <!-- 详情 drawer：头 descriptions + 明细（行金额/进度服务端权威）。 -->
    <el-drawer v-model="detailVisible" title="销售订单详情" size="680px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="订单号">{{ detail.orderNo }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="客户">{{ detail.customerName }}</el-descriptions-item>
        <el-descriptions-item label="下单时间">{{ formatTime(detail.orderTime) }}</el-descriptions-item>
        <el-descriptions-item label="备注">{{ detail.remark || '—' }}</el-descriptions-item>
      </el-descriptions>
      <el-table v-if="detail" :data="detail.items" border size="small" class="detail-items">
        <el-table-column label="#" width="48">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="productName" label="产品" min-width="140" />
        <el-table-column prop="count" label="数量" width="90" />
        <el-table-column prop="productPrice" label="单价（元）" width="100" />
        <el-table-column prop="totalPrice" label="行金额（元）" width="105" />
        <el-table-column prop="outCount" label="已出库" width="90" />
        <el-table-column prop="returnCount" label="已退货" width="90" />
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
import { listSimpleUsers } from '@/api/system'
import {
  pageSaleOrders,
  getSaleOrder,
  createSaleOrder,
  updateSaleOrder,
  deleteSaleOrder,
  updateSaleOrderStatus,
  exportSaleOrders,
  pageCustomers,
  pageProducts,
} from '@/api/erp'

// 销售订单页（issue #92，spec §3.4/§4.1/§6）：未审核可改可删；审核/反审核带
// 独立权限码 erp:sale-order:approve；出库/退货挂已审核订单（此处审核是前置）。
// 销售员为纯记录字段（saleUserId，可选）。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:sale-order:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:sale-order:update'))
const canDelete = computed(() => userStore.hasPerm('erp:sale-order:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:sale-order:approve'))

const customerList = ref<any[]>([])
const productList = ref<any[]>([])
const userList = ref<any[]>([])
const statusOptions = useDictOptions('erp_audit_status')

const searchItems = computed(() => [
  { prop: 'orderNo', label: '订单号' },
  {
    prop: 'customerId',
    label: '客户',
    type: 'select',
    options: customerList.value.map((c) => ({ label: c.name, value: c.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'orderNo', label: '订单号', minWidth: 160 },
  { prop: 'customerName', label: '客户', minWidth: 120 },
  { prop: 'orderTime', label: '下单时间', minWidth: 160, formatter: (row: any) => formatTime(row.orderTime) },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

// 分页透传；删除/编辑走自定义动作（需要按状态显隐）。
const pageApi = { page: pageSaleOrders }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { orderNo: q.orderNo, customerId: q.customerId, status: q.status }
}

// ---------- 新增/编辑 ----------
const formVisible = ref(false)
const formTitle = ref('新增销售订单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{
  customerId?: number
  saleUserId?: number
  orderTime: string
  remark: string
  items: DocItemRow[]
}>({
  customerId: undefined,
  saleUserId: undefined,
  orderTime: '',
  remark: '',
  items: [],
})

const rules = {
  customerId: [{ required: true, message: '请选择客户', trigger: 'change' }],
  orderTime: [{ required: true, message: '请选择下单时间', trigger: 'change' }],
}

function openCreate() {
  editingID.value = undefined
  form.value = { customerId: undefined, saleUserId: undefined, orderTime: '', remark: '', items: [] }
  formTitle.value = '新增销售订单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getSaleOrder(id)) as any
  editingID.value = detail.id
  form.value = {
    customerId: detail.customerId,
    saleUserId: detail.saleUserId || undefined,
    orderTime: toLocalInput(detail.orderTime),
    remark: detail.remark || '',
    items: (detail.items || []).map((it: any) => ({
      productId: it.productId,
      count: it.count,
      productPrice: it.productPrice,
    })),
  }
  formTitle.value = '编辑销售订单'
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
    saleUserId: form.value.saleUserId || 0,
    orderTime: form.value.orderTime,
    remark: form.value.remark,
    items: form.value.items.map((it) => ({
      productId: it.productId,
      count: it.count,
      productPrice: it.productPrice || '0.00',
    })),
  }
  submitting.value = true
  try {
    if (editingID.value) {
      await updateSaleOrder({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createSaleOrder(payload)
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
  await ElMessageBox.confirm(`确认审核销售订单 ${row.orderNo}？审核后出库单方可挂单。`, '审核确认', {
    type: 'warning',
  })
  await updateSaleOrderStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核销售订单 ${row.orderNo}？存在已审核出库/退货单时将被拒绝。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updateSaleOrderStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除销售订单 ${row.orderNo}？`, '删除确认', { type: 'warning' })
  await deleteSaleOrder(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getSaleOrder(id)) as any
  detailVisible.value = true
}

onMounted(async () => {
  const [customers, products, users] = await Promise.all([
    pageCustomers({ pageNo: 1, pageSize: 200 }),
    pageProducts({ pageNo: 1, pageSize: 200 }),
    listSimpleUsers(),
  ])
  customerList.value = (customers as any)?.list || []
  productList.value = (products as any)?.list || []
  userList.value = (users as any) || []
})
</script>

<style scoped>
.detail-items {
  margin-top: 12px;
}
</style>
