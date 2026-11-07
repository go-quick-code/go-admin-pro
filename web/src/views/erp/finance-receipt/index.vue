<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="240"
      perm-prefix="erp:finance-receipt"
      title="收款单"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
        <ExcelActions
          title="收款单"
          export-perm="erp:finance-receipt:export"
          :export-action="exportFinanceReceipts"
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
        <!-- 审核/反审核：独立权限码 erp:finance-receipt:approve（spec §4.1）；
             审核校验核销上限并回写已收金额，反审核重算释放额度。 -->
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

    <!-- 新增/编辑：明细只挂已审核销售出库（收）/销售退货（退）；单号服务端发放；
         核销合计与回写金额服务端权威。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="920px"
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
        <el-form-item label="结算账户" prop="accountId">
          <el-select v-model="form.accountId" filterable style="width: 100%" placeholder="请选择结算账户">
            <el-option v-for="a in accountList" :key="a.id" :label="a.name" :value="a.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="经手人" prop="receiptUserId">
          <el-select v-model="form.receiptUserId" filterable clearable style="width: 100%" placeholder="可选（纯记录）">
            <el-option v-for="u in userList" :key="u.id" :label="u.nickname" :value="u.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="收款时间" prop="receiptTime">
          <el-date-picker
            v-model="form.receiptTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="选择收款时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="form.remark" type="textarea" :rows="2" maxlength="500" placeholder="可选" />
        </el-form-item>
      </el-form>

      <div class="doc-items-editor">
        <div class="section-head">
          <span>核销明细（只能挂已审核销售出库/退货）</span>
          <el-button size="small" type="primary" plain @click="addItem">添加明细</el-button>
        </div>
        <el-table :data="form.items" border size="small" empty-text="请至少添加一条明细">
          <el-table-column label="#" width="48">
            <template #default="{ $index }">{{ $index + 1 }}</template>
          </el-table-column>
          <el-table-column label="业务类型" width="140">
            <template #default="{ row }">
              <el-select v-model="row.bizType" style="width: 100%" @change="row.bizId = undefined">
                <el-option v-for="t in bizTypeOptions" :key="t.value" :label="t.label" :value="t.value" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="被核销单据" min-width="300">
            <template #default="{ row }">
              <el-select v-model="row.bizId" filterable placeholder="请选择已审核单据" style="width: 100%">
                <el-option
                  v-for="d in docOptions(row.bizType)"
                  :key="d.id"
                  :label="docLabel(row.bizType, d)"
                  :value="d.id"
                />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="本次核销金额（元）" width="180">
            <template #default="{ row }">
              <el-input v-model="row.price" placeholder="如 100.00" inputmode="decimal" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="70">
            <template #default="{ $index }">
              <el-button link type="danger" @click="removeItem($index)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>

      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>

    <!-- 详情 drawer：头 descriptions + 核销明细。 -->
    <el-drawer v-model="detailVisible" title="收款单详情" size="720px">
      <el-descriptions v-if="detail" :column="2" border>
        <el-descriptions-item label="单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ detail.statusName }}</el-descriptions-item>
        <el-descriptions-item label="客户">{{ detail.customerName }}</el-descriptions-item>
        <el-descriptions-item label="结算账户">{{ detail.accountName }}</el-descriptions-item>
        <el-descriptions-item label="收款时间">{{ formatTime(detail.receiptTime) }}</el-descriptions-item>
        <el-descriptions-item label="核销金额合计">{{ detail.totalPrice }}</el-descriptions-item>
        <el-descriptions-item label="备注">{{ detail.remark || '—' }}</el-descriptions-item>
      </el-descriptions>
      <el-table v-if="detail" :data="detail.items" border size="small" class="detail-items">
        <el-table-column label="#" width="48">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="bizTypeName" label="业务类型" width="110" />
        <el-table-column prop="bizNo" label="被核销单据" min-width="170" />
        <el-table-column prop="price" label="核销金额（元）" width="140" />
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
import { listSimpleUsers } from '@/api/system'
import {
  pageFinanceReceipts,
  getFinanceReceipt,
  createFinanceReceipt,
  updateFinanceReceipt,
  deleteFinanceReceipt,
  updateFinanceReceiptStatus,
  exportFinanceReceipts,
  pageFinanceAccounts,
  pageCustomers,
  pageSaleOuts,
  pageSaleReturns,
} from '@/api/erp'

// 收款单页（issue #94，spec §3.6/§4.7/§6）：明细只挂已审核销售出库（收）/
// 销售退货（退）；审核时后端校验「本单明细合计 + 其它已审核核销 ≤ 单据总额」，
// 超限 409；审核回写 sale_out.receiptPrice / sale_return.refundPrice，反审核
// 重算释放额度；存在已审核收款时出库/退货不可反审核（后端 409）。
interface FinanceItemRow {
  bizType: string
  bizId?: number
  price: string
}

const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:finance-receipt:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:finance-receipt:update'))
const canDelete = computed(() => userStore.hasPerm('erp:finance-receipt:delete'))
const canApprove = computed(() => userStore.hasPerm('erp:finance-receipt:approve'))

const customerList = ref<any[]>([])
const accountList = ref<any[]>([])
const userList = ref<any[]>([])
const approvedSaleOuts = ref<any[]>([])
const approvedSaleReturns = ref<any[]>([])
const statusOptions = useDictOptions('erp_audit_status')

const bizTypeOptions = [
  { label: '销售出库', value: 'sale_out' },
  { label: '销售退货', value: 'sale_return' },
]

