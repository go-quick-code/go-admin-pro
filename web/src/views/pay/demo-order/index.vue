<template>
  <div class="app-container">
    <!-- 示例订单页（issue #137，spec §6）：pay 模块唯一消费方（ADR 0021）的
        演示页——下单（商品字典，金额服务端定）→ 发起支付跳收银台 → 支付/退款
        状态由 #134 回调链路联动回写。操作按钮走独立权限码（spec §6）。 -->
    <el-card shadow="never" class="toolbar-card">
      <div class="toolbar-row">
        <el-select
          v-if="canCreate"
          v-model="selectedSpuId"
          placeholder="选择演示商品"
          style="width: 240px"
        >
          <el-option v-for="spu in spus" :key="spu.id" :label="`${spu.name}（¥${spu.price}）`" :value="spu.id" />
        </el-select>
        <el-button v-if="canCreate" type="primary" :loading="creating" :disabled="!selectedSpuId" @click="doCreate">
          示例下单（去收银台支付）
        </el-button>
        <span class="toolbar-hint">商品名称与金额由服务端字典定（请求不收金额）</span>
      </div>
    </el-card>

    <el-card shadow="never" class="list-card">
      <template #header>
        <span class="section-title">示例订单</span>
      </template>
      <el-table v-loading="loadingList" :data="rows" border stripe>
        <el-table-column prop="id" label="订单 ID" width="90" />
        <el-table-column prop="spuName" label="商品" min-width="140" show-overflow-tooltip />
        <el-table-column label="金额" width="100" align="right">
          <template #default="{ row }">
            <span class="amount">{{ row.price }}</span>
          </template>
        </el-table-column>
        <el-table-column label="支付状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.payStatus === 1 ? 'success' : 'warning'" size="small">
              {{ row.payStatus === 1 ? '已支付' : '未支付' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="支付渠道" width="100">
          <template #default="{ row }">{{ channelName(row.payChannelCode) }}</template>
        </el-table-column>
        <el-table-column label="已退累计" width="100" align="right">
          <template #default="{ row }">
            <span v-if="Number(row.refundPrice) > 0" class="refund-amount">{{ row.refundPrice }}</span>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="支付时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.payTime) || '-' }}</template>
        </el-table-column>
        <el-table-column label="退款时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.refundTime) || '-' }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.payStatus === 0 && row.appKey" link type="primary" @click="goPay(row)">
              发起支付
            </el-button>
            <el-button
              v-if="canRefund && row.payStatus === 1 && Number(row.refundPrice) < Number(row.price)"
              link
              type="danger"
              @click="doRefund(row)"
            >
              发起退款
            </el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-pagination
        v-model:current-page="query.pageNo"
        v-model:page-size="query.pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @size-change="loadList"
        @current-change="loadList"
      />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { listDemoSPUs, createDemoOrder, pageDemoOrders, refundDemoOrder } from '@/api/pay'

// 示例订单页（issue #137，spec §4.11/§6）：创建/退款端点登录即可（本人校验在
// service 层强制），按钮 hasPerm 显隐（pay:demo-order:create / refund，菜单
// 830 + 按钮 8301/8302）。发起支付跳收银台（appKey + merchantOrderId 定位，
// 照 #136 我的充值形态）。

const router = useRouter()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('pay:demo-order:create'))
const canRefund = computed(() => userStore.hasPerm('pay:demo-order:refund'))

const spus = ref<any[]>([])
const selectedSpuId = ref<number | undefined>(undefined)
const creating = ref(false)

const rows = ref<any[]>([])
const total = ref(0)
const loadingList = ref(false)
const query = ref({ pageNo: 1, pageSize: 10 })

const channelLabels: Record<string, string> = { mock: '模拟支付', wallet: '钱包' }
function channelName(code: string) {
  return channelLabels[code] ?? (code || '-')
}

async function loadSpus() {
  try {
    spus.value = (await listDemoSPUs()) || []
    if (spus.value.length) selectedSpuId.value = spus.value[0].id
  } catch {
    spus.value = []
  }
}

async function loadList() {
  loadingList.value = true
  try {
    const data = await pageDemoOrders(query.value)
    rows.value = data?.list || []
    total.value = data?.total || 0
  } finally {
    loadingList.value = false
  }
}

async function doCreate() {
  if (!selectedSpuId.value) {
    ElMessage.warning('请选择演示商品')
    return
  }
  creating.value = true
  try {
    const data = await createDemoOrder({ spuId: selectedSpuId.value })
    ElMessage.success('示例订单已创建，请完成支付')
    // 收银台定位（appKey + 消费方单号 DEMO-<订单 ID>）。
    router.push({
      path: '/pay/cashier',
      query: { appKey: data?.appKey, merchantOrderId: data?.merchantOrderId },
    })
  } finally {
    creating.value = false
  }
}

function goPay(row: any) {
  router.push({
    path: '/pay/cashier',
    query: { appKey: row.appKey, merchantOrderId: row.merchantOrderId },
  })
}

// 金额展示辅助（ADR 0007：金额字符串承载，前端不做浮点运算）：解析走纯数字
// 字符串拆「分」，减法是分值整数减（decimal(12,2) 分值上限 999999999999 远小
// 于 2^53，整数精确），格式化按两位小数回拼——确认弹窗的「未退余额」不再经
// Number() 浮点减法。退款的权威金额由服务端计算（金额快照进退款单）。
function parseCents(text: unknown): number {
  const m = /^(-)?(\d+)(?:\.(\d{1,2}))?$/.exec(String(text ?? '').trim())
  if (!m) return 0
  const whole = parseInt(m[2], 10)
  const frac = m[3] ? parseInt(m[3].padEnd(2, '0'), 10) : 0
  return (m[1] ? -1 : 1) * (whole * 100 + frac)
}

function formatCents(cents: number): string {
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(cents)
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`
}

function remainingPrice(price: unknown, refundPrice: unknown): string {
  return formatCents(parseCents(price) - parseCents(refundPrice))
}

async function doRefund(row: any) {
  const remaining = remainingPrice(row.price, row.refundPrice)
  const confirmed = await ElMessageBox.confirm(
    `将原路全额退回该订单未退余额 ${remaining} 元（实付 ${row.price}，已退 ${row.refundPrice}），成功后经回调链路联动退款状态。`,
    '发起示例退款',
    { type: 'warning', confirmButtonText: '确认退款', cancelButtonText: '取消' },
  ).catch(() => false)
  if (!confirmed) return
  await refundDemoOrder(row.id)
  ElMessage.success('退款已发起')
  await loadList()
}

onMounted(() => {
  loadSpus()
  loadList()
})
</script>

<style scoped>
.toolbar-card {
  margin-bottom: 16px;
}

.toolbar-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.toolbar-hint {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.list-card .section-title {
  font-weight: 600;
}

.amount {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.refund-amount {
  color: var(--el-color-danger);
  font-variant-numeric: tabular-nums;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}
</style>
