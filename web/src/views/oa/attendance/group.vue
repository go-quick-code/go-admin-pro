<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      title="考勤组"
      dialog-width="560px"
    >
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status, { 0: 'success', 1: 'info' })" size="small">
          {{ statusName(row.status) }}
        </el-tag>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="openMembers(row)">组员</el-button>
        <el-button link type="warning" @click="toggleStatus(row)">
          {{ row.status === 0 ? '停用' : '启用' }}
        </el-button>
      </template>
    </ProTable>
    <div class="field-hint">
      考勤组绑定一个班次；组员按用户物化，一个用户只属于一个考勤组。未加入任何考勤组的用户打卡会返回明确错误。
      组内有成员时只能停用不能删除（先移出组员）。
    </div>

    <el-dialog v-model="memberDialog.visible" title="组员管理" width="780px" append-to-body>
      <el-descriptions :column="2" border size="small">
        <el-descriptions-item label="考勤组">{{ memberDialog.groupName }}</el-descriptions-item>
        <el-descriptions-item label="绑定班次">{{ memberDialog.shiftName }}</el-descriptions-item>
      </el-descriptions>

      <div class="member-tools">
        <el-select
          v-model="memberDialog.userIds"
          multiple
          filterable
          collapse-tags
          placeholder="选择要加入的用户"
          style="width: 280px"
        >
          <el-option
            v-for="u in users"
            :key="u.id"
            :label="`${u.nickname}（#${u.id}）`"
            :value="u.id"
          />
        </el-select>
        <el-button type="primary" @click="doAddUsers">添加成员</el-button>
        <el-tree-select
          v-model="memberDialog.deptIds"
          :data="deptTree"
          :props="{ label: 'name', children: 'children' }"
          node-key="id"
          multiple
          show-checkbox
          check-strictly
          clearable
          placeholder="选择部门"
          style="width: 240px"
        />
        <el-button @click="doAddDept">按部门导入</el-button>
      </div>

      <el-table v-loading="memberDialog.loading" :data="memberDialog.members" border stripe max-height="360">
        <el-table-column prop="userId" label="用户 ID" width="110" />
        <el-table-column prop="nickname" label="昵称" min-width="180" />
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button link type="danger" @click="doRemove(row.userId)">移出</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="field-hint">
        一个用户只属于一个考勤组：添加到本组会自动从原组改绑。按部门导入取部门当前成员，后续部门人员变化不自动同步。
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { listSimpleUsers, treeDepts } from '@/api/system'
import { statusOptions, statusTagType } from '@/utils/format'
import {
  pageShifts,
  pageGroups,
  getGroup,
  createGroup,
  updateGroup,
  deleteGroup,
  setGroupStatus,
  addGroupMembers,
  addGroupMembersByDept,
  removeGroupMember,
} from '@/api/oa'

// 考勤组管理（issue #60，spec §3.9/§4.10/§6）：CRUD + 启停 + 组员批量物化
//（按用户/按部门）。权限码 oa:attendance:group:manage（菜单 530）。

const tableRef = ref<any>()
const shiftOptions = ref<{ label: string; value: number }[]>([])
const users = ref<any[]>([])
const deptTree = ref<any[]>([])

const memberDialog = reactive({
  visible: false,
  loading: false,
  groupId: 0,
  groupName: '',
  shiftName: '',
  members: [] as any[],
  userIds: [] as number[],
  deptIds: [] as number[],
})

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '考勤组名称', minWidth: 160 },
  {
    prop: 'shiftName',
    label: '绑定班次',
    minWidth: 140,
    formatter: (row: any) =>
      row.shift ? `${row.shift.name}（${row.shift.startTime}-${row.shift.endTime}）` : '-',
  },
  { prop: 'memberCount', label: '组员数', width: 90 },
  { prop: 'status', label: '状态', width: 90, slot: true },
]

const searchItems = [
  { prop: 'name', label: '考勤组名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = computed(() => [
  { prop: 'name', label: '考勤组名称', required: true },
  { prop: 'shiftId', label: '绑定班次', type: 'select', required: true, options: shiftOptions.value },
])

const api = {
  page: pageGroups,
  detail: getGroup,
  create: createGroup,
  update: updateGroup,
  remove: deleteGroup,
}

function statusName(status: number) {
  return status === 0 ? '启用' : '停用'
}

async function loadRefs() {
  // 班次下拉取全量（含停用，已有组可能绑定停用班次）；用户与部门为组员导入候选。
  const shiftPage = await pageShifts({ pageNo: 1, pageSize: 200 })
  shiftOptions.value = (shiftPage?.list || []).map((s: any) => ({
    label: `${s.name}（${s.startTime}-${s.endTime}）`,
    value: s.id,
  }))
  users.value = (await listSimpleUsers()) || []
  deptTree.value = (await treeDepts()) || []
}

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await ElMessageBox.confirm(
    `确认${next === 0 ? '启用' : '停用'}考勤组「${row.name}」吗？${next === 1 ? '停用后组员将无法打卡。' : ''}`,
    '提示',
    { type: 'warning' },
  )
  await setGroupStatus(row.id, next)
  ElMessage.success(next === 0 ? '已启用' : '已停用')
  tableRef.value?.reload()
}

async function openMembers(row: any) {
  memberDialog.visible = true
  memberDialog.groupId = row.id
  memberDialog.groupName = row.name
  memberDialog.userIds = []
  memberDialog.deptIds = []
  await refreshMembers()
}

async function refreshMembers() {
  memberDialog.loading = true
  try {
    const detail = await getGroup(memberDialog.groupId)
    memberDialog.groupName = detail.name
    memberDialog.shiftName = detail.shift
      ? `${detail.shift.name}（${detail.shift.startTime}-${detail.shift.endTime}）`
      : '-'
    memberDialog.members = detail.members || []
  } finally {
    memberDialog.loading = false
  }
}

async function afterMemberChange(message: string) {
  ElMessage.success(message)
  await refreshMembers()
  tableRef.value?.reload()
}

async function doAddUsers() {
  if (!memberDialog.userIds.length) {
    ElMessage.warning('请选择要添加的成员')
    return
  }
  const data = await addGroupMembers(memberDialog.groupId, memberDialog.userIds)
  memberDialog.userIds = []
  await afterMemberChange(`已添加 ${data?.added ?? 0} 名组员`)
}

async function doAddDept() {
  if (!memberDialog.deptIds.length) {
    ElMessage.warning('请选择要导入的部门')
    return
  }
  const data = await addGroupMembersByDept(memberDialog.groupId, memberDialog.deptIds)
  memberDialog.deptIds = []
  await afterMemberChange(`已按部门导入 ${data?.added ?? 0} 名组员`)
}

async function doRemove(userId: number) {
  await ElMessageBox.confirm(`确认将该用户移出考勤组吗？`, '提示', { type: 'warning' })
  await removeGroupMember(memberDialog.groupId, userId)
  await afterMemberChange('已移出组员')
}

onMounted(loadRefs)
</script>

<style scoped>
.member-tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin: 12px 0;
}

.field-hint {
  margin-top: 12px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}
</style>
