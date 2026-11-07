<template>
  <div v-loading="loading" class="app-container">
    <!-- 四时值卡片：今日/昨日/本月/今年，每张卡同时展示销售额与采购额 -->
    <el-row :gutter="16">
      <el-col v-for="card in cards" :key="card.key" :xs="12" :sm="12" :md="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-head">
            <div class="stat-icon" :class="card.color">
              <el-icon :size="22"><component :is="card.icon" /></el-icon>
            </div>
            <span class="stat-label">{{ card.label }}</span>
          </div>
          <div class="stat-line">
            <span class="stat-tag sale">销售额</span>
            <span class="stat-value">{{ formatMoney(card.sale) }}</span>
          </div>
          <div class="stat-line">
            <span class="stat-tag purchase">采购额</span>
            <span class="stat-value purchase-value">{{ formatMoney(card.purchase) }}</span>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="row-gap">
      <el-col :span="24">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span>近 {{ trendMonths }} 个月销售/采购趋势</span>
              <div class="legend">
                <span v-if="trendRange" class="legend-range">{{ trendRange }}</span>
                <span class="legend-item"><i class="legend-dot sale" />销售额</span>
                <span class="legend-item"><i class="legend-dot purchase" />采购额</span>
              </div>
            </div>
          </template>
          <svg
            v-if="trend.length"
            class="trend-svg"
            :viewBox="`0 0 ${chartWidth} ${chartHeight}`"
            role="img"
            :aria-label="`近 ${trendMonths} 个月销售额与采购额趋势`"
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
                :x="bar.saleX"
                :y="bar.saleY"
                :width="bar.width"
                :height="bar.saleHeight"
                rx="2"
                class="bar-sale"
              >
                <title>{{ bar.month }} 销售额 {{ formatMoney(bar.saleAmount) }}</title>
              </rect>
              <rect
                :x="bar.purchaseX"
                :y="bar.purchaseY"
                :width="bar.width"
                :height="bar.purchaseHeight"
                rx="2"
                class="bar-purchase"
              >
                <title>{{ bar.month }} 采购额 {{ formatMoney(bar.purchaseAmount) }}</title>
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
import { Calendar, Coin, Tickets, Warning } from '@element-plus/icons-vue'
import {
  getPurchaseStatisticsSummary,
  getPurchaseStatisticsTimeSummary,
  getSaleStatisticsSummary,
  getSaleStatisticsTimeSummary,
} from '@/api/erp'

// ERP 首页统计（issue #95，docs/erp-mvp-spec.md §5/§6）：四卡片（今日/昨日/
// 本月/今年，各展示销售额与采购额）+ 近 N 个月双序列柱状趋势。图表做法复用
// CRM 仪表盘（内联 SVG，不引入图表依赖）；金额全程按十进制字符串处理
//（ADR 0007：前端不得把金额先转成浮点参与展示与合计）。

interface StatisticsSummary {
  today: string
  yesterday: string
  month: string
  year: string
}

interface StatisticsTrendPoint {
  month: string
  amount: string
}

interface StatisticsTimeSummary {
  months: number
  list: StatisticsTrendPoint[]
}

const loading = ref(false)
const trendMonths = ref(6)
const saleSummary = reactive<StatisticsSummary>({
  today: '0.00',
  yesterday: '0.00',
  month: '0.00',
  year: '0.00',
})
const purchaseSummary = reactive<StatisticsSummary>({
  today: '0.00',
  yesterday: '0.00',
  month: '0.00',
  year: '0.00',
})
const saleTrend = ref<StatisticsTrendPoint[]>([])
const purchaseTrend = ref<StatisticsTrendPoint[]>([])

// 金额展示全程按字符串处理（ADR 0007）。
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
    key: 'today',
    label: '今日',
    sale: saleSummary.today,
    purchase: purchaseSummary.today,
    icon: Coin,
    color: 'blue',
  },
  {
    key: 'yesterday',
    label: '昨日',
    sale: saleSummary.yesterday,
    purchase: purchaseSummary.yesterday,
    icon: Calendar,
    color: 'purple',
  },
  {
    key: 'month',
    label: '本月',
    sale: saleSummary.month,
    purchase: purchaseSummary.month,
    icon: Tickets,
    color: 'green',
  },
  {
    key: 'year',
    label: '今年',
    sale: saleSummary.year,
    purchase: purchaseSummary.year,
    icon: Warning,
    color: 'orange',
  },
])

