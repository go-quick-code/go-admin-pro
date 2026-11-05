<template>
  <div v-loading="loading" class="app-container">
    <el-row :gutter="16">
      <el-col v-for="card in cards" :key="card.key" :xs="12" :sm="12" :md="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon" :class="card.color">
            <el-icon :size="24"><component :is="card.icon" /></el-icon>
          </div>
          <div class="stat-body">
            <div class="stat-value">{{ card.value }}</div>
            <div class="stat-label">{{ card.label }}</div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="row-gap">
      <el-col :md="10" :xs="24">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span>商机阶段漏斗</span>
              <el-tag size="small" type="info">stage 1–6</el-tag>
            </div>
          </template>
          <div v-if="funnel.length" class="funnel">
            <div v-for="item in funnel" :key="item.stage" class="funnel-row">
              <span class="funnel-label">{{ item.stageName }}</span>
              <div class="funnel-track">
                <div class="funnel-bar" :style="{ width: funnelWidth(item) }" />
              </div>
              <span class="funnel-meta">{{ item.count }} 个 · {{ formatMoney(item.amount) }}</span>
            </div>
          </div>
          <el-empty v-else description="暂无商机数据" :image-size="60" />
        </el-card>
      </el-col>

      <el-col :md="14" :xs="24">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span>近 12 个月合同/回款趋势</span>
              <div class="legend">
                <span v-if="trendRange" class="legend-range">{{ trendRange }}</span>
                <span class="legend-item"><i class="legend-dot contract" />已签合同</span>
                <span class="legend-item"><i class="legend-dot receipt" />回款</span>
              </div>
            </div>
          </template>
          <svg
            v-if="trend.length"
            class="trend-svg"
            :viewBox="`0 0 ${chartWidth} ${chartHeight}`"
            role="img"
            aria-label="近 12 个月已签合同与回款趋势"
          >
            <g v-for="tick in yTicks" :key="`tick-${tick.label}-${tick.y}`">
              <line
                :x1="chartLeft"
                :x2="chartWidth - chartRight"
                :y1="tick.y"
                :y2="tick.y"
                class="grid-line"
              />
              <text :x="chartLeft - 8" :y="tick.y + 4" text-anchor="end" class="axis-label">
                {{ tick.label }}
              </text>
            </g>
            <g v-for="bar in trendBars" :key="bar.month">
              <rect
                :x="bar.contractX"
                :y="bar.contractY"
                :width="bar.width"
                :height="bar.contractHeight"
                rx="2"
                class="bar-contract"
              >
                <title>{{ bar.month }} 已签合同 {{ formatMoney(bar.contractAmount) }}</title>
              </rect>
              <rect
                :x="bar.receiptX"
                :y="bar.receiptY"
                :width="bar.width"
                :height="bar.receiptHeight"
                rx="2"
                class="bar-receipt"
              >
                <title>{{ bar.month }} 回款 {{ formatMoney(bar.receiptAmount) }}</title>
              </rect>
              <text
                :x="bar.centerX"
                :y="chartHeight - chartBottom + 18"
                text-anchor="middle"
                class="axis-label"
              >
                {{ bar.label }}
              </text>
            </g>
          </svg>
          <el-empty v-else description="暂无趋势数据" :image-size="60" />
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { Coin, Tickets, User, Warning } from '@element-plus/icons-vue'
import { getDashboardSummary } from '@/api/crm'

interface DashboardCards {
  customerCount: number
  inProgressOpportunityAmount: string
  signedContractAmount: string
  overdueReceivableCount: number
  overdueReceivableAmount: string
}

interface DashboardFunnelItem {
  stage: number
  stageName: string
  count: number
  amount: string
}

interface DashboardTrendPoint {
  month: string
  contractAmount: string
  receiptAmount: string
}

const loading = ref(false)
const summary = reactive<{
  cards: DashboardCards
  funnel: DashboardFunnelItem[]
  trend: DashboardTrendPoint[]
}>({
  cards: {
    customerCount: 0,
    inProgressOpportunityAmount: '0.00',
    signedContractAmount: '0.00',
    overdueReceivableCount: 0,
    overdueReceivableAmount: '0.00',
  },
  funnel: [],
  trend: [],
})

