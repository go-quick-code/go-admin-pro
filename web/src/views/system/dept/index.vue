<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="system:dept"
      title="部门"
      :default-expand-all="true"
      @success="loadDeptOptions"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #form-parentId="{ form }">
        <el-tree-select
          v-model="form.parentId"
          :data="deptOptions"
          :props="{ label: 'name', value: 'id', children: 'children' }"
          check-strictly
          clearable
          style="width: 100%"
          placeholder="不选则为顶级部门"
        />
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import { treeDepts, getDept, createDept, updateDept, deleteDept } from '@/api/system'

const deptOptions = ref<any[]>([])

const columns = [
  { prop: 'name', label: '部门名称', minWidth: 200 },
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'phone', label: '联系电话', minWidth: 130 },
  { prop: 'email', label: '邮箱', minWidth: 160 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const formItems = [
  { prop: 'parentId', label: '上级部门', slot: true },
  { prop: 'name', label: '部门名称', required: true },
  { prop: 'sort', label: '排序', type: 'number' },
  { prop: 'leaderUserId', label: '负责人 ID', type: 'number' },
  { prop: 'phone', label: '联系电话' },
  { prop: 'email', label: '邮箱' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
]

const defaultForm = { parentId: 0, sort: 0, status: 0 }

const api = {
  list: treeDepts,
  detail: getDept,
  create: createDept,
  update: updateDept,
  remove: deleteDept,
}

async function loadDeptOptions() {
  deptOptions.value = await treeDepts()
}

onMounted(loadDeptOptions)
</script>
