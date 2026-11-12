<template>
  <div class="app-container">
    <!-- 我的充值（issue #136，spec §4.11 自助端点 / 菜单 823）：登录即可，
        服务层强制本人钱包归属。发起充值（套餐快照 / 自定义金额）→ 同事务挂
        支付订单 → 跳收银台支付（钱包渠道被禁，spec §4.7）；支付成功余额到账
        （含赠送），充值单支持原路全额退款（余额充足才可发起，spec §4.7）。 -->
    <el-row :gutter="16">
      <el-col :span="8">
        <el-card shadow="never" class="wallet-card">
          <div class="wallet-balance-label">钱包余额</div>
          <div class="wallet-balance">{{ wallet?.balance ?? '-' }}</div>
          <div class="wallet-sub">累计充值 {{ wallet?.totalRecharge ?? '-' }}</div>
        </el-card>
      </el-col>
      <el-col :span="16">
        <el-card shadow="never">
          <template #header>
            <span class="section-title">发起充值</span>
          </template>
          <div v-loading="loadingOptions" class="package-list">
            <el-radio-group v-model="selectedPackageId" class="package-group">
              <el-radio v-for="opt in packages" :key="opt.id" :value="opt.id" border class="package-item">
                <div class="package-info">
                  <div class="package-name">{{ opt.name }}</div>
                  <div class="package-price">
                    <span class="pay">{{ opt.payPrice }} 元</span>
                    <span v-if="opt.bonusPrice !== '0.00'" class="bonus">送 {{ opt.bonusPrice }}</span>
                  </div>
                </div>
              </el-radio>
            </el-radio-group>
            <div v-if="!loadingOptions && !packages.length" class="package-empty">
              暂无可用套餐（管理员可在「充值套餐」页配置），可选择自定义金额充值
            </div>
            <el-radio-group v-model="selectedPackageId" class="package-group">
              <el-radio :value="0" border class="package-item">
                <div class="package-info">
                  <div class="package-name">自定义金额</div>
                  <div class="package-price custom-input">
                    <el-input
                      v-model="customPayPrice"
                      :disabled="selectedPackageId !== 0"
                      placeholder="十进制金额（元），如 50.00"
                      style="width: 220px"
                    />
                  </div>
                </div>
              </el-radio>
            </el-radio-group>
            <div class="recharge-actions">
              <el-button
                type="primary"
                :loading="submitting"
                :disabled="selectedPackageId === 0 && !customPayPrice"
                @click="doRecharge"
              >
                发起充值（去收银台支付）
              </el-button>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-card shadow="never" class="list-card">
      <template #header>
        <span class="section-title">我的充值单</span>
      </template>
      <el-table v-loading="loadingList" :data="rows" border stripe>
        <el-table-column prop="id" label="充值单号" width="90">
          <template #default="{ row }">RCH-{{ row.id }}</template>
        </el-table-column>
        <el-table-column label="实付 / 赠送 / 总额" min-width="170" align="right">
          <template #default="{ row }">
            <span class="amount">{{ row.payPrice }}</span>
            <span v-if="row.bonusPrice !== '0.00'" class="bonus"> + {{ row.bonusPrice }}</span>
            <span class="total"> = {{ row.totalPrice }}</span>
          </template>
        </el-table-column>
        <el-table-column label="支付状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.payStatus === 1 ? 'success' : 'warning'" size="small">
              {{ row.payStatus === 1 ? '已到账' : '待支付' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="支付渠道" width="100">
          <template #default="{ row }">{{ channelName(row.payChannelCode) }}</template>
        </el-table-column>
        <el-table-column label="到账时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.payTime) || '-' }}</template>
        </el-table-column>
        <el-table-column label="退款状态" width="110">
          <template #default="{ row }">
            <el-tag :type="refundTagType(row.refundStatus)" size="small">
              {{ refundLabel(row.refundStatus) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="退款扣回" width="110" align="right">
          <template #default="{ row }">
            <span v-if="row.refundStatus === 10" class="refund-amount">{{ row.refundTotalPrice }}</span>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.payStatus === 0" link type="primary" @click="goPay(row)">去支付</el-button>
            <el-button
              v-if="row.payStatus === 1 && row.refundStatus !== 10"
              link
              type="danger"
              @click="doRefund(row)"
            >
              申请退款
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
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { formatTime } from '@/utils/format'
import {
  getMyWallet,
  listRechargePackageOptions,
  createRecharge,
  pageMyRecharges,
  createPayRefund,
} from '@/api/pay'

// 我的充值页（issue #136，spec §4.11/§4.7）：自助页无权限码（菜单 823）。
// 流程：选套餐（快照）或自定义金额 → 发起充值（返回 appKey + merchantOrderId）
// → 跳收银台选渠道支付 → 支付成功余额到账（pay + bonus）；充值单可原路全额
// 退款（后端守卫：仅全额 + 余额充足，spec §4.7，不足 409 不发起）。

const router = useRouter()

const wallet = ref<any>(null)
const packages = ref<any[]>([])
const loadingOptions = ref(false)
const selectedPackageId = ref<number | undefined>(undefined)
const customPayPrice = ref('')
const submitting = ref(false)

const rows = ref<any[]>([])
const total = ref(0)
const loadingList = ref(false)
const query = ref({ pageNo: 1, pageSize: 10 })

const channelLabels: Record<string, string> = { mock: '模拟支付', wallet: '钱包' }
function channelName(code: string) {
  return channelLabels[code] ?? (code || '-')
}

function refundLabel(status: number) {
  if (status === 10) return '退款成功'
  if (status === 20) return '退款失败'
  return '无退款'
}
function refundTagType(status: number) {
  if (status === 10) return 'info'
  if (status === 20) return 'danger'
  return 'success'
}

async function loadWallet() {
  try {
    wallet.value = await getMyWallet()
  } catch {
    wallet.value = null
  }
}

async function loadPackages() {
  loadingOptions.value = true
  try {
    packages.value = (await listRechargePackageOptions()) || []
    if (packages.value.length) selectedPackageId.value = packages.value[0].id
    else selectedPackageId.value = 0
  } catch {
    packages.value = []
    selectedPackageId.value = 0
  } finally {
    loadingOptions.value = false
  }
}

async function loadList() {
  loadingList.value = true
  try {
    const data = await pageMyRecharges(query.value)
    rows.value = data?.list || []
    total.value = data?.total || 0
  } finally {
    loadingList.value = false
  }
}

async function doRecharge() {
  if (selectedPackageId.value === 0 && !customPayPrice.value) {
    ElMessage.warning('请选择套餐或填写自定义充值金额')
    return
  }
  submitting.value = true
  try {
    const body =
      selectedPackageId.value && selectedPackageId.value > 0
        ? { packageId: selectedPackageId.value }
        : { payPrice: customPayPrice.value }
    const data = await createRecharge(body)
    ElMessage.success('充值单已创建，请完成支付')
    // 收银台定位（appKey + 消费方单号 RCH-<充值单 ID>）：钱包渠道被后端禁用。
    router.push({
      path: '/pay/cashier',
      query: { appKey: data?.appKey, merchantOrderId: data?.merchantOrderId },
    })
  } finally {
    submitting.value = false
  }
}

function goPay(row: any) {
  router.push({
    path: '/pay/cashier',
    query: { appKey: row.appKey, merchantOrderId: row.merchantOrderId },
  })
}

async function doRefund(row: any) {
  const confirmed = await ElMessageBox.confirm(
    `将原路全额退回该笔充值：扣回 ${row.totalPrice} 元（实付 ${row.payPrice} + 赠送 ${row.bonusPrice}），余额不足将拒绝发起。`,
    '申请充值退款',
    { type: 'warning', confirmButtonText: '确认退款', cancelButtonText: '取消' },
  ).catch(() => false)
  if (!confirmed) return
  // 消费方退款单号由前端生成（幂等键，重试换新号——幂等语义同消费方契约）。
  const merchantRefundId = `RFR-RCH${row.id}-${Date.now()}`
  await createPayRefund({
    appKey: row.appKey,
    merchantOrderId: row.merchantOrderId,
    merchantRefundId,
    refundPrice: row.payPrice,
    reason: '充值退款',
  })
  ElMessage.success('退款已发起')
  await Promise.all([loadList(), loadWallet()])
}

onMounted(() => {
  loadWallet()
  loadPackages()
  loadList()
})
</script>

<style scoped>
.wallet-card {
  text-align: center;
}

.wallet-balance-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.wallet-balance {
  font-size: 28px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  margin: 8px 0;
}

.wallet-sub {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.section-title {
  font-weight: 600;
}

.package-group {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 12px;
  align-items: stretch;
}

.package-item {
  margin-right: 0;
  padding: 10px 16px;
  height: auto;
}

.package-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.package-name {
  font-weight: 600;
}

.package-price {
  font-size: 13px;
}

.package-price .pay {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.package-price .bonus {
  color: var(--el-color-success);
  margin-left: 8px;
}

.package-empty {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  margin-bottom: 12px;
}

.recharge-actions {
  margin-top: 4px;
}

.list-card {
  margin-top: 16px;
}

.amount {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.bonus {
  color: var(--el-color-success);
}

.total {
  color: var(--el-text-color-secondary);
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
