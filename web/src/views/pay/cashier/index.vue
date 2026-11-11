<template>
  <div class="app-container">
    <!-- 收银台（issue #133，spec §4.2/§6）：布局内独立操作页（照 OA 我的考勤
        形态）——订单信息卡 + 渠道选择 + mock 模拟回调按钮。金额服务端定：页面
        不提供任何金额输入，submit 只选渠道。 -->
    <el-card shadow="never" class="cashier-card">
      <div class="cashier-head">
        <div class="cashier-title">收银台</div>
        <el-button :loading="loading" @click="reload">刷新</el-button>
      </div>

      <el-form :inline="true" class="cashier-query" @submit.prevent="loadOrder">
        <el-form-item label="应用标识">
          <el-input v-model="query.appKey" placeholder="消费方应用的 appKey" clearable style="width: 220px" />
        </el-form-item>
        <el-form-item label="消费方单号">
          <el-input
            v-model="query.merchantOrderId"
            placeholder="下单时的 merchantOrderId"
            clearable
            style="width: 260px"
            @keyup.enter="loadOrder"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" @click="loadOrder">查询订单</el-button>
        </el-form-item>
      </el-form>

      <el-alert
        v-if="errorMsg"
        type="warning"
        show-icon
        :closable="false"
        :title="errorMsg"
        class="cashier-alert"
      />

      <template v-if="order">
        <el-descriptions :column="2" border class="cashier-info">
          <el-descriptions-item label="订单号">{{ order.no }}</el-descriptions-item>
          <el-descriptions-item label="消费方单号">{{ order.merchantOrderId }}</el-descriptions-item>
          <el-descriptions-item label="商品标题">{{ order.subject }}</el-descriptions-item>
          <el-descriptions-item label="实付金额">
            <span class="amount">{{ order.price }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="statusTagType(order.status)" size="small">{{ statusLabel(order.status) }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="过期时间">{{ formatTime(order.expireTime) || '-' }}</el-descriptions-item>
        </el-descriptions>

        <!-- 待支付：渠道选择 + 发起支付；mock 渠道支付后展示模拟回调按钮。 -->
        <template v-if="order.status === 0">
          <div class="cashier-section">
            <span class="cashier-label">选择支付方式</span>
            <el-radio-group v-model="channelCode" :disabled="submitting">
              <el-radio v-for="ch in channels" :key="ch.code" :value="ch.code">
                {{ ch.name }}（{{ ch.code }}）
              </el-radio>
            </el-radio-group>
            <span v-if="!channels.length" class="cashier-empty">暂无可用渠道（应用未绑定或渠道停用）</span>
          </div>
          <div class="cashier-actions">
            <el-button
              type="primary"
              size="large"
              :disabled="!channelCode"
              :loading="submitting"
              @click="doSubmit"
            >
              发起支付
            </el-button>
          </div>
          <!-- mock 渠道（displayMode=none）：订单停在待支付，展示「模拟回调」
              按钮（成功/失败），触发与真实渠道回调相同的解析入账链路。 -->
          <div v-if="showMockButtons" class="cashier-actions">
            <el-button type="success" :loading="mocking === 'success'" @click="doMockNotify('success')">
              模拟回调（成功）
            </el-button>
            <el-button type="warning" :loading="mocking === 'failure'" @click="doMockNotify('failure')">
              模拟回调（失败）
            </el-button>
          </div>
        </template>

        <el-result
          v-else-if="order.status === 10"
          icon="success"
          title="支付成功"
          :sub-title="`订单号 ${order.no}`"
        />
        <el-result
          v-else
          icon="info"
          :title="statusLabel(order.status)"
          :sub-title="`订单号 ${order.no}`"
        />
      </template>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { formatTime } from '@/utils/format'
import { getCashierOrder, submitPayOrder, mockPayNotify } from '@/api/pay'

// 收银台页（issue #133，spec §4.2）：登录即可用（菜单 840 无权限码），
// 本人订单校验在服务层强制（非本人 403、跨租户 404）。
// 流程：查询订单 → 选渠道 → 发起支付（wallet 同步成功 / mock 停待支付）→
// mock 渠道出现「模拟回调」按钮 → 支付成功入账（建商户回调任务，#134 消费）。

const statusLabels: Record<number, string> = { 0: '待支付', 10: '成功', 20: '已退款', 30: '已关闭' }
function statusLabel(status: number) {
  return statusLabels[status] ?? String(status)
}
function statusTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 20 || status === 30) return 'info'
  return 'warning'
}

const query = ref({ appKey: '', merchantOrderId: '' })
const order = ref<any>(null)
const channels = ref<any[]>([])
const channelCode = ref('')
const loading = ref(false)
const submitting = ref(false)
const mocking = ref<'' | 'success' | 'failure'>('')
const errorMsg = ref('')

// 展示模拟回调按钮：订单仍待支付且最近一次发起渠道为 mock（服务端
// submit 时回写 channelCode，刷新后判定依然成立）。
const showMockButtons = computed(() => {
  return !!order.value && order.value.status === 0 && order.value.channelCode === 'mock'
})

async function loadOrder() {
  if (!query.value.appKey || !query.value.merchantOrderId) {
    errorMsg.value = '请填写应用标识与消费方单号'
    return
  }
  loading.value = true
  errorMsg.value = ''
  try {
    const data = await getCashierOrder({ appKey: query.value.appKey, merchantOrderId: query.value.merchantOrderId })
    order.value = data?.order || null
    channels.value = data?.channels || []
    channelCode.value = channels.value[0]?.code || ''
    if (!order.value) errorMsg.value = '订单不存在或不可见'
  } catch (e: any) {
    order.value = null
    channels.value = []
    errorMsg.value = e?.message || '订单查询失败'
  } finally {
    loading.value = false
  }
}

function reload() {
  if (query.value.appKey && query.value.merchantOrderId) loadOrder()
}

async function doSubmit() {
  if (!order.value || !channelCode.value) return
  submitting.value = true
  try {
    const data = await submitPayOrder({ orderId: order.value.id, channelCode: channelCode.value })
    if (data?.status === 10) {
      ElMessage.success('支付成功')
    } else if (data?.displayMode === 'none') {
      ElMessage.info('订单已发起支付，等待支付结果')
    }
    await loadOrder()
  } finally {
    submitting.value = false
  }
}

async function doMockNotify(result: 'success' | 'failure') {
  if (!order.value) return
  mocking.value = result
  try {
    await mockPayNotify({ orderId: order.value.id, result })
    if (result === 'success') {
      ElMessage.success('模拟回调成功，订单已支付')
    } else {
      ElMessage.warning('模拟回调失败已记录，订单保持待支付')
    }
    await loadOrder()
  } finally {
    mocking.value = ''
  }
}
</script>

<style scoped>
.cashier-card {
  max-width: 860px;
}

.cashier-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.cashier-title {
  font-size: 20px;
  font-weight: 600;
}

.cashier-query {
  margin-bottom: 16px;
}

.cashier-alert {
  margin-bottom: 16px;
}

.cashier-info {
  margin-bottom: 16px;
}

.cashier-section {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 16px 0;
  flex-wrap: wrap;
}

.cashier-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.cashier-empty {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.cashier-actions {
  display: flex;
  gap: 16px;
  margin: 12px 0 16px;
}

.amount {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
</style>
