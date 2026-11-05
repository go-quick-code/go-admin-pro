<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      :show-actions="false"
      perm-prefix="bpm:definition"
      title="流程定义"
      dialog-width="760px"
    >
      <template #cell-status="{ row }">
        <el-tag :type="statusTag(row.status)" size="small">{{ statusText(row.status) }}</el-tag>
      </template>
      <template #cell-nodes="{ row }">
        <span>{{ (row.nodes || []).length }} 个节点</span>
      </template>
      <!-- 操作列（自定义）：按状态机展示，发布/停用共用 bpm:definition:publish。 -->
      <template #cell-actions="{ row }">
        <template v-if="row.status === 0">
          <el-button v-if="canUpdate" link type="primary" @click.stop="openEdit(row)">编辑</el-button>
          <el-button v-if="canPublish" link type="success" @click.stop="doPublish(row)">发布</el-button>
          <el-button v-if="canDelete" link type="danger" @click.stop="doDelete(row)">删除</el-button>
        </template>
        <el-button v-if="row.status === 1 && canPublish" link type="warning" @click.stop="doDisable(row)">
          停用
        </el-button>
        <el-button link type="info" @click.stop="openHistory(row)">版本历史</el-button>
      </template>
      <!-- 有序节点编辑器（spec §4.2：六种审批人规则）。 -->
      <template #form-nodes="{ form }">
        <BpmNodeEditor v-model="form.nodes" />
      </template>
    </ProTable>

    <!-- 版本历史：按 key 拉全部版本（含草稿/发布/停用），新→旧。 -->
    <el-dialog v-model="historyVisible" title="版本历史" width="720px" append-to-body>
      <el-table :data="historyRows" border size="small" v-loading="historyLoading">
        <el-table-column prop="version" label="版本" width="70" />
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status)" size="small">{{ statusText(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="节点数" width="80">
          <template #default="{ row }">{{ (row.nodes || []).length }}</template>
        </el-table-column>
        <el-table-column label="发布时间" min-width="160">
          <template #default="{ row }">{{ formatTime(row.publishedAt) || '-' }}</template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="160">
          <template #default="{ row }">{{ formatTime(row.createTime) }}</template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import BpmNodeEditor from '@/components/BpmNodeEditor.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import {
  pageDefinitions,
  getDefinition,
  createDefinition,
  updateDefinition,
  deleteDefinition,
  publishDefinition,
  disableDefinition,
} from '@/api/bpm'

// 流程定义管理页（issue #44，spec §4.1/§4.2）：草稿 CRUD、发布/停用
// 状态机（同 key 单发布版本，发布新版本自动停用旧版本）、版本历史。
// key/businessType 创建后不可变（表单仅新增时可见）；操作列按状态展示。

const tableRef = ref()
const userStore = useUserStore()
const canUpdate = computed(() => userStore.hasPerm('bpm:definition:update'))
const canDelete = computed(() => userStore.hasPerm('bpm:definition:delete'))
const canPublish = computed(() => userStore.hasPerm('bpm:definition:publish'))

const statusOptions = [
  { label: '草稿', value: 0 },
  { label: '已发布', value: 1 },
  { label: '已停用', value: 2 },
]

const searchItems = [
  { prop: 'key', label: '流程标识' },
  { prop: 'name', label: '流程名称' },
  { prop: 'businessType', label: '业务类型' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'key', label: '流程标识', minWidth: 140 },
  { prop: 'version', label: '版本', width: 70 },
  { prop: 'name', label: '流程名称', minWidth: 140 },
  { prop: 'businessType', label: '业务类型', minWidth: 150 },
  { prop: 'nodes', label: '节点', width: 90, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  {
    prop: 'publishedAt',
    label: '发布时间',
    minWidth: 160,
    formatter: (row: any) => formatTime(row.publishedAt) || '-',
  },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 160,
    formatter: (row: any) => formatTime(row.createTime),
  },
  { prop: 'actions', label: '操作', width: 220, slot: true },
]

const formItems = [
  { prop: 'key', label: '流程标识', required: true, visibleOn: 'create' },
  { prop: 'name', label: '流程名称', required: true },
  { prop: 'businessType', label: '业务类型', required: true, visibleOn: 'create' },
  { prop: 'nodes', label: '审批节点', required: true, slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm: Record<string, any> = { key: '', name: '', businessType: '', remark: '' }

const api = {
  page: pageDefinitions,
  detail: getDefinition,
  create: createDefinition,
  update: updateDefinition,
  remove: deleteDefinition,
}

function statusText(status: number): string {
  return statusOptions.find((o) => o.value === status)?.label ?? `未知(${status})`
}
function statusTag(status: number): string {
  if (status === 0) return 'info'
  if (status === 1) return 'success'
  return 'danger'
}

async function doPublish(row: any) {
  await ElMessageBox.confirm(
    `确认发布「${row.name}」v${row.version} 吗？同 key 已发布版本将自动停用，发布后不可修改。`,
    '发布确认',
    { type: 'warning' },
  )
  await publishDefinition(row.id)
  ElMessage.success('发布成功')
  tableRef.value?.reload()
}

async function doDisable(row: any) {
  await ElMessageBox.confirm(
    `确认停用「${row.name}」v${row.version} 吗？停用后不可发起新实例，在途实例继续。`,
    '停用确认',
    { type: 'warning' },
  )
  await disableDefinition(row.id)
  ElMessage.success('已停用')
  tableRef.value?.reload()
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除草稿「${row.name}」v${row.version} 吗？`, '删除确认', {
    type: 'warning',
  })
  await deleteDefinition(row.id)
  ElMessage.success('删除成功')
  tableRef.value?.reload()
}

// 编辑走 ProTable 的编辑弹窗（回显详情），入口由本页按状态机控制（仅草稿）。
function openEdit(row: any) {
  tableRef.value?.openEdit(row)
}

// 版本历史（spec §5：定义列表 → 版本历史）。
const historyVisible = ref(false)
const historyLoading = ref(false)
const historyRows = ref<any[]>([])

async function openHistory(row: any) {
  historyVisible.value = true
  historyLoading.value = true
  try {
    const data = await pageDefinitions({ key: row.key, pageNo: 1, pageSize: 100 })
    historyRows.value = (data.list || []).sort((a: any, b: any) => b.version - a.version)
  } finally {
    historyLoading.value = false
  }
}
</script>
