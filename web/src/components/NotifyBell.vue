<template>
  <div class="notify-bell">
    <el-badge :value="unread" :hidden="unread <= 0" :max="99">
      <el-icon class="bell" @click="open"><Bell /></el-icon>
    </el-badge>

    <el-drawer v-model="visible" title="通知" :size="400" @closed="refresh">
      <template #header>
        <div class="drawer-header">
          <span>通知</span>
          <el-button
            link
            type="primary"
            :disabled="unread <= 0 || markingAll"
            @click="onMarkAll"
          >
            全部已读
          </el-button>
        </div>
      </template>

      <div v-loading="loading" class="notify-list">
        <el-empty v-if="!loading && messages.length === 0" description="暂无通知" />
        <div
          v-for="msg in messages"
          :key="msg.id"
          class="notify-item"
          :class="{ unread: !msg.readAt }"
          @click="goto(msg)"
        >
          <div class="item-head">
            <span class="dot" v-if="!msg.readAt"></span>
            <el-tag size="small" :type="typeTag(msg.type)" effect="plain">
              {{ typeLabel(msg.type) }}
            </el-tag>
            <span class="time">{{ formatTime(msg.createTime) }}</span>
          </div>
          <div class="title">{{ msg.title }}</div>
          <div class="content" v-if="msg.content">{{ msg.content }}</div>
        </div>
        <div v-if="total > messages.length" class="load-more">
          <el-button link type="primary" @click="loadMore">加载更多</el-button>
        </div>
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { formatTime } from '@/utils/format'
import {
  getPushTicket,
  getUnreadCount,
  markAllMessagesRead,
  markMessageRead,
  pageMessages,
  type NotifyMessage,
} from '@/api/notify'

// 顶栏通知铃铛（issue #45 轮询 + #78 SSE + #79 WebSocket）：传输优先级
// WebSocket → SSE → 60 秒轮询（spec §4.5.6），任一传输失败自动降级；
// 断线按 1/2/5/10/30 秒退避重连，页面恢复可见时立即重连。列表抽屉点击
// 条目按跳转目标（route + routeParams）路由跳转，未读点击即已读。
const PAGE_SIZE = 20

const router = useRouter()
const unread = ref(0)
const visible = ref(false)
const loading = ref(false)
const markingAll = ref(false)
const messages = ref<NotifyMessage[]>([])
const total = ref(0)

// typeLabel/typeTag：类型枚举的展示文案与标签色（值由后端枚举约束）。
function typeLabel(type: string): string {
  switch (type) {
    case 'bpm_todo':
      return '待办'
    case 'bpm_result':
      return '审批结果'
    case 'announcement':
      return '公告'
    case 'meeting_reminder':
      return '会议提醒'
    default:
      return type
  }
}

function typeTag(type: string): 'primary' | 'success' | 'warning' | 'info' {
  switch (type) {
    case 'bpm_todo':
      return 'warning'
    case 'bpm_result':
      return 'success'
    case 'announcement':
      return 'primary'
    case 'meeting_reminder':
      return 'info'
    default:
      return 'info'
  }
}

async function refresh() {
  try {
    const data = await getUnreadCount()
    unread.value = data?.count ?? 0
  } catch {
    // 轮询失败静默（未登录/网络抖动），下次轮询重试
  }
}

// ---------- 实时推送（#78 SSE + #79 WebSocket，spec §4.5.6）----------
// EventSource/WebSocket 不能带 Authorization 头：先带 Bearer 换一次性票据
// 再建流（ADR 0013，避免 JWT 进入 URL/日志）。票据一次性：原生重连会复用
// 已消费的 URL（必然 401），因此出错即手动关闭，换新票据重连。
// 传输优先级 WebSocket → SSE → 60 秒轮询：WS onclose 自动换新票据降级 SSE，
// SSE 失败降级轮询；退避 1/2/5/10/30 秒（重连从 WS 起步，WS 恢复即自动升回）。
const RETRY_DELAYS = [1000, 2000, 5000, 10000, 30000]

let socket: WebSocket | undefined
let eventSource: EventSource | undefined
let retryIndex = 0
let reconnectTimer: number | undefined
let pollTimer: number | undefined
let disposed = false

function startPolling() {
  if (pollTimer !== undefined) return
  pollTimer = window.setInterval(refresh, 60_000)
}

function stopPolling() {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

// scheduleReconnect 断线退避重连：1/2/5/10/30 秒，封顶 30 秒。
function scheduleReconnect() {
  if (disposed || reconnectTimer !== undefined) return
  const delay = RETRY_DELAYS[Math.min(retryIndex, RETRY_DELAYS.length - 1)]
  retryIndex++
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = undefined
    connectPush()
  }, delay)
}

function onPushEvent() {
  // 事件到达即刷新未读数；抽屉打开时一并刷新列表。
  refresh()
  if (visible.value) load()
}

function closeSocket() {
  if (socket) {
    const ws = socket
    socket = undefined
    ws.onopen = ws.onmessage = ws.onerror = ws.onclose = null
    ws.close()
  }
}

function closeSSE() {
  if (eventSource) {
    eventSource.close()
    eventSource = undefined
  }
}

// scheduleReconnect 后统一从 connectPush 进入：先试 WS，onclose 再降级 SSE。

