<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      title="公海"
      :show-toolbar="false"
      :action-width="100"
    >
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-poolEnterTime="{ row }">
        {{ formatTime(row.poolEnterTime) }}
      </template>
      <template #actions="{ row }">
        <el-button
          v-if="userStore.hasPerm('crm:customer:claim')"
          link
          type="primary"
          @click.stop="handleClaim(row)"
        >
          认领
        </el-button>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime, dictLabel } from '@/utils/format'
import { pagePoolCustomers, claimCustomer } from '@/api/crm'
import { useUserStore } from '@/stores/user'

// 公海（issue #13，docs/crm-mvp-spec.md §4.2）：无负责人客户池，
// 列表不受数据范围过滤；认领受认领上限约束（默认 50），先到先得。
// 客户状态对齐规格：1 潜在 / 2 活跃 / 3 流失。
const customerStatusOptions = [
  { label: '潜在', value: 1 },
  { label: '活跃', value: 2 },
  { label: '流失', value: 3 },
]

function statusLabel(status: number) {
  return dictLabel(customerStatusOptions, status)
}

function statusTagType(status: number) {
  if (status === 2) return 'success'
  if (status === 3) return 'danger'
  return 'info'
}

const tableRef = ref()
const userStore = useUserStore()

const searchItems = computed(() => [
  { prop: 'name', label: '客户名称' },
  { prop: 'status', label: '状态', type: 'select', options: customerStatusOptions },
])

const columns = [
  { prop: 'customerNo', label: '客户编号', width: 150 },
  { prop: 'name', label: '客户名称', minWidth: 160 },
  { prop: 'industry', label: '行业', width: 110 },
  { prop: 'level', label: '级别', width: 90 },
  { prop: 'source', label: '来源', width: 110 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'releaseReason', label: '释放原因', minWidth: 130 },
  {
    prop: 'poolEnterTime',
    label: '进入公海时间',
    minWidth: 170,
    slot: true,
  },
]

// 公海页只读列表：不提供新增/编辑/删除，仅认领。
const api = { page: pagePoolCustomers }

async function handleClaim(row: any) {
  const confirmed = await ElMessageBox.confirm(
    `认领「${row.name}」到自己名下？认领后该客户将按数据权限对你的角色可见。`,
    '认领客户',
    { type: 'warning' },
  ).catch(() => false)
  if (!confirmed) return
  await claimCustomer(row.id)
  ElMessage.success('认领成功')
  tableRef.value?.reload()
}
</script>
