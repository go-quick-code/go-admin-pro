<template>
  <div class="app-container">
    <!-- 示例提现页（issue #137，spec §6）：申请（自助，user_account 服务端取
        登录主体）→ 管理端审批（创建转账单，wallet 渠道入账申请人钱包）→
        转账成功经 #134 回调链路联动提现单 10；关闭置 20 不建转账单。
        审批/关闭按钮走独立权限码 hasPerm 显隐（spec §6）。 -->
    <el-card shadow="never" class="toolbar-card">
      <div v-if="canCreate" class="toolbar-row">
        <el-input v-model="subject" placeholder="提现标题" style="width: 240px" />
        <el-input v-model="price" placeholder="提现金额（元），如 20.00" style="width: 200px" />
        <el-button type="primary" :loading="creating" :disabled="!subject || !price" @click="doCreate">
          申请提现（钱包渠道，v1 仅支持本人）
        </el-button>
        <span class="toolbar-hint">收款账号/姓名由服务端取登录主体（请求不收）</span>
      </div>
    </el-card>

    <el-card shadow="never" class="list-card">
      <template #header>
        <span class="section-title">示例提现单</span>
      </template>
      <el-table v-loading="loadingList" :data="rows" border stripe>
        <el-table-column prop="id" label="提现单 ID" width="100">
          <template #default="{ row }">WD-{{ row.id }}</template>
        </el-table-column>
        <el-table-column prop="subject" label="标题" min-width="140" show-overflow-tooltip />
        <el-table-column label="金额" width="100" align="right">
          <template #default="{ row }">
            <span class="amount">{{ row.price }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="userAccount" label="收款账号" width="110" />
        <el-table-column prop="userName" label="收款人" width="110" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="转账单" width="100">
          <template #default="{ row }">
            <span v-if="row.payTransferId">ZZ 单据 {{ row.payTransferId }}</span>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="转账渠道" width="100">
          <template #default="{ row }">{{ channelName(row.transferChannelCode) }}</template>
        </el-table-column>
        <el-table-column label="转账时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.transferTime) || '-' }}</template>
        </el-table-column>
        <el-table-column label="失败信息" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">{{ row.transferErrorMsg || '-' }}</template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="canApprove && row.status === 0 && !row.payTransferId"
              link
              type="primary"
              @click="doApprove(row)"
            >
              审批
            </el-button>
            <el-button
              v-if="canClose && row.status === 0 && !row.payTransferId"
              link
              type="danger"
              @click="doClose(row)"
            >
              关闭
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
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import { createDemoWithdraw, pageDemoWithdraws, approveDemoWithdraw, closeDemoWithdraw } from '@/api/pay'

// 示例提现页（issue #137，spec §4.8/§6）：申请端点登录即可（自助，归属即登录
// 主体），按钮 hasPerm 显隐（pay:demo-withdraw:create / approve / close，菜单
// 831 + 按钮 8311–8313）。审批创建转账单（wallet 渠道入账申请人钱包，消费方
// 单号 WD-<提现单 ID>），转账成功经回调链路联动 10；仅等待且未发起转账可关闭
//（转账在途 409）。

const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('pay:demo-withdraw:create'))
const canApprove = computed(() => userStore.hasPerm('pay:demo-withdraw:approve'))
const canClose = computed(() => userStore.hasPerm('pay:demo-withdraw:close'))

const subject = ref('')
const price = ref('')
const creating = ref(false)

const rows = ref<any[]>([])
const total = ref(0)
const loadingList = ref(false)
const query = ref({ pageNo: 1, pageSize: 10 })

const statusLabels: Record<number, string> = { 0: '等待', 10: '成功', 20: '关闭' }
function statusLabel(status: number) {
  return statusLabels[status] ?? String(status)
}
function statusTagType(status: number) {
  if (status === 10) return 'success'
  if (status === 20) return 'info'
  return 'warning'
}

const channelLabels: Record<string, string> = { mock: '模拟渠道', wallet: '钱包' }
function channelName(code: string) {
  return channelLabels[code] ?? (code || '-')
}

async function loadList() {
  loadingList.value = true
  try {
    const data = await pageDemoWithdraws(query.value)
    rows.value = data?.list || []
    total.value = data?.total || 0
  } finally {
    loadingList.value = false
  }
}

async function doCreate() {
  creating.value = true
  try {
    await createDemoWithdraw({ subject: subject.value, price: price.value })
    ElMessage.success('提现申请已提交，等待管理员审批')
    subject.value = ''
    price.value = ''
    await loadList()
  } finally {
    creating.value = false
  }
}

async function doApprove(row: any) {
  const confirmed = await ElMessageBox.confirm(
    `审批后将创建转账单（wallet 渠道向收款账号 ${row.userAccount} 入账 ${row.price} 元），转账成功经回调链路联动提现单成功。`,
    '提现审批',
    { type: 'warning', confirmButtonText: '确认审批', cancelButtonText: '取消' },
  ).catch(() => false)
  if (!confirmed) return
  await approveDemoWithdraw(row.id)
  ElMessage.success('审批完成，转账已发起')
  await loadList()
}

async function doClose(row: any) {
  const confirmed = await ElMessageBox.confirm(
    `关闭后提现单置 20（不创建转账单），不可恢复。`,
    '关闭提现',
    { type: 'warning', confirmButtonText: '确认关闭', cancelButtonText: '取消' },
  ).catch(() => false)
  if (!confirmed) return
  await closeDemoWithdraw(row.id)
  ElMessage.success('提现单已关闭')
  await loadList()
}

onMounted(() => {
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

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}
</style>
