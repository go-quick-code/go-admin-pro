<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      perm-prefix="mall:trade"
      title="交易订单"
      @row-click="openDetail"
    >
      <template #toolbar>
        <!-- 时间区间（下单时间过滤）：ProTable 搜索项仅支持输入/下拉，这里并入
            查询参数（照 erp stock-record 先例）。 -->
        <el-date-picker
          v-model="timeRange"
          type="daterange"
          value-format="YYYY-MM-DD"
          range-separator="至"
          start-placeholder="下单开始"
          end-placeholder="下单结束"
          style="width: 260px"
          @change="tableRef?.reload?.()"
        />
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status, statusTagMap)" size="small">
          {{ statusLabel(row.status) }}
        </el-tag>
      </template>
      <template #cell-payMoney="{ row }">
        <span class="pay-money">{{ row.payMoney }}</span>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openDetail(row)">详情</el-button>
        <el-button v-if="canCancel && row.status === 0" link type="danger" @click.stop="doCancel(row)">
          取消
        </el-button>
        <el-button v-if="canComplete && row.status === 10" link type="success" @click.stop="doComplete(row)">
          完成
        </el-button>
        <el-button v-if="canUpdate" link type="warning" @click.stop="openRemark(row)">备注</el-button>
      </template>
    </ProTable>

    <!-- 订单详情抽屉：管理面字段全量（支付信息/取消/关闭 + 用户/管理员备注）
        + 订单项成交快照表格 + 收货快照（spec §4「详情（含 items）」）。 -->
    <el-drawer v-model="detailVisible" :title="detail ? `订单 ${detail.no}` : '订单详情'" size="820px">
      <el-descriptions v-if="detail" :column="2" border size="small">
        <el-descriptions-item label="订单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="statusTagType(detail.status, statusTagMap)" size="small">
            {{ statusLabel(detail.status) }}
          </el-descriptions-item>
        </el-descriptions-item>
        <el-descriptions-item label="会员 ID">{{ detail.userId }}</el-descriptions-item>
        <el-descriptions-item label="下单时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
        <el-descriptions-item label="商品件数">{{ detail.totalQuantity }}</el-descriptions-item>
        <el-descriptions-item label="商品金额">{{ detail.itemMoney }}</el-descriptions-item>
        <el-descriptions-item label="优惠金额">{{ detail.discountMoney }}</el-descriptions-item>
        <el-descriptions-item label="运费">{{ detail.postage }}</el-descriptions-item>
        <el-descriptions-item label="实付金额">
          <span class="pay-money">{{ detail.payMoney }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="支付单号">{{ detail.payOrderNo || '-' }}</el-descriptions-item>
        <el-descriptions-item label="支付渠道">{{ detail.payChannelCode || '-' }}</el-descriptions-item>
        <el-descriptions-item label="支付时间">{{ formatTime(detail.payTime) }}</el-descriptions-item>
        <el-descriptions-item label="用户备注">{{ detail.userRemark || '-' }}</el-descriptions-item>
        <el-descriptions-item label="管理员备注">{{ detail.adminRemark || '-' }}</el-descriptions-item>
        <el-descriptions-item label="取消时间">{{ formatTime(detail.cancelTime) }}</el-descriptions-item>
        <el-descriptions-item label="取消原因">{{ detail.cancelReason || '-' }}</el-descriptions-item>
        <el-descriptions-item label="关闭时间">{{ formatTime(detail.closeTime) }}</el-descriptions-item>
        <el-descriptions-item label="关闭原因">{{ detail.closeReason || '-' }}</el-descriptions-item>
      </el-descriptions>

      <template v-if="detail">
        <h4 class="section-title">收货快照</h4>
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="收货人">{{ detail.receiverName || '-' }}</el-descriptions-item>
          <el-descriptions-item label="手机号">{{ detail.receiverMobile || '-' }}</el-descriptions-item>
          <el-descriptions-item label="地区">{{ detail.receiverArea || '-' }}</el-descriptions-item>
          <el-descriptions-item label="详细地址">{{ detail.receiverDetail || '-' }}</el-descriptions-item>
        </el-descriptions>

        <h4 class="section-title">订单项</h4>
        <el-table :data="detail.items || []" border stripe size="small">
          <el-table-column label="商品" min-width="180" show-overflow-tooltip>
            <template #default="{ row }">
              <span>{{ row.spuName }}</span>
            </template>
          </el-table-column>
          <el-table-column label="规格" min-width="140" show-overflow-tooltip>
            <template #default="{ row }">{{ propsText(row.skuProperties) }}</template>
          </el-table-column>
          <el-table-column label="单价" prop="price" width="90" align="right" />
          <el-table-column label="数量" prop="count" width="70" align="right" />
          <el-table-column label="小计" prop="subtotal" width="100" align="right" />
        </el-table>
      </template>
    </el-drawer>

    <!-- 管理员备注弹窗（任意状态可补注，终态也允许；≤500 字后端校验）。 -->
    <el-dialog v-model="remarkVisible" title="管理员备注" width="480px">
      <el-descriptions v-if="remarkTarget" :column="1" border size="small" class="remark-summary">
        <el-descriptions-item label="订单">{{ remarkTarget.no }}</el-descriptions-item>
        <el-descriptions-item label="现有备注">{{ remarkTarget.adminRemark || '-' }}</el-descriptions-item>
      </el-descriptions>
      <el-input
        v-model="remarkForm"
        type="textarea"
        :rows="4"
        maxlength="500"
        show-word-limit
        placeholder="管理员备注（选填，覆盖保存）"
      />
      <template #footer>
        <el-button @click="remarkVisible = false">取消</el-button>
        <el-button type="primary" :loading="remarkSubmitting" @click="submitRemark">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, statusTagType } from '@/utils/format'
