<template>
  <div class="app-container">
    <!-- 转账单页（issue #137，spec §6）：只读列表（append-only，spec §4.10
        ——无新增/编辑/删除按钮；导出随 #138）；行点击打开详情抽屉
        （含关联应用信息）。 -->
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="pay:transfer"
      title="转账单"
      @row-click="openDetail"
    >
      <template #toolbar>
        <!-- 导出（issue #138，spec §4.12）：复用列表筛选口径，pay:transfer:export 显隐。 -->
        <ExcelActions
          title="转账单"
          export-perm="pay:transfer:export"
          :export-action="exportPayTransfers"
          :export-params="exportParams"
        />
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-price="{ row }">
        <span class="amount">{{ row.price }}</span>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openDetail(row)">详情</el-button>
      </template>
    </ProTable>

    <el-drawer v-model="detailVisible" :title="detail?.no || '转账单详情'" size="720px">
      <el-descriptions v-if="detail" :column="1" border>
        <el-descriptions-item label="转账单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="消费方转账单号">{{ detail.merchantTransferId }}</el-descriptions-item>
        <el-descriptions-item label="应用 ID">{{ detail.appId }}</el-descriptions-item>
        <el-descriptions-item label="转账金额">{{ detail.price }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="statusTagType(detail.status)" size="small">{{ statusLabel(detail.status) }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="转账渠道">{{ detail.channelCode || '-' }}</el-descriptions-item>
        <el-descriptions-item label="收款账号">{{ detail.userAccount || '-' }}</el-descriptions-item>
        <el-descriptions-item label="收款人">{{ detail.userName || '-' }}</el-descriptions-item>
        <el-descriptions-item label="归属用户">
          {{ detail.userId ? `类型 ${detail.userType} / ID ${detail.userId}` : '外部账户（无归属）' }}
        </el-descriptions-item>
        <el-descriptions-item label="渠道转账回执">{{ detail.channelTransferNo || '-' }}</el-descriptions-item>
        <el-descriptions-item label="回调地址">{{ detail.notifyUrl || '-' }}</el-descriptions-item>
        <el-descriptions-item label="转账成功时间">{{ formatTime(detail.successTime) || '-' }}</el-descriptions-item>
        <el-descriptions-item v-if="detail.channelErrorCode" label="渠道错误码">{{ detail.channelErrorCode }}</el-descriptions-item>
        <el-descriptions-item v-if="detail.channelErrorMsg" label="渠道错误信息">{{ detail.channelErrorMsg }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
      </el-descriptions>

      <template v-if="detail?.app">
        <div class="section-title">关联应用</div>
        <el-descriptions :column="1" border>
          <el-descriptions-item label="应用名称">{{ detail.app.name }}</el-descriptions-item>
          <el-descriptions-item label="应用标识">{{ detail.app.appKey }}</el-descriptions-item>
        </el-descriptions>
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { pagePayTransfers, getPayTransfer, exportPayTransfers } from '@/api/pay'

// 转账单页（issue #137，spec §4.8/§6）：交易单据 append-only 只读列表，
// 详情抽屉展示转账单与关联应用。状态：0 等待 / 5 进行中 / 10 成功 / 20 关闭。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('pay:transfer:query'))

const statusLabels: Record<number, string> = { 0: '等待', 5: '进行中', 10: '成功', 20: '关闭' }
function statusLabel(status: number) {
  return statusLabels[status] ?? String(status)
}
function statusTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 20 || status === 5) return 'info'
  return 'warning'
}

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'no', label: '转账单号', minWidth: 180, showOverflowTooltip: true },
  { prop: 'merchantTransferId', label: '消费方转账单号', minWidth: 160, showOverflowTooltip: true },
  { prop: 'subject', label: '转账标题', minWidth: 140, showOverflowTooltip: true },
  { prop: 'price', label: '转账金额', width: 110, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'channelCode', label: '渠道', width: 100 },
  { prop: 'userAccount', label: '收款账号', minWidth: 120, showOverflowTooltip: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'no', label: '转账单号' },
  { prop: 'merchantTransferId', label: '消费方转账单号' },
  { prop: 'channelCode', label: '渠道' },
  {
    prop: 'status',
    label: '状态',
    type: 'select',
    options: [
      { label: '等待', value: 0 },
      { label: '进行中', value: 5 },
      { label: '成功', value: 10 },
      { label: '关闭', value: 20 },
    ],
  },
]

const api = { page: pagePayTransfers }

const tableRef = ref()

// 导出参数：取当前列表筛选（分页参数与空值由 ExcelActions 剔除）。
function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, merchantTransferId: q.merchantTransferId, channelCode: q.channelCode, status: q.status }
}

// ---------- 详情抽屉 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)

async function openDetail(row: any) {
  detailVisible.value = true
  detail.value = row
  if (canQuery.value && row.id) {
    const data = await getPayTransfer(row.id)
    if (data) detail.value = data
  }
}
</script>

<style scoped>
.amount {
  font-variant-numeric: tabular-nums;
}

.section-title {
  margin: 16px 0 8px;
  font-weight: 600;
}
</style>
