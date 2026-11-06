<template>
  <div class="app-container">
    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <span class="title">我的请假额度（{{ year }} 年）</span>
        </div>
        <div class="toolbar-right">
          <el-select v-model="year" style="width: 110px" @change="loadMine">
            <el-option v-for="y in yearOptions" :key="y" :label="`${y} 年`" :value="y" />
          </el-select>
        </div>
      </div>
      <el-table v-loading="loading" :data="items">
        <el-table-column prop="typeName" label="类型" min-width="110" />
        <el-table-column prop="typeCode" label="编码" min-width="100" />
        <el-table-column label="占用额度" width="90">
          <template #default="{ row }">
            <el-tag :type="row.balanceManaged ? 'success' : 'info'" size="small">
              {{ row.balanceManaged ? '占用' : '不占' }}
            </el-tag>
          </template>
        </el-table-column>
        <template v-if="hasManaged">
          <el-table-column label="初始额度" width="90">
            <template #default="{ row }">{{ row.balance?.quota ?? row.annualQuota }}</template>
          </el-table-column>
          <el-table-column label="调整累计" width="90">
            <template #default="{ row }">{{ row.balance?.adjustment ?? '0.00' }}</template>
          </el-table-column>
          <el-table-column label="已使用" width="90">
            <template #default="{ row }">{{ row.balance?.used ?? '0.00' }}</template>
          </el-table-column>
          <el-table-column label="可用" width="90">
            <template #default="{ row }">
              <span :class="Number(row.balance?.available ?? 0) < 0 ? 'negative' : ''">
                {{ row.balance?.available ?? '0.00' }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="最近调整" min-width="180" show-overflow-tooltip>
            <template #default="{ row }">
              {{ row.balance?.adjustReason || '-' }}
            </template>
          </el-table-column>
        </template>
      </el-table>
      <div class="hint">
        占用额度的类型在提交请假时检查可用额度（初始额度 + 调整 − 已使用）；不占用额度的类型不限额度。
        未初始化的类型余额为零，请联系管理员进行年度初始化。
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getMyLeaveBalances } from '@/api/oa'

// 我的额度页（issue #51，菜单 528）：登录即可（后端 GET /oa/leave/balance/mine
// 同为登录即可），只读展示本人按类型/年度的余额。
interface BalanceItem {
  year: number
  quota: string
  adjustment: string
  used: string
  available: string
  adjustReason: string
  updater: number
  updateTime: string
}

interface MineItem {
  leaveTypeId: number
  typeCode: string
  typeName: string
  balanceManaged: boolean
  annualQuota: string
  balance: BalanceItem | null
}

const currentYear = new Date().getFullYear()
const yearOptions = Array.from({ length: 5 }, (_, i) => currentYear - 1 + i)

const loading = ref(false)
const year = ref(currentYear)
const items = ref<MineItem[]>([])

const hasManaged = computed(() => items.value.some((i) => i.balanceManaged))

async function loadMine() {
  loading.value = true
  try {
    const data = await getMyLeaveBalances(year.value)
    items.value = data.items || []
  } finally {
    loading.value = false
  }
}

onMounted(loadMine)
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}
.toolbar-left,
.toolbar-right {
  display: flex;
  gap: 8px;
  align-items: center;
}
.title {
  font-weight: 600;
}
.hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-top: 10px;
}
.negative {
  color: var(--el-color-danger);
}
</style>