import {
  pageTradeOrders,
  getTradeOrder,
  updateTradeOrderRemark,
  cancelTradeOrder,
  completeTradeOrder,
} from '@/api/mall'

// 交易订单页（issue #180，spec §4/§5）：ProTable 分页（状态/编号/会员/
// 时间过滤）+ 详情抽屉（管理字段全量 + 订单项成交快照 + 收货快照）+
// 取消（仅待支付，后端走状态机 409 兜底）/完成（仅已支付）/备注（任意
// 状态）。取消/完成/备注挂 mall:trade:cancel/complete/update，详情随查询码。
// 库存回补/状态翻转全部服务端收口，前端不做本地推算。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('mall:trade:query'))
const canCancel = computed(() => userStore.hasPerm('mall:trade:cancel'))
const canComplete = computed(() => userStore.hasPerm('mall:trade:complete'))
const canUpdate = computed(() => userStore.hasPerm('mall:trade:update'))

const tableRef = ref<InstanceType<typeof ProTable> | null>(null)

// 订单五态（spec §3.3「0 待支付 / 10 已支付 / 20 已完成 / 30 已取消 /
// 40 已关闭」，对齐芋道数值）。
const statusTagMap: Record<number, 'warning' | 'primary' | 'success' | 'info' | 'danger'> = {
  0: 'warning',
  10: 'primary',
  20: 'success',
  30: 'info',
  40: 'danger',
}
const statusLabels: Record<number, string> = {
  0: '待支付',
  10: '已支付',
  20: '已完成',
  30: '已取消',
  40: '已关闭',
}
function statusLabel(status: number) {
  return statusLabels[status] ?? String(status)
}

const statusOptions = Object.entries(statusLabels).map(([value, label]) => ({
  label,
  value: Number(value),
}))

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'no', label: '订单号', minWidth: 180 },
  { prop: 'userId', label: '会员 ID', width: 90 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'totalQuantity', label: '件数', width: 80 },
  { prop: 'payMoney', label: '实付金额', width: 110, slot: true },
  { prop: 'adminRemark', label: '管理员备注', minWidth: 140 },
  { prop: 'createTime', label: '下单时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'no', label: '订单号' },
  { prop: 'userId', label: '会员 ID' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

// 时间区间并入分页查询参数（ProTable 搜索项无 daterange，照 erp stock-record
// 先例；end 纯日期「含当日」语义由后端外推）。
const timeRange = ref<[string, string] | null>(null)
function withTimeRange(params: any) {
  return {
    ...params,
    startTime: timeRange.value?.[0],
    endTime: timeRange.value?.[1],
  }
}
const pageApi = { page: (params: any) => pageTradeOrders(withTimeRange(params)) }

// ---------- 详情抽屉 ----------

const detail = ref<any>(null)
const detailVisible = ref(false)

async function openDetail(row: any) {
  detail.value = await getTradeOrder(row.id)
  detailVisible.value = true
}

// 规格快照 JSON（[{propertyName,valueName}]）→ 「名: 值」文本（单规格 [] 显示 -）。
function propsText(raw: any) {
  if (!Array.isArray(raw) || raw.length === 0) return '-'
  return raw
    .map((p: any) => [p.propertyName, p.valueName].filter(Boolean).join(': '))
    .join('；')
}

// ---------- 取消 / 完成（行内确认，服务端状态机兜底 409） ----------

async function doCancel(row: any) {
  await ElMessageBox.confirm(`确认取消订单 ${row.no}？库存将回补。`, '取消订单', {
    type: 'warning',
  })
  await cancelTradeOrder(row.id)
  ElMessage.success('订单已取消')
  tableRef.value?.reload()
}

async function doComplete(row: any) {
  await ElMessageBox.confirm(`确认将订单 ${row.no} 标记为已完成？`, '完成订单', {
    type: 'success',
  })
  await completeTradeOrder(row.id)
  ElMessage.success('订单已完成')
  tableRef.value?.reload()
}

// ---------- 管理员备注 ----------

const remarkVisible = ref(false)
const remarkSubmitting = ref(false)
const remarkTarget = ref<any>(null)
const remarkForm = ref('')

function openRemark(row: any) {
  remarkTarget.value = row
  remarkForm.value = row.adminRemark || ''
  remarkVisible.value = true
}

async function submitRemark() {
  if (!remarkTarget.value) return
  remarkSubmitting.value = true
  try {
    await updateTradeOrderRemark(remarkTarget.value.id, remarkForm.value)
    ElMessage.success('备注已保存')
    remarkVisible.value = false
    tableRef.value?.reload()
  } finally {
    remarkSubmitting.value = false
  }
}
</script>

<style scoped>
.pay-money {
  font-weight: 600;
  color: var(--el-color-danger);
}

.section-title {
  margin: 16px 0 8px;
  font-weight: 600;
}

.remark-summary {
  margin-bottom: 12px;
}
</style>
