<template>
  <div class="app-container">
    <el-card shadow="never">
      <!-- 工具栏：视图切换 + 时间导航 + 新建 -->
      <div class="toolbar">
        <div class="toolbar-left">
          <el-radio-group v-model="viewMode" size="small" @change="onViewChange">
            <el-radio-button value="month">月视图</el-radio-button>
            <el-radio-button value="week">周视图</el-radio-button>
            <el-radio-button value="list">列表</el-radio-button>
          </el-radio-group>
          <el-button :icon="ArrowLeft" circle size="small" @click="shift(-1)" />
          <span class="range-title">{{ rangeTitle }}</span>
          <el-button :icon="ArrowRight" circle size="small" @click="shift(1)" />
          <el-button link type="primary" @click="goToday">今天</el-button>
        </div>
        <div class="toolbar-right">
          <el-input
            v-model="keyword"
            placeholder="搜索标题"
            clearable
            size="small"
            class="keyword"
            @keyup.enter="load"
            @clear="load"
          />
          <el-button v-if="canCreate" type="primary" @click="openCreate()">新建日程</el-button>
        </div>
      </div>

      <!-- 月视图：自建网格（不引入 FullCalendar，spec §6） -->
      <div v-if="viewMode === 'month'" v-loading="loading" class="calendar">
        <div class="week-header">
          <div v-for="name in weekNames" :key="name" class="week-name">{{ name }}</div>
        </div>
        <div v-for="(week, wi) in monthWeeks" :key="wi" class="week-row">
          <div
            v-for="day in week"
            :key="day.date"
            class="day-cell"
            :class="dayClass(day)"
            @click="canCreate && openCreate(day.date)"
          >
            <div class="day-head">
              <span class="day-num">{{ day.day }}</span>
              <el-tag v-if="day.isToday" size="small" effect="plain">今天</el-tag>
            </div>
            <div class="day-events">
              <div
                v-for="event in eventsOn(day.date)"
                :key="event.id"
                class="event-chip"
                :class="`visibility-${event.visibility}`"
                :title="eventTooltip(event)"
                @click.stop="openEvent(event)"
              >
                <span class="event-time">{{ event.allDay ? '全天' : event.startTime.slice(11, 16) }}</span>
                <span class="event-title">{{ event.title }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 周视图：周一起始的七列，逐日列出日程 -->
      <div v-else-if="viewMode === 'week'" v-loading="loading" class="week-view">
        <div v-for="day in weekDays" :key="day.date" class="week-column" :class="dayClass(day)">
          <div class="week-day-head" @click="canCreate && openCreate(day.date)">
            <span class="week-day-name">周{{ weekNames[(day.weekday + 6) % 7] }}</span>
            <span class="week-day-num">{{ day.day }}</span>
            <el-tag v-if="day.isToday" size="small" effect="plain">今天</el-tag>
          </div>
          <div class="week-day-events">
            <div
              v-for="event in eventsOn(day.date)"
              :key="event.id"
              class="event-card"
              :class="`visibility-${event.visibility}`"
              @click="openEvent(event)"
            >
              <div class="event-card-time">
                {{ event.allDay ? '全天' : `${event.startTime.slice(11, 16)} - ${event.endTime.slice(11, 16)}` }}
              </div>
              <div class="event-card-title">{{ event.title }}</div>
              <div v-if="event.description" class="event-card-desc">{{ event.description }}</div>
            </div>
            <div v-if="!eventsOn(day.date).length" class="week-empty">暂无日程</div>
          </div>
        </div>
      </div>

      <!-- 列表视图：当前月窗口内的日程表格 -->
      <el-table v-else v-loading="loading" :data="events" border size="small" empty-text="当前时段暂无日程">
        <el-table-column prop="title" label="标题" min-width="200" />
        <el-table-column label="开始" width="170">
          <template #default="{ row }">{{ row.allDay ? row.startTime.slice(0, 10) : row.startTime }}</template>
        </el-table-column>
        <el-table-column label="结束" width="170">
          <template #default="{ row }">{{ row.allDay ? row.endTime.slice(0, 10) : row.endTime }}</template>
        </el-table-column>
        <el-table-column label="全天" width="70">
          <template #default="{ row }">{{ row.allDay ? '是' : '否' }}</template>
        </el-table-column>
        <el-table-column label="可见性" width="90">
          <template #default="{ row }">
            <el-tag :type="visibilityTag(row.visibility)" size="small" effect="plain">
              {{ visibilityText(row.visibility) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="description" label="描述" min-width="180" show-overflow-tooltip />
        <el-table-column label="操作" width="150">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEvent(row)">查看</el-button>
            <el-button
              v-if="isOwner(row) && canUpdate"
              link
              type="primary"
              @click="openEvent(row, true)"
            >
              编辑
            </el-button>
            <el-button v-if="isOwner(row) && canDelete" link type="danger" @click="doCancel(row)">
              取消
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="legend">
        <el-tag size="small" type="info" effect="plain">私有</el-tag>
        <el-tag size="small" type="warning" effect="plain">部门</el-tag>
        <el-tag size="small" type="success" effect="plain">公开</el-tag>
        <span class="legend-hint">
          私有仅创建人可见；部门按创建时部门快照对本部门及子部门可见；公开全租户可见（创建需 oa:calendar:public）。
        </span>
      </div>
    </el-card>

    <!-- 新建 / 编辑 / 查看 -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="560px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px" :disabled="!editable">
        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" maxlength="200" placeholder="如：部门周会" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="3"
            maxlength="1000"
            show-word-limit
            placeholder="可选"
          />
        </el-form-item>
        <el-form-item label="全天">
          <el-switch v-model="form.allDay" :disabled="!editable" />
        </el-form-item>
        <el-form-item v-if="form.allDay" label="日期" required>
          <div class="range-row">
            <el-date-picker
              v-model="form.dateStart"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="开始日期"
              style="width: 180px"
            />
            <span class="range-sep">至</span>
            <el-date-picker
              v-model="form.dateEnd"
              type="date"
              value-format="YYYY-MM-DD"
              placeholder="结束日期"
              style="width: 180px"
            />
          </div>
        </el-form-item>
        <el-form-item v-else label="时间" required>
          <div class="range-row">
            <el-date-picker
              v-model="form.timeStart"
              type="datetime"
              value-format="YYYY-MM-DD HH:mm:ss"
              placeholder="开始时间"
              style="width: 180px"
            />
            <span class="range-sep">至</span>
            <el-date-picker
              v-model="form.timeEnd"
              type="datetime"
              value-format="YYYY-MM-DD HH:mm:ss"
              placeholder="结束时间"
              style="width: 180px"
            />
          </div>
        </el-form-item>
        <el-form-item label="可见性">
          <el-radio-group v-model="form.visibility" :disabled="!editable">
            <el-radio-button :value="0">私有</el-radio-button>
            <el-radio-button :value="1">部门</el-radio-button>
            <el-radio-button :value="2" :disabled="!canPublic">公开</el-radio-button>
          </el-radio-group>
          <div v-if="!canPublic" class="field-hint">公开日程需要 oa:calendar:public 权限。</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="dialogVisible = false">{{ editable ? '取消' : '关闭' }}</el-button>
          <el-button
            v-if="editable && form.id > 0 && isOwner(form) && canDelete"
            type="danger"
            plain
            @click="doCancel(form)"
          >
            取消日程
          </el-button>
          <el-button v-if="editable" type="primary" :loading="saving" @click="save">保存</el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ArrowLeft, ArrowRight } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox, type FormInstance } from 'element-plus'
import { useUserStore } from '@/stores/user'
import {
  cancelCalendarEvent,
  createCalendarEvent,
  listCalendarEvents,
  updateCalendarEvent,
} from '@/api/oa'

// 日程（issue #56，菜单 524 日程 / 536 我的日程，spec §4.11/§6）：
// 月/周/列表三视图 + 新建/编辑/取消。可见性由后端三态谓词收口；前端只按
// 权限码显隐动作（oa:calendar:create/update/delete/public）并展示可见性标签。
interface CalendarEventRow {
  id: number
  creatorId: number
  title: string
  description: string
  startTime: string
  endTime: string
  allDay: boolean
  visibility: number
  deptId: number
  source: number
  status: number
  createTime: string
  updateTime: string
}

interface CalendarDay {
  date: string
  day: number
  weekday: number
  isToday: boolean
  inMonth: boolean
}

const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('oa:calendar:create'))
const canUpdate = computed(() => userStore.hasPerm('oa:calendar:update'))
const canDelete = computed(() => userStore.hasPerm('oa:calendar:delete'))
const canPublic = computed(() => userStore.hasPerm('oa:calendar:public'))
const currentUserId = computed(() => Number(userStore.userInfo?.id || 0))

