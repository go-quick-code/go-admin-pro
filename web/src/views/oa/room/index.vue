<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      title="会议室"
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
      会议室有未来有效预约时只能停用不能删除（删除返回 409）；停用后不能再创建新预约，已有预约仍有效。
      预约入口见「会议室预约 / 我的会议」页面。
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { statusOptions, statusTagType } from '@/utils/format'
import {
  pageMeetingRooms,
  createMeetingRoom,
  updateMeetingRoom,
  deleteMeetingRoom,
  setMeetingRoomStatus,
} from '@/api/oa'

// 会议室管理（issue #57，spec §3.7/§4.11/§4.14/§6）：CRUD + 启停。
// 权限码 oa:room:manage（按钮 5251）；页面查询挂 oa:room:query（菜单 525）。
// 删除约束由后端在事务内校验：有未来有效预约 → 409，只能停用。

const tableRef = ref<any>()

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '会议室名称', minWidth: 160 },
  { prop: 'location', label: '位置', minWidth: 180 },
  { prop: 'capacity', label: '容纳人数', width: 100 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 90, slot: true },
]

const searchItems = [
  { prop: 'name', label: '会议室名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '会议室名称', required: true, placeholder: '如：大会议室' },
  { prop: 'location', label: '位置', placeholder: '如：3 层东侧' },
  { prop: 'capacity', label: '容纳人数', type: 'number' },
  { prop: 'sort', label: '排序', type: 'number' },
]

const defaultForm = { capacity: 0, sort: 0 }

const api = {
  page: pageMeetingRooms,
  create: createMeetingRoom,
  update: (data: any) => {
    const { id, ...rest } = data || {}
    return updateMeetingRoom(Number(id), rest)
  },
  remove: deleteMeetingRoom,
}

function statusName(status: number) {
  return status === 0 ? '启用' : '停用'
}

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await ElMessageBox.confirm(
    `确认${next === 0 ? '启用' : '停用'}会议室「${row.name}」吗？`,
    '提示',
    { type: 'warning' },
  )
  await setMeetingRoomStatus(row.id, next)
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
