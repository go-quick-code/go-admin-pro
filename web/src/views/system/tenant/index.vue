<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="system:tenant"
      title="租户"
      dialog-width="640px"
    >
      <template #toolbar>
        <span class="tip">新建租户会自动初始化管理员账号 admin / 123456</span>
      </template>
      <template #cell-status="{ row }">
        <el-switch :model-value="row.status === 0" @change="toggleStatus(row)" />
      </template>
      <template #cell-expireTime="{ row }">
        {{ row.expireTime ? formatTime(row.expireTime) : '永不过期' }}
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageTenants,
  createTenant,
  updateTenant,
  updateTenantStatus,
  deleteTenant,
} from '@/api/system'

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '租户名称', minWidth: 140 },
  { prop: 'contactName', label: '联系人', minWidth: 110 },
  { prop: 'contactMobile', label: '联系电话', minWidth: 130 },
  { prop: 'accountCount', label: '账号额度', width: 100 },
  { prop: 'expireTime', label: '过期时间', minWidth: 170, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '租户名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '租户名称', required: true },
  { prop: 'contactName', label: '联系人' },
  { prop: 'contactMobile', label: '联系电话' },
  { prop: 'domain', label: '绑定域名', placeholder: '可选，如 demo.example.com' },
  { prop: 'accountCount', label: '账号额度', type: 'number' },
  { prop: 'expireTime', label: '过期时间', type: 'datetime' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = { status: 0, accountCount: 10 }

const api = {
  page: pageTenants,
  create: createTenant,
  update: updateTenant,
  remove: deleteTenant,
}

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await updateTenantStatus({ id: row.id, status: next })
  row.status = next
  ElMessage.success(next === 0 ? '租户已启用' : '租户已停用')
}
</script>

<style scoped>
.tip {
  color: #909399;
  font-size: 12px;
  margin-left: 8px;
}
</style>
