<template>
  <div class="app-container">
    <el-card shadow="never" class="punch-card">
      <div class="punch-head">
        <div>
          <div class="punch-date">{{ today.date || '-' }}</div>
          <div v-if="today.group" class="punch-group">
            考勤组：{{ today.group.name }}
            <template v-if="today.group.shift">
              ｜班次：{{ today.group.shift.name }}（{{ today.group.shift.startTime }}-{{
                today.group.shift.endTime
              }}，宽限 {{ today.group.shift.graceMinutes }} 分钟）
            </template>
            <el-tag v-if="today.group.status !== 0" type="info" size="small" class="ml8">已停用</el-tag>
          </div>
          <div v-else class="punch-group">尚未加入任何考勤组</div>
        </div>
        <el-button type="primary" :loading="todayLoading" @click="loadToday">刷新</el-button>
      </div>

      <el-alert
        v-if="!today.group"
        type="warning"
        show-icon
        :closable="false"
        title="你尚未加入任何考勤组，无法打卡，请联系管理员将你加入考勤组。"
        class="punch-alert"
      />
      <el-alert
        v-else-if="today.group.status !== 0"
        type="warning"
        show-icon
        :closable="false"
        title="你所在的考勤组已停用，无法打卡，请联系管理员。"
        class="punch-alert"
      />

      <div class="punch-actions">
        <el-button
          type="primary"
          size="large"
          :disabled="!punchable"
          :loading="punching === 'in'"
          @click="doPunch('in')"
        >
          上班打卡
        </el-button>
        <el-button
          type="success"
          size="large"
          :disabled="!punchable"
          :loading="punching === 'out'"
          @click="doPunch('out')"
        >
          下班打卡
        </el-button>
      </div>

      <el-table :data="today.punches" border stripe size="small" empty-text="今天还没有打卡记录">
        <el-table-column prop="typeName" label="类型" width="90">
          <template #default="{ row }">
            <el-tag :type="row.type === 0 ? 'primary' : 'success'" size="small">{{ row.typeName }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="time" label="打卡时间" min-width="180">
          <template #default="{ row }">{{ formatTime(row.time) }}</template>
        </el-table-column>
        <el-table-column prop="sourceName" label="来源" width="90" />
        <el-table-column prop="ip" label="IP" min-width="130" />
        <el-table-column prop="userAgent" label="User-Agent" min-width="200" />
      </el-table>
    </el-card>

    <el-card shadow="never" class="records-card">
      <div class="records-head">
        <span class="records-title">我的打卡记录</span>
        <el-date-picker
          v-model="dateRange"
          type="daterange"
          value-format="YYYY-MM-DD"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          :clearable="true"
          @change="searchRecords"
        />
      </div>
      <el-table v-loading="recordsLoading" :data="records" border stripe size="small">
        <el-table-column prop="date" label="日期" width="110" />
        <el-table-column prop="typeName" label="类型" width="90" />
        <el-table-column prop="time" label="打卡时间" min-width="180">
          <template #default="{ row }">{{ formatTime(row.time) }}</template>
        </el-table-column>
        <el-table-column prop="sourceName" label="来源" width="90" />
        <el-table-column prop="ip" label="IP" min-width="130" />
        <el-table-column prop="userAgent" label="User-Agent" min-width="200" />
      </el-table>
      <el-pagination
        v-model:current-page="query.pageNo"
        v-model:page-size="query.pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @size-change="loadRecords"
        @current-change="loadRecords"
      />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { formatTime } from '@/utils/format'
import { getPunchToday, punchIn, punchOut, pageMyPunches } from '@/api/oa'

// 我的考勤（issue #60，spec §4.10/§6）：Web 打卡卡片 + 当天打卡 + 我的打卡记录。
// 员工自助页登录即可用（菜单 531，无权限码）；无启用考勤组时后端打卡返回
// 明确 400，页面据 group 状态禁用按钮并提示。

interface PunchRow {
  id: number
  type: number
  typeName: string
  date: string
  time: string
  source: number
  sourceName: string
  ip: string
  userAgent: string
  groupId: number
  groupName: string
}

const today = ref<{
  date: string
  group: null | { id: number; name: string; status: number; shift?: any }
  punches: PunchRow[]
}>({ date: '', group: null, punches: [] })
const todayLoading = ref(false)
const punching = ref<'' | 'in' | 'out'>('')

const query = reactive({ pageNo: 1, pageSize: 10 })
const dateRange = ref<string[]>([])
const records = ref<PunchRow[]>([])
const total = ref(0)
const recordsLoading = ref(false)

const punchable = computed(() => !!today.value.group && today.value.group.status === 0)

async function loadToday() {
  todayLoading.value = true
  try {
    const data = await getPunchToday()
    today.value = {
      date: data?.date || '',
      group: data?.group || null,
      punches: data?.punches || [],
    }
  } finally {
    todayLoading.value = false
  }
}

async function doPunch(kind: 'in' | 'out') {
  punching.value = kind
  try {
    const row = kind === 'in' ? await punchIn() : await punchOut()
    ElMessage.success(`${row?.typeName || (kind === 'in' ? '上班' : '下班')}打卡成功`)
    await Promise.all([loadToday(), loadRecords()])
  } finally {
    punching.value = ''
  }
}

async function loadRecords() {
  recordsLoading.value = true
  try {
    const data = await pageMyPunches({
      pageNo: query.pageNo,
      pageSize: query.pageSize,
      startDate: dateRange.value?.[0] || '',
      endDate: dateRange.value?.[1] || '',
    })
    records.value = data?.list || []
    total.value = data?.total || 0
  } finally {
    recordsLoading.value = false
  }
}

function searchRecords() {
  query.pageNo = 1
  loadRecords()
}

onMounted(() => {
  loadToday()
  loadRecords()
})
</script>

<style scoped>
.punch-card {
  margin-bottom: 16px;
}

.punch-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.punch-date {
  font-size: 20px;
  font-weight: 600;
}

.punch-group {
  margin-top: 6px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.punch-alert {
  margin-top: 12px;
}

.punch-actions {
  display: flex;
  gap: 16px;
  margin: 20px 0 16px;
}

.records-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.records-title {
  font-size: 15px;
  font-weight: 600;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}

.ml8 {
  margin-left: 8px;
}
</style>