// 金额展示全程按字符串处理（ADR 0007：前端不得把十进制金额先转成浮点）。
function formatMoney(value?: string | number | null) {
  if (value === null || value === undefined || value === '') return '0.00'
  const text = String(value)
  const negative = text.startsWith('-')
  const body = negative ? text.slice(1) : text
  const [intPart = '0', fracPart = ''] = body.split('.')
  const grouped = (intPart || '0').replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${negative ? '-' : ''}${grouped}.${(fracPart + '00').slice(0, 2)}`
}

// toCents 只用于图表几何比例（柱高/轴刻度），不参与金额展示与合计。
function toCents(value?: string | number | null) {
  if (value === null || value === undefined || value === '') return 0
  const text = String(value)
  const negative = text.startsWith('-')
  const body = negative ? text.slice(1) : text
  const [intPart = '0', fracPart = ''] = body.split('.')
  const cents =
    Number.parseInt(intPart || '0', 10) * 100 + Number.parseInt((fracPart + '00').slice(0, 2), 10)
  return negative ? -cents : cents
}

function trimNumber(value: number) {
  if (Number.isInteger(value)) return String(value)
  return value.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')
}

function axisMoney(cents: number) {
  const yuan = cents / 100
  if (yuan >= 100_000_000) return `${trimNumber(Math.round((yuan / 100_000_000) * 10) / 10)}亿`
  if (yuan >= 10_000) return `${trimNumber(Math.round((yuan / 10_000) * 10) / 10)}万`
  return trimNumber(yuan)
}

const cards = computed(() => [
  {
    key: 'customerCount',
    label: '客户总数',
    value: String(summary.cards.customerCount ?? 0),
    icon: User,
    color: 'blue',
  },
  {
    key: 'inProgressOpportunityAmount',
    label: '进行中商机金额（元）',
    value: formatMoney(summary.cards.inProgressOpportunityAmount),
    icon: Coin,
    color: 'purple',
  },
  {
    key: 'signedContractAmount',
    label: '已签合同金额（元）',
    value: formatMoney(summary.cards.signedContractAmount),
    icon: Tickets,
    color: 'green',
  },
  {
    key: 'overdue',
    label: '逾期回款（期 / 未收金额 元）',
    value: `${summary.cards.overdueReceivableCount ?? 0} 期 / ${formatMoney(summary.cards.overdueReceivableAmount)}`,
    icon: Warning,
    color: 'red',
  },
])

const funnel = computed(() => summary.funnel)
const trend = computed(() => summary.trend)
const trendRange = computed(() =>
  summary.trend.length
    ? `${summary.trend[0].month} 至 ${summary.trend[summary.trend.length - 1].month}`
    : '',
)

const funnelMaxAmount = computed(() =>
  Math.max(0, ...summary.funnel.map((item) => toCents(item.amount))),
)
const funnelMaxCount = computed(() =>
  Math.max(0, ...summary.funnel.map((item) => Number(item.count || 0))),
)

// 漏斗条形：优先按金额成比例（金额全为 0 时退回按数量），最小可见宽度 4%。
function funnelWidth(item: DashboardFunnelItem) {
  if (funnelMaxAmount.value > 0) {
    const ratio = toCents(item.amount) / funnelMaxAmount.value
    return ratio > 0 ? `${Math.max(ratio * 100, 4)}%` : '0%'
  }
  const ratio = Number(item.count || 0) / Math.max(funnelMaxCount.value, 1)
  return ratio > 0 ? `${Math.max(ratio * 100, 4)}%` : '0%'
}

// 内联 SVG 柱状图（不引入图表依赖）：固定 viewBox，容器宽度自适应。
const chartWidth = 720
const chartHeight = 260
const chartLeft = 64
const chartRight = 16
const chartTop = 16
const chartBottom = 36
const plotHeight = chartHeight - chartTop - chartBottom

const chartMaxCents = computed(() => {
  const values = summary.trend.flatMap((point) => [
    toCents(point.contractAmount),
    toCents(point.receiptAmount),
  ])
  const max = Math.max(0, ...values)
  return max > 0 ? max : 100
})

const yTicks = computed(() =>
  [0, 0.5, 1].map((ratio) => ({
    ratio,
    y: chartTop + plotHeight - ratio * plotHeight,
    label: axisMoney(Math.round(chartMaxCents.value * ratio)),
  })),
)

const trendBars = computed(() => {
  const points = summary.trend
  const groupWidth = (chartWidth - chartLeft - chartRight) / Math.max(points.length, 1)
  const width = Math.min(14, Math.max(4, (groupWidth - 16) / 2))
  const gap = 2
  return points.map((point, index) => {
    const centerX = chartLeft + groupWidth * (index + 0.5)
    const contractHeight = Math.round(
      (toCents(point.contractAmount) / chartMaxCents.value) * plotHeight,
    )
    const receiptHeight = Math.round((toCents(point.receiptAmount) / chartMaxCents.value) * plotHeight)
    const startX = centerX - (width * 2 + gap) / 2
    return {
      month: point.month,
      label: `${point.month.slice(5)}月`,
      centerX,
      width,
      contractX: startX,
      contractY: chartTop + plotHeight - contractHeight,
      contractHeight,
      receiptX: startX + width + gap,
      receiptY: chartTop + plotHeight - receiptHeight,
      receiptHeight,
      contractAmount: point.contractAmount,
      receiptAmount: point.receiptAmount,
    }
  })
})

async function loadSummary() {
  loading.value = true
  try {
    const data = await getDashboardSummary()
    summary.cards = { ...summary.cards, ...(data?.cards || {}) }
    summary.funnel = data?.funnel || []
    summary.trend = data?.trend || []
  } finally {
    loading.value = false
  }
}

onMounted(loadSummary)
</script>

<style scoped>
.stat-card :deep(.el-card__body) {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
}

.stat-icon {
  width: 52px;
  height: 52px;
  border-radius: 14px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.stat-icon.blue {
  background: rgba(64, 158, 255, 0.12);
  color: #409eff;
}

.stat-icon.purple {
  background: rgba(124, 108, 245, 0.12);
  color: #7c6cf5;
}

.stat-icon.green {
  background: rgba(103, 194, 58, 0.12);
  color: #67c23a;
}

.stat-icon.red {
  background: rgba(245, 108, 108, 0.12);
  color: #f56c6c;
}

.stat-body {
  min-width: 0;
}

.stat-value {
  font-size: 22px;
  font-weight: 700;
  line-height: 1.25;
  word-break: break-all;
}

.stat-label {
  color: #909399;
  font-size: 13px;
  margin-top: 2px;
}

.row-gap {
  margin-top: 16px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-weight: 600;
}

.legend {
  display: flex;
  align-items: center;
  gap: 12px;
  font-weight: 400;
  font-size: 12px;
  color: #606266;
}

.legend-range {
  color: #909399;
}

.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.legend-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}

.legend-dot.contract {
  background: #409eff;
}

.legend-dot.receipt {
  background: #67c23a;
}

.funnel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 4px 0;
}

.funnel-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.funnel-label {
  width: 40px;
  flex-shrink: 0;
  color: #606266;
  font-size: 13px;
}

.funnel-track {
  flex: 1;
  height: 22px;
  border-radius: 11px;
  background: #f5f7fa;
  display: flex;
  justify-content: center;
  overflow: hidden;
}

.funnel-bar {
  height: 100%;
  border-radius: 11px;
  background: linear-gradient(90deg, #409eff, #7c6cf5);
  transition: width 0.4s ease;
}

.funnel-meta {
  width: 138px;
  flex-shrink: 0;
  text-align: right;
  color: #606266;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.trend-svg {
  width: 100%;
  height: auto;
  display: block;
}

.grid-line {
  stroke: #ebeef5;
  stroke-width: 1;
}

.axis-label {
  fill: #909399;
  font-size: 11px;
}

.bar-contract {
  fill: #409eff;
}

.bar-receipt {
  fill: #67c23a;
}
</style>
