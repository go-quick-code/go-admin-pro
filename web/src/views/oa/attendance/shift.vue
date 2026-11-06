<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      title="班次"
      dialog-width="560px"
    >
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status, { 0: 'success', 1: 'info' })" size="small">
          {{ statusName(row.status) }}
        </el-tag>
      </template>
      <template #actions="{ row }">
        <el-button link type="warning" @click="toggleStatus(row)">
          {{ row.status === 0 ? '停用' : '启用' }}
        </el-button>
      </template>
    </ProTable>
    <div class="field-hint">
      一天一个时段，下班时间必须晚于上班时间（不支持跨天班次）；宽限分钟为迟到/早退的容忍时长。
      被考勤组绑定的班次不能删除，只能先调整考勤组或停用。
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { statusOptions, statusTagType } from '@/utils/format'
import {
  pageShifts,
  createShift,
  updateShift,
  deleteShift,
  setShiftStatus,
} from '@/api/oa'

// 班次管理（issue #60，spec §3.9/§4.10/§6）：CRUD + 启停。
// 权限码 oa:attendance:shift:manage（菜单 527）；时段格式由后端严格校验。

const tableRef = ref<any>()

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '班次名称', minWidth: 140 },
  { prop: 'startTime', label: '上班时间', width: 100 },
  { prop: 'endTime', label: '下班时间', width: 100 },
  { prop: 'graceMinutes', label: '宽限(分钟)', width: 110 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 90, slot: true },
]

const searchItems = [
  { prop: 'name', label: '班次名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '班次名称', required: true, placeholder: '如标准班' },
  { prop: 'startTime', label: '上班时间', required: true, placeholder: 'HH:MM，如 09:00' },
  { prop: 'endTime', label: '下班时间', required: true, placeholder: 'HH:MM，如 18:00' },
  { prop: 'graceMinutes', label: '宽限分钟', type: 'number' },
  { prop: 'sort', label: '排序', type: 'number' },
]

const defaultForm = { graceMinutes: 0, sort: 0 }

const api = {
  page: pageShifts,
  create: createShift,
  update: updateShift,
  remove: deleteShift,
}

function statusName(status: number) {
  return status === 0 ? '启用' : '停用'
}

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await ElMessageBox.confirm(
    `确认${next === 0 ? '启用' : '停用'}班次「${row.name}」吗？`,
    '提示',
    { type: 'warning' },
  )
  await setShiftStatus(row.id, next)
  ElMessage.success(next === 0 ? '已启用' : '已停用')
  tableRef.value?.reload()
}
</script>

<style scoped>
.field-hint {
  margin-top: 12px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}
</style>
