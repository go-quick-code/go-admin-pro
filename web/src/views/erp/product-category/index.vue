<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="erp:product-category"
      title="产品分类"
      :default-expand-all="true"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #toolbar>
        <!-- Excel 导出（issue #88，spec §4.11：ERP 只导出不导入）。 -->
        <ExcelActions title="产品分类" export-perm="erp:product-category:export" :export-action="exportProductCategories" />
      </template>
      <template #form-parentId="{ form }">
        <el-tree-select
          v-model="form.parentId"
          :data="categoryTree"
          :props="{ label: 'name', value: 'id', children: 'children' }"
          check-strictly
          clearable
          style="width: 100%"
          placeholder="不选则为顶级分类"
        />
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  listProductCategories,
  getProductCategory,
  createProductCategory,
  updateProductCategory,
  deleteProductCategory,
  exportProductCategories,
} from '@/api/erp'

// 产品分类树页（issue #88，对齐 crm/product-category 的树形页写法）：
// 后端 GET list 返回扁平列表（spec §5：前端组树），这里按 parentId 组树后交给
// ProTable 渲染；删除约束由后端兜底——有子分类/被产品引用时返回 409 提示。
const tableRef = ref()
const categoryTree = ref<any[]>([])

// 前端组树（spec §5）：parent_id 无限层级，孤儿节点降级为根，不丢数据。
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

const columns = [
  { prop: 'name', label: '分类名称', minWidth: 200 },
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'code', label: '分类编码', minWidth: 120 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const formItems = [
  { prop: 'parentId', label: '上级分类', slot: true },
  { prop: 'name', label: '分类名称', required: true },
  { prop: 'code', label: '分类编码' },
  { prop: 'sort', label: '排序', type: 'number' },
  // 状态口径：0 启用 / 1 停用（芋道 CommonStatus，spec §3.1）。
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
]

const defaultForm = { parentId: 0, name: '', code: '', sort: 0, status: 0 }

const api = {
  list: async () => {
    const list = (await listProductCategories()) || []
    categoryTree.value = buildCategoryTree(list)
    return categoryTree.value
  },
  detail: getProductCategory,
  create: createProductCategory,
  update: updateProductCategory,
  remove: deleteProductCategory,
}
</script>
