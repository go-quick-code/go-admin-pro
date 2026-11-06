<template>
  <div class="app-container">
    <el-tabs v-model="activeTab">
      <!-- 类型管理 -->
      <el-tab-pane label="请假类型" name="type">
        <el-card shadow="never">
          <div class="toolbar">
            <div class="toolbar-left">
              <el-select v-model="typeFilter" clearable placeholder="全部状态" style="width: 120px" @change="loadTypes">
                <el-option label="启用" :value="0" />
                <el-option label="停用" :value="1" />
              </el-select>
              <el-input
                v-model="typeKeyword"
                clearable
                placeholder="名称 / 编码"
                style="width: 200px"
                @clear="loadTypes"
                @keyup.enter="loadTypes"
              />
              <el-button :icon="Search" circle @click="loadTypes" />
            </div>
            <div class="toolbar-right">
              <el-button v-if="canManage" type="primary" @click="openTypeDialog()">新增类型</el-button>
            </div>
          </div>
          <el-table v-loading="typeLoading" :data="types" row-key="id">
            <el-table-column prop="sort" label="顺序" width="70" />
            <el-table-column prop="name" label="名称" min-width="120" />
            <el-table-column prop="code" label="编码" min-width="120" />
            <el-table-column label="占用额度" width="90">
              <template #default="{ row }">
                <el-tag :type="row.balanceManaged ? 'success' : 'info'" size="small">
                  {{ row.balanceManaged ? '占用' : '不占' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="annualQuota" label="年度额度(天)" width="110" />
            <el-table-column label="状态" width="80">
              <template #default="{ row }">
                <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
                  {{ row.status === 0 ? '启用' : '停用' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="remark" label="备注" min-width="140" show-overflow-tooltip />
            <el-table-column v-if="canManage" label="操作" width="200" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" @click="openTypeDialog(row)">编辑</el-button>
                <el-button link :type="row.status === 0 ? 'warning' : 'success'" @click="toggleStatus(row)">
                  {{ row.status === 0 ? '停用' : '启用' }}
                </el-button>
                <el-button link type="danger" @click="removeType(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <!-- 额度管理 -->
      <el-tab-pane label="请假额度" name="balance">
        <el-card shadow="never">
          <div class="toolbar">
            <div class="toolbar-left">
              <el-select v-model="balanceFilter.year" style="width: 110px" @change="loadBalances">
                <el-option v-for="y in yearOptions" :key="y" :label="`${y} 年`" :value="y" />
              </el-select>
              <el-select
                v-model="balanceFilter.leaveTypeId"
                clearable
                filterable
                placeholder="全部类型"
                style="width: 160px"
                @change="loadBalances"
              >
                <el-option v-for="t in types" :key="t.id" :label="t.name" :value="t.id" />
              </el-select>
              <el-select
                v-model="balanceFilter.userId"
                clearable
                filterable
                placeholder="全部员工"
                style="width: 160px"
                @change="loadBalances"
              >
                <el-option v-for="u in users" :key="u.id" :label="u.nickname" :value="u.id" />
              </el-select>
            </div>
            <div class="toolbar-right">
              <el-button v-if="canManage" type="primary" plain @click="openInitDialog">年度初始化</el-button>
            </div>
          </div>
          <el-table v-loading="balanceLoading" :data="balances">
            <el-table-column prop="year" label="年度" width="80" />
            <el-table-column prop="userName" label="员工" min-width="110" />
            <el-table-column prop="typeName" label="类型" min-width="110" />
            <el-table-column prop="quota" label="初始额度" width="90" />
            <el-table-column prop="adjustment" label="调整累计" width="90" />
            <el-table-column prop="used" label="已使用" width="90" />
            <el-table-column label="可用" width="90">
              <template #default="{ row }">
                <span :class="Number(row.available) < 0 ? 'negative' : ''">{{ row.available }}</span>
              </template>
            </el-table-column>
            <el-table-column prop="adjustReason" label="调整原因" min-width="140" show-overflow-tooltip />
            <el-table-column prop="updateTime" label="最近调整时间" width="160" />
            <el-table-column v-if="canManage" label="操作" width="90" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" @click="openAdjustDialog(row)">调整</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div class="pager">
            <el-pagination
              v-model:current-page="balancePageNo"
              :page-size="balancePageSize"
              :total="balanceTotal"
              layout="total, prev, pager, next"
              @current-change="loadBalances"
            />
          </div>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <!-- 类型编辑弹窗 -->
    <el-dialog v-model="typeDialog.visible" :title="typeDialog.mode === 'create' ? '新增类型' : '编辑类型'" width="520px" append-to-body>
      <el-form label-width="100px">
        <el-form-item label="类型编码" v-if="typeDialog.mode === 'create'">
          <el-input v-model="typeDialog.code" maxlength="50" placeholder="小写字母/数字/连字符，如 annual" />
          <div class="field-hint">编码创建后不可修改。</div>
        </el-form-item>
        <el-form-item label="类型名称">
          <el-input v-model="typeDialog.name" maxlength="50" placeholder="如 年假 / 事假" />
        </el-form-item>
        <el-form-item label="占用额度">
          <el-switch v-model="typeDialog.balanceManaged" />
          <div class="field-hint">占用额度：提交请假时检查可用额度（初始额度 + 调整 − 已使用）。</div>
        </el-form-item>
        <el-form-item v-if="typeDialog.balanceManaged" label="年度额度">
          <el-input-number v-model="typeDialog.annualQuota" :min="0" :max="9999.9" :step="0.5" :precision="1" />
          <span class="field-hint-inline">天（半天按 0.5）</span>
        </el-form-item>
        <el-form-item label="显示顺序">
          <el-input-number v-model="typeDialog.sort" :min="0" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="typeDialog.remark" maxlength="200" show-word-limit />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="typeDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="typeDialog.saving" @click="saveType">保存</el-button>
      </template>
    </el-dialog>

    <!-- 额度初始化弹窗 -->
    <el-dialog v-model="initDialog.visible" title="额度年度初始化" width="520px" append-to-body>
      <el-form label-width="100px">
        <el-form-item label="自然年">
          <el-select v-model="initDialog.year">
            <el-option v-for="y in yearOptions" :key="y" :label="`${y} 年`" :value="y" />
          </el-select>
        </el-form-item>
        <el-form-item label="员工">
          <el-select v-model="initDialog.userIds" multiple filterable style="width: 100%" placeholder="选择员工（可多选）">
            <el-option v-for="u in users" :key="u.id" :label="u.nickname" :value="u.id" />
          </el-select>
          <div class="field-hint">幂等：已有额度的员工不会被覆盖或重复创建。</div>
        </el-form-item>
        <el-form-item label="请假类型">
          <el-select v-model="initDialog.typeIds" multiple filterable style="width: 100%" placeholder="留空 = 全部占用额度的启用类型">
            <el-option v-for="t in managedTypes" :key="t.id" :label="`${t.name}（${t.annualQuota} 天）`" :value="t.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="initDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="initDialog.saving" @click="doInit">初始化</el-button>
      </template>
    </el-dialog>

    <!-- 额度调整弹窗 -->
    <el-dialog v-model="adjustDialog.visible" title="调整额度" width="480px" append-to-body>
      <el-form label-width="100px">
        <el-form-item label="员工 / 类型">
          <span>{{ adjustDialog.userName }} · {{ adjustDialog.typeName }}（{{ adjustDialog.year }} 年）</span>
        </el-form-item>
        <el-form-item label="当前额度">
          <span>
            初始 {{ adjustDialog.quota }} + 调整 {{ adjustDialog.adjustment }} − 已用 {{ adjustDialog.used }} =
            可用 {{ adjustDialog.available }}
          </span>
        </el-form-item>
        <el-form-item label="调整量">
          <el-input-number v-model="adjustDialog.delta" :step="0.5" :precision="1" />
          <span class="field-hint-inline">天（负数 = 调减）</span>
        </el-form-item>
        <el-form-item label="调整原因">
          <el-input v-model="adjustDialog.reason" maxlength="200" show-word-limit placeholder="必填，将留痕记录操作人与时间" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="adjustDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="adjustDialog.saving" @click="doAdjust">调整</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import {
  adjustLeaveBalance,
  createLeaveType,
  deleteLeaveType,
  initLeaveBalances,
  listLeaveTypes,
  pageLeaveBalances,
  setLeaveTypeStatus,
  updateLeaveType,
} from '@/api/oa'
import { listSimpleUsers } from '@/api/system'

// 请假类型与额度管理页（issue #51，spec §6 类型管理/额度管理）。
// 管理码 oa:leave:manage 挂菜单 522：页面入口即持码（分配了菜单的角色可见），
// 按钮再用 hasPerm 兜底（直接访问 URL 时按钮隐藏）。

interface LeaveTypeRow {
  id: number
  name: string
  code: string
  balanceManaged: boolean
  annualQuota: string
  status: number
  sort: number
  remark: string
}

interface BalanceRow {
  id: number
  userId: number
  userName: string
  leaveTypeId: number
  typeName: string
  year: number
  quota: string
  adjustment: string
  used: string
  available: string
  adjustReason: string
  updateTime: string
}

interface SimpleUser {
  id: number
  nickname: string
}

const userStore = useUserStore()
const canManage = computed(() => userStore.hasPerm('oa:leave:manage'))

const activeTab = ref('type')

// ---- 类型管理 ----
const typeLoading = ref(false)
const types = ref<LeaveTypeRow[]>([])
const typeFilter = ref<number | undefined>(undefined)
const typeKeyword = ref('')

const typeDialog = ref({
  visible: false,
  saving: false,
  mode: 'create' as 'create' | 'update',
  id: 0,
  code: '',
  name: '',
  balanceManaged: false,
  annualQuota: 0,
  sort: 0,
  remark: '',
})

const managedTypes = computed(() => types.value.filter((t) => t.balanceManaged && t.status === 0))

async function loadTypes() {
  typeLoading.value = true
  try {
    const params: Record<string, number | string> = {}
    if (typeFilter.value !== undefined && typeFilter.value !== null) params.status = typeFilter.value
    if (typeKeyword.value) params.keyword = typeKeyword.value
    const data = await listLeaveTypes(params)
    types.value = data || []
  } finally {
    typeLoading.value = false
  }
}

function openTypeDialog(row?: LeaveTypeRow) {
  if (row) {
    typeDialog.value = {
      visible: true,
      saving: false,
      mode: 'update',
      id: row.id,
      code: row.code,
      name: row.name,
      balanceManaged: row.balanceManaged,
      annualQuota: Number(row.annualQuota),
      sort: row.sort,
      remark: row.remark,
    }
  } else {
    typeDialog.value = {
      visible: true,
      saving: false,
      mode: 'create',
      id: 0,
      code: '',
      name: '',
      balanceManaged: false,
      annualQuota: 0,
      sort: 0,
      remark: '',
    }
  }
}

async function saveType() {
  typeDialog.value.saving = true
  try {
    if (typeDialog.value.mode === 'create') {
      await createLeaveType({
        name: typeDialog.value.name,
        code: typeDialog.value.code,
        balanceManaged: typeDialog.value.balanceManaged,
        annualQuota: typeDialog.value.annualQuota.toFixed(1),
        sort: typeDialog.value.sort,
        remark: typeDialog.value.remark,
      })
    } else {
      await updateLeaveType({
        id: typeDialog.value.id,
        name: typeDialog.value.name,
        balanceManaged: typeDialog.value.balanceManaged,
        annualQuota: typeDialog.value.balanceManaged ? typeDialog.value.annualQuota.toFixed(1) : '0.0',
        sort: typeDialog.value.sort,
        remark: typeDialog.value.remark,
      })
    }
    ElMessage.success('已保存')
    typeDialog.value.visible = false
    await loadTypes()
  } finally {
    typeDialog.value.saving = false
  }
}

async function toggleStatus(row: LeaveTypeRow) {
  const next = row.status === 0 ? 1 : 0
  await setLeaveTypeStatus(row.id, next)
  ElMessage.success(next === 0 ? '已启用' : '已停用')
  await loadTypes()
}

async function removeType(row: LeaveTypeRow) {
  await ElMessageBox.confirm(`确认删除类型「${row.name}」吗？被请假申请引用的类型只能停用。`, '删除类型', { type: 'warning' })
  await deleteLeaveType(row.id)
  ElMessage.success('已删除')
  await loadTypes()
}

// ---- 额度管理 ----
const currentYear = new Date().getFullYear()
const yearOptions = Array.from({ length: 5 }, (_, i) => currentYear - 1 + i)

const users = ref<SimpleUser[]>([])
const balanceLoading = ref(false)
const balances = ref<BalanceRow[]>([])
const balanceTotal = ref(0)
const balancePageNo = ref(1)
const balancePageSize = 10
const balanceFilter = ref<{ year: number; leaveTypeId?: number; userId?: number }>({ year: currentYear })

const initDialog = ref({ visible: false, saving: false, year: currentYear, userIds: [] as number[], typeIds: [] as number[] })

const adjustDialog = ref({
  visible: false,
  saving: false,
  userId: 0,
  userName: '',
  leaveTypeId: 0,
  typeName: '',
  year: currentYear,
  quota: '0.00',
  adjustment: '0.00',
  used: '0.00',
  available: '0.00',
  delta: 0.5,
  reason: '',
})

async function loadBalances() {
  balanceLoading.value = true
  try {
    const params: Record<string, number> = {
      pageNo: balancePageNo.value,
      pageSize: balancePageSize,
      year: balanceFilter.value.year,
    }
    if (balanceFilter.value.leaveTypeId) params.leaveTypeId = balanceFilter.value.leaveTypeId
    if (balanceFilter.value.userId) params.userId = balanceFilter.value.userId
    const data = await pageLeaveBalances(params)
    balances.value = data.list || []
    balanceTotal.value = data.total || 0
  } finally {
    balanceLoading.value = false
  }
}

function openInitDialog() {
  initDialog.value = { visible: true, saving: false, year: balanceFilter.value.year, userIds: [], typeIds: [] }
}

async function doInit() {
  if (initDialog.value.userIds.length === 0) {
    ElMessage.warning('请选择员工')
    return
  }
  initDialog.value.saving = true
  try {
    const body: { year: number; userIds: number[]; typeIds?: number[] } = {
      year: initDialog.value.year,
      userIds: initDialog.value.userIds,
    }
    if (initDialog.value.typeIds.length > 0) body.typeIds = initDialog.value.typeIds
    const data = await initLeaveBalances(body)
    ElMessage.success(`初始化完成：新建 ${data.created} 行（共 ${data.total} 行，已有行不覆盖）`)
    initDialog.value.visible = false
    balanceFilter.value.year = initDialog.value.year
    balancePageNo.value = 1
    await loadBalances()
  } finally {
    initDialog.value.saving = false
  }
}

function openAdjustDialog(row: BalanceRow) {
  adjustDialog.value = {
    visible: true,
    saving: false,
    userId: row.userId,
    userName: row.userName,
    leaveTypeId: row.leaveTypeId,
    typeName: row.typeName,
    year: row.year,
    quota: row.quota,
    adjustment: row.adjustment,
    used: row.used,
    available: row.available,
    delta: 0.5,
    reason: '',
  }
}

async function doAdjust() {
  if (!adjustDialog.value.reason.trim()) {
    ElMessage.warning('请填写调整原因')
    return
  }
  if (adjustDialog.value.delta === 0) {
    ElMessage.warning('调整量不能为 0')
    return
  }
  adjustDialog.value.saving = true
  try {
    await adjustLeaveBalance({
      userId: adjustDialog.value.userId,
      leaveTypeId: adjustDialog.value.leaveTypeId,
      year: adjustDialog.value.year,
      delta: adjustDialog.value.delta.toFixed(1),
      reason: adjustDialog.value.reason,
    })
    ElMessage.success('已调整并留痕')
    adjustDialog.value.visible = false
    await loadBalances()
  } finally {
    adjustDialog.value.saving = false
  }
}

onMounted(async () => {
  await loadTypes()
  await loadBalances()
  // 员工下拉 best-effort：未授权 system:user:simple-list 的角色降级为空列表
  //（与 CRM 客户页同款降级，admin/超管可见完整列表）。
  try {
    const simpleUsers = await listSimpleUsers()
    users.value = simpleUsers || []
  } catch {
    users.value = []
  }
})
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
  gap: 8px;
  align-items: center;
}
.toolbar-right {
  display: flex;
  gap: 8px;
}
.field-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}
.field-hint-inline {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-left: 8px;
}
.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
.negative {
  color: var(--el-color-danger);
}
</style>
