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
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
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

    <!-- 客户详情抽屉：基本信息 + 联系人 + 负责人历史 tab
        （issue #9 tab 结构；issue #13 融入释放/转移入口与历史 tab） -->
    <el-drawer v-model="detailVisible" :title="detail?.name || '客户详情'" size="900px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="客户编号">{{ detail.customerNo }}</el-descriptions-item>
            <el-descriptions-item label="客户名称">{{ detail.name }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="statusTagType(detail.status)" size="small">{{ statusLabel(detail.status) }}</el-tag>
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
            <template #cell-gender="{ row }">{{ genderLabel(row.gender) }}</template>
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
import { formatTime, dictLabel } from '@/utils/format'
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
} from '@/api/crm'
import { treeDepts, listSimpleUsers } from '@/api/system'
import { useUserStore } from '@/stores/user'

// 客户状态对齐规格：1 潜在 / 2 活跃 / 3 流失（docs/crm-mvp-spec.md §3）
const customerStatusOptions = [
  { label: '潜在', value: 1 },
  { label: '活跃', value: 2 },
  { label: '流失', value: 3 },
]

// 联系人性别对齐字典 sys_user_sex：0 未知 / 1 男 / 2 女（issue #9）
const contactGenderOptions = [
  { label: '未知', value: 0 },
  { label: '男', value: 1 },
  { label: '女', value: 2 },
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

function statusTagType(status: number) {
  if (status === 2) return 'success'
  if (status === 3) return 'danger'
  return 'info'
}

function genderLabel(gender: number) {
  return dictLabel(contactGenderOptions, gender)
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

// 新建联系人时注入所属客户（所属客户创建后不可漂移，更新接口不接受 customerId）。
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
