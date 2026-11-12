<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      perm-prefix="pay:wallet"
      title="钱包管理"
      @row-click="openFlows"
    >
      <template #cell-userType="{ row }">
        <el-tag :type="row.userType === 2 ? 'primary' : 'success'" size="small">
          {{ userTypeLabel(row.userType) }}
        </el-tag>
      </template>
      <template #cell-balance="{ row }">
        <span class="amount">{{ row.balance }}</span>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openFlows(row)">流水</el-button>
      </template>
    </ProTable>

    <!-- 流水抽屉（spec §6：钱包管理 = 钱包列表 + 流水抽屉）：正入负出着色、
        结存快照列；逐笔与代数和一致（后端唯一入账入口保证）。append-only，
        无新增/编辑/删除按钮（spec §4.10）。 -->
    <el-drawer v-model="flowVisible" :title="flowTitle" size="860px">
      <el-descriptions :column="2" border size="small" class="wallet-summary">
        <el-descriptions-item label="钱包 ID">{{ wallet?.id }}</el-descriptions-item>
        <el-descriptions-item label="归属">{{ wallet ? `${userTypeLabel(wallet.userType)} #${wallet.userId}` : '-' }}</el-descriptions-item>
        <el-descriptions-item label="当前余额">{{ wallet?.balance }}</el-descriptions-item>
        <el-descriptions-item label="累计充值">{{ wallet?.totalRecharge }}</el-descriptions-item>
        <el-descriptions-item label="累计支出">{{ wallet?.totalExpense }}</el-descriptions-item>
        <el-descriptions-item label="更新时间">{{ formatTime(wallet?.updateTime) }}</el-descriptions-item>
      </el-descriptions>

      <el-table v-loading="flowLoading" :data="flows" size="small" border>
        <el-table-column prop="no" label="流水号" min-width="190" show-overflow-tooltip />
        <el-table-column label="业务类型" width="100">
          <template #default="{ row }">
            <el-tag :type="bizTagType(row.bizType)" size="small">{{ bizLabel(row.bizType) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="title" label="标题" min-width="150" show-overflow-tooltip />
        <el-table-column prop="bizId" label="业务单据" width="90" />
        <el-table-column label="变动金额" width="110" align="right">
          <template #default="{ row }">
            <span :class="['price', priceSign(row.price)]">{{ row.price }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="balance" label="结存快照" width="110" align="right" />
        <el-table-column label="入账时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.createTime) }}</template>
        </el-table-column>
      </el-table>
      <el-pagination
        v-model:current-page="flowQuery.pageNo"
        v-model:page-size="flowQuery.pageSize"
        :total="flowTotal"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @size-change="loadFlows"
        @current-change="loadFlows"
      />
      <!-- 流水导出（issue #138，spec §4.12）：导出当前钱包流水，pay:wallet:export
          显隐（spec §7 流水导出挂钱包权限码对）。 -->
      <div class="flow-actions">
        <ExcelActions
          title="钱包流水"
          export-perm="pay:wallet:export"
          :export-action="exportWalletTransactions"
          :export-params="flowExportParams"
        />
      </div>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { pageWallets, pageWalletTransactions, exportWalletTransactions } from '@/api/pay'

// 钱包管理页（issue #132，spec §6）：钱包列表 + 流水抽屉。钱包与流水只读
//（无新增/编辑/删除：余额变动只走后端唯一入账入口 spec §4.3，钱包不可删
// spec §4.10）；业务类型 1 充值/2 充值退款/3 支付/4 支付退款/5 余额调整/6 转账。
// 流水导出（issue #138，spec §4.12）：抽屉内按当前钱包导出（pay:wallet:export）。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('pay:wallet:query'))

const userTypeLabels: Record<number, string> = { 1: '会员', 2: '后台用户' }
function userTypeLabel(type: number) {
  return userTypeLabels[type] ?? `类型${type}`
}

// biz_type 展示（与后端 model.WalletBizTypes 对齐）。
const bizLabels: Record<number, string> = {
  1: '充值',
  2: '充值退款',
  3: '支付',
  4: '支付退款',
  5: '余额调整',
  6: '转账',
}
function bizLabel(biz: number) {
  return bizLabels[biz] ?? String(biz)
}
function bizTagType(biz: number) {
  if (biz === 3) return 'danger' // 支付（出）
  if (biz === 2) return 'warning' // 充值退款（出）
  return 'success' // 充值/支付退款/转账（入）与预留
}
function priceSign(price: string) {
  return price?.startsWith('-') ? 'neg' : 'pos'
}

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'userId', label: '归属用户', width: 100 },
  { prop: 'userType', label: '归属类型', width: 100, slot: true },
  { prop: 'balance', label: '余额', width: 120, slot: true },
  { prop: 'totalRecharge', label: '累计充值', minWidth: 110 },
  { prop: 'totalExpense', label: '累计支出', minWidth: 110 },
  {
    prop: 'updateTime',
    label: '更新时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.updateTime),
  },
]

const searchItems = [
  { prop: 'userId', label: '归属用户 ID' },
  {
    prop: 'userType',
    label: '归属类型',
    type: 'select',
    options: [
      { label: '会员', value: 1 },
      { label: '后台用户', value: 2 },
    ],
  },
]

const api = { page: pageWallets }

// ---------- 流水抽屉 ----------

const wallet = ref<any>(null)
const flowVisible = ref(false)
const flowLoading = ref(false)
const flows = ref<any[]>([])
const flowTotal = ref(0)
const flowQuery = ref({ pageNo: 1, pageSize: 10 })

const flowTitle = computed(() => (wallet.value ? `钱包流水：${userTypeLabel(wallet.value.userType)} #${wallet.value.userId}` : '钱包流水'))

async function openFlows(row: any) {
  wallet.value = row
  flowQuery.value = { pageNo: 1, pageSize: 10 }
  flowVisible.value = true
  await loadFlows()
}

async function loadFlows() {
  if (!wallet.value) return
  flowLoading.value = true
  try {
    const data = await pageWalletTransactions({
      walletId: wallet.value.id,
      pageNo: flowQuery.value.pageNo,
      pageSize: flowQuery.value.pageSize,
    })
    flows.value = data.list || []
    flowTotal.value = data.total || 0
  } finally {
    flowLoading.value = false
  }
}

// 流水导出参数：当前抽屉钱包（全量不分页，分页参数由 ExcelActions 剔除）。
function flowExportParams() {
  return { walletId: wallet.value?.id }
}
</script>

<style scoped>
.wallet-summary {
  margin-bottom: 12px;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}

.flow-actions {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
}

.price.pos {
  color: var(--el-color-success);
}

.price.neg {
  color: var(--el-color-danger);
}

.amount {
  font-weight: 600;
}
</style>
