<template>
  <div class="app-container">
    <el-card shadow="never">
      <el-form :inline="true" class="search-form">
        <el-form-item label="月份">
          <el-date-picker
            v-model="query.month"
            type="month"
            value-format="YYYY-MM"
            :clearable="false"
            placeholder="选择月份"
          />
        </el-form-item>
        <el-form-item label="员工">
          <el-select
            v-model="query.userId"
            filterable
            clearable
            placeholder="全部可见范围"
            style="width: 200px"
          >
            <el-option
              v-for="u in users"
              :key="u.id"
              :label="`${u.nickname}（#${u.id}）`"
              :value="u.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

      <div class="hint">
        汇总按用户/月聚合生效状态（管理员校正优先）：出勤天数 = 正常 + 迟到 + 早退 +
        迟到且早退；请假天数含半天（0.5）。可见范围：HR/管理员看全租户、部门负责人看
        部门子树、其他员工仅本人。
      </div>

      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column label="员工" min-width="150">
          <template #default="{ row }">
            {{ row.nickname || '-' }}（#{{ row.userId }}）
          </template>
        </el-table-column>
        <el-table-column prop="month" label="月份" width="100" />
        <el-table-column prop="attendanceDays" label="出勤天数" width="100" />
        <el-table-column prop="workDays" label="工作日" width="90" />
        <el-table-column prop="normal" label="正常" width="80" />
        <el-table-column prop="late" label="迟到" width="80" />
        <el-table-column prop="early" label="早退" width="80" />
        <el-table-column prop="lateAndEarly" label="迟到且早退" width="110" />
        <el-table-column prop="missing" label="缺卡" width="80" />
        <el-table-column prop="rest" label="休息" width="80" />
        <el-table-column prop="leaveDays" label="请假天数" width="100" />
        <el-table-column prop="lateMinutes" label="迟到(分)" width="90" />
        <el-table-column prop="earlyMinutes" label="早退(分)" width="90" />
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="showDetail(row)">当月明细</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-model:current-page="query.pageNo"
        v-model:page-size="query.pageSize"
        :total="total"
        :page-sizes="[10, 20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @size-change="load"
        @current-change="load"
      />
    </el-card>

    <el-drawer v-model="detail.visible" :title="detail.title" size="720px">
      <el-table v-loading="detail.loading" :data="detail.rows" border stripe size="small">
        <el-table-column prop="workDate" label="日期" width="110" />
        <el-table-column label="生效状态" width="130">
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
        <el-table-column prop="leaveDays" label="请假(天)" width="90" />
      </el-table>
      <el-pagination
        v-model:current-page="detail.pageNo"
        v-model:page-size="detail.pageSize"
        :total="detail.total"
        :page-sizes="[10, 20, 50]"
        layout="total, prev, pager, next"
        class="pager"
        @current-change="loadDetail"
      />
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { listSimpleUsers } from '@/api/system'
import { formatTime, statusTagType } from '@/utils/format'
import { pageAttendanceSummaries, pageAttendanceResults } from '@/api/oa'

// 考勤月度汇总（issue #62，spec §4.10/§6）：管理端按用户/月聚合出勤、正常、
// 迟到、早退、缺卡、请假天数。查询挂 oa:attendance:summary:query（菜单 534）；
// 可见范围由后端收口（oa:attendance:manage 全租户 / 部门负责人子树 / 本人）。
// 「当月明细」抽屉复用每日结果分页接口，便于逐项核对汇总口径。

interface SummaryRow {
  userId: number
  nickname: string
  month: string
  days: number
  workDays: number
  attendanceDays: number
  normal: number
  late: number
  early: number
  lateAndEarly: number
  missing: number
  rest: number
  leave: number
  leaveDays: number
  lateMinutes: number
  earlyMinutes: number
}

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
  leaveDays: number
}

const users = ref<any[]>([])
const list = ref<SummaryRow[]>([])
const total = ref(0)
const loading = ref(false)
const query = reactive<{ pageNo: number; pageSize: number; month: string; userId?: number }>({
  pageNo: 1,
  pageSize: 10,
  month: currentMonthKey(),
})

const detail = reactive<{
  visible: boolean
  loading: boolean
  title: string
  userId: number
  month: string
  pageNo: number
  pageSize: number
  total: number
  rows: ResultRow[]
}>({
  visible: false,
  loading: false,
  title: '',
  userId: 0,
  month: '',
  pageNo: 1,
  pageSize: 10,
  total: 0,
  rows: [],
})

function pad2(n: number) {
  return String(n).padStart(2, '0')
}

function currentMonthKey() {
  const now = new Date()
  return `${now.getFullYear()}-${pad2(now.getMonth() + 1)}`
}

function monthRange(month: string) {
  const [year, mon] = month.split('-').map((v) => Number(v))
  const lastDay = new Date(year, mon, 0).getDate()
  return [`${month}-01`, `${month}-${pad2(lastDay)}`]
}

// 结果状态标签色（与考勤结果页一致）：正常成功、迟到/早退警告、
// 迟到且早退/缺卡危险、休息信息、请假主色。
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

async function load() {
  loading.value = true
  try {
    const data = await pageAttendanceSummaries({
      pageNo: query.pageNo,
      pageSize: query.pageSize,
      month: query.month,
      userId: query.userId || 0,
    })
    list.value = data?.list || []
    total.value = data?.total || 0
  } finally {
    loading.value = false
  }
}

function search() {
  query.pageNo = 1
  load()
}

function reset() {
  query.userId = undefined
  query.month = currentMonthKey()
  query.pageNo = 1
  load()
}

function showDetail(row: SummaryRow) {
  detail.visible = true
  detail.userId = row.userId
  detail.month = row.month || query.month
  detail.title = `${row.nickname || '员工 #' + row.userId} · ${detail.month} 当月明细`
  detail.pageNo = 1
  detail.rows = []
  detail.total = 0
  loadDetail()
}

async function loadDetail() {
  if (!detail.userId || !detail.month) return
  detail.loading = true
  try {
    const [startDate, endDate] = monthRange(detail.month)
    const data = await pageAttendanceResults({
      pageNo: detail.pageNo,
      pageSize: detail.pageSize,
      userId: detail.userId,
      startDate,
      endDate,
    })
    detail.rows = data?.list || []
    detail.total = data?.total || 0
  } finally {
    detail.loading = false
  }
}

onMounted(async () => {
  // 用户下拉依赖 system:user:simple-list；未授权时降级为空列表，
  // 汇总表格仍按可见范围正常加载（不因下拉失效而白屏）。
  try {
    users.value = (await listSimpleUsers()) || []
  } catch {
    users.value = []
  }
  await load()
})
</script>

<style scoped>
.hint {
  margin-bottom: 12px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}

.ml8 {
  margin-left: 8px;
}
</style>
