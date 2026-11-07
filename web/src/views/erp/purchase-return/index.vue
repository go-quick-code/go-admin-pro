<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:purchase-return"
      title="采购退货"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="采购退货单"
          export-perm="erp:purchase-return:export"
          :export-action="exportPurchaseReturns"
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
        <!-- 审核/反审核：独立权限码 erp:purchase-return:approve（spec §4.1）。 -->
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

    <!-- 新增/编辑：必挂已审核入库单（挂单选择只出 status=20）；仓库取自入库单
         （入参无仓库字段，服务端回填）；供应商需与入库单一致。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="880px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="92px">
        <el-form-item label="入库单" prop="inId">
          <el-select
            v-model="form.inId"
            filterable
            style="width: 100%"
            placeholder="请选择已审核采购入库单"
            :disabled="!!editingID"
            @change="onInDocChange"
          >
            <el-option
              v-for="d in approvedIns"
              :key="d.id"
              :label="`${d.no}（${d.supplierName} / ${d.warehouseName}）`"
              :value="d.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="供应商" prop="supplierId">
          <el-select v-model="form.supplierId" filterable style="width: 100%" placeholder="请选择供应商">
            <el-option v-for="s in supplierList" :key="s.id" :label="s.name" :value="s.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="退货时间" prop="returnTime">
          <el-date-picker
            v-model="form.returnTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="选择退货时间"
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

    <!-- 详情 drawer：头 descriptions（仓库取自入库单）+ 明细。 -->
    <el-drawer v-model="detailVisible" title="采购退货单详情" size="680px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="采购入库单">{{ detail.inNo }}</el-descriptions-item>
        <el-descriptions-item label="供应商">{{ detail.supplierName }}</el-descriptions-item>
        <el-descriptions-item label="仓库">{{ detail.warehouseName }}</el-descriptions-item>
        <el-descriptions-item label="退货时间">{{ formatTime(detail.returnTime) }}</el-descriptions-item>
        <el-descriptions-item label="单据金额">{{ detail.totalPrice }}</el-descriptions-item>
        <el-descriptions-item label="已核销退款">{{ detail.refundPrice }}</el-descriptions-item>
        <el-descriptions-item label="备注">{{ detail.remark || '—' }}</el-descriptions-item>
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
import {
  pagePurchaseReturns,
  getPurchaseReturn,
  createPurchaseReturn,
  updatePurchaseReturn,
  deletePurchaseReturn,
  updatePurchaseReturnStatus,
  exportPurchaseReturns,
  pagePurchaseIns,
  pageSuppliers,
  pageProducts,
} from '@/api/erp'

// 采购退货单页（issue #91，spec §3.3/§4.4/§6）：必挂已审核入库单（挂单选择只出
// status=20，spec §6）；仓库取自入库单（服务端回填，表单无仓库项）；审核落账减
// 库存并回写订单 return_count。单号/行金额/主表金额服务端权威。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:purchase-return:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:purchase-return:update'))
const canDelete = computed(() => userStore.hasPerm('erp:purchase-return:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:purchase-return:approve'))

const supplierList = ref<any[]>([])
const productList = ref<any[]>([])
const approvedIns = ref<any[]>([])
const statusOptions = [
  { label: '未审核', value: 10 },
  { label: '已审核', value: 20 },
]

const searchItems = computed(() => [
  { prop: 'no', label: '单号' },
  {
    prop: 'supplierId',
    label: '供应商',
    type: 'select',
    options: supplierList.value.map((s) => ({ label: s.name, value: s.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'no', label: '单号', minWidth: 160 },
  { prop: 'supplierName', label: '供应商', minWidth: 120 },
  { prop: 'inNo', label: '采购入库单号', minWidth: 160 },
  { prop: 'warehouseName', label: '仓库', minWidth: 110 },
  { prop: 'returnTime', label: '退货时间', minWidth: 160, formatter: (row: any) => formatTime(row.returnTime) },
  { prop: 'totalPrice', label: '单据金额（元）', width: 120 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

// 分页透传；删除/编辑走自定义动作（需要按状态显隐）。
const pageApi = { page: pagePurchaseReturns }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, supplierId: q.supplierId, status: q.status }
}

// ---------- 新增/编辑 ----------
const formVisible = ref(false)
const formTitle = ref('新增采购退货单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{
  inId?: number
  supplierId?: number
  returnTime: string
  remark: string
  items: DocItemRow[]
}>({
  inId: undefined,
  supplierId: undefined,
  returnTime: '',
  remark: '',
  items: [],
})

const rules = {
  inId: [{ required: true, message: '请选择已审核采购入库单', trigger: 'change' }],
  supplierId: [{ required: true, message: '请选择供应商', trigger: 'change' }],
  returnTime: [{ required: true, message: '请选择退货时间', trigger: 'change' }],
}

function openCreate() {
  editingID.value = undefined
  form.value = { inId: undefined, supplierId: undefined, returnTime: '', remark: '', items: [] }
  formTitle.value = '新增采购退货单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getPurchaseReturn(id)) as any
  editingID.value = detail.id
  form.value = {
    inId: detail.inId,
    supplierId: detail.supplierId,
    returnTime: toLocalInput(detail.returnTime),
    remark: detail.remark || '',
    items: (detail.items || []).map((it: any) => ({
      productId: it.productId,
      count: it.count,
      productPrice: it.productPrice,
    })),
  }
  formTitle.value = '编辑采购退货单'
  formVisible.value = true
}

// 服务端返回业务时区时间字符串（"2006-01-02 15:04:05"），datetime 控件按同格式回填。
function toLocalInput(value: string) {
  if (!value) return ''
  return value.length > 19 ? value.slice(0, 19).replace('T', ' ') : value.replace('T', ' ')
}

// 选中入库单后带出其供应商（供应商需与入库单一致，后端校验）。
function onInDocChange(inId: number) {
  const doc = approvedIns.value.find((d) => d.id === inId)
  if (doc) {
    form.value.supplierId = doc.supplierId
  }
}

async function submit() {
  await formRef.value?.validate()
  if (!form.value.items.length) {
    ElMessage.warning('请至少添加一条明细')
    return
  }
  const payload: any = {
    inId: form.value.inId,
    supplierId: form.value.supplierId,
    returnTime: form.value.returnTime,
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
      await updatePurchaseReturn({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createPurchaseReturn(payload)
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
  await ElMessageBox.confirm(`确认审核退货单 ${row.no}？审核后将减少库存。`, '审核确认', { type: 'warning' })
  await updatePurchaseReturnStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核落账')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核退货单 ${row.no}？将生成反向流水并恢复库存（原流水保留）。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updatePurchaseReturnStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除退货单 ${row.no}？删除后将释放入库单占用。`, '删除确认', { type: 'warning' })
  await deletePurchaseReturn(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getPurchaseReturn(id)) as any
  detailVisible.value = true
}

onMounted(async () => {
  supplierList.value = ((await pageSuppliers({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  productList.value = ((await pageProducts({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  // 挂单选择只出已审核入库单（spec §6：挂单字段只能从已审核单中选择）。
  approvedIns.value = ((await pagePurchaseIns({ pageNo: 1, pageSize: 200, status: 20 })) as any)?.list || []
})
</script>

<style scoped>
.detail-items {
  margin-top: 12px;
}
</style>
