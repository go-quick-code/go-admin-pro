<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="erp:product"
      title="产品"
      :action-width="160"
    >
      <template #toolbar>
        <!-- Excel 导出（issue #88，spec §4.11：ERP 只导出不导入），复用当前列表筛选。 -->
        <ExcelActions
          title="产品"
          export-perm="erp:product:export"
          :export-action="exportProducts"
          :export-params="productExportParams"
        />
      </template>
      <template #form-categoryId="{ form }">
        <el-tree-select
          v-model="form.categoryId"
          :data="categoryTree"
          :props="{ label: 'name', value: 'id', children: 'children' }"
          check-strictly
          clearable
          style="width: 100%"
          placeholder="请选择产品分类"
        />
      </template>
      <template #form-unitId="{ form }">
        <el-select v-model="form.unitId" clearable style="width: 100%" placeholder="请选择产品单位">
          <el-option v-for="u in unitList" :key="u.id" :label="u.name" :value="u.id" />
        </el-select>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageProducts,
  getProduct,
  createProduct,
  updateProduct,
  deleteProduct,
  listProductCategories,
  pageProductUnits,
  exportProducts,
} from '@/api/erp'

// 产品列表/表单页（issue #88，spec §3.1/§6）：分类树来自 /erp/product-category/list
//（前端组树），单位选项来自 /erp/product-unit/page（单位是实体字典，与 CRM 的
// 系统字典 crm_product_unit 不同域）。金额/重量一律十进制字符串（Money/qty，
// ADR 0017），参考价缺省 0.00，保质期/重量可空。
const tableRef = ref()
const categoryTree = ref<any[]>([])
const unitList = ref<any[]>([])

// 展平分类树（带层级缩进），供列表筛选的下拉框使用；表单走树选择器插槽。
const categorySelectOptions = computed(() => {
  const out: { label: string; value: number }[] = []
  const walk = (nodes: any[], depth: number) => {
    for (const n of nodes || []) {
      out.push({ label: `${'　'.repeat(depth)}${n.name}`, value: n.id })
      walk(n.children || [], depth + 1)
    }
  }
  walk(categoryTree.value, 0)
  return out
})

// 分类 ID → 名称映射，供列表列展示（树未加载时回退显示 ID）。
const categoryNameById = computed<Record<number, string>>(() => {
  const map: Record<number, string> = {}
  const walk = (nodes: any[]) => {
    for (const n of nodes || []) {
      map[n.id] = n.name
      walk(n.children || [])
    }
  }
  walk(categoryTree.value)
  return map
})

// 单位 ID → 名称映射，供列表列展示。
const unitNameById = computed<Record<number, string>>(() => {
  const map: Record<number, string> = {}
  for (const u of unitList.value) map[u.id] = u.name
  return map
})

// 前端组树（spec §5）：与分类页同一套逻辑。
function buildCategoryTree(list: any[]) {
  const nodes = new Map<number, any>()
  list.forEach((row) => nodes.set(row.id, { ...row, children: [] }))
  const roots: any[] = []
  for (const row of list) {
    const node = nodes.get(row.id)
    const parent = row.parentId ? nodes.get(row.parentId) : undefined
    if (parent) parent.children.push(node)
    else roots.push(node)
  }
  const sortRec = (items: any[]) => {
    items.sort((a, b) => a.sort - b.sort || a.id - b.id)
    items.forEach((item) => sortRec(item.children))
  }
  sortRec(roots)
  return roots
}

const productStatusOptions = statusOptions

const searchItems = computed(() => [
  { prop: 'name', label: '产品名称' },
  { prop: 'barCode', label: '商品条码' },
  { prop: 'categoryId', label: '产品分类', type: 'select', options: categorySelectOptions.value },
  { prop: 'status', label: '状态', type: 'select', options: productStatusOptions },
])

const columns = [
  {
    prop: 'categoryId',
    label: '产品分类',
    minWidth: 120,
    formatter: (row: any) => categoryNameById.value[row.categoryId] ?? row.categoryId,
  },
  { prop: 'name', label: '产品名称', minWidth: 140 },
  { prop: 'barCode', label: '商品条码', minWidth: 130 },
  { prop: 'unitId', label: '单位', width: 90, formatter: (row: any) => unitNameById.value[row.unitId] ?? row.unitId },
  { prop: 'standard', label: '规格', minWidth: 110 },
  { prop: 'purchasePrice', label: '采购参考价（元）', minWidth: 130 },
  { prop: 'salePrice', label: '销售参考价（元）', minWidth: 130 },
  { prop: 'minPrice', label: '最低参考价（元）', minWidth: 130 },
  { prop: 'status', label: '状态', width: 90, formatter: (row: any) => (row.status === 0 ? '启用' : '停用') },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const formItems = [
  { prop: 'categoryId', label: '产品分类', required: true, slot: true },
  { prop: 'unitId', label: '产品单位', required: true, slot: true },
  { prop: 'name', label: '产品名称', required: true },
  { prop: 'barCode', label: '商品条码' },
  { prop: 'standard', label: '规格' },
  { prop: 'purchasePrice', label: '采购参考价（元）' },
  { prop: 'salePrice', label: '销售参考价（元）' },
  { prop: 'minPrice', label: '最低参考价（元）' },
  { prop: 'expiryDay', label: '保质期（天）', type: 'number' },
  { prop: 'weight', label: '重量（kg）' },
  { prop: 'status', label: '状态', type: 'radio', options: productStatusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm: Record<string, any> = {
  categoryId: undefined,
  unitId: undefined,
  name: '',
  barCode: '',
  standard: '',
  purchasePrice: '',
  salePrice: '',
  minPrice: '',
  expiryDay: undefined,
  weight: '',
  status: 0,
  remark: '',
}

// 产品导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function productExportParams() {
  const q = tableRef.value?.query || {}
  return {
    name: q.name,
    barCode: q.barCode,
    categoryId: q.categoryId,
    status: q.status,
  }
}

const api = {
  page: pageProducts,
  detail: getProduct,
  create: createProduct,
  update: updateProduct,
  remove: deleteProduct,
}

async function loadMasterData() {
  const list = (await listProductCategories()) || []
  categoryTree.value = buildCategoryTree(list)
  unitList.value = ((await pageProductUnits({ pageNo: 1, pageSize: 200 })) as any)?.list || []
}

onMounted(loadMasterData)
</script>
