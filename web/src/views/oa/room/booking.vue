<template>
  <div class="app-container">
    <el-card shadow="never">
      <el-form :inline="true" class="search-form">
        <el-form-item label="会议室">
          <el-select v-model="query.roomId" clearable filterable placeholder="全部会议室" style="width: 200px">
            <el-option v-for="room in rooms" :key="room.id" :label="roomLabel(room)" :value="room.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="日期">
          <el-date-picker
            v-model="query.date"
            type="date"
            value-format="YYYY-MM-DD"
            placeholder="选择日期"
            :clearable="true"
            style="width: 180px"
          />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="query.status" clearable placeholder="全部状态" style="width: 140px">
            <el-option label="有效" :value="0" />
            <el-option label="已取消" :value="1" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

      <div class="toolbar">
        <el-button v-if="canBook" type="primary" @click="openCreate">预约会议</el-button>
        <span class="hint">
          同一会议室的有效预约不能重叠（相邻时段不算冲突）；预约成功会自动生成一条部门可见日程。
          有未来有效预约的会议室只能停用不能删除。
        </span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="title" label="会议主题" min-width="180" />
        <el-table-column label="会议室" min-width="160">
          <template #default="{ row }">{{ roomCell(row) }}</template>
        </el-table-column>
        <el-table-column label="预约人" width="150">
          <template #default="{ row }">{{ row.userName || `#${row.userId}` }}</template>
        </el-table-column>
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">{{ row.startTime }}</template>
        </el-table-column>
        <el-table-column label="结束时间" width="170">
          <template #default="{ row }">{{ row.endTime }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
              {{ row.status === 0 ? '有效' : '已取消' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="日程" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.calendarEventId" size="small" effect="plain">#{{ row.calendarEventId }}</el-tag>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="提醒" width="170">
          <template #default="{ row }">{{ row.remindedAt || '-' }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="row.status === 0 && (canManage || row.userId === currentUserId)"
              link
              type="danger"
              @click="doCancel(row)"
            >
              取消
            </el-button>
            <span v-else>-</span>
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

    <!-- 预约会议 -->
    <el-dialog
      v-model="dialog.visible"
      title="预约会议"
      width="560px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="会议室" prop="roomId">
          <el-select v-model="form.roomId" filterable placeholder="请选择会议室" style="width: 100%">
            <el-option v-for="room in rooms" :key="room.id" :label="roomLabel(room)" :value="room.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="会议主题" prop="title">
          <el-input v-model="form.title" maxlength="200" placeholder="如：产品评审会" />
        </el-form-item>
        <el-form-item label="开始时间" prop="startTime">
          <el-date-picker
            v-model="form.startTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="开始时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="结束时间" prop="endTime">
          <el-date-picker
            v-model="form.endTime"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            placeholder="结束时间"
            style="width: 100%"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="submit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance } from 'element-plus'
import { useUserStore } from '@/stores/user'
import {
  listMeetingRooms,
  pageRoomBookings,
  createRoomBooking,
  cancelRoomBooking,
  getRoomBooking,
} from '@/api/oa'

// 会议室预约（issue #57，spec §4.11/§4.14/§6）：管理端「会议室预约」538 与
// 员工侧「我的会议」539 复用本页面。列表按房间/日期/状态筛选；无
// oa:room:manage 的查看者由后端强制只看本人预约。创建挂 oa:room:booking；
// 取消校验预约人本人或 oa:room:manage，并同步取消生成的部门可见日程。

interface BookingRow {
  id: number
  roomId: number
  roomName: string
  roomLocation: string
  userId: number
  userName: string
  title: string
  startTime: string
  endTime: string
  status: number
  calendarEventId?: number | null
  remindedAt: string
}

const route = useRoute()
const userStore = useUserStore()
const canBook = computed(() => userStore.hasPerm('oa:room:booking'))
const canManage = computed(() => userStore.hasPerm('oa:room:manage'))
const currentUserId = computed(() => Number(userStore.userInfo?.id || 0))

const formRef = ref<FormInstance>()
const rooms = ref<any[]>([])
const list = ref<BookingRow[]>([])
const total = ref(0)
const loading = ref(false)

function todayKey(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

const query = reactive({
  pageNo: 1,
  pageSize: 10,
  roomId: undefined as number | undefined,
  date: todayKey(),
  status: undefined as number | undefined,
})

const dialog = reactive({ visible: false, saving: false })
const form = reactive({ roomId: undefined as number | undefined, title: '', startTime: '', endTime: '' })
const rules = {
  roomId: [{ required: true, message: '请选择会议室', trigger: 'change' }],
  title: [{ required: true, message: '请填写会议主题', trigger: 'blur' }],
  startTime: [{ required: true, message: '请选择开始时间', trigger: 'change' }],
  endTime: [{ required: true, message: '请选择结束时间', trigger: 'change' }],
}

function roomLabel(room: any): string {
  const spot = room.location ? `（${room.location}）` : ''
  const capacity = room.capacity ? ` · ${room.capacity} 人` : ''
  return `${room.name}${spot}${capacity}`
}

function roomCell(row: BookingRow): string {
  const spot = row.roomLocation ? `（${row.roomLocation}）` : ''
  return `${row.roomName || `#${row.roomId}`}${spot}`
}

async function loadRooms() {
  const data = await listMeetingRooms()
  rooms.value = Array.isArray(data) ? data : []
}

async function load() {
  loading.value = true
  try {
    const params: Record<string, any> = {
      pageNo: query.pageNo,
      pageSize: query.pageSize,
    }
    if (query.roomId) params.roomId = query.roomId
    if (query.date) params.date = query.date
    if (query.status !== undefined && query.status !== null) params.status = query.status
    const data = await pageRoomBookings(params)
    list.value = data.list || []
    total.value = data.total || 0
  } finally {
    loading.value = false
  }
}

function search() {
  query.pageNo = 1
  load()
}

function reset() {
  query.roomId = undefined
  query.date = todayKey()
  query.status = undefined
  query.pageNo = 1
  load()
}

function defaultWindow(): { start: string; end: string } {
  const date = query.date || todayKey()
  return { start: `${date} 09:00:00`, end: `${date} 10:00:00` }
}

function openCreate() {
  const { start, end } = defaultWindow()
  Object.assign(form, {
    roomId: query.roomId || rooms.value[0]?.id,
    title: '',
    startTime: start,
    endTime: end,
  })
  dialog.visible = true
}

async function submit() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return
  if (form.startTime >= form.endTime) {
    ElMessage.warning('结束时间必须晚于开始时间')
    return
  }
  dialog.saving = true
  try {
    await createRoomBooking({
      roomId: Number(form.roomId),
      title: form.title.trim(),
      startTime: form.startTime,
      endTime: form.endTime,
    })
    ElMessage.success('预约成功，已生成部门可见日程')
    dialog.visible = false
    await load()
  } finally {
    dialog.saving = false
  }
}

async function doCancel(row: BookingRow) {
  try {
    await ElMessageBox.confirm(`确定取消预约「${row.title}」？关联日程会同步取消。`, '取消预约', {
      type: 'warning',
    })
  } catch {
    return
  }
  await cancelRoomBooking(row.id)
  ElMessage.success('预约已取消')
  await load()
}

// 通知跳转：/oa/room-mine?id=预约ID —— 打开后回读该预约（不依赖当前列表日期）。
async function openFromRoute() {
  const id = Number(route.query.id)
  if (!id) return
  const row = await getRoomBooking(id)
  if (row) {
    ElMessage.info(`${row.title}：${row.startTime} - ${row.endTime}`)
  }
}

onMounted(async () => {
  await loadRooms()
  await load()
  await openFromRoute()
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
  font-size: 12px;
  line-height: 1.6;
}
.pager {
  margin-top: 12px;
  justify-content: flex-end;
}
</style>