const weekNames = ['日', '一', '二', '三', '四', '五', '六']
const viewMode = ref<'month' | 'week' | 'list'>('month')
const cursor = ref(new Date())
const keyword = ref('')
const loading = ref(false)
const events = ref<CalendarEventRow[]>([])

// ---------- 日期工具（浏览器本地时区；与后端业务时区口径一致） ----------
function pad(n: number): string {
  return String(n).padStart(2, '0')
}
function dateKey(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}
function dayOf(d: Date): CalendarDay {
  const today = new Date()
  return {
    date: dateKey(d),
    day: d.getDate(),
    weekday: d.getDay(),
    isToday: dateKey(d) === dateKey(today),
    inMonth: d.getMonth() === cursor.value.getMonth(),
  }
}
function addDays(d: Date, n: number): Date {
  const x = new Date(d)
  x.setDate(x.getDate() + n)
  return x
}
function startOfWeek(d: Date): Date {
  const offset = (d.getDay() + 6) % 7 // 周一为一周起点
  const x = new Date(d.getFullYear(), d.getMonth(), d.getDate())
  return addDays(x, -offset)
}
function rangeOf(d: Date): { start: Date; end: Date } {
  if (viewMode.value === 'week') {
    const start = startOfWeek(d)
    return { start, end: addDays(start, 7) }
  }
  const start = new Date(d.getFullYear(), d.getMonth(), 1)
  return { start, end: new Date(d.getFullYear(), d.getMonth() + 1, 1) }
}

