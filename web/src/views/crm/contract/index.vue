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
      perm-prefix="crm:contract"
      title="合同"
      :action-width="220"
      dialog-width="900px"
      @row-click="openDetail"
    >
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-amount="{ row }">{{ row.amount ?? '0.00' }}</template>
      <template #cell-signDate="{ row }">{{ formatDate(row.signDate) }}</template>
      <template #cell-ownerUserName="{ row }">
        <span v-if="row.ownerUserName">{{ row.ownerUserName }}（{{ row.ownerDeptName || '-' }}）</span>
        <span v-else>-</span>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click.stop="openDetail(row)">详情</el-button>
        <el-button
          v-if="transitionsFor(row).length"
          link
          type="warning"
          @click.stop="openStatus(row)"
        >
          状态
        </el-button>
      </template>

      <!-- 新建：客户必选，来源商机可空（选了则须与客户一致）；编辑：客户/商机不可改。 -->
      <template #form-customerId="{ form }">
        <el-select
          v-model="form.customerId"
          filterable
          style="width: 100%"
          placeholder="请选择所属客户"
          @change="loadOpportunities"
        >
          <el-option v-for="c in customers" :key="c.id" :label="c.name" :value="c.id" />
        </el-select>
      </template>
      <template #form-customerName="{ form }">
        <span>{{ form.customerName || '-' }}</span>
      </template>
      <template #form-opportunityId="{ form }">
        <el-select
          v-model="form.opportunityId"
          filterable
          clearable
          style="width: 100%"
          placeholder="可不选；选择后客户须与商机一致"
        >
          <el-option v-for="o in opportunities" :key="o.id" :label="o.name" :value="o.id" />
        </el-select>
      </template>
      <template #form-opportunityName="{ form }">
        <span>{{ form.opportunityName || '-' }}</span>
      </template>
      <!-- 金额：有行项目时由后端按 SUM(行项目金额) 计算并忽略手填；已签合同金额锁定。 -->
      <template #form-amount="{ form }">
        <el-input
          v-if="!isSigned(form)"
          v-model="form.amount"
          clearable
          placeholder="0.00（元；有行项目时忽略，可留空）"
        />
        <span v-else>{{ form.amount ?? '0.00' }} 元（已签锁定）</span>
      </template>
      <template #form-signDate="{ form }">
        <el-date-picker
          v-model="form.signDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择签订日期"
        />
      </template>
      <template #form-startDate="{ form }">
        <el-date-picker
          v-model="form.startDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择生效日期"
        />
      </template>
      <template #form-endDate="{ form }">
        <el-date-picker
          v-model="form.endDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择失效日期"
        />
      </template>
      <!-- 行项目：已签合同锁定（后端拒绝修改行项目），其余状态可增删行。
           金额与合计本地即时展示；落库金额由后端按 数量×单价 计算。 -->
      <template #form-products="{ form }">
        <el-table
          v-if="!isSigned(form)"
          :data="form.products"
          border
          size="small"
          class="line-table"
        >
          <el-table-column label="产品" min-width="180">
            <template #default="{ $index }">
              <el-select
                v-model="form.products[$index].productId"
                filterable
                placeholder="请选择产品"
                style="width: 100%"
              >
                <el-option
                  v-for="p in products"
                  :key="p.id"
                  :label="`${p.name}（${p.price}）`"
                  :value="p.id"
                />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="数量" width="130">
            <template #default="{ $index }">
              <el-input-number
                v-model="form.products[$index].quantity"
                :min="1"
                :step="1"
                :precision="0"
                controls-position="right"
                style="width: 100%"
              />
            </template>
          </el-table-column>
          <el-table-column label="单价（元）" width="150">
            <template #default="{ $index }">
              <el-input v-model="form.products[$index].price" clearable placeholder="0.00" />
            </template>
          </el-table-column>
          <el-table-column label="金额（元）" width="120">
            <template #default="{ $index }">
              <span>{{ lineAmount(form.products[$index]) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="70">
            <template #default="{ $index }">
              <el-button link type="danger" @click="form.products.splice($index, 1)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <template v-else>
          <el-table v-if="form.products?.length" :data="form.products" border size="small">
            <el-table-column prop="productName" label="产品" min-width="160" />
            <el-table-column prop="quantity" label="数量" width="90" />
            <el-table-column prop="price" label="单价（元）" width="120" />
            <el-table-column prop="amount" label="金额（元）" width="120" />
          </el-table>
          <el-empty v-else description="暂无行项目" :image-size="60" />
        </template>
        <div v-if="!isSigned(form)" class="line-footer">
          <el-button size="small" @click="addLine(form)">添加行</el-button>
          <span class="line-total">合计：{{ formTotal(form.products) }}</span>
        </div>
      </template>
    </ProTable>

    <!-- 合同详情抽屉：基本信息 + 产品行项目 + 跟进记录 tab（跟进可见性跟随合同负责人）。 -->
    <el-drawer v-model="detailVisible" :title="detail?.name || '合同详情'" size="900px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="合同编号">{{ detail.contractNo }}</el-descriptions-item>
            <el-descriptions-item label="合同名称">{{ detail.name }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="statusTagType(detail.status)" size="small">{{ statusLabel(detail.status) }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="所属客户">{{ detail.customerName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="来源商机">{{ detail.opportunityName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="负责人">
              {{ detail.ownerUserName || '-' }}{{ detail.ownerDeptName ? `（${detail.ownerDeptName}）` : '' }}
            </el-descriptions-item>
            <el-descriptions-item label="合同金额">{{ detail.amount ?? '0.00' }} 元</el-descriptions-item>
            <el-descriptions-item label="行项目合计">{{ detail.totalAmount ?? '0.00' }} 元</el-descriptions-item>
            <el-descriptions-item label="签订日期">{{ formatDate(detail.signDate) }}</el-descriptions-item>
            <el-descriptions-item label="生效日期">{{ formatDate(detail.startDate) }}</el-descriptions-item>
            <el-descriptions-item label="失效日期">{{ formatDate(detail.endDate) }}</el-descriptions-item>
            <el-descriptions-item v-if="detail.rejectReason" label="驳回原因">
              {{ detail.rejectReason }}
            </el-descriptions-item>
            <el-descriptions-item label="备注">{{ detail.remark || '-' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
            <el-descriptions-item label="更新时间">{{ formatTime(detail.updateTime) }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>
        <el-tab-pane label="产品行项目" name="products">
          <el-table v-if="detail.products?.length" :data="detail.products" border size="small">
            <el-table-column prop="productName" label="产品" min-width="160" />
            <el-table-column prop="quantity" label="数量" width="90" />
            <el-table-column prop="price" label="单价（元）" width="120" />
            <el-table-column prop="amount" label="金额（元）" width="120" />
          </el-table>
          <el-empty v-else description="暂无行项目" :image-size="60" />
          <div v-if="detail.products?.length" class="line-footer">
            <span class="line-total">合计：{{ detail.totalAmount ?? '0.00' }} 元</span>
          </div>
        </el-tab-pane>
        <el-tab-pane label="跟进记录" name="follow-ups">
          <ProTable
            :columns="followUpColumns"
            :form-items="followUpFormItems"
            :search-items="followUpSearchItems"
            :api="followUpApi"
            :extra-query="followUpExtraQuery"
            :default-form="followUpDefaultForm"
            :before-submit="injectContractId"
            perm-prefix="crm:follow-up"
            title="跟进记录"
          >
            <template #cell-type="{ row }">
              <el-tag size="small" :type="followUpTypeTag(row.type)">{{ followUpTypeLabel(row.type) }}</el-tag>
            </template>
            <template #cell-nextFollowTime="{ row }">
              <span>{{ row.nextFollowTime ? formatTime(row.nextFollowTime) : '-' }}</span>
            </template>
          </ProTable>
        </el-tab-pane>
      </el-tabs>
      <template #footer>
        <el-button @click="detailVisible = false">关闭</el-button>
        <el-button
          v-if="detail && transitionsFor(detail).length"
          type="primary"
          @click="openStatus(detail)"
        >
          状态流转
        </el-button>
      </template>
    </el-drawer>

    <!-- 状态流转对话框：草稿→待审（提交）、待审→已签（审批通过）/草稿（驳回）、
         任意非作废→作废（终态）。审批通过与驳回须 crm:contract:approve。 -->
    <el-dialog v-model="statusVisible" title="合同状态流转" width="460px" destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="合同">
          <span>{{ statusRow?.name }}</span>
        </el-form-item>
        <el-form-item label="当前状态">
          <el-tag :type="statusTagType(statusRow?.status)" size="small">
            {{ statusLabel(statusRow?.status) }}
          </el-tag>
        </el-form-item>
        <el-form-item label="目标状态">
          <el-radio-group v-model="statusTo">
            <el-radio v-for="opt in transitionOptions" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="statusTo === 1" label="驳回原因">
          <el-input
            v-model="statusReason"
            type="textarea"
            :rows="2"
            maxlength="500"
            placeholder="可留空"
          />
        </el-form-item>
        <el-alert
          v-if="statusTo === 4"
          type="warning"
          :closable="false"
          title="作废后合同不可再流转、不可编辑（终态）"
        />
      </el-form>
      <template #footer>
        <el-button @click="statusVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!statusTo" @click="confirmStatus">
          确定
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime, dictLabel } from '@/utils/format'
import {
  pageContracts,
  getContract,
  createContract,
  updateContract,
  deleteContract,
  changeContractStatus,
  pageCustomers,
  pageOpportunities,
  pageProducts,
  pageFollowUps,
  getFollowUp,
  createFollowUp,
  updateFollowUp,
  deleteFollowUp,
} from '@/api/crm'
import { useUserStore } from '@/stores/user'

// 合同状态对齐规格（docs/crm-mvp-spec.md §4.4）：
// 1 草稿 → 2 待审（提交）→ 3 已签（审批通过）；待审可驳回回草稿；4 作废（终态）。
const statusOptions = [
  { label: '草稿', value: 1 },
  { label: '待审', value: 2 },
  { label: '已签', value: 3 },
  { label: '作废', value: 4 },
]

// 跟进类型对齐芋道字典语义（issue #12 同款）
const followUpTypeOptions = [
  { label: '电话', value: 1 },
  { label: '拜访', value: 2 },
  { label: '微信', value: 3 },
  { label: '邮件', value: 4 },
  { label: '其他', value: 5 },
]

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

function isSigned(form: any) {
  return form?.status === 3
}

function followUpTypeLabel(type: number) {
  return dictLabel(followUpTypeOptions, type)
}

function followUpTypeTag(type: number) {
  if (type === 1) return 'primary'
  if (type === 2) return 'success'
  return 'info'
}

// ---------- 行项目本地金额计算（仅展示；落库金额由后端 数量×单价 计算） ----------
function parseMoney(text: any): number {
  if (typeof text !== 'string' || text.trim() === '') return 0
  const n = Number(text)
  return Number.isFinite(n) ? n : 0
}

function lineAmount(line: any): string {
  if (!line?.quantity || !line?.price) return '0.00'
  return (line.quantity * parseMoney(line.price)).toFixed(2)
}

function formTotal(lines: any[]): string {
  if (!lines?.length) return '0.00'
  return lines.reduce((sum, l) => sum + parseMoney(lineAmount(l)), 0).toFixed(2)
}

function addLine(form: any) {
  if (!Array.isArray(form.products)) form.products = []
  form.products.push({ productId: undefined, quantity: 1, price: '0.00' })
}

// ---------- 状态流转选项：按当前状态 + 权限码过滤 ----------
// 审批通过与驳回是审批人动作（后端 ApprovalService 再校验 crm:contract:approve）；
// 提交与作废只挂 crm:contract:update。
const userStore = useUserStore()

function transitionsFor(row: any) {
  const options: { label: string; value: number; perm: string }[] = []
  if (row?.status === 1) {
    options.push({ label: '提交审批', value: 2, perm: 'crm:contract:update' })
    options.push({ label: '作废', value: 4, perm: 'crm:contract:update' })
  } else if (row?.status === 2) {
    options.push({ label: '审批通过', value: 3, perm: 'crm:contract:approve' })
    options.push({ label: '驳回回草稿', value: 1, perm: 'crm:contract:approve' })
    options.push({ label: '作废', value: 4, perm: 'crm:contract:update' })
  } else if (row?.status === 3) {
    options.push({ label: '作废', value: 4, perm: 'crm:contract:update' })
  }
  return options.filter((opt) => userStore.hasPerm(opt.perm))
}

const tableRef = ref()
const customers = ref<any[]>([])
const products = ref<any[]>([])
const opportunities = ref<any[]>([])

const searchItems = [
  { prop: 'name', label: '合同名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const columns = [
  { prop: 'contractNo', label: '合同编号', width: 150 },
  { prop: 'name', label: '合同名称', minWidth: 160 },
  { prop: 'customerName', label: '所属客户', minWidth: 140 },
  { prop: 'amount', label: '合同金额（元）', width: 130, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'signDate', label: '签订日期', width: 120, slot: true },
  { prop: 'ownerUserName', label: '负责人', minWidth: 150, slot: true },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'customerId', label: '所属客户', slot: true, requiredOn: 'create', visibleOn: 'create' },
  { prop: 'customerName', label: '所属客户', slot: true, visibleOn: 'update' },
  { prop: 'opportunityId', label: '来源商机', slot: true, visibleOn: 'create' },
  { prop: 'opportunityName', label: '来源商机', slot: true, visibleOn: 'update' },
  { prop: 'name', label: '合同名称', required: true },
  { prop: 'amount', label: '合同金额', slot: true },
  { prop: 'signDate', label: '签订日期', slot: true },
  { prop: 'startDate', label: '生效日期', slot: true },
  { prop: 'endDate', label: '失效日期', slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
  { prop: 'products', label: '产品行项目', slot: true },
]

const defaultForm = { products: [] }

const api = {
  page: pageContracts,
  detail: getContract,
  create: createContract,
  update: updateContract,
  remove: deleteContract,
}

// 提交前规整：金额空串省略（后端 money 只收十进制字符串或省略字段）；
// 已签合同行项目与金额锁定（后端拒绝 products、忽略 amount），编辑只提交元数据；
// 行项目裁剪为最小结构并兜底数量/单价（编辑回显带出的展示字段一并剔除）。
function normalizeSubmit(payload: any) {
  const out: any = { ...payload }
  if (out.amount === '' || out.amount == null) delete out.amount
  if (out.status === 3) {
    delete out.products
    delete out.amount
  } else if (Array.isArray(out.products)) {
    out.products = out.products
      .filter((p: any) => p.productId)
      .map((p: any) => ({
        productId: p.productId,
        quantity: p.quantity && p.quantity > 0 ? p.quantity : 1,
        price: p.price === '' || p.price == null ? '0.00' : p.price,
      }))
  }
  return out
}

// 来源商机下拉：随所选客户加载（空客户无候选；后端仍会校验商机与客户一致）。
async function loadOpportunities(customerId?: number) {
  if (!customerId) {
    opportunities.value = []
    return
  }
  const page = await pageOpportunities({ customerId, pageNo: 1, pageSize: 200 })
  opportunities.value = page.list || []
}

const detail = ref<any>(null)
const detailVisible = ref(false)
const detailTab = ref('info')

async function openDetail(row: any) {
  detail.value = await getContract(row.id)
  detailTab.value = 'info'
  detailVisible.value = true
}

// ---------- 状态流转（唯一状态入口 PUT /crm/contract/status） ----------
const statusVisible = ref(false)
const statusRow = ref<any>(null)
const statusTo = ref<number>()
const statusReason = ref('')
const transitionOptions = computed(() => transitionsFor(statusRow.value))

function openStatus(row: any) {
  statusRow.value = row
  statusTo.value = undefined
  statusReason.value = ''
  statusVisible.value = true
}

async function confirmStatus() {
  if (!statusTo.value || !statusRow.value) return
  await changeContractStatus(statusRow.value.id, statusTo.value, statusReason.value)
  ElMessage.success(`已变更为「${statusLabel(statusTo.value)}」`)
  statusVisible.value = false
  tableRef.value?.reload()
  if (detailVisible.value && detail.value?.id === statusRow.value.id) {
    detail.value = await getContract(statusRow.value.id)
  }
}

// ---------- 跟进记录 tab：列表范围锁定当前合同，可见性跟随合同负责人 ----------
const followUpColumns = [
  { prop: 'type', label: '类型', width: 90, slot: true },
  { prop: 'content', label: '内容', minWidth: 220 },
  { prop: 'nextFollowTime', label: '下次跟进时间', minWidth: 170, slot: true },
  {
    prop: 'createTime',
    label: '跟进时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const followUpSearchItems = [
  { prop: 'type', label: '类型', type: 'select', options: followUpTypeOptions },
]

const followUpFormItems = [
  { prop: 'type', label: '类型', type: 'select', options: followUpTypeOptions },
  { prop: 'content', label: '内容', required: true, type: 'textarea' },
  { prop: 'nextFollowTime', label: '下次跟进时间', type: 'datetime' },
]

const followUpDefaultForm = { type: 5 }

const followUpExtraQuery = computed(() => (detail.value ? { contractId: detail.value.id } : {}))

// 新建跟进时注入所属合同（customerId/opportunityId/contractId 三选一）。
function injectContractId(payload: any, mode: 'create' | 'update') {
  if (mode === 'create' && detail.value) {
    return { ...payload, contractId: detail.value.id }
  }
  return payload
}

const followUpApi = {
  page: pageFollowUps,
  detail: getFollowUp,
  create: createFollowUp,
  update: updateFollowUp,
  remove: deleteFollowUp,
}

onMounted(async () => {
  // 客户与产品下拉：租户内全量（v1 规模可控，取前 200 条）。
  const [custPage, prodPage] = await Promise.all([
    pageCustomers({ pageNo: 1, pageSize: 200 }),
    pageProducts({ pageNo: 1, pageSize: 200 }),
  ])
  customers.value = custPage.list || []
  products.value = prodPage.list || []
})
</script>

<style scoped>
.line-table {
  width: 100%;
}
.line-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
}
.line-total {
  font-weight: 600;
  color: var(--el-color-danger);
}
</style>
