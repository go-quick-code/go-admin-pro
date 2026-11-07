<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:purchase-order"
      title="采购订单"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="采购订单"
          export-perm="erp:purchase-order:export"
          :export-action="exportPurchaseOrders"
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
        <!-- 审核/反审核：独立权限码 erp:purchase-order:approve（spec §4.1）；
             存在已审核入库/退货时后端 409 拒绝反审核（spec §4.4）。 -->
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

    <!-- 新增/编辑：单号由服务端发放（无单号输入），行金额服务端计算（无行金额输入）。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="880px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="92px">
        <el-form-item label="供应商" prop="supplierId">
          <el-select v-model="form.supplierId" filterable style="width: 100%" placeholder="请选择供应商">
            <el-option v-for="s in supplierList" :key="s.id" :label="s.name" :value="s.id" />
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

    <!-- 详情 drawer：头 descriptions + 明细（行金额/进度回写服务端权威）。 -->
    <el-drawer v-model="detailVisible" title="采购订单详情" size="720px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="单号">{{ detail.orderNo }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="供应商">{{ detail.supplierName }}</el-descriptions-item>
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
        <el-table-column prop="totalPrice" label="行金额（元）" width="110" />
        <el-table-column prop="inCount" label="已入库" width="90" />
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
import {
  pagePurchaseOrders,
  getPurchaseOrder,
  createPurchaseOrder,
  updatePurchaseOrder,
  deletePurchaseOrder,
  updatePurchaseOrderStatus,
  exportPurchaseOrders,
  pageSuppliers,
  pageProducts,
} from '@/api/erp'

// 采购订单页（issue #91，spec §3.3/§4.4/§6）：未审核可改可删；审核只翻状态
//（不动库存）；反审核在存在已审核入库/退货时后端 409 拒绝。单号/行金额/进度
//（已入库/已退货）服务端权威。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:purchase-order:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:purchase-order:update'))
const canDelete = computed(() => userStore.hasPerm('erp:purchase-order:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:purchase-order:approve'))

const supplierList = ref<any[]>([])
const productList = ref<any[]>([])
const statusOptions = [
  { label: '未审核', value: 10 },
  { label: '已审核', value: 20 },
]

const searchItems = computed(() => [
  { prop: 'orderNo', label: '订单号' },
  {
    prop: 'supplierId',
    label: '供应商',
    type: 'select',
    options: supplierList.value.map((s) => ({ label: s.name, value: s.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'orderNo', label: '订单号', minWidth: 160 },
  { prop: 'supplierName', label: '供应商', minWidth: 140 },
  { prop: 'orderTime', label: '下单时间', minWidth: 160, formatter: (row: any) => formatTime(row.orderTime) },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

// 分页透传；删除/编辑走自定义动作（需要按状态显隐）。
const pageApi = { page: pagePurchaseOrders }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { orderNo: q.orderNo, supplierId: q.supplierId, status: q.status }
}

// ---------- 新增/编辑 ----------
const formVisible = ref(false)
const formTitle = ref('新增采购订单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{ supplierId?: number; orderTime: string; remark: string; items: DocItemRow[] }>({
  supplierId: undefined,
  orderTime: '',
  remark: '',
  items: [],
})

const rules = {
  supplierId: [{ required: true, message: '请选择供应商', trigger: 'change' }],
  orderTime: [{ required: true, message: '请选择下单时间', trigger: 'change' }],
}

function openCreate() {
  editingID.value = undefined
  form.value = { supplierId: undefined, orderTime: '', remark: '', items: [] }
  formTitle.value = '新增采购订单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getPurchaseOrder(id)) as any
  editingID.value = detail.id
  form.value = {
    supplierId: detail.supplierId,
    orderTime: toLocalInput(detail.orderTime),
    remark: detail.remark || '',
    items: (detail.items || []).map((it: any) => ({
      productId: it.productId,
      count: it.count,
      productPrice: it.productPrice,
    })),
  }
  formTitle.value = '编辑采购订单'
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
    supplierId: form.value.supplierId,
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
      await updatePurchaseOrder({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createPurchaseOrder(payload)
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
  await ElMessageBox.confirm(`确认审核采购订单 ${row.orderNo}？`, '审核确认', { type: 'warning' })
  await updatePurchaseOrderStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核采购订单 ${row.orderNo}？存在已审核入库/退货单时将被拒绝。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updatePurchaseOrderStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除采购订单 ${row.orderNo}？`, '删除确认', { type: 'warning' })
  await deletePurchaseOrder(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getPurchaseOrder(id)) as any
  detailVisible.value = true
}

onMounted(async () => {
  supplierList.value = ((await pageSuppliers({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  productList.value = ((await pageProducts({ pageNo: 1, pageSize: 200 })) as any)?.list || []
})
</script>

<style scoped>
.detail-items {
  margin-top: 12px;
}
</style>
