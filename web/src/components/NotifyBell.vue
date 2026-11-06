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

// 顶栏通知铃铛（issue #45 轮询 + issue #78 实时推送）：优先 SSE 收事件
//（spec §4.5.6 传输优先级 WebSocket → SSE → 轮询，WS 由 #79 接入）；
// 事件到达刷新未读数，SSE 不可用降级 60 秒轮询，断线按 1/2/5/10/30 秒
// 退避重连。列表抽屉点击条目按跳转目标（route + routeParams）路由跳转，
// 未读点击即已读。
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

// ---------- 实时推送（issue #78，spec §4.5.6）----------
// EventSource 不能带 Authorization 头：先带 Bearer 换一次性票据再建流
//（ADR 0013，避免 JWT 进入 URL）。票据一次性：浏览器 EventSource 的原生
// 重连会复用已消费的 URL（必然 401），因此出错即手动关闭，换新票据重连。
const SSE_RETRY_DELAYS = [1000, 2000, 5000, 10000, 30000]

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
  const delay = SSE_RETRY_DELAYS[Math.min(retryIndex, SSE_RETRY_DELAYS.length - 1)]
  retryIndex++
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = undefined
    connectPush()
  }, delay)
}

function closeSSE() {
  if (eventSource) {
    eventSource.close()
    eventSource = undefined
  }
}

async function connectPush() {
  if (disposed || eventSource) return
  let ticket = ''
  try {
    ticket = (await getPushTicket())?.ticket ?? ''
  } catch {
    // 换票失败（推送未开启 404 / 网络抖动）：降级轮询并退避重试。
    // 401 会经 request 拦截器跳登录页，组件随页面卸载停止。
    startPolling()
    scheduleReconnect()
    return
  }
  if (!ticket || disposed) return
  const source = new EventSource(`/admin-api/notify/push/sse?ticket=${encodeURIComponent(ticket)}`)
  eventSource = source
  source.onopen = () => {
    // 连通：重置退避；未读数交给实时事件，轮询暂停。
    retryIndex = 0
    stopPolling()
  }
  source.onmessage = () => {
    // 事件到达即刷新未读数；抽屉打开时一并刷新列表。
    refresh()
    if (visible.value) load()
  }
  source.onerror = () => {
    closeSSE()
    startPolling()
    scheduleReconnect()
  }
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
})

onBeforeUnmount(() => {
  disposed = true
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
