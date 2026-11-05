<template>
  <div class="app-container">
    <el-card shadow="never">
      <!-- 工具栏：月份导航 + 统计 + 批量设置入口 -->
      <div class="toolbar">
        <div class="toolbar-left">
          <el-button :icon="ArrowLeft" circle @click="shiftMonth(-1)" />
          <span class="month-title">{{ monthTitle }}</span>
          <el-button :icon="ArrowRight" circle @click="shiftMonth(1)" />
          <el-button link type="primary" @click="goToday">本月</el-button>
        </div>
        <div class="toolbar-right">
          <span class="summary">
            <el-tag type="success" size="small" effect="plain">工作日 {{ countBy(0) }}</el-tag>
            <el-tag type="info" size="small" effect="plain">休息日 {{ countBy(1) }}</el-tag>
            <el-tag type="danger" size="small" effect="plain">节假日 {{ countBy(2) }}</el-tag>
          </span>
          <el-button v-if="canBatch" type="primary" plain @click="openBatch">批量设置</el-button>
        </div>
      </div>

      <!-- 月历网格（自建，不引入 FullCalendar；spec §6） -->
      <div v-loading="loading" class="calendar">
        <div class="week-header">
          <div v-for="name in weekNames" :key="name" class="week-name">{{ name }}</div>
        </div>
        <div v-for="(week, wi) in weeks" :key="wi" class="week-row">
          <div
            v-for="(day, di) in week"
            :key="day?.date ?? `empty-${di}`"
            class="day-cell"
            :class="dayClass(day)"
            @click="day && openSet(day)"
          >
            <template v-if="day">
              <div class="day-head">
                <span class="day-num">{{ Number(day.date.slice(8, 10)) }}</span>
                <el-tag v-if="day.explicit" size="small" effect="dark" :type="tagType(day.type)">
                  {{ typeName(day.type) }}
                </el-tag>
                <el-tag v-else size="small" effect="plain" :type="tagType(day.type)">
                  {{ typeName(day.type) }}
                </el-tag>
              </div>
              <div class="day-remark" :title="day.remark">{{ day.remark || (day.explicit ? '' : defaultHint(day)) }}</div>
            </template>
          </div>
        </div>
      </div>
      <div class="legend">
        实色标签 = 显式记录（点击日期可维护）；浅色标签 = 默认规则（周一至周五工作日、周六周日休息）。
      </div>
    </el-card>

    <!-- 单日设置弹窗 -->
    <el-dialog v-model="setDialog.visible" title="单日设置" width="420px" append-to-body>
      <el-form label-width="80px">
        <el-form-item label="日期">
          <span>{{ setDialog.date }}</span>
        </el-form-item>
        <el-form-item label="日类型">
          <el-radio-group v-model="setDialog.type">
            <el-radio-button :value="0">工作日</el-radio-button>
            <el-radio-button :value="1">休息日</el-radio-button>
            <el-radio-button :value="2">节假日</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="setDialog.remark" maxlength="200" show-word-limit placeholder="如节假日名称、调休说明" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button
          v-if="setDialog.explicit && canDelete"
          type="danger"
          plain
          @click="doClear(setDialog.date)"
        >
          恢复默认
        </el-button>
        <el-button @click="setDialog.visible = false">取消</el-button>
        <el-button v-if="canSet" type="primary" :loading="setDialog.saving" @click="doSet">保存</el-button>
      </template>
    </el-dialog>

    <!-- 批量设置弹窗 -->
    <el-dialog v-model="batchDialog.visible" title="批量设置" width="520px" append-to-body>
      <el-form label-width="90px">
        <el-form-item label="选择方式">
          <el-radio-group v-model="batchDialog.mode">
            <el-radio-button value="range">日期区间</el-radio-button>
            <el-radio-button value="dates">多选日期</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="batchDialog.mode === 'range'" label="日期区间">
          <el-date-picker
            v-model="batchDialog.range"
            type="daterange"
            value-format="YYYY-MM-DD"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            :clearable="true"
          />
        </el-form-item>
        <el-form-item v-if="batchDialog.mode === 'range'" label="限定星期">
          <el-checkbox-group v-model="batchDialog.weekdays">
            <el-checkbox v-for="(name, idx) in weekNames" :key="idx" :value="idx">{{ name }}</el-checkbox>
          </el-checkbox-group>
          <div class="field-hint">留空 = 区间内全部日期；勾选周六/周日可用于整年周末调休上班。</div>
        </el-form-item>
        <el-form-item v-else label="多选日期">
          <el-date-picker
            v-model="batchDialog.dates"
            type="dates"
            value-format="YYYY-MM-DD"
            placeholder="选择一个或多个日期"
          />
        </el-form-item>
        <el-form-item label="日类型">
          <el-radio-group v-model="batchDialog.type">
            <el-radio-button :value="0">工作日</el-radio-button>
            <el-radio-button :value="1">休息日</el-radio-button>
            <el-radio-button :value="2">节假日</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="batchDialog.remark" maxlength="200" show-word-limit placeholder="写入本批全部日期" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="batchDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="batchDialog.saving" @click="doBatch">应用</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ArrowLeft, ArrowRight } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { getWorkdayMonth, setWorkday, batchWorkday, clearWorkday } from '@/api/oa'

