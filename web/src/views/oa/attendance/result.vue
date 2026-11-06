<template>
  <div class="app-container">
    <el-card shadow="never">
      <el-form :inline="true" class="search-form">
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
        <el-form-item label="日期">
          <el-date-picker
            v-model="dateRange"
            type="daterange"
            value-format="YYYY-MM-DD"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            :clearable="true"
          />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="query.status" clearable placeholder="全部状态" style="width: 150px">
            <el-option
              v-for="opt in statusOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

      <div class="toolbar">
        <el-button type="primary" @click="openRecompute">手工重算</el-button>
        <span class="hint">
          结果由打卡 + 工作日历 + 班次派生；管理员校正只写 override 状态与备注，不覆盖原始打卡，校正优先展示。
        </span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="workDate" label="日期" width="110" />
        <el-table-column label="员工" min-width="150">
          <template #default="{ row }">
            {{ row.nickname || '-' }}（#{{ row.userId }}）
          </template>
        </el-table-column>
        <el-table-column label="生效状态" width="140">
          <template #default="{ row }">
            <el-tag :type="resultTagType(row.status)" size="small">{{ row.statusName }}</el-tag>
            <el-tag v-if="row.corrected" type="warning" size="small" class="ml8">已校正</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="派生状态" width="110">
          <template #default="{ row }">
            <el-tag type="info" size="small" effect="plain">{{ row.derivedStatusName }}</el-tag>
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
        <el-table-column prop="computedAt" label="计算时间" min-width="160">
          <template #default="{ row }">{{ formatTime(row.computedAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openCorrect(row)">校正</el-button>
            <el-button v-if="row.corrected" link type="warning" @click="clearCorrection(row)">
              撤销校正
            </el-button>
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

    <el-dialog v-model="correctDialog.visible" title="考勤校正" width="520px" append-to-body>
      <el-descriptions :column="2" border size="small">
        <el-descriptions-item label="员工">
          {{ correctDialog.row?.nickname || '-' }}（#{{ correctDialog.row?.userId }}）
        </el-descriptions-item>
        <el-descriptions-item label="日期">{{ correctDialog.row?.workDate }}</el-descriptions-item>
        <el-descriptions-item label="原始上班卡">
          {{ formatTime(correctDialog.row?.inTime) }}
        </el-descriptions-item>
        <el-descriptions-item label="原始下班卡">
          {{ formatTime(correctDialog.row?.outTime) }}
        </el-descriptions-item>
        <el-descriptions-item label="派生状态">
          {{ correctDialog.row?.derivedStatusName }}
        </el-descriptions-item>
        <el-descriptions-item label="迟到/早退">
          {{ correctDialog.row?.lateMinutes }} / {{ correctDialog.row?.earlyMinutes }} 分钟
        </el-descriptions-item>
      </el-descriptions>
      <el-form label-width="90px" class="correct-form">
        <el-form-item label="校正状态" required>
          <el-select v-model="correctDialog.overrideStatus" placeholder="请选择校正状态" style="width: 100%">
            <el-option
              v-for="opt in statusOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="校正备注" required>
          <el-input
            v-model="correctDialog.remark"
            type="textarea"
            :rows="3"
            maxlength="200"
            show-word-limit
            placeholder="请填写校正原因（1–200 字）"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="correctDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="correctDialog.loading" @click="submitCorrect">
          确定
        </el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="recomputeDialog.visible" title="手工重算" width="480px" append-to-body>
      <el-form label-width="90px">
        <el-form-item label="员工" required>
          <el-select
            v-model="recomputeDialog.userId"
            filterable
            placeholder="请选择员工"
            style="width: 100%"
          >
            <el-option
              v-for="u in users"
              :key="u.id"
              :label="`${u.nickname}（#${u.id}）`"
              :value="u.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="日期范围" required>
          <el-date-picker
            v-model="recomputeDialog.range"
            type="daterange"
            value-format="YYYY-MM-DD"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            style="width: 100%"
          />
        </el-form-item>
      </el-form>
      <div class="field-hint">
        重算按用户 + 日期范围（含两端，最多 366 天）覆盖派生字段；管理员校正与原始打卡不受影响。
      </div>
      <template #footer>
        <el-button @click="recomputeDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="recomputeDialog.loading" @click="submitRecompute">
          重算
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { listSimpleUsers } from '@/api/system'
import { formatTime, statusTagType } from '@/utils/format'
import {
  pageAttendanceResults,
  recomputeAttendanceResults,
  correctAttendanceResult,
  clearAttendanceResultCorrection,
} from '@/api/oa'

