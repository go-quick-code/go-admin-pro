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
      </template>
      <template #form-ownerUserId="{ form }">
        <el-select v-model="form.ownerUserId" clearable filterable style="width: 100%" placeholder="缺省为当前登录用户">
          <el-option v-for="u in users" :key="u.id" :label="u.nickname" :value="u.id" />
        </el-select>
      </template>
    </ProTable>

    <!-- 客户详情抽屉：基本信息 + 联系人 tab（issue #9，联系人可见性跟随客户） -->
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
      </el-tabs>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { formatTime, dictLabel } from '@/utils/format'
import {
  pageCustomers,
  getCustomer,
  createCustomer,
  updateCustomer,
  deleteCustomer,
  pageContacts,
  getContact,
  createContact,
  updateContact,
  deleteContact,
} from '@/api/crm'
import { treeDepts, listSimpleUsers } from '@/api/system'

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

const tableRef = ref()
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

async function openDetail(row: any) {
  detail.value = await getCustomer(row.id)
  detailTab.value = 'info'
  detailVisible.value = true
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