const monthWeeks = computed<CalendarDay[][]>(() => {
  const first = new Date(cursor.value.getFullYear(), cursor.value.getMonth(), 1)
  const gridStart = addDays(first, -((first.getDay() + 6) % 7))
  const weeks: CalendarDay[][] = []
  for (let w = 0; w < 6; w++) {
    const row: CalendarDay[] = []
    for (let i = 0; i < 7; i++) row.push(dayOf(addDays(gridStart, w * 7 + i)))
    weeks.push(row)
  }
  return weeks
})
const weekDays = computed<CalendarDay[]>(() => {
  const start = startOfWeek(cursor.value)
  return Array.from({ length: 7 }, (_, i) => dayOf(addDays(start, i)))
})

const rangeTitle = computed(() => {
  if (viewMode.value === 'week') {
    const { start, end } = rangeOf(cursor.value)
    const last = addDays(end, -1)
    return `${start.getMonth() + 1}月${start.getDate()}日 - ${last.getMonth() + 1}月${last.getDate()}日`
  }
  return `${cursor.value.getFullYear()} 年 ${cursor.value.getMonth() + 1} 月`
})

function dayClass(day: CalendarDay): Record<string, boolean> {
  return {
    'is-outside': !day.inMonth && viewMode.value === 'month',
    'is-today': day.isToday,
  }
}

// ---------- 取数 ----------
function rangeQuery(): { startTime: string; endTime: string } {
  const { start, end } = rangeOf(cursor.value)
  return { startTime: `${dateKey(start)} 00:00:00`, endTime: `${dateKey(end)} 00:00:00` }
}

