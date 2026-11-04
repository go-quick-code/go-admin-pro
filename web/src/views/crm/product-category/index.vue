<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="crm:product-category"
      title="产品分类"
      :default-expand-all="true"
      @success="loadCategoryTree"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
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
import { onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  treeProductCategories,
  createProductCategory,
  updateProductCategory,
  deleteProductCategory,
} from '@/api/crm'

// 产品分类树页（issue #11，对齐 system/dept 的树形页写法）：
// 列表即树（api.list 无分页）；删除约束由后端兜底——有子分类/有产品时返回业务错误提示。
const categoryTree = ref<any[]>([])

const columns = [
  { prop: 'name', label: '分类名称', minWidth: 200 },
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const formItems = [
  { prop: 'parentId', label: '上级分类', slot: true },
  { prop: 'name', label: '分类名称', required: true },
  { prop: 'sort', label: '排序', type: 'number' },
  // 状态口径：0 启用 / 1 停用（芋道 CommonStatus，规格 §3 原文；与产品上下架不同）。
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
]

const defaultForm = { parentId: 0, sort: 0, status: 0 }

const api = {
  list: treeProductCategories,
  create: createProductCategory,
  update: updateProductCategory,
  remove: deleteProductCategory,
}

async function loadCategoryTree() {
  categoryTree.value = (await treeProductCategories()) || []
}

onMounted(loadCategoryTree)
</script>