// 工作日历维护页（issue #50，spec §3.3/§6 考勤节）：按月日历视图 +
// 单日/批量维护。三态语义由后端保证（显式记录优先、无记录走默认规则），
// 页面只做展示与提交。

interface WorkdayDay {
  date: string
  weekday: number
  type: number
  explicit: boolean
  id: number
  remark: string
}

const userStore = useUserStore()
const canSet = computed(() => userStore.hasPerm('oa:workday:set'))
const canBatch = computed(() => userStore.hasPerm('oa:workday:batch'))
const canDelete = computed(() => userStore.hasPerm('oa:workday:delete'))

const weekNames = ['日', '一', '二', '三', '四', '五', '六']
const typeNames = ['工作日', '休息日', '节假日']

const loading = ref(false)
const month = ref(formatMonth(new Date()))
const days = ref<WorkdayDay[]>([])

const monthTitle = computed(() => month.value.replace('-', ' 年 ') + ' 月')

// weeks 把整月按周切行（周日起），首尾补 undefined 占位。
const weeks = computed<(WorkdayDay | undefined)[][]>(() => {
  if (days.value.length === 0) return []
  const first = days.value[0]
  const rows: (WorkdayDay | undefined)[][] = []
  let row: (WorkdayDay | undefined)[] = new Array(first.weekday).fill(undefined)
  for (const day of days.value) {
    row.push(day)
    if (row.length === 7) {
      rows.push(row)
      row = []
    }
  }
  if (row.length > 0) {
    rows.push(row)
  }
  return rows
})

function typeName(type: number): string {
  return typeNames[type] ?? `未知(${type})`
}
function tagType(type: number): 'success' | 'info' | 'danger' {
  if (type === 0) return 'success'
  if (type === 1) return 'info'
  return 'danger'
}
function countBy(type: number): number {
  return days.value.filter((d) => d.type === type).length
}
function dayClass(day: WorkdayDay | undefined) {
  return {
    'is-empty': !day,
    'is-rest': !!day && day.type === 1,
    'is-holiday': !!day && day.type === 2,
    'is-today': !!day && day.date === todayStr(),
  }
}
// 默认规则提示（非显式记录的周末给出「默认休息」说明，工作日不重复提示）。
function defaultHint(day: WorkdayDay): string {
  if (day.explicit) return ''
  return day.weekday === 0 || day.weekday === 6 ? '默认休息' : ''
}