async function load() {
  loading.value = true
  try {
    const params: Record<string, string> = { ...rangeQuery() }
    if (keyword.value.trim()) params.keyword = keyword.value.trim()
    const data = await listCalendarEvents(params)
    events.value = Array.isArray(data) ? data : []
  } finally {
    loading.value = false
  }
}

function onViewChange() {
  load()
}
function shift(delta: number) {
  if (viewMode.value === 'week') {
    cursor.value = addDays(cursor.value, delta * 7)
  } else if (viewMode.value === 'month') {
    cursor.value = new Date(cursor.value.getFullYear(), cursor.value.getMonth() + delta, 1)
  } else {
    cursor.value = new Date(cursor.value.getFullYear(), cursor.value.getMonth() + delta, 1)
  }
  load()
}
function goToday() {
  cursor.value = new Date()
  load()
}

// ---------- 日程在日期上的分布 ----------
// 与后端「相交」语义一致：start ≤ date ≤ end；结束时间为 00:00:00 时该日期
// 不算（跨天日程的收尾日不重复展示）。
function occursOn(event: CalendarEventRow, key: string): boolean {
  const start = event.startTime.slice(0, 10)
  const end = event.endTime.slice(0, 10)
  if (key < start || key > end) return false
  if (key === end && event.endTime.slice(11, 19) === '00:00:00' && start !== end) return false
  return true
}
function eventsOn(key: string): CalendarEventRow[] {
  return events.value
    .filter((event) => occursOn(event, key))
    .sort((a, b) => (a.startTime < b.startTime ? -1 : a.startTime > b.startTime ? 1 : a.id - b.id))
}
function eventTooltip(event: CalendarEventRow): string {
  const time = event.allDay
    ? `${event.startTime.slice(0, 10)} 全天`
    : `${event.startTime} - ${event.endTime}`
  return `${event.title}\n${time}\n${visibilityText(event.visibility)}`
}

// ---------- 可见性 / 归属 ----------
function visibilityText(value: number): string {
  return ['私有', '部门', '公开'][value] ?? `未知(${value})`
}
function visibilityTag(value: number): string {
  if (value === 2) return 'success'
  if (value === 1) return 'warning'
  return 'info'
}
function isOwner(row: any): boolean {
  return currentUserId.value > 0 && Number(row?.creatorId) === currentUserId.value
}

// ---------- 表单 ----------
const dialogVisible = ref(false)
const dialogMode = ref<'create' | 'edit' | 'view'>('create')
const saving = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({
  id: 0,
  title: '',
  description: '',
  allDay: false,
  visibility: 1,
  dateStart: '',
  dateEnd: '',
  timeStart: '',
  timeEnd: '',
})
const rules = {
  title: [{ required: true, message: '请填写日程标题', trigger: 'blur' }],
}
const editable = computed(() => dialogMode.value !== 'view')
const dialogTitle = computed(() => {
  if (dialogMode.value === 'create') return '新建日程'
  if (dialogMode.value === 'edit') return '编辑日程'
  return '日程详情'
})

function fillFromRow(row: CalendarEventRow) {
  form.id = row.id
  form.title = row.title
  form.description = row.description || ''
  form.allDay = !!row.allDay
  form.visibility = Number(row.visibility)
  form.dateStart = row.startTime.slice(0, 10)
  form.dateEnd = row.endTime.slice(0, 10)
  form.timeStart = row.startTime
  form.timeEnd = row.endTime
}

function openCreate(date?: string) {
  if (!canCreate.value) return
  dialogMode.value = 'create'
  const key = date || dateKey(cursor.value)
  Object.assign(form, {
    id: 0,
    title: '',
    description: '',
    allDay: false,
    visibility: 1,
    dateStart: key,
    dateEnd: key,
    timeStart: `${key} 09:00:00`,
    timeEnd: `${key} 10:00:00`,
  })
  dialogVisible.value = true
}

function openEvent(row: CalendarEventRow, forceEdit = false) {
  dialogMode.value = forceEdit || (isOwner(row) && canUpdate.value) ? 'edit' : 'view'
  if (dialogMode.value === 'edit' && !canUpdate.value) dialogMode.value = 'view'
  fillFromRow(row)
  dialogVisible.value = true
}

