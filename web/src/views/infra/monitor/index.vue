<template>
  <div class="app-container">
    <!-- 监控面板（issue #84，spec §4.10/§7.2）：仅平台租户（非平台 403）。
         卡片概览 + CSS 条形图（无图表库依赖）+ 30s 自动刷新（页面隐藏时暂停）。 -->
    <div class="header-bar">
      <span class="title">监控面板</span>
      <div>
        <span class="refresh-hint">{{ autoRefresh ? '每 30 秒自动刷新' : '自动刷新已暂停（页面隐藏）' }}</span>
        <el-button :icon="Refresh" circle size="small" @click="loadAll" />
      </div>
    </div>

    <!-- 应用信息 -->
    <el-card shadow="never" class="block">
      <template #header>应用</template>
      <el-descriptions :column="3" border size="small">
        <el-descriptions-item label="应用名">{{ overview?.app?.name || '-' }}</el-descriptions-item>
        <el-descriptions-item label="环境">{{ overview?.app?.env || '-' }}</el-descriptions-item>
        <el-descriptions-item label="版本">{{ overview?.app?.version || '-' }}</el-descriptions-item>
        <el-descriptions-item label="Go 版本">{{ overview?.app?.goVersion || '-' }}</el-descriptions-item>
        <el-descriptions-item label="启动时间">{{ overview?.app?.startTime || '-' }}</el-descriptions-item>
        <el-descriptions-item label="运行时长">{{ formatUptime(overview?.app?.uptimeSeconds) }}</el-descriptions-item>
      </el-descriptions>
    </el-card>

    <!-- 运行期 / 连接池 / 调度器 / 租户规模 -->
    <el-row :gutter="12" class="block">
      <el-col :span="6">
        <el-card shadow="never">
          <template #header>Go 运行期</template>
          <div class="stat-line"><span>goroutine 数</span><b>{{ overview?.runtime?.goroutines ?? '-' }}</b></div>
          <div class="stat-line"><span>堆内存</span><b>{{ formatBytes(overview?.runtime?.heapBytes) }}</b></div>
          <div class="stat-line"><span>向系统申请</span><b>{{ formatBytes(overview?.runtime?.sysBytes) }}</b></div>
          <div class="stat-line"><span>GC 次数</span><b>{{ overview?.runtime?.gcCount ?? '-' }}</b></div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="never">
          <template #header>DB 连接池</template>
          <div class="stat-line"><span>使用中 / 已打开</span><b>{{ overview?.dbPool?.inUse ?? '-' }} / {{ overview?.dbPool?.open ?? '-' }}</b></div>
          <div class="stat-line"><span>空闲</span><b>{{ overview?.dbPool?.idle ?? '-' }}</b></div>
          <div class="stat-line"><span>上限</span><b>{{ overview?.dbPool?.maxOpen ?? '-' }}</b></div>
          <div class="stat-line"><span>累计等待</span><b>{{ overview?.dbPool?.waitCount ?? '-' }} 次 / {{ overview?.dbPool?.waitMillis ?? '-' }} ms</b></div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="never">
          <template #header>调度器</template>
          <div class="stat-line"><span>任务总数</span><b>{{ overview?.jobs?.total ?? '-' }}</b></div>
          <div class="stat-line"><span>启用数</span><b>{{ overview?.jobs?.enabled ?? '-' }}</b></div>
          <div class="stat-line"><span>注册 handler</span><b>{{ overview?.jobs?.handlerCount ?? '-' }}</b></div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="never">
          <template #header>规模</template>
          <div class="stat-line"><span>租户数</span><b>{{ overview?.tenants?.count ?? '-' }}</b></div>
          <div class="stat-line"><span>用户数</span><b>{{ overview?.tenants?.users ?? '-' }}</b></div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 近 24h 流量 / 消息 -->
    <el-row :gutter="12" class="block">
      <el-col :span="12">
        <el-card shadow="never">
          <template #header>近 24h 访问</template>
          <div class="stat-line"><span>访问量</span><b>{{ overview?.traffic?.total ?? '-' }}</b></div>
          <div class="stat-line"><span>错误率（4xx+5xx）</span>
            <b :class="{ danger: (overview?.traffic?.errorRate ?? 0) > 0 }">{{ overview?.traffic?.errorRate ?? '-' }}%</b>
          </div>
          <div class="stat-line"><span>平均耗时</span><b>{{ overview?.traffic?.avgMs ?? '-' }} ms</b></div>
          <div class="stat-line"><span>最大耗时</span><b>{{ overview?.traffic?.maxMs ?? '-' }} ms</b></div>
        </el-card>
      </el-col>
      <el-col :span="12">
        <el-card shadow="never">
          <template #header>近 24h 消息</template>
          <div class="stat-line"><span>短信成功 / 失败</span>
            <b>{{ overview?.messages?.smsSuccess ?? '-' }} / <span class="danger">{{ overview?.messages?.smsFailed ?? '-' }}</span></b>
          </div>
          <div class="stat-line"><span>邮件成功 / 失败</span>
            <b>{{ overview?.messages?.mailSuccess ?? '-' }} / <span class="danger">{{ overview?.messages?.mailFailed ?? '-' }}</span></b>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 小时趋势：CSS 条形图（请求数，无图表库依赖） -->
    <el-card shadow="never" class="block">
      <template #header>
        <div class="chart-header">
          <span>小时趋势（近 {{ hoursWindow }} 小时请求数）</span>
          <el-radio-group v-model="hoursWindow" size="small" @change="loadRequests">
            <el-radio-button :value="6">6 小时</el-radio-button>
            <el-radio-button :value="24">24 小时</el-radio-button>
            <el-radio-button :value="72">72 小时</el-radio-button>
            <el-radio-button :value="168">7 天</el-radio-button>
          </el-radio-group>
        </div>
      </template>
      <el-empty v-if="!buckets.length" description="暂无请求数据" :image-size="60" />
      <div v-else class="bar-chart">
        <div v-for="b in buckets" :key="b.hour" class="bar-row">
          <span class="bar-label">{{ b.hour }}</span>
          <div class="bar-track">
            <div
              class="bar-fill"
              :class="barClass(b)"
              :style="{ width: barWidth(b) }"
              :title="barTitle(b)"
            />
          </div>
          <span class="bar-value">{{ b.total }}</span>
        </div>
      </div>
      <div v-if="buckets.length" class="legend">
        <span><i class="dot ok" />2xx</span>
        <span><i class="dot redirect" />3xx</span>
        <span><i class="dot warn" />4xx</span>
        <span><i class="dot danger" />5xx</span>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { getMonitorOverview, getMonitorRequests } from '@/api/infra'

