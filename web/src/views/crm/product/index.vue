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
      :action-width="220"
    >
      <!-- 状态切换（spec §5 PUT /product/status）：只改 status，挂 crm:product:update。 -->
      <template #actions="{ row }">
        <el-button v-if="canUpdateProduct" link type="warning" @click.stop="toggleProductStatus(row)">
          {{ row.status === 1 ? '下架' : '上架' }}
        </el-button>
      </template>
      <template #toolbar>
        <!-- Excel（issue #20）：导入/模板挂 crm:product:import，导出挂 crm:product:export。
             导入用分类名称关联分类（可空）；导出带上当前列表筛选。 -->
        <ExcelActions
          title="产品"
          import-perm="crm:product:import"
          export-perm="crm:product:export"
          :import-action="importProducts"
          :template-action="downloadProductImportTemplate"
          :export-action="exportProducts"
          :export-params="productExportParams"
          @success="reloadProducts"
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
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { useUserStore } from '@/stores/user'
import { useDictOptions } from '@/composables/useDict'
import { dictLabel, formatTime } from '@/utils/format'
import {
  pageProducts,
  getProduct,
  createProduct,
  updateProduct,
  updateProductStatus,
  deleteProduct,
  treeProductCategories,
  importProducts,
  downloadProductImportTemplate,
  exportProducts,
} from '@/api/crm'

// 产品列表/表单页（issue #11，codegen 产物为基底、分类字段改造为树选择器）：
// 分类树来自 /crm/product-category/tree，表单用树选择器、列表列与筛选用扁平映射。
// 产品状态对齐芋道商品上下架：1 上架 / 0 下架。
const productStatusOptions = [
  { label: '下架', value: 0 },
  { label: '上架', value: 1 },
]
// 单位选项来自系统字典 crm_product_unit（种子见 internal/seed/seed.go）。
const productUnitOptions = useDictOptions('crm_product_unit')

const tableRef = ref()
const categoryTree = ref<any[]>([])
const userStore = useUserStore()
const canUpdateProduct = computed(() => userStore.hasPerm('crm:product:update'))

function reloadProducts() {
  tableRef.value?.reload()
}

async function toggleProductStatus(row: any) {
  const next = row.status === 1 ? 0 : 1
  await updateProductStatus(row.id, next)
  ElMessage.success(next === 1 ? '上架成功' : '下架成功')
  reloadProducts()
}

// 产品导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function productExportParams() {
  const q = tableRef.value?.query || {}
  return {
    name: q.name,
    code: q.code,
    categoryId: q.categoryId,
    unit: q.unit,
    status: q.status,
  }
}

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

const searchItems = computed(() => [
  { prop: 'name', label: '产品名称' },
  { prop: 'code', label: '产品编码' },
  { prop: 'categoryId', label: '产品分类', type: 'select', options: categorySelectOptions.value },
  { prop: 'unit', label: '单位', type: 'select', options: productUnitOptions },
  { prop: 'status', label: '状态', type: 'select', options: productStatusOptions },
])

const columns = [
  {
    prop: 'categoryId',
    label: '产品分类',
    minWidth: 120,
    formatter: (row: any) => categoryNameById.value[row.categoryId] ?? row.categoryId,
  },
  { prop: 'name', label: '产品名称', minWidth: 120 },
  { prop: 'code', label: '产品编码', minWidth: 120 },
  { prop: 'unit', label: '单位', width: 110, formatter: (row: any) => dictLabel(productUnitOptions, row.unit) },
  { prop: 'price', label: '价格（元）', minWidth: 120 },
  { prop: 'status', label: '状态', width: 90, formatter: (row: any) => dictLabel(productStatusOptions, row.status) },
  { prop: 'description', label: '描述', minWidth: 120 },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'categoryId', label: '产品分类', required: true, slot: true },
  { prop: 'name', label: '产品名称', required: true },
  { prop: 'code', label: '产品编码', required: true },
  { prop: 'unit', label: '单位', type: 'select', options: productUnitOptions },
  { prop: 'price', label: '价格（元）', required: true },
  { prop: 'status', label: '状态', type: 'select', options: productStatusOptions },
  { prop: 'description', label: '描述', type: 'textarea' },
]

const defaultForm: Record<string, any> = {
  categoryId: undefined,
  name: '',
  code: '',
  unit: '',
  price: '',
  status: 1,
  description: '',
}

const api = {
  page: pageProducts,
  detail: getProduct,
  create: createProduct,
  update: updateProduct,
  remove: deleteProduct,
}

async function loadCategoryTree() {
  categoryTree.value = (await treeProductCategories()) || []
}

onMounted(loadCategoryTree)
</script>