async function save() {
  if (!form.title.trim()) {
    ElMessage.warning('请填写日程标题')
    return
  }
  let start = form.timeStart
  let end = form.timeEnd
  if (form.allDay) {
    const startDate = (form.dateStart || form.timeStart.slice(0, 10) || '').trim()
    const endDate = (form.dateEnd || startDate).trim()
    if (!startDate || !endDate) {
      ElMessage.warning('请选择日期')
      return
    }
    start = `${startDate} 00:00:00`
    end = `${endDate} 23:59:59`
  }
  if (!start || !end) {
    ElMessage.warning('请选择起止时间')
    return
  }
  if (start >= end) {
    ElMessage.warning('结束时间必须晚于开始时间')
    return
  }
  const payload = {
    id: form.id,
    title: form.title.trim(),
    description: form.description.trim(),
    startTime: start,
    endTime: end,
    allDay: form.allDay,
    visibility: form.visibility,
  }
  saving.value = true
  try {
    if (form.id > 0) {
      await updateCalendarEvent(payload)
    } else {
      await createCalendarEvent(payload)
    }
    ElMessage.success('日程已保存')
    dialogVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function doCancel(row: any) {
  try {
    await ElMessageBox.confirm(`确定取消日程「${row.title}」？取消后不再展示。`, '取消日程', {
      type: 'warning',
    })
  } catch {
    return
  }
  await cancelCalendarEvent(Number(row.id))
  ElMessage.success('日程已取消')
  dialogVisible.value = false
  await load()
}

onMounted(load)
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
  gap: 12px;
}
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.keyword {
  width: 180px;
}
.range-title {
  font-size: 17px;
  font-weight: 600;
  min-width: 150px;
  text-align: center;
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
  min-height: 108px;
  border: 1px solid var(--el-border-color-lighter);
  padding: 6px;
  cursor: pointer;
  transition: background-color 0.15s;
  overflow: hidden;
}
.day-cell:hover {
  background-color: var(--el-fill-color-light);
}
.day-cell.is-outside {
  background-color: var(--el-fill-color-lighter);
  opacity: 0.55;
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
.day-events {
  margin-top: 4px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.event-chip {
  display: flex;
  gap: 4px;
  align-items: baseline;
  font-size: 12px;
  line-height: 18px;
  padding: 0 4px;
  border-radius: 3px;
  overflow: hidden;
  white-space: nowrap;
}
.event-time {
  flex: none;
  opacity: 0.75;
}
.event-title {
  overflow: hidden;
  text-overflow: ellipsis;
}
.visibility-0 {
  background: var(--el-color-info-light-9);
  color: var(--el-color-info-dark-2);
}
.visibility-1 {
  background: var(--el-color-warning-light-9);
  color: var(--el-color-warning-dark-2);
}
.visibility-2 {
  background: var(--el-color-success-light-9);
  color: var(--el-color-success-dark-2);
}
.week-view {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 8px;
  align-items: stretch;
}
.week-column {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
  min-height: 260px;
  overflow: hidden;
}
.week-column.is-today {
  outline: 2px solid var(--el-color-primary-light-5);
  outline-offset: -2px;
}
.week-day-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  background: var(--el-fill-color-light);
  cursor: pointer;
}
.week-day-name {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.week-day-num {
  font-weight: 600;
}
.week-day-events {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 6px;
}
.event-card {
  border-radius: 4px;
  padding: 4px 6px;
  cursor: pointer;
}
.event-card-time {
  font-size: 12px;
  opacity: 0.8;
}
.event-card-title {
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.event-card-desc {
  font-size: 12px;
  opacity: 0.75;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.week-empty {
  color: var(--el-text-color-placeholder);
  font-size: 12px;
  text-align: center;
  padding-top: 12px;
}
.legend {
  margin-top: 10px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.legend-hint {
  margin-left: 4px;
}
.range-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.range-sep {
  color: var(--el-text-color-secondary);
}
.field-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}
.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
