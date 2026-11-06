<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      :before-submit="beforeSubmit"
      perm-prefix="system:tenant-package"
      title="租户套餐"
      dialog-width="760px"
    >
      <template #toolbar>
        <span class="tip">套餐决定租户可见菜单与资源上限；配额 0 表示不限</span>
      </template>
      <template #cell-status="{ row }">
        <el-switch :model-value="row.status === 0" @change="toggleStatus(row)" />
      </template>
      <template #form-menuIds="{ form }">
        <MenuTreeSelect v-model="form.menuIds" :data="menuTree" />
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import MenuTreeSelect from '@/components/MenuTreeSelect.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageTenantPackages,
  createTenantPackage,
  updateTenantPackage,
  updateTenantPackageStatus,
  deleteTenantPackage,
  listMenus,
} from '@/api/system'

const flatMenus = ref<any[]>([])

const menuTree = computed(() => buildMenuTree(flatMenus.value))

function buildMenuTree(flat: any[]) {
  const nodes = flat
    .filter((menu) => menu.type !== 3)
    .map((menu) => ({ ...menu, children: [] as any[] }))
  const byID = new Map<number, any>(nodes.map((node) => [node.id, node]))
  const roots: any[] = []
  for (const node of nodes) {
    const parent = byID.get(node.parentId)
    if (parent) parent.children.push(node)
    else roots.push(node)
  }
  return roots
}

function quotaText(value: any) {
  return value === 0 || value === null || value === undefined ? '不限' : value
}

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '套餐名称', minWidth: 140 },
  { prop: 'maxUsers', label: '用户数', width: 90, formatter: (row: any) => quotaText(row.maxUsers) },
  { prop: 'maxStorageMb', label: '存储(MB)', width: 100, formatter: (row: any) => quotaText(row.maxStorageMb) },
  { prop: 'maxSmsMonthly', label: '月短信', width: 90, formatter: (row: any) => quotaText(row.maxSmsMonthly) },
  { prop: 'maxMailMonthly', label: '月邮件', width: 90, formatter: (row: any) => quotaText(row.maxMailMonthly) },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '套餐名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '套餐名称', required: true },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'maxUsers', label: '用户数上限', type: 'number' },
  { prop: 'maxStorageMb', label: '存储上限 MB', type: 'number' },
  { prop: 'maxSmsMonthly', label: '月短信条数', type: 'number' },
  { prop: 'maxMailMonthly', label: '月邮件封数', type: 'number' },
  { prop: 'menuIds', label: '可访问菜单', slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = {
  status: 0,
  menuIds: [] as number[],
  maxUsers: 0,
  maxStorageMb: 0,
  maxSmsMonthly: 0,
  maxMailMonthly: 0,
}

function beforeSubmit(payload: any) {
  return { ...payload, menuIds: Array.isArray(payload.menuIds) ? payload.menuIds : [] }
}

const api = {
  page: pageTenantPackages,
  create: createTenantPackage,
  update: updateTenantPackage,
  remove: deleteTenantPackage,
}

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await updateTenantPackageStatus({ id: row.id, status: next })
  row.status = next
  ElMessage.success(next === 0 ? '套餐已启用' : '套餐已停用')
}

onMounted(async () => {
  flatMenus.value = (await listMenus()) || []
})
</script>
