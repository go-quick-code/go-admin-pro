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
      perm-prefix="crm:opportunity"
      title="商机"
      :action-width="220"
      dialog-width="860px"
      @row-click="openDetail"
    >
      <template #cell-stage="{ row }">
        <el-tag :type="stageTagType(row.stage)" size="small">{{ stageLabel(row.stage) }}</el-tag>
      </template>
      <template #cell-expectedAmount="{ row }">{{ row.expectedAmount ?? '-' }}</template>
      <template #cell-ownerUserName="{ row }">
        <span v-if="row.ownerUserName">{{ row.ownerUserName }}（{{ row.ownerDeptName || '-' }}）</span>
        <span v-else>-</span>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click.stop="openDetail(row)">详情</el-button>
        <el-button
          v-if="userStore.hasPerm('crm:opportunity:stage')"
          link
          type="success"
          @click.stop="openStage(row)"
        >
          阶段
        </el-button>
      </template>
      <template #form-customerId="{ form }">
        <el-select
          v-model="form.customerId"
          filterable
          style="width: 100%"
          placeholder="请选择所属客户"
        >
          <el-option v-for="c in customers" :key="c.id" :label="c.name" :value="c.id" />
        </el-select>
      </template>
      <template #form-expectedAmount="{ form }">
        <el-input v-model="form.expectedAmount" clearable placeholder="0.00（元，可留空）" />
      </template>
      <template #form-expectedCloseDate="{ form }">
        <el-date-picker
          v-model="form.expectedCloseDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择预计成交日期"
        />
      </template>
      <!-- 行项目编辑表：增删行、选产品、填数量单价，金额与合计本地即时计算
          （提交不含金额，由后端按 数量×单价 计算落库，见 api/crm.ts 注释）。 -->
      <template #form-products="{ form }">
        <el-table :data="form.products" border size="small" class="line-table">
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
        <div class="line-footer">
          <el-button size="small" @click="addLine(form)">添加行</el-button>
          <span class="line-total">合计：{{ formTotal(form.products) }}</span>
        </div>
      </template>
    </ProTable>

    <!-- 商机详情抽屉：基本信息（含阶段推进）+ 产品行项目 + 跟进记录 tab
        （跟进可见性跟随商机负责人，issue #15 对 #12 客户维度 tab 的镜像）。 -->
    <el-drawer v-model="detailVisible" :title="detail?.name || '商机详情'" size="900px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="商机编号">{{ detail.opportunityNo }}</el-descriptions-item>
            <el-descriptions-item label="商机名称">{{ detail.name }}</el-descriptions-item>
            <el-descriptions-item label="阶段">
              <el-tag :type="stageTagType(detail.stage)" size="small">{{ stageLabel(detail.stage) }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="所属客户">{{ detail.customerName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="负责人">
              {{ detail.ownerUserName || '-' }}{{ detail.ownerDeptName ? `（${detail.ownerDeptName}）` : '' }}
            </el-descriptions-item>
            <el-descriptions-item label="预计金额">{{ detail.expectedAmount }} 元</el-descriptions-item>
            <el-descriptions-item label="行项目合计">{{ detail.totalAmount ?? '0.00' }} 元</el-descriptions-item>
            <el-descriptions-item label="预计成交日期">{{ detail.expectedCloseDate || '-' }}</el-descriptions-item>
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
            :before-submit="injectOpportunityId"
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
    </el-drawer>

    <!-- 阶段推进对话框：1–4 互相可推，5 赢单 / 6 输单终态（后端终态锁定 400）。 -->
    <el-dialog v-model="stageVisible" title="推进阶段" width="420px" destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="商机">
          <span>{{ stageRow?.name }}</span>
        </el-form-item>
        <el-form-item label="当前阶段">
          <el-tag :type="stageTagType(stageRow?.stage)" size="small">{{ stageLabel(stageRow?.stage) }}</el-tag>
        </el-form-item>
        <el-form-item v-if="!isTerminalStage(stageRow?.stage)" label="目标阶段">
          <el-radio-group v-model="stageTo">
            <el-radio
              v-for="opt in stageOptions.filter((s) => !isTerminalStage(s.value))"
              :key="opt.value"
              :value="opt.value"
            >
              {{ opt.label }}
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-alert
          v-else
          type="warning"
          :closable="false"
          :title="`商机已${stageLabel(stageRow?.stage)}（终态），阶段不可再变更`"
        />
      </el-form>
      <template #footer>
        <el-button @click="stageVisible = false">关闭</el-button>
        <el-button
          v-if="!isTerminalStage(stageRow?.stage)"
          type="primary"
          :disabled="!stageTo || stageTo === stageRow?.stage"
          @click="confirmStage"
        >
          确定推进
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
  pageOpportunities,
  getOpportunity,
  createOpportunity,
  updateOpportunity,
  deleteOpportunity,
  advanceOpportunityStage,
  pageCustomers,
  pageProducts,
  pageFollowUps,
  getFollowUp,
  createFollowUp,
  updateFollowUp,
  deleteFollowUp,
} from '@/api/crm'
import { useUserStore } from '@/stores/user'

// 商机阶段对齐规格（docs/crm-mvp-spec.md §3/§4.4）：
// 1 初步 / 2 需求 / 3 报价 / 4 谈判 为进行中；5 赢单、6 输单为终态。
const stageOptions = [
  { label: '初步', value: 1 },
  { label: '需求', value: 2 },
  { label: '报价', value: 3 },
  { label: '谈判', value: 4 },
  { label: '赢单', value: 5 },
  { label: '输单', value: 6 },
]

// 跟进类型对齐芋道字典语义（issue #12 同款）
const followUpTypeOptions = [
  { label: '电话', value: 1 },
  { label: '拜访', value: 2 },
  { label: '微信', value: 3 },
  { label: '邮件', value: 4 },
  { label: '其他', value: 5 },
]

function stageLabel(stage: number) {
  return dictLabel(stageOptions, stage)
}

function stageTagType(stage?: number) {
  if (stage === 5) return 'success'
  if (stage === 6) return 'danger'
  return 'info'
}

function isTerminalStage(stage?: number) {
  return stage === 5 || stage === 6
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

const tableRef = ref()
const userStore = useUserStore()
const customers = ref<any[]>([])
const products = ref<any[]>([])

const searchItems = [
  { prop: 'name', label: '商机名称' },
  { prop: 'stage', label: '阶段', type: 'select', options: stageOptions },
]

const columns = [
  { prop: 'opportunityNo', label: '商机编号', width: 150 },
  { prop: 'name', label: '商机名称', minWidth: 160 },
  { prop: 'customerName', label: '所属客户', minWidth: 140 },
  { prop: 'stage', label: '阶段', width: 90, slot: true },
  { prop: 'expectedAmount', label: '预计金额（元）', width: 130, slot: true },
  {
    prop: 'expectedCloseDate',
    label: '预计成交日期',
    width: 120,
    formatter: (row: any) => row.expectedCloseDate || '-',
  },
  { prop: 'ownerUserName', label: '负责人', minWidth: 150, slot: true },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'customerId', label: '所属客户', required: true, slot: true },
  { prop: 'name', label: '商机名称', required: true },
  { prop: 'expectedAmount', label: '预计金额', slot: true },
  { prop: 'expectedCloseDate', label: '预计成交日期', slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
  { prop: 'products', label: '产品行项目', slot: true },
]

const defaultForm = { stage: 1, products: [] }

const api = {
  page: pageOpportunities,
  detail: getOpportunity,
  create: createOpportunity,
  update: updateOpportunity,
  remove: deleteOpportunity,
}

// 提交前规整：金额空串省略（后端 money 只收十进制字符串或省略字段）、
// 行项目裁剪为最小结构并兜底数量/单价（编辑回显带出的展示字段一并剔除）。
// 金额本身仍由后端按 数量×单价 计算，前端不提交。
function normalizeSubmit(payload: any) {
  const out: any = { ...payload }
  if (out.expectedAmount === '' || out.expectedAmount == null) delete out.expectedAmount
  if (Array.isArray(out.products)) {
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

const detail = ref<any>(null)
const detailVisible = ref(false)
const detailTab = ref('info')

async function openDetail(row: any) {
  detail.value = await getOpportunity(row.id)
  detailTab.value = 'info'
  detailVisible.value = true
}

// ---------- 阶段推进（issue #15）：独立按钮码 crm:opportunity:stage ----------
const stageVisible = ref(false)
const stageRow = ref<any>(null)
const stageTo = ref<number>()

function openStage(row: any) {
  stageRow.value = row
  stageTo.value = undefined
  stageVisible.value = true
}

async function confirmStage() {
  if (!stageTo.value || !stageRow.value) return
  await advanceOpportunityStage(stageRow.value.id, stageTo.value)
  ElMessage.success(`已推进到「${stageLabel(stageTo.value)}」`)
  stageVisible.value = false
  tableRef.value?.reload()
}

// ---------- 跟进记录 tab：列表范围锁定当前商机，可见性跟随商机负责人 ----------
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

const followUpExtraQuery = computed(() => (detail.value ? { opportunityId: detail.value.id } : {}))

// 新建跟进时注入所属商机（与 customerId 二选一，更新接口不换所属对象）。
function injectOpportunityId(payload: any, mode: 'create' | 'update') {
  if (mode === 'create' && detail.value) {
    return { ...payload, opportunityId: detail.value.id }
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
