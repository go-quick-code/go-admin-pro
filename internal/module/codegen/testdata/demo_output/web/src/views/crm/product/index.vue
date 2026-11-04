<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="crm:product"
      title="产品"
      :action-width="170"
    >
      <template #cell-ownerUserName="{ row }">
        <span v-if="row.ownerUserName">{{ row.ownerUserName }}（{{ row.ownerDeptName || '-' }}）</span>
        <el-tag v-else type="warning" size="small">未分配</el-tag>
      </template>
      <template #form-ownerUserId="{ form }">
        <el-select v-model="form.ownerUserId" clearable filterable style="width: 100%" placeholder="缺省为当前登录用户">
          <el-option v-for="u in users" :key="u.id" :label="u.nickname" :value="u.id" />
        </el-select>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { dictLabel, formatTime } from '@/utils/format'
import {
  pageProducts,
  getProduct,
  createProduct,
  updateProduct,
  deleteProduct,
} from '@/api/crm'
import { treeDepts, listSimpleUsers } from '@/api/system'

// 产品状态（生成自字段元信息：状态）。
const productStatusOptions = [
  { label: '在售', value: 1 },
  { label: '下架', value: 2 },
]
// TODO：单位选项接入系统字典 crm_product_unit（web 尚无字典读取封装，先留空选项）。
const productUnitOptions: { label: string; value: any }[] = []

const tableRef = ref()
const users = ref<any[]>([])
const deptOptions = ref<{ label: string; value: number }[]>([])

// searchItems 用 computed：负责人/部门选项在 onMounted 异步加载后刷新下拉。
const searchItems = computed(() => [
  { prop: 'name', label: '产品名称' },
  { prop: 'unit', label: '单位', type: 'select', options: productUnitOptions },
  { prop: 'status', label: '状态', type: 'select', options: productStatusOptions },
  {
    prop: 'ownerUserId',
    label: '负责人',
    type: 'select',
    options: users.value.map((u: any) => ({ label: u.nickname, value: u.id })),
  },
  { prop: 'ownerDeptId', label: '部门', type: 'select', options: deptOptions.value },
])

const columns = [
  { prop: 'name', label: '产品名称', minWidth: 120 },
  { prop: 'category', label: '产品分类', minWidth: 120 },
  { prop: 'unit', label: '单位', width: 110, formatter: (row: any) => dictLabel(productUnitOptions, row.unit) },
  { prop: 'price', label: '价格（元）', minWidth: 120 },
  { prop: 'status', label: '状态', width: 90, formatter: (row: any) => dictLabel(productStatusOptions, row.status) },
  { prop: 'remark', label: '备注', minWidth: 120 },
  { prop: 'ownerUserName', label: '负责人', minWidth: 150, slot: true },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'name', label: '产品名称', required: true },
  { prop: 'category', label: '产品分类' },
  { prop: 'unit', label: '单位', type: 'select', options: productUnitOptions },
  { prop: 'price', label: '价格（元）', required: true },
  { prop: 'status', label: '状态', type: 'select', options: productStatusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
  { prop: 'ownerUserId', label: '负责人', type: 'select', slot: true },
]

const defaultForm: Record<string, any> = {
  name: '',
  category: '',
  unit: '',
  price: '',
  status: 1,
  remark: '',
  ownerUserId: 0,
}

const api = {
  page: pageProducts,
  detail: getProduct,
  create: createProduct,
  update: updateProduct,
  remove: deleteProduct,
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