async function getTicketOrFallback(): Promise<string> {
  try {
    return (await getPushTicket())?.ticket ?? ''
  } catch {
    // 换票失败（推送未开启 404 / 网络抖动）：降级轮询并退避重试。
    // 401 会经 request 拦截器跳登录页，组件随页面卸载停止。
    startPolling()
    scheduleReconnect()
    return ''
  }
}

// connectPush 建立实时流：传输优先级 WebSocket → SSE（spec §4.5.6）。
async function connectPush() {
  if (disposed || socket || eventSource) return
  const ticket = await getTicketOrFallback()
  if (!ticket || disposed) return
  const ws = new WebSocket(`/admin-api/notify/push/ws?ticket=${encodeURIComponent(ticket)}`)
  socket = ws
  ws.onopen = () => {
    if (socket !== ws) return
    // 连通：重置退避；未读数交给实时事件，轮询暂停。
    retryIndex = 0
    stopPolling()
  }
  ws.onmessage = () => onPushEvent()
  // WS 失败（握手被拒/断线）：onerror 后必触发 onclose，在此降级。
  ws.onclose = () => {
    if (socket !== ws) return
    socket = undefined
    fallbackToSSE()
  }
  ws.onerror = () => {}
}

// fallbackToSSE 是 WS 不可用时的自动降级：换新票据（一次性，不复用 WS 的
// 已消费票据）立即走 SSE。SSE 再失败则轮询兜底 + 退避重连。
function fallbackToSSE() {
  if (disposed || eventSource || socket) return
  connectSSE()
}

async function connectSSE() {
  if (disposed || eventSource || socket) return
  const ticket = await getTicketOrFallback()
  if (!ticket || disposed) return
  const source = new EventSource(`/admin-api/notify/push/sse?ticket=${encodeURIComponent(ticket)}`)
  eventSource = source
  source.onopen = () => {
    if (eventSource !== source) return
    retryIndex = 0
    stopPolling()
  }
  source.onmessage = () => onPushEvent()
  source.onerror = () => {
    if (eventSource !== source) return
    eventSource = undefined
    source.close()
    startPolling()
    scheduleReconnect()
  }
}

// onVisibility 页面恢复可见时立即重连（提前结束退避等待）；隐藏时保持
// 现有连接不断开（后台标签页仍收事件，回来不丢通知）。
function onVisibility() {
  if (disposed || document.visibilityState !== 'visible') return
  if (socket || eventSource) return
  if (reconnectTimer !== undefined) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = undefined
  }
  connectPush()
}

async function load() {
  loading.value = true
  try {
    const data = await pageMessages({ pageNo: 1, pageSize: PAGE_SIZE })
    messages.value = data?.list ?? []
    total.value = data?.total ?? 0
  } finally {
    loading.value = false
  }
}

function loadMore() {
  // 列表按 id 倒序，往后翻页取更早的通知
  const next = Math.ceil(messages.value.length / PAGE_SIZE) + 1
  pageMessages({ pageNo: next, pageSize: PAGE_SIZE }).then((data) => {
    const more = data?.list ?? []
    messages.value = messages.value.concat(more)
  })
}

function open() {
  visible.value = true
  load()
}

async function onMarkAll() {
  markingAll.value = true
  try {
    await markAllMessagesRead()
    await Promise.all([refresh(), load()])
  } finally {
    markingAll.value = false
  }
}

// goto 点击条目：未读即标记已读（幂等），再按跳转目标路由跳转；
// 无 route 的通知仅做已读。routeParams 是后端约定的 JSON 对象字符串，
// 解析结果收紧为 LocationQueryRaw 兼容的 string 值。
async function goto(msg: NotifyMessage) {
  if (!msg.readAt) {
    markMessageRead(msg.id)
      .then(refresh)
      .catch(() => {})
  }
  if (msg.route) {
    const query: Record<string, string> = {}
    if (msg.routeParams) {
      try {
        const parsed: unknown = JSON.parse(msg.routeParams)
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
            if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
              query[key] = String(value)
            }
          }
        }
      } catch {
        // 非法 JSON 按无参数处理
      }
    }
    visible.value = false
    await router.push({ path: msg.route, query })
  }
}

onMounted(() => {
  refresh()
  connectPush()
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  disposed = true
  document.removeEventListener('visibilitychange', onVisibility)
  closeSocket()
  closeSSE()
  stopPolling()
  if (reconnectTimer !== undefined) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = undefined
  }
})
</script>

<style scoped>
.notify-bell {
  display: flex;
  align-items: center;
}

.bell {
  font-size: 18px;
  cursor: pointer;
  color: #606266;
}

.drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.notify-list {
  min-height: 120px;
}

.notify-item {
  padding: 12px;
  border-radius: 8px;
  cursor: pointer;
  transition: background-color 0.2s;
}

.notify-item:hover {
  background: #f5f7fa;
}

.notify-item.unread .title {
  font-weight: 600;
}

.item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #f56c6c;
  flex-shrink: 0;
}

.time {
  margin-left: auto;
  font-size: 12px;
  color: #909399;
}

.title {
  font-size: 14px;
  color: #303133;
  line-height: 1.5;
}

.content {
  font-size: 13px;
  color: #909399;
  line-height: 1.5;
  margin-top: 2px;
}

.load-more {
  text-align: center;
  padding: 8px 0;
}
</style>
