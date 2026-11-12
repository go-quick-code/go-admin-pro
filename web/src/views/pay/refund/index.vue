<template>
  <div class="app-container">
    <!-- 退款单页（issue #135，spec §6）：只读列表（append-only，spec §4.10
        ——无新增/编辑/删除按钮）；行点击打开详情抽屉（含关联订单信息）。 -->
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="pay:refund"
      title="退款单"
      @row-click="openDetail"
    >
      <template #toolbar>
        <!-- 导出（issue #138，spec §4.12）：复用列表筛选口径，pay:refund:export 显隐。 -->
        <ExcelActions
          title="退款单"
          export-perm="pay:refund:export"
          :export-action="exportPayRefunds"
          :export-params="exportParams"
        />
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-refundPrice="{ row }">
        <span class="amount">{{ row.refundPrice }}</span>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openDetail(row)">详情</el-button>
      </template>
    </ProTable>

    <el-drawer v-model="detailVisible" :title="detail?.no || '退款单详情'" size="720px">
      <el-descriptions v-if="detail" :column="1" border>
        <el-descriptions-item label="退款单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="消费方退款单号">{{ detail.merchantRefundId }}</el-descriptions-item>
        <el-descriptions-item label="应用 ID">{{ detail.appId }}</el-descriptions-item>
        <el-descriptions-item label="退款金额">{{ detail.refundPrice }}</el-descriptions-item>
        <el-descriptions-item label="原单实付">{{ detail.payPrice }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="statusTagType(detail.status)" size="small">{{ statusLabel(detail.status) }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="退款渠道">{{ detail.channelCode || '-' }}</el-descriptions-item>
        <el-descriptions-item label="渠道退款单号">{{ detail.channelRefundNo || '-' }}</el-descriptions-item>
        <el-descriptions-item label="退款原因">{{ detail.reason || '-' }}</el-descriptions-item>
        <el-descriptions-item label="回调地址">{{ detail.notifyUrl || '-' }}</el-descriptions-item>
        <el-descriptions-item label="下单用户">类型 {{ detail.userType }} / ID {{ detail.userId }}</el-descriptions-item>
        <el-descriptions-item label="退款成功时间">{{ formatTime(detail.successTime) || '-' }}</el-descriptions-item>
        <el-descriptions-item v-if="detail.channelErrorCode" label="渠道错误码">{{ detail.channelErrorCode }}</el-descriptions-item>
        <el-descriptions-item v-if="detail.channelErrorMsg" label="渠道错误信息">{{ detail.channelErrorMsg }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
      </el-descriptions>

      <template v-if="detail?.order">
        <div class="section-title">关联订单</div>
        <el-descriptions :column="1" border>
          <el-descriptions-item label="订单号">{{ detail.order.no }}</el-descriptions-item>
          <el-descriptions-item label="消费方单号">{{ detail.order.merchantOrderId }}</el-descriptions-item>
          <el-descriptions-item label="商品标题">{{ detail.order.subject }}</el-descriptions-item>
          <el-descriptions-item label="实付金额">{{ detail.order.price }}</el-descriptions-item>
          <el-descriptions-item label="订单状态">
            <el-tag :type="orderTagType(detail.order.status)" size="small">
              {{ orderLabel(detail.order.status) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="支付渠道">{{ detail.order.channelCode || '-' }}</el-descriptions-item>
          <el-descriptions-item label="已退累计">{{ detail.order.refundPrice }}</el-descriptions-item>
          <el-descriptions-item label="支付成功时间">{{ formatTime(detail.order.successTime) || '-' }}</el-descriptions-item>
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
import { pagePayRefunds, getPayRefund, exportPayRefunds } from '@/api/pay'

// 退款单页（issue #135，spec §4.4/§6）：交易单据 append-only 只读列表，
// 详情抽屉展示退款单与关联订单（原路退回：渠道/实付以订单为准）。
// 退款单状态：0 待处理 / 10 成功 / 20 失败；订单状态：0 待支付 / 10 成功 /
// 20 已退款 / 30 已关闭。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('pay:refund:query'))

const statusLabels: Record<number, string> = { 0: '待处理', 10: '成功', 20: '失败' }
function statusLabel(status: number) {
  return statusLabels[status] ?? String(status)
}
function statusTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 20) return 'danger'
  return 'info'
}

const orderLabels: Record<number, string> = { 0: '待支付', 10: '成功', 20: '已退款', 30: '已关闭' }
function orderLabel(status: number) {
  return orderLabels[status] ?? String(status)
}
function orderTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 20 || status === 30) return 'info'
  return 'warning'
}

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'no', label: '退款单号', minWidth: 180, showOverflowTooltip: true },
  { prop: 'merchantRefundId', label: '消费方退款单号', minWidth: 160, showOverflowTooltip: true },
  { prop: 'orderNo', label: '原订单号', minWidth: 180, showOverflowTooltip: true },
  { prop: 'refundPrice', label: '退款金额', width: 110, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'channelCode', label: '渠道', width: 100 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'no', label: '退款单号' },
  { prop: 'merchantRefundId', label: '消费方退款单号' },
  { prop: 'orderNo', label: '原订单号' },
  { prop: 'channelCode', label: '渠道' },
  {
    prop: 'status',
    label: '状态',
    type: 'select',
    options: [
      { label: '待处理', value: 0 },
      { label: '成功', value: 10 },
      { label: '失败', value: 20 },
    ],
  },
]

const api = { page: pagePayRefunds }

const tableRef = ref()

// 导出参数：取当前列表筛选（分页参数与空值由 ExcelActions 剔除）。
function exportParams() {
  const q = tableRef.value?.query || {}
  return { no: q.no, merchantRefundId: q.merchantRefundId, channelCode: q.channelCode, status: q.status }
}

// ---------- 详情抽屉 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)

async function openDetail(row: any) {
  detailVisible.value = true
  detail.value = row
  if (canQuery.value && row.id) {
    const data = await getPayRefund(row.id)
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