// 概览与小时趋势各持一份数据；30s 定时刷新，document 隐藏时暂停
// （visibilitychange），恢复可见立即刷新一次。
const REFRESH_MILLIS = 30_000

const overview = ref<any>(null)
const buckets = ref<any[]>([])
const hoursWindow = ref(24)
const autoRefresh = ref(true)
let timer: ReturnType<typeof setInterval> | null = null

async function loadOverview() {
  try {
    overview.value = await getMonitorOverview()
  } catch {
    // 403（非平台租户）等错误由全局拦截器提示；轮询失败不打断页面。
  }
}

async function loadRequests() {
  try {
    const res = await getMonitorRequests(hoursWindow.value)
    buckets.value = res?.buckets || []
  } catch {
    buckets.value = []
  }
}

function loadAll() {
  loadOverview()
  loadRequests()
}

// 可见性感知刷新：页面隐藏暂停定时器，恢复可见立即拉一次并重启定时器。
function handleVisibility() {
  if (document.hidden) {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
    autoRefresh.value = false
  } else {
    loadAll()
    if (!timer) {
      timer = setInterval(loadAll, REFRESH_MILLIS)
    }
    autoRefresh.value = true
  }
}

onMounted(() => {
  loadAll()
  timer = setInterval(loadAll, REFRESH_MILLIS)
  document.addEventListener('visibilitychange', handleVisibility)
})

onBeforeUnmount(() => {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  document.removeEventListener('visibilitychange', handleVisibility)
})

// —— 展示辅助 ——

function formatUptime(seconds?: number) {
  if (seconds === undefined || seconds === null) return '-'
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d} 天 ${h} 小时 ${m} 分`
  if (h > 0) return `${h} 小时 ${m} 分`
  return `${m} 分 ${seconds % 60} 秒`
}

// 字节数本地格式化（带 GB 档，堆/系统内存可能超过 formatSize 的 MB 上限语义）。
function formatBytes(bytes?: number) {
  if (bytes === undefined || bytes === null) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

const maxTotal = computed(() => Math.max(...buckets.value.map((b) => b.total || 0), 1))

// 条形宽度按窗口内最大请求数归一，保底 2% 保证 1 条请求也可见。
function barWidth(b: any) {
  return `${Math.max((b.total / maxTotal.value) * 100, 2)}%`
}

// 桶颜色取该小时的错误特征：有 5xx 红、有 4xx 黄、有 3xx 青，其余绿。
function barClass(b: any) {
  if (b.s5xx > 0) return 'danger'
  if (b.s4xx > 0) return 'warn'
  if (b.s3xx > 0) return 'redirect'
  return 'ok'
}

function barTitle(b: any) {
  return `${b.hour}：共 ${b.total}（2xx ${b.s2xx} / 3xx ${b.s3xx} / 4xx ${b.s4xx} / 5xx ${b.s5xx}），平均 ${b.avgMs} ms，最大 ${b.maxMs} ms`
}
</script>

<style scoped>
.header-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.header-bar .title {
  font-size: 16px;
  font-weight: 600;
}
.refresh-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-right: 8px;
}
.block {
  margin-bottom: 12px;
}
.stat-line {
  display: flex;
  justify-content: space-between;
  padding: 4px 0;
  font-size: 13px;
}
.stat-line span {
  color: var(--el-text-color-secondary);
}
.danger {
  color: var(--el-color-danger);
}
.chart-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.bar-chart {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.bar-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.bar-label {
  width: 140px;
  color: var(--el-text-color-secondary);
  text-align: right;
  flex-shrink: 0;
}
.bar-track {
  flex: 1;
  height: 14px;
  background: var(--el-fill-color-light);
  border-radius: 3px;
  overflow: hidden;
}
.bar-fill {
  height: 100%;
  border-radius: 3px;
  transition: width 0.3s ease;
}
.bar-fill.ok {
  background: var(--el-color-success);
}
.bar-fill.redirect {
  background: var(--el-color-info);
}
.bar-fill.warn {
  background: var(--el-color-warning);
}
.bar-fill.danger {
  background: var(--el-color-danger);
}
.bar-value {
  width: 64px;
  flex-shrink: 0;
}
.legend {
  display: flex;
  gap: 16px;
  margin-top: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.legend .dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 4px;
}
.legend .dot.ok {
  background: var(--el-color-success);
}
.legend .dot.redirect {
  background: var(--el-color-info);
}
.legend .dot.warn {
  background: var(--el-color-warning);
}
.legend .dot.danger {
  background: var(--el-color-danger);
}
</style>
