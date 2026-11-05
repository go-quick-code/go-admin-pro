<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="crm:customer"
      title="客户"
      :action-width="200"
      @row-click="openDetail"
    >
      <template #toolbar>
        <!-- Excel（issue #20）：导入/模板挂 crm:customer:import，导出挂 crm:customer:export；
             导出带上当前列表筛选，后端再套数据范围谓词。 -->
        <ExcelActions
          title="客户"
          import-perm="crm:customer:import"
          export-perm="crm:customer:export"
          :import-action="importCustomers"
          :template-action="downloadCustomerImportTemplate"
          :export-action="exportCustomers"
          :export-params="customerExportParams"
          @success="reloadCustomers"
        />
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status, customerStatusTagTypes)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-ownerUserName="{ row }">
        <span v-if="row.ownerUserName">{{ row.ownerUserName }}（{{ row.ownerDeptName || '-' }}）</span>
        <el-tag v-else type="warning" size="small">公海</el-tag>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click.stop="openDetail(row)">详情</el-button>
        <!-- 公海纵切（issue #13）：负责人本人或持对应权限码可释放/转移。 -->
        <el-button
          v-if="userStore.hasPerm('crm:customer:release')"
          link
          type="warning"
          @click.stop="handleRelease(row)"
        >
          释放
        </el-button>
        <el-button
          v-if="userStore.hasPerm('crm:customer:transfer')"
          link
          type="primary"
          @click.stop="openTransfer(row)"
        >
          转移
        </el-button>
      </template>
      <template #form-ownerUserId="{ form }">
        <el-select v-model="form.ownerUserId" clearable filterable style="width: 100%" placeholder="缺省为当前登录用户">
          <el-option v-for="u in users" :key="u.id" :label="u.nickname" :value="u.id" />
        </el-select>
      </template>
    </ProTable>

    <!-- 客户详情抽屉：基本信息 + 联系人 + 负责人历史 + 跟进记录 tab
        （issue #9 tab 结构；issue #13 融入释放/转移入口与历史 tab；issue #12 加跟进记录 tab） -->
    <el-drawer v-model="detailVisible" :title="detail?.name || '客户详情'" size="900px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="客户编号">{{ detail.customerNo }}</el-descriptions-item>
            <el-descriptions-item label="客户名称">{{ detail.name }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="statusTagType(detail.status, customerStatusTagTypes)" size="small">{{ statusLabel(detail.status) }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="负责人">
              {{ detail.ownerUserName || '公海' }}{{ detail.ownerDeptName ? `（${detail.ownerDeptName}）` : '' }}
            </el-descriptions-item>
            <el-descriptions-item label="行业">{{ detail.industry || '-' }}</el-descriptions-item>
            <el-descriptions-item label="级别">{{ detail.level || '-' }}</el-descriptions-item>
            <el-descriptions-item label="来源">{{ detail.source || '-' }}</el-descriptions-item>
            <el-descriptions-item label="联系电话">{{ detail.phone || '-' }}</el-descriptions-item>
            <el-descriptions-item label="网址">{{ detail.website || '-' }}</el-descriptions-item>
            <el-descriptions-item label="地址">{{ detail.address || '-' }}</el-descriptions-item>
            <el-descriptions-item label="备注">{{ detail.remark || '-' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
            <el-descriptions-item label="更新时间">{{ formatTime(detail.updateTime) }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>
        <el-tab-pane label="联系人" name="contacts">
          <ProTable
            ref="contactTableRef"
            :columns="contactColumns"
            :form-items="contactFormItems"
            :search-items="contactSearchItems"
            :api="contactApi"
            :extra-query="contactExtraQuery"
            :default-form="contactDefaultForm"
            :before-submit="injectCustomerId"
            perm-prefix="crm:contact"
            title="联系人"
          >
            <template #toolbar>
              <!-- 联系人导入（issue #20）：用客户编号关联所属客户（须对当前用户可见）。 -->
              <ExcelActions
                title="联系人"
                import-perm="crm:contact:import"
                :import-action="importContacts"
                :template-action="downloadContactImportTemplate"
                @success="reloadContacts"
              />
            </template>
            <template #cell-gender="{ row }">{{ genderLabel(row.gender) }}</template>
          </ProTable>
        </el-tab-pane>
        <el-tab-pane label="商机" name="opportunities">
          <!-- 商机 tab（issue #15）：列表范围锁定当前客户，可见性跟随商机的
              数据范围（owner-scoped，与客户列表可能不同，范围外商机不出现）。 -->
          <ProTable
            :columns="opportunityColumns"
            :form-items="opportunityFormItems"
            :search-items="opportunitySearchItems"
            :api="opportunityApi"
            :extra-query="opportunityExtraQuery"
            :default-form="opportunityDefaultForm"
            :before-submit="submitOpportunity"
            perm-prefix="crm:opportunity"
            title="商机"
          >
            <template #cell-stage="{ row }">
              <el-tag :type="stageTagType(row.stage)" size="small">{{ stageLabel(row.stage) }}</el-tag>
            </template>
            <template #form-expectedAmount="{ form }">
              <el-input v-model="form.expectedAmount" clearable placeholder="0.00（元，可留空）" />
            </template>
          </ProTable>
        </el-tab-pane>
        <el-tab-pane label="合同" name="contracts">
          <!-- 合同 tab（issue #16）：列表范围锁定当前客户，可见性跟随合同的数据范围
               （owner-scoped 正体，范围外合同不出现）。 -->
          <ProTable
            :columns="contractColumns"
            :form-items="contractFormItems"
            :search-items="contractSearchItems"
            :api="contractApi"
            :extra-query="contractExtraQuery"
            :default-form="contractDefaultForm"
            :before-submit="submitContract"
            perm-prefix="crm:contract"
            title="合同"
          >
            <template #cell-status="{ row }">
              <el-tag :type="contractStatusTagType(row.status)" size="small">
                {{ contractStatusLabel(row.status) }}
              </el-tag>
            </template>
            <template #form-amount="{ form }">
              <el-input v-model="form.amount" clearable placeholder="0.00（元，可留空）" />
            </template>
          </ProTable>
        </el-tab-pane>
        <el-tab-pane label="负责人历史" name="owner-history">
          <el-timeline v-if="history.length" class="history-list">
            <el-timeline-item
              v-for="h in history"
              :key="h.id"
              :timestamp="formatTime(h.createTime)"
              :type="actionTagType(h.action)"
            >
              <el-tag size="small" :type="actionTagType(h.action)" class="history-action">
                {{ actionLabel(h.action) }}
              </el-tag>
              <span class="history-text">{{ historyText(h) }}</span>
            </el-timeline-item>
          </el-timeline>
          <el-empty v-else description="暂无负责人变更记录" :image-size="60" />
        </el-tab-pane>
        <el-tab-pane label="跟进记录" name="follow-ups">
          <ProTable
            :columns="followUpColumns"
            :form-items="followUpFormItems"
            :search-items="followUpSearchItems"
            :api="followUpApi"
            :extra-query="followUpExtraQuery"
            :default-form="followUpDefaultForm"
            :before-submit="injectCustomerId"
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

    <!-- 转移对话框：选择同租户其他用户作为新负责人（issue #13）。 -->
    <el-dialog v-model="transferVisible" title="转移客户" width="420px" destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="客户">
          <span>{{ transferRow?.name }}</span>
        </el-form-item>
        <el-form-item label="新负责人">
          <el-select v-model="transferTo" filterable style="width: 100%" placeholder="请选择新负责人">
            <el-option
              v-for="u in users.filter((u: any) => u.id !== transferRow?.ownerUserId)"
              :key="u.id"
              :label="u.nickname"
              :value="u.id"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="transferVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!transferTo" @click="confirmTransfer">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime, dictLabel, statusTagType, type StatusTagType } from '@/utils/format'
import {
  pageCustomers,
  getCustomer,
  createCustomer,
  updateCustomer,
  deleteCustomer,
  releaseCustomer,
  transferCustomer,
  pageCustomerOwnerHistory,
  pageContacts,
  getContact,
  createContact,
  updateContact,
  deleteContact,
  importContacts,
  downloadContactImportTemplate,
  importCustomers,
  downloadCustomerImportTemplate,
  exportCustomers,
  pageOpportunities,
  createOpportunity,
  pageContracts,
  createContract,
  pageFollowUps,
  getFollowUp,
  createFollowUp,
  updateFollowUp,
  deleteFollowUp,
} from '@/api/crm'
import { treeDepts, listSimpleUsers } from '@/api/system'
import { useUserStore } from '@/stores/user'

// 客户状态对齐规格：1 潜在 / 2 活跃 / 3 流失（docs/crm-mvp-spec.md §3）
const customerStatusOptions = [
  { label: '潜在', value: 1 },
  { label: '活跃', value: 2 },
  { label: '流失', value: 3 },
]

const customerStatusTagTypes: Record<number, StatusTagType> = {
  2: 'success',
  3: 'danger',
}

// 联系人性别对齐字典 sys_user_sex：0 未知 / 1 男 / 2 女（issue #9）
const contactGenderOptions = [
  { label: '未知', value: 0 },
  { label: '男', value: 1 },
  { label: '女', value: 2 },
]

// 跟进类型对齐芋道字典语义：1 电话 / 2 拜访 / 3 微信 / 4 邮件 / 5 其他（issue #12）
const followUpTypeOptions = [
  { label: '电话', value: 1 },
  { label: '拜访', value: 2 },
  { label: '微信', value: 3 },
  { label: '邮件', value: 4 },
  { label: '其他', value: 5 },
]

// 负责人历史操作类型（issue #13）：create 创建 / claim 认领 / release 释放 / transfer 转移。
const actionOptions = [
  { label: '创建', value: 'create' },
  { label: '认领', value: 'claim' },
  { label: '释放', value: 'release' },
  { label: '转移', value: 'transfer' },
]

function statusLabel(status: number) {
  return dictLabel(customerStatusOptions, status)
}

function genderLabel(gender: number) {
  return dictLabel(contactGenderOptions, gender)
}

function followUpTypeLabel(type: number) {
  return dictLabel(followUpTypeOptions, type)
}

function followUpTypeTag(type: number) {
  if (type === 1) return 'primary'
  if (type === 2) return 'success'
  if (type === 3) return 'warning'
  if (type === 4) return 'info'
  return 'info'
}

function actionLabel(action: string) {
  return dictLabel(actionOptions, action)
}

function actionTagType(action: string) {
  if (action === 'claim') return 'success'
  if (action === 'release') return 'warning'
  if (action === 'create') return 'primary'
  return 'info'
}

function historyText(h: any) {
  const from = h.fromOwnerUserName || '公海'
  const to = h.toOwnerUserName || '公海'
  if (h.action === 'create') return `${to} 成为首任负责人`
  return `${from} → ${to}`
}

const tableRef = ref()
const contactTableRef = ref()

function reloadCustomers() {
  tableRef.value?.reload()
}

function reloadContacts() {
  contactTableRef.value?.reload()
}

// 客户导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function customerExportParams() {
  const q = tableRef.value?.query || {}
  return {
    name: q.name,
    status: q.status,
    ownerUserId: q.ownerUserId,
    ownerDeptId: q.ownerDeptId,
  }
}
const userStore = useUserStore()
const users = ref<any[]>([])
const deptOptions = ref<{ label: string; value: number }[]>([])

// searchItems 用 computed：负责人/部门选项在 onMounted 异步加载后刷新下拉。
const searchItems = computed(() => [
  { prop: 'name', label: '客户名称' },
  { prop: 'status', label: '状态', type: 'select', options: customerStatusOptions },
  {
    prop: 'ownerUserId',
    label: '负责人',
    type: 'select',
    options: users.value.map((u: any) => ({ label: u.nickname, value: u.id })),
  },
  { prop: 'ownerDeptId', label: '部门', type: 'select', options: deptOptions.value },
])

const columns = [
  { prop: 'customerNo', label: '客户编号', width: 150 },
  { prop: 'name', label: '客户名称', minWidth: 160 },
  { prop: 'industry', label: '行业', width: 110 },
  { prop: 'level', label: '级别', width: 90 },
  { prop: 'source', label: '来源', width: 110 },
  { prop: 'phone', label: '联系电话', width: 130 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'ownerUserName', label: '负责人', minWidth: 150, slot: true },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'name', label: '客户名称', required: true },
  { prop: 'industry', label: '行业' },
  { prop: 'level', label: '级别' },
  { prop: 'source', label: '来源' },
  { prop: 'phone', label: '联系电话' },
  { prop: 'website', label: '网址' },
  { prop: 'address', label: '地址' },
  { prop: 'status', label: '状态', type: 'select', options: customerStatusOptions },
  { prop: 'ownerUserId', label: '负责人', type: 'select', slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = { status: 1, ownerUserId: 0 }

const api = {
  page: pageCustomers,
  detail: getCustomer,
  create: createCustomer,
  update: updateCustomer,
  remove: deleteCustomer,
}

const detail = ref<any>(null)
const detailVisible = ref(false)
const detailTab = ref('info')
const history = ref<any[]>([])

async function openDetail(row: any) {
  detail.value = await getCustomer(row.id)
  detailTab.value = 'info'
  detailVisible.value = true
  // 负责人历史（issue #13）：创建/认领/释放/转移全记录，新→旧。
  history.value = await pageCustomerOwnerHistory(row.id, { pageNo: 1, pageSize: 20 })
}

// ---------- 公海操作（issue #13）：释放到公海 / 转移给同租户其他用户 ----------

// 释放到公海：填写释放原因（可空），确认后调用释放接口。
async function handleRelease(row: any) {
  const reason = await ElMessageBox.prompt(
    `将「${row.name}」释放到公海？释放后所有人可通过公海认领。`,
    '释放客户',
    {
      type: 'warning',
      inputPlaceholder: '释放原因（可空，最多 255 字）',
      inputValidator: (v: string) => !v || v.length <= 255 || '释放原因最多 255 字',
    },
  )
    .then(({ value }) => value || '')
    .catch(() => null)
  if (reason === null) return
  await releaseCustomer(row.id, reason)
  ElMessage.success('已释放到公海')
  tableRef.value?.reload()
}

const transferVisible = ref(false)
const transferRow = ref<any>(null)
const transferTo = ref<number>()

function openTransfer(row: any) {
  transferRow.value = row
  transferTo.value = undefined
  transferVisible.value = true
}

async function confirmTransfer() {
  if (!transferTo.value || !transferRow.value) return
  await transferCustomer(transferRow.value.id, transferTo.value)
  ElMessage.success('转移成功')
  transferVisible.value = false
  tableRef.value?.reload()
}

// ---------- 联系人 tab（issue #9）：列表范围锁定当前客户，可见性跟随客户 ----------
const contactColumns = [
  { prop: 'name', label: '姓名', minWidth: 110 },
  { prop: 'position', label: '职位', minWidth: 110 },
  { prop: 'mobile', label: '手机号', width: 130 },
  { prop: 'email', label: '邮箱', minWidth: 160 },
  { prop: 'gender', label: '性别', width: 70, slot: true },
  { prop: 'decisionRole', label: '决策角色', width: 100 },
  { prop: 'remark', label: '备注', minWidth: 120 },
]

const contactSearchItems = [
  { prop: 'name', label: '姓名' },
  { prop: 'mobile', label: '手机号' },
]

const contactFormItems = [
  { prop: 'name', label: '姓名', required: true },
  { prop: 'position', label: '职位' },
  { prop: 'mobile', label: '手机号' },
  { prop: 'email', label: '邮箱' },
  { prop: 'gender', label: '性别', type: 'select', options: contactGenderOptions },
  { prop: 'decisionRole', label: '决策角色' },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const contactDefaultForm = { gender: 0 }

const contactExtraQuery = computed(() => (detail.value ? { customerId: detail.value.id } : {}))

// 新建联系人/跟进时注入所属客户（所属客户创建后不可漂移，更新接口不接受 customerId）。
function injectCustomerId(payload: any, mode: 'create' | 'update') {
  if (mode === 'create' && detail.value) {
    return { ...payload, customerId: detail.value.id }
  }
  return payload
}

const contactApi = {
  page: pageContacts,
  detail: getContact,
  create: createContact,
  update: updateContact,
  remove: deleteContact,
}

// ---------- 商机 tab（issue #15）：列表范围锁定当前客户；商机的可见性跟随其
// 负责人（owner-scoped 正体），因此本 tab 只显示当前用户可见的商机 ----------
// 商机阶段对齐规格：1 初步 / 2 需求 / 3 报价 / 4 谈判 进行中；5 赢单 / 6 输单终态。
const stageOptions = [
  { label: '初步', value: 1 },
  { label: '需求', value: 2 },
  { label: '报价', value: 3 },
  { label: '谈判', value: 4 },
  { label: '赢单', value: 5 },
  { label: '输单', value: 6 },
]

function stageLabel(stage: number) {
  return dictLabel(stageOptions, stage)
}

function stageTagType(stage: number) {
  if (stage === 5) return 'success'
  if (stage === 6) return 'danger'
  return 'info'
}

const opportunityColumns = [
  { prop: 'opportunityNo', label: '商机编号', width: 150 },
  { prop: 'name', label: '商机名称', minWidth: 150 },
  { prop: 'stage', label: '阶段', width: 90, slot: true },
  { prop: 'expectedAmount', label: '预计金额（元）', width: 130 },
  { prop: 'ownerUserName', label: '负责人', minWidth: 130 },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const opportunitySearchItems = [
  { prop: 'name', label: '商机名称' },
  { prop: 'stage', label: '阶段', type: 'select', options: stageOptions },
]

// tab 内只做轻量新建（名称/预计金额/备注）；行项目与阶段推进在商机管理页操作。
const opportunityFormItems = [
  { prop: 'name', label: '商机名称', required: true },
  { prop: 'expectedAmount', label: '预计金额', slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const opportunityDefaultForm = {}

const opportunityExtraQuery = computed(() => (detail.value ? { customerId: detail.value.id } : {}))

const opportunityApi = {
  page: pageOpportunities,
  create: createOpportunity,
}

// tab 内新建商机：注入所属客户 + 金额空串省略（后端 money 只收十进制字符串）。
function submitOpportunity(payload: any, mode: 'create' | 'update') {
  if (mode === 'create' && detail.value) {
    payload = { ...payload, customerId: detail.value.id }
  }
  const out = { ...payload }
  if (out.expectedAmount === '' || out.expectedAmount == null) delete out.expectedAmount
  return out
}

// ---------- 合同 tab（issue #16）：列表范围锁定当前客户；合同的可见性跟随其
// 负责人（owner-scoped 正体），因此本 tab 只显示当前用户可见的合同 ----------
// 合同状态对齐规格：1 草稿 / 2 待审 / 3 已签 / 4 作废（终态）。
const contractStatusOptions = [
  { label: '草稿', value: 1 },
  { label: '待审', value: 2 },
  { label: '已签', value: 3 },
  { label: '作废', value: 4 },
]

function contractStatusLabel(status: number) {
  return dictLabel(contractStatusOptions, status)
}

function contractStatusTagType(status: number) {
  if (status === 2) return 'warning'
  if (status === 3) return 'success'
  if (status === 4) return 'danger'
  return 'info'
}

const contractColumns = [
  { prop: 'contractNo', label: '合同编号', width: 150 },
  { prop: 'name', label: '合同名称', minWidth: 150 },
  { prop: 'amount', label: '合同金额（元）', width: 130 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'ownerUserName', label: '负责人', minWidth: 130 },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const contractSearchItems = [
  { prop: 'name', label: '合同名称' },
  { prop: 'status', label: '状态', type: 'select', options: contractStatusOptions },
]

// tab 内只做轻量新建（名称/金额/备注）；行项目与状态流转在合同管理页操作。
const contractFormItems = [
  { prop: 'name', label: '合同名称', required: true },
  { prop: 'amount', label: '合同金额', slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const contractDefaultForm = {}

const contractExtraQuery = computed(() => (detail.value ? { customerId: detail.value.id } : {}))

const contractApi = {
  page: pageContracts,
  create: createContract,
}

// tab 内新建合同：注入所属客户 + 金额空串省略（后端 money 只收十进制字符串）。
function submitContract(payload: any, mode: 'create' | 'update') {
  if (mode === 'create' && detail.value) {
    payload = { ...payload, customerId: detail.value.id }
  }
  const out = { ...payload }
  if (out.amount === '' || out.amount == null) delete out.amount
  return out
}

// ---------- 跟进记录 tab（issue #12）：列表范围锁定当前客户，可见性跟随客户；
// 新增跟进由后端同事务刷新客户 lastFollowTime，编辑/删除不回退 ----------
const followUpColumns = [
  { prop: 'type', label: '类型', width: 90, slot: true },
  { prop: 'content', label: '内容', minWidth: 220 },
  {
    prop: 'nextFollowTime',
    label: '下次跟进时间',
    minWidth: 170,
    slot: true,
  },
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

const followUpExtraQuery = computed(() => (detail.value ? { customerId: detail.value.id } : {}))

const followUpApi = {
  page: pageFollowUps,
  detail: getFollowUp,
  create: createFollowUp,
  update: updateFollowUp,
  remove: deleteFollowUp,
}

function flattenDeptTree(nodes: any[], out: { label: string; value: number }[] = []) {
  for (const n of nodes || []) {
    out.push({ label: n.name, value: n.id })
    if (n.children?.length) flattenDeptTree(n.children, out)
  }
  return out
}

onMounted(async () => {
  const [depts, simpleUsers] = await Promise.all([treeDepts(), listSimpleUsers()])
  deptOptions.value = flattenDeptTree(depts)
  users.value = simpleUsers
})
</script>

<style scoped>
.history-list {
  padding-left: 4px;
}
.history-action {
  margin-right: 8px;
}
.history-text {
  font-size: 13px;
}
</style>