function docOptions(bizType: string) {
  return bizType === 'sale_return' ? approvedSaleReturns.value : approvedSaleOuts.value
}

function docLabel(bizType: string, doc: any) {
  const settled = bizType === 'sale_return' ? doc.refundPrice : doc.receiptPrice
  return `${doc.no}（总额 ${doc.totalPrice}，已核销 ${settled || '0.00'}）`
}

const searchItems = computed(() => [
  { prop: 'no', label: '单号' },
  {
    prop: 'customerId',
    label: '客户',
    type: 'select',
    options: customerList.value.map((c) => ({ label: c.name, value: c.id })),
  },
  {
    prop: 'accountId',
    label: '结算账户',
    type: 'select',
    options: accountList.value.map((a) => ({ label: a.name, value: a.id })),
  },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
])

const columns = [
  { prop: 'no', label: '单号', minWidth: 160 },
  { prop: 'customerName', label: '客户', minWidth: 120 },
  { prop: 'accountName', label: '结算账户', minWidth: 120 },
  { prop: 'receiptTime', label: '收款时间', minWidth: 160, formatter: (row: any) => formatTime(row.receiptTime) },
  { prop: 'totalPrice', label: '核销金额合计（元）', width: 150 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

const pageApi = { page: pageFinanceReceipts }

function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, customerId: q.customerId, accountId: q.accountId, status: q.status }
}

// ---------- 新增/编辑 ----------
const formVisible = ref(false)
const formTitle = ref('新增收款单')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{
  customerId?: number
  accountId?: number
  receiptUserId?: number
  receiptTime: string
  remark: string
  items: FinanceItemRow[]
}>({ customerId: undefined, accountId: undefined, receiptUserId: undefined, receiptTime: '', remark: '', items: [] })

const rules = {
  customerId: [{ required: true, message: '请选择客户', trigger: 'change' }],
  accountId: [{ required: true, message: '请选择结算账户', trigger: 'change' }],
  receiptTime: [{ required: true, message: '请选择收款时间', trigger: 'change' }],
}

function addItem() {
  form.value.items.push({ bizType: 'sale_out', bizId: undefined, price: '' })
}

function removeItem(index: number) {
  form.value.items.splice(index, 1)
}

function openCreate() {
  editingID.value = undefined
  form.value = {
    customerId: undefined,
    accountId: undefined,
    receiptUserId: undefined,
    receiptTime: '',
    remark: '',
    items: [],
  }
  formTitle.value = '新增收款单'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getFinanceReceipt(id)) as any
  editingID.value = detail.id
  form.value = {
    customerId: detail.customerId,
    accountId: detail.accountId,
    receiptUserId: detail.receiptUserId || undefined,
    receiptTime: toLocalInput(detail.receiptTime),
    remark: detail.remark || '',
    items: (detail.items || []).map((it: any) => ({
      bizType: it.bizType,
      bizId: it.bizId,
      price: it.price,
    })),
  }
  formTitle.value = '编辑收款单'
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
    ElMessage.warning('请至少添加一条核销明细')
    return
  }
  if (form.value.items.some((it) => !it.bizId || !it.price)) {
    ElMessage.warning('请为每条明细选择被核销单据并填写核销金额')
    return
  }
  const payload: any = {
    customerId: form.value.customerId,
    accountId: form.value.accountId,
    receiptUserId: form.value.receiptUserId || 0,
    receiptTime: form.value.receiptTime,
    remark: form.value.remark,
    items: form.value.items.map((it) => ({
      bizType: it.bizType,
      bizId: it.bizId,
      price: it.price,
    })),
  }
  submitting.value = true
  try {
    if (editingID.value) {
      await updateFinanceReceipt({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createFinanceReceipt(payload)
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
    `确认审核收款单 ${row.no}？将校验核销上限并回写被核销单据已收金额。`,
    '审核确认',
    { type: 'warning' },
  )
  await updateFinanceReceiptStatus({ id: row.id, status: 20 })
  ElMessage.success('已审核落账')
  tableRef.value?.reload?.()
}

async function doUnapprove(row: any) {
  await ElMessageBox.confirm(
    `确认反审核收款单 ${row.no}？将释放核销额度并重算被核销单据已收金额。`,
    '反审核确认',
    { type: 'warning' },
  )
  await updateFinanceReceiptStatus({ id: row.id, status: 10 })
  ElMessage.success('已反审核')
  tableRef.value?.reload?.()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除收款单 ${row.no}？仅未审核单可删。`, '删除确认', { type: 'warning' })
  await deleteFinanceReceipt(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}

// ---------- 详情 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)
async function openDetail(id: number) {
  detail.value = (await getFinanceReceipt(id)) as any
  detailVisible.value = true
}

onMounted(async () => {
  customerList.value = ((await pageCustomers({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  accountList.value = ((await pageFinanceAccounts({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  const users = await listSimpleUsers()
  userList.value = (users as any) || []
  // 挂单选择只出已审核单据（spec §4.7：明细只能挂已审核销售出库/退货）。
  approvedSaleOuts.value = ((await pageSaleOuts({ pageNo: 1, pageSize: 200, status: 20 })) as any)?.list || []
  approvedSaleReturns.value = ((await pageSaleReturns({ pageNo: 1, pageSize: 200, status: 20 })) as any)?.list || []
})
</script>

<style scoped>
.doc-items-editor {
  margin-top: 4px;
}
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  font-weight: 600;
}
.detail-items {
  margin-top: 12px;
}
</style>