// 销售趋势提供月份轴；采购金额按月份键合并（两个接口月份序列一致）。
const trend = computed(() => {
  const purchaseByMonth = new Map(purchaseTrend.value.map((point) => [point.month, point.amount]))
  return saleTrend.value.map((point) => ({
    month: point.month,
    saleAmount: point.amount,
    purchaseAmount: purchaseByMonth.get(point.month) ?? '0.00',
  }))
})

const trendRange = computed(() =>
  trend.value.length ? `${trend.value[0].month} 至 ${trend.value[trend.value.length - 1].month}` : '',
)

// 内联 SVG 柱状图（不引入图表依赖）：固定 viewBox，容器宽度自适应。
const chartWidth = 960
const chartHeight = 280
const chartLeft = 72
const chartRight = 16
const chartTop = 16
const chartBottom = 36
const plotHeight = chartHeight - chartTop - chartBottom

const chartMaxCents = computed(() => {
  const values = trend.value.flatMap((point) => [
    toCents(point.saleAmount),
    toCents(point.purchaseAmount),
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
  const points = trend.value
  const groupWidth = (chartWidth - chartLeft - chartRight) / Math.max(points.length, 1)
  const width = Math.min(20, Math.max(6, (groupWidth - 24) / 2))
  const gap = 3
  return points.map((point, index) => {
    const centerX = chartLeft + groupWidth * (index + 0.5)
    const saleHeight = Math.round((toCents(point.saleAmount) / chartMaxCents.value) * plotHeight)
    const purchaseHeight = Math.round(
      (toCents(point.purchaseAmount) / chartMaxCents.value) * plotHeight,
    )
    const startX = centerX - (width * 2 + gap) / 2
    return {
      month: point.month,
      label: `${point.month.slice(5)}月`,
      centerX,
      width,
      saleX: startX,
      saleY: chartTop + plotHeight - saleHeight,
      saleHeight,
      purchaseX: startX + width + gap,
      purchaseY: chartTop + plotHeight - purchaseHeight,
      purchaseHeight,
      saleAmount: point.saleAmount,
      purchaseAmount: point.purchaseAmount,
    }
  })
})

async function loadStatistics() {
  loading.value = true
  try {
    const [saleSum, purchaseSum, saleTime, purchaseTime] = await Promise.all([
      getSaleStatisticsSummary(),
      getPurchaseStatisticsSummary(),
      getSaleStatisticsTimeSummary({ months: trendMonths.value }),
      getPurchaseStatisticsTimeSummary({ months: trendMonths.value }),
    ])
    Object.assign(saleSummary, saleSum || {})
    Object.assign(purchaseSummary, purchaseSum || {})
    saleTrend.value = (saleTime as StatisticsTimeSummary)?.list || []
    purchaseTrend.value = (purchaseTime as StatisticsTimeSummary)?.list || []
    const months = Number((saleTime as StatisticsTimeSummary)?.months)
    if (Number.isInteger(months) && months > 0) trendMonths.value = months
  } finally {
    loading.value = false
  }
}

onMounted(loadStatistics)
</script>

<style scoped>
.stat-card :deep(.el-card__body) {
  padding: 18px 20px;
}

.stat-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

.stat-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
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

.stat-icon.orange {
  background: rgba(230, 162, 60, 0.14);
  color: #e6a23c;
}

.stat-label {
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}

.stat-line {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  line-height: 1.9;
}

.stat-tag {
  font-size: 12px;
  color: #909399;
  flex-shrink: 0;
}

.stat-tag.sale {
  color: #409eff;
}

.stat-tag.purchase {
  color: #e6a23c;
}

.stat-value {
  font-size: 20px;
  font-weight: 700;
  word-break: break-all;
  font-variant-numeric: tabular-nums;
}

.purchase-value {
  color: #b88230;
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

.legend-dot.sale {
  background: #409eff;
}

.legend-dot.purchase {
  background: #e6a23c;
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

.bar-sale {
  fill: #409eff;
}

.bar-purchase {
  fill: #e6a23c;
}
</style>
