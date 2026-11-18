<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="mall:category"
      title="商品分类"
      :default-expand-all="true"
      @success="loadCategoryTree"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
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
          placeholder="不选则为根分类"
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
} from '@/api/mall'

// 商品分类页（issue #176，spec §3.2/§4）：两级树表（照 crm product-category
// 页形状）。父必须是根级（后端两级校验 400；树选择里子分类可选但提交会被
// 拒绝——用 check-strictly 展示全树，两级约束以后端兜底为准）；同级同名
// 409；有子级或被 SPU 引用禁删 409。叶子才可挂 SPU（SPU 表单随 T4）。

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
  { prop: 'name', label: '分类名称', required: true, placeholder: '同级唯一，如 手机' },
  { prop: 'sort', label: '排序', type: 'number' },
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
