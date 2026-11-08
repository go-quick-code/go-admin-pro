<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:stock-out"
      title="其它出库"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="其它出库单"
          export-perm="erp:stock-out:export"
          :export-action="exportStockOuts"
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
        <!-- 审核（余额不足 409）/反审核：独立权限码 erp:stock-out:approve。 -->
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

    <!-- 新增/编辑：单号由服务端发放（无单号输入），行金额服务端计算。 -->
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
        <el-form-item label="类型" prop="type">
          <el-select v-model="form.type" style="width: 100%" placeholder="请选择出库类型">
            <el-option v-for="opt in typeOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
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

    <!-- 详情 drawer。 -->
    <el-drawer v-model="detailVisible" title="其它出库单详情" size="640px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="仓库">{{ detail.warehouseName }}</el-descriptions-item>
        <el-descriptions-item label="出库时间">{{ formatTime(detail.outTime) }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ detail.typeName }}</el-descriptions-item>
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
import { useDictOptions } from '@/composables/useDict'
import {
  pageStockOuts,
  getStockOut,
  createStockOut,
  updateStockOut,
  deleteStockOut,
  updateStockOutStatus,
  exportStockOuts,
  pageProducts,
  pageWarehouses,
} from '@/api/erp'

// 其它出库单页（issue #90，spec §3.5/§4.1/§6）：与其它入库单镜像；审核出账
//（余额不足 409）、反审核回滚；approve 为独立权限码 erp:stock-out:approve。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:stock-out:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:stock-out:update'))
const canDelete = computed(() => userStore.hasPerm('erp:stock-out:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:stock-out:approve'))

const warehouseList = ref<any[]>([])
const productList = ref<any[]>([])
const typeOptions = useDictOptions('erp_stock_out_type')
const statusOptions = useDictOptions('erp_audit_status')

const searchItems = computed(() => [
  { prop: 'no', label: '单号' },
  {
    prop: 'warehouseId',
    label: '仓库',
    type: 'select',
    options: warehouseList.value.map((w) => ({ label: w.name, value: w.id })),
  },
  { prop: 'type', label: '类型', type: 'select', options: typeOptions },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'no', label: '单号', minWidth: 160 },
  { prop: 'warehouseName', label: '仓库', minWidth: 120 },
  { prop: 'docTime', label: '出库时间', minWidth: 160, formatter: (row: any) => formatTime(row.docTime) },
  { prop: 'typeName', label: '类型', width: 90 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

const pageApi = { page: pageStockOuts }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, warehouseId: q.warehouseId, type: q.type, status: q.status }
}

// ---------- 新增/编辑 ----------
const formVisible = ref(false)
const formTitle = ref('新增其它出库单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{ warehouseId?: number; outTime: string; type?: string; remark: string; items: DocItemRow[] }>({
  warehouseId: undefined,
  outTime: '',
  type: '99',
  remark: '',
  items: [],
})

const rules = {
  warehouseId: [{ required: true, message: '请选择仓库', trigger: 'change' }],
  outTime: [{ required: true, message: '请选择出库时间', trigger: 'change' }],
}

function openCreate() {
  editingID.value = undefined
  form.value = { warehouseId: undefined, outTime: '', type: '99', remark: '', items: [] }
  formTitle.value = '新增其它出库单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getStockOut(id)) as any
  editingID.value = detail.id
  form.value = {
    warehouseId: detail.warehouseId,
    outTime: toLocalInput(detail.outTime),
    type: String(detail.type),
    remark: detail.remark || '',
    items: (detail.items || []).map((it: any) => ({
      productId: it.productId,
      count: it.count,
      productPrice: it.productPrice,
    })),
  }
  formTitle.value = '编辑其它出库单'
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
    warehouseId: form.value.warehouseId,
    outTime: form.value.outTime,
    type: Number(form.value.type ?? 99),
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
      await updateStockOut({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createStockOut(payload)
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
  await ElMessageBox.confirm(`确认审核出库单 ${row.no}？审核后将扣减库存（余额不足会拒绝）。`, '审核确认', {
    type: 'warning',
  })
  await updateStockOutStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核落账')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核出库单 ${row.no}？将生成反向流水并恢复库存（原流水保留）。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updateStockOutStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除出库单 ${row.no}？`, '删除确认', { type: 'warning' })
  await deleteStockOut(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getStockOut(id)) as any
  detailVisible.value = true
}

onMounted(async () => {
  warehouseList.value = ((await pageWarehouses({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  productList.value = ((await pageProducts({ pageNo: 1, pageSize: 200 })) as any)?.list || []
})
</script>

<style scoped>
.detail-items {
  margin-top: 12px;
}
</style>