// 考勤结果与校正（issue #61，spec §4.10/§6）：管理端查询 + 管理员校正 +
// 手工重算。查询挂 oa:attendance:result:query（菜单 533）；校正/重算挂
// oa:attendance:result:correct（按钮 5331）。可见范围由后端收口：持有
// oa:attendance:manage（按钮 5332）看全租户，部门负责人看子树，否则仅本人。
// 校正只写 override 状态与备注，不覆盖原始打卡；列表以「已校正」标记区分。

interface ResultRow {
  id: number
  userId: number
  nickname: string
  workDate: string
  inTime: string
  outTime: string
  lateMinutes: number
  earlyMinutes: number
  missingIn: boolean
  missingOut: boolean
  leaveDays: number
  status: number
  statusName: string
  derivedStatus: number
  derivedStatusName: string
  corrected: boolean
  overrideStatus: number | null
  overrideRemark: string
  computedAt: string
}

const statusOptions = [
  { label: '正常', value: 0 },
  { label: '迟到', value: 1 },
  { label: '早退', value: 2 },
  { label: '迟到且早退', value: 3 },
  { label: '缺卡', value: 4 },
  { label: '休息', value: 5 },
  { label: '请假', value: 6 },
]

const users = ref<any[]>([])
const list = ref<ResultRow[]>([])
const total = ref(0)
const loading = ref(false)
const dateRange = ref<string[]>(currentMonthRange())
const query = reactive<{ pageNo: number; pageSize: number; userId?: number; status?: number }>({
  pageNo: 1,
  pageSize: 10,
})

const correctDialog = reactive<{
  visible: boolean
  loading: boolean
  row: ResultRow | null
  overrideStatus?: number
  remark: string
}>({ visible: false, loading: false, row: null, remark: '' })

const recomputeDialog = reactive<{
  visible: boolean
  loading: boolean
  userId?: number
  range: string[]
}>({ visible: false, loading: false, range: [] })

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

async function load() {
  loading.value = true
  try {
    const data = await pageAttendanceResults({
      pageNo: query.pageNo,
      pageSize: query.pageSize,
      userId: query.userId || 0,
      startDate: dateRange.value?.[0] || '',
      endDate: dateRange.value?.[1] || '',
      status: query.status === undefined || query.status === null ? undefined : query.status,
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
  query.status = undefined
  dateRange.value = currentMonthRange()
  query.pageNo = 1
  load()
}

function openCorrect(row: ResultRow) {
  correctDialog.row = row
  correctDialog.overrideStatus = row.overrideStatus ?? row.derivedStatus
  correctDialog.remark = row.overrideRemark || ''
  correctDialog.visible = true
}

async function submitCorrect() {
  if (!correctDialog.row) return
  if (correctDialog.overrideStatus === undefined || correctDialog.overrideStatus === null) {
    ElMessage.warning('请选择校正状态')
    return
  }
  if (!correctDialog.remark.trim()) {
    ElMessage.warning('请填写校正备注')
    return
  }
  correctDialog.loading = true
  try {
    await correctAttendanceResult(correctDialog.row.id, {
      overrideStatus: correctDialog.overrideStatus,
      remark: correctDialog.remark.trim(),
    })
    ElMessage.success('校正已生效')
    correctDialog.visible = false
    await load()
  } finally {
    correctDialog.loading = false
  }
}

async function clearCorrection(row: ResultRow) {
  await ElMessageBox.confirm(
    `确认撤销 ${row.workDate} 的考勤校正吗？撤销后按派生结果显示。`,
    '提示',
    { type: 'warning' },
  )
  await clearAttendanceResultCorrection(row.id)
  ElMessage.success('已撤销校正')
  await load()
}

function openRecompute() {
  recomputeDialog.userId = undefined
  recomputeDialog.range = currentMonthRange()
  recomputeDialog.visible = true
}

async function submitRecompute() {
  if (!recomputeDialog.userId) {
    ElMessage.warning('请选择员工')
    return
  }
  if (!recomputeDialog.range || recomputeDialog.range.length !== 2) {
    ElMessage.warning('请选择日期范围')
    return
  }
  recomputeDialog.loading = true
  try {
    const data = await recomputeAttendanceResults({
      userId: recomputeDialog.userId,
      startDate: recomputeDialog.range[0],
      endDate: recomputeDialog.range[1],
    })
    ElMessage.success(`已重算 ${data?.days || 0} 天`)
    recomputeDialog.visible = false
    await load()
  } finally {
    recomputeDialog.loading = false
  }
}

onMounted(async () => {
  // 用户下拉依赖 system:user:simple-list；未授权时降级为空列表，
  // 结果表格仍按可见范围正常加载（不因下拉失效而白屏）。
  try {
    users.value = (await listSimpleUsers()) || []
  } catch {
    users.value = []
  }
  await load()
})
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

.hint {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}

.ml8 {
  margin-left: 8px;
}

.correct-form {
  margin-top: 16px;
}
</style>