function formatMonth(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}
function todayStr(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function shiftMonth(delta: number) {
  const [y, m] = month.value.split('-').map(Number)
  const next = new Date(y, m - 1 + delta, 1)
  month.value = formatMonth(next)
  void loadMonth()
}
function goToday() {
  month.value = formatMonth(new Date())
  void loadMonth()
}

async function loadMonth() {
  loading.value = true
  try {
    const data = await getWorkdayMonth(month.value)
    days.value = data.days || []
  } finally {
    loading.value = false
  }
}

// ---- 单日设置 ----
const setDialog = ref({ visible: false, saving: false, date: '', type: 0, remark: '', explicit: false })

function openSet(day: WorkdayDay) {
  setDialog.value = {
    visible: true,
    saving: false,
    date: day.date,
    type: day.type,
    remark: day.remark,
    explicit: day.explicit,
  }
}

async function doSet() {
  setDialog.value.saving = true
  try {
    await setWorkday({ date: setDialog.value.date, type: setDialog.value.type, remark: setDialog.value.remark })
    ElMessage.success('已保存')
    setDialog.value.visible = false
    await loadMonth()
  } finally {
    setDialog.value.saving = false
  }
}

async function doClear(date: string) {
  await ElMessageBox.confirm(`确认清除 ${date} 的显式记录、恢复默认规则吗？`, '恢复默认', { type: 'warning' })
  await clearWorkday(date)
  ElMessage.success('已恢复默认')
  setDialog.value.visible = false
  await loadMonth()
}

// ---- 批量设置 ----
const batchDialog = ref({
  visible: false,
  saving: false,
  mode: 'range' as 'range' | 'dates',
  range: [] as string[],
  dates: [] as string[],
  weekdays: [] as number[],
  type: 0,
  remark: '',
})

function openBatch() {
  batchDialog.value = {
    visible: true,
    saving: false,
    mode: 'range',
    range: [],
    dates: [],
    weekdays: [],
    type: 0,
    remark: '',
  }
}

async function doBatch() {
  const d = batchDialog.value
  const body: {
    type: number
    remark?: string
    dates?: string[]
    startDate?: string
    endDate?: string
    weekdays?: number[]
  } = { type: d.type, remark: d.remark }
  if (d.mode === 'range') {
    if (!d.range || d.range.length !== 2) {
      ElMessage.warning('请选择日期区间')
      return
    }
    body.startDate = d.range[0]
    body.endDate = d.range[1]
    if (d.weekdays.length > 0) {
      body.weekdays = [...d.weekdays].sort((a, b) => a - b)
    }
  } else {
    if (!d.dates || d.dates.length === 0) {
      ElMessage.warning('请选择日期')
      return
    }
    body.dates = [...d.dates].sort()
  }
  d.saving = true
  try {
    const resp = await batchWorkday(body)
    ElMessage.success(`已设置 ${resp.count} 天`)
    d.visible = false
    await loadMonth()
  } finally {
    d.saving = false
  }
}

onMounted(loadMonth)
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.month-title {
  font-size: 18px;
  font-weight: 600;
  min-width: 130px;
  text-align: center;
}
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.summary {
  display: flex;
  gap: 6px;
}
.week-header,
.week-row {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
}
.week-name {
  text-align: center;
  color: var(--el-text-color-secondary);
  padding: 6px 0;
  font-size: 13px;
}
.day-cell {
  min-height: 76px;
  border: 1px solid var(--el-border-color-lighter);
  padding: 6px;
  cursor: pointer;
  transition: background-color 0.15s;
}
.day-cell:hover {
  background-color: var(--el-fill-color-light);
}
.day-cell.is-empty {
  cursor: default;
  background-color: var(--el-fill-color-lighter);
}
.day-cell.is-rest {
  background-color: var(--el-color-info-light-9);
}
.day-cell.is-holiday {
  background-color: var(--el-color-danger-light-9);
}
.day-cell.is-today {
  outline: 2px solid var(--el-color-primary-light-5);
  outline-offset: -2px;
}
.day-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.day-num {
  font-size: 15px;
  font-weight: 600;
}
.day-remark {
  margin-top: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.legend {
  margin-top: 10px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.field-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}
</style>
