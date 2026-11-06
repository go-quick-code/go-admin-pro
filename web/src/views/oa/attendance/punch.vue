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

    <el-card shadow="never" class="results-card">
      <div class="records-head">
        <span class="records-title">我的考勤结果</span>
        <el-date-picker
          v-model="resultRange"
          type="daterange"
          value-format="YYYY-MM-DD"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          :clearable="true"
          @change="searchResults"
        />
      </div>
      <el-table v-loading="resultsLoading" :data="results" border stripe size="small">
        <el-table-column prop="workDate" label="日期" width="110" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="resultTagType(row.status)" size="small">{{ row.statusName }}</el-tag>
            <el-tag v-if="row.corrected" type="warning" size="small" class="ml8">已校正</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="inTime" label="上班打卡" min-width="150">
          <template #default="{ row }">{{ formatTime(row.inTime) }}</template>
        </el-table-column>
        <el-table-column prop="outTime" label="下班打卡" min-width="150">
          <template #default="{ row }">{{ formatTime(row.outTime) }}</template>
        </el-table-column>
        <el-table-column prop="lateMinutes" label="迟到(分)" width="90" />
        <el-table-column prop="earlyMinutes" label="早退(分)" width="90" />
        <el-table-column label="缺卡" width="110">
          <template #default="{ row }">
            {{ row.missingIn ? '缺上班卡' : '' }}{{ row.missingIn && row.missingOut ? '、' : ''
            }}{{ row.missingOut ? '缺下班卡' : '' }}{{ !row.missingIn && !row.missingOut ? '-' : '' }}
          </template>
        </el-table-column>
        <el-table-column prop="leaveDays" label="请假(天)" width="90" />
        <el-table-column prop="overrideRemark" label="校正备注" min-width="160">
          <template #default="{ row }">{{ row.overrideRemark || '-' }}</template>
        </el-table-column>
      </el-table>
      <el-pagination
        v-model:current-page="resultQuery.pageNo"
        v-model:page-size="resultQuery.pageSize"
        :total="resultTotal"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @size-change="loadResults"
        @current-change="loadResults"
      />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { formatTime, statusTagType } from '@/utils/format'
import { getPunchToday, punchIn, punchOut, pageMyPunches, pageMyAttendanceResults } from '@/api/oa'

// 我的考勤（issue #60，spec §4.10/§6）：Web 打卡卡片 + 当天打卡 + 我的打卡记录。
// 员工自助页登录即可用（菜单 531，无权限码）；无启用考勤组时后端打卡返回
// 明确 400，页面据 group 状态禁用按钮并提示。
// 考勤结果列表（issue #61）：按日期区间查看派生结果（正常/迟到/早退/缺卡/休息）
// 与管理员校正标记（校正优先，原始打卡仍完整展示）。

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

// 考勤结果列表：默认查询当月，避免首屏扫全表。
interface ResultRow {
  id: number
  workDate: string
  status: number
  statusName: string
  corrected: boolean
  inTime: string
  outTime: string
  lateMinutes: number
  earlyMinutes: number
  missingIn: boolean
  missingOut: boolean
  leaveDays: number
  overrideRemark: string
}

const resultQuery = reactive({ pageNo: 1, pageSize: 10 })
const resultRange = ref<string[]>(currentMonthRange())
const results = ref<ResultRow[]>([])
const resultTotal = ref(0)
const resultsLoading = ref(false)

function pad2(n: number) {
  return String(n).padStart(2, '0')
}

function formatDay(d: Date) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}

function currentMonthRange() {
  const now = new Date()
  return [formatDay(new Date(now.getFullYear(), now.getMonth(), 1)), formatDay(now)]
}

// 结果状态标签色：正常成功、迟到/早退警告、迟到且早退/缺卡危险、休息信息、请假主色。
function resultTagType(status: number) {
  return statusTagType(status, {
    0: 'success',
    1: 'warning',
    2: 'warning',
    3: 'danger',
    4: 'danger',
    5: 'info',
    6: 'primary',
  })
}

async function loadResults() {
  resultsLoading.value = true
  try {
    const data = await pageMyAttendanceResults({
      pageNo: resultQuery.pageNo,
      pageSize: resultQuery.pageSize,
      startDate: resultRange.value?.[0] || '',
      endDate: resultRange.value?.[1] || '',
    })
    results.value = data?.list || []
    resultTotal.value = data?.total || 0
  } finally {
    resultsLoading.value = false
  }
}

function searchResults() {
  resultQuery.pageNo = 1
  loadResults()
}

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
  loadResults()
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

.results-card {
  margin-top: 16px;
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
