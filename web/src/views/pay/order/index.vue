<template>
  <div class="app-container">
    <!-- 支付订单页（issue #133，spec §6）：只读列表（append-only，spec §4.10
        ——无新增/编辑/删除按钮）；行点击打开详情抽屉（含最近一次发起 extension）。 -->
    <ProTable
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="pay:order"
      title="支付订单"
      @row-click="openDetail"
    >
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

    <el-drawer v-model="detailVisible" :title="detail?.no || '订单详情'" size="720px">
      <el-descriptions v-if="detail" :column="1" border>
        <el-descriptions-item label="订单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="消费方单号">{{ detail.merchantOrderId }}</el-descriptions-item>
        <el-descriptions-item label="应用 ID">{{ detail.appId }}</el-descriptions-item>
        <el-descriptions-item label="商品标题">{{ detail.subject }}</el-descriptions-item>
        <el-descriptions-item label="商品描述">{{ detail.body || '-' }}</el-descriptions-item>
        <el-descriptions-item label="实付金额">{{ detail.price }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="statusTagType(detail.status)" size="small">{{ statusLabel(detail.status) }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="支付渠道">{{ detail.channelCode || '-' }}</el-descriptions-item>
        <el-descriptions-item label="渠道单号">{{ detail.channelOrderNo || '-' }}</el-descriptions-item>
        <el-descriptions-item label="回调地址">{{ detail.notifyUrl || '-' }}</el-descriptions-item>
        <el-descriptions-item label="下单用户">类型 {{ detail.userType }} / ID {{ detail.userId }}</el-descriptions-item>
        <el-descriptions-item label="过期时间">{{ formatTime(detail.expireTime) || '-' }}</el-descriptions-item>
        <el-descriptions-item label="支付成功时间">{{ formatTime(detail.successTime) || '-' }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
      </el-descriptions>

      <template v-if="detail?.extension">
        <div class="section-title">最近一次发起（extension）</div>
        <el-descriptions :column="1" border>
          <el-descriptions-item label="发起单号">{{ detail.extension.no }}</el-descriptions-item>
          <el-descriptions-item label="渠道">{{ detail.extension.channelCode }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="extensionTagType(detail.extension.status)" size="small">
              {{ extensionLabel(detail.extension.status) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="错误信息">{{ detail.extension.channelErrorMsg || '-' }}</el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ formatTime(detail.extension.createTime) }}</el-descriptions-item>
        </el-descriptions>
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { pagePayOrders, getPayOrder } from '@/api/pay'

// 支付订单页（issue #133，spec §4.10/§6）：交易单据 append-only 只读列表，
// 详情抽屉展示订单与最近一次发起 extension（一次支付尝试一条）。
// 状态对齐芋道枚举：0 待支付 / 10 成功 / 20 已退款 / 30 已关闭。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('pay:order:query'))

const statusLabels: Record<number, string> = { 0: '待支付', 10: '成功', 20: '已退款', 30: '已关闭' }
function statusLabel(status: number) {
  return statusLabels[status] ?? String(status)
}
function statusTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 30 || status === 20) return 'info'
  return 'warning'
}

const extensionLabels: Record<number, string> = { 0: '处理中', 10: '成功', 20: '失败' }
function extensionLabel(status: number) {
  return extensionLabels[status] ?? String(status)
}
function extensionTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 20) return 'danger'
  return 'info'
}

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'no', label: '订单号', minWidth: 180, showOverflowTooltip: true },
  { prop: 'merchantOrderId', label: '消费方单号', minWidth: 150, showOverflowTooltip: true },
  { prop: 'subject', label: '商品标题', minWidth: 140, showOverflowTooltip: true },
  { prop: 'price', label: '实付金额', width: 110, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'channelCode', label: '渠道', width: 100 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'no', label: '订单号' },
  { prop: 'merchantOrderId', label: '消费方单号' },
  { prop: 'channelCode', label: '渠道' },
  {
    prop: 'status',
    label: '状态',
    type: 'select',
    options: [
      { label: '待支付', value: 0 },
      { label: '成功', value: 10 },
      { label: '已退款', value: 20 },
      { label: '已关闭', value: 30 },
    ],
  },
]

const api = { page: pagePayOrders }

// ---------- 详情抽屉 ----------
const detailVisible = ref(false)
const detail = ref<any>(null)

async function openDetail(row: any) {
  detailVisible.value = true
  detail.value = row
  if (canQuery.value && row.id) {
    const data = await getPayOrder(row.id)
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
