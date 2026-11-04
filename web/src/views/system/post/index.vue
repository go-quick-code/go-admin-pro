<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="system:post"
      title="岗位"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import { pagePosts, createPost, updatePost, deletePost } from '@/api/system'

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '岗位名称', minWidth: 140 },
  { prop: 'code', label: '岗位编码', minWidth: 150 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 180 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '岗位名称' },
  { prop: 'code', label: '岗位编码' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '岗位名称', required: true },
  { prop: 'code', label: '岗位编码', required: true },
  { prop: 'sort', label: '排序', type: 'number' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = { sort: 0, status: 0 }

const api = {
  page: pagePosts,
  create: createPost,
  update: updatePost,
  remove: deletePost,
}
</script>
