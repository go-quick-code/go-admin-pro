<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      perm-prefix="mall:member"
      title="会员管理"
      dialog-width="680px"
      @row-click="openDetail"
    >
      <template #toolbar>
        <el-button v-if="canPointQuery" @click="openPointConfig">积分规则</el-button>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #cell-levelId="{ row }">
        {{ levelName(row.levelId) }}
      </template>
      <template #cell-pointBalance="{ row }">
        <span class="point-balance">{{ row.pointBalance }}</span>
      </template>
      <template #form-levelId="{ form }">
        <el-select v-model="form.levelId" clearable placeholder="未分配" style="width: 100%">
          <el-option v-for="opt in levelOptions" :key="opt.id" :label="opt.name" :value="opt.id" />
        </el-select>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openDetail(row)">详情</el-button>
        <el-button v-if="canUpdate" link type="warning" @click.stop="openAdjust(row)">调整积分</el-button>
        <el-button v-if="canQuery" link type="primary" @click.stop="openFlows(row)">流水</el-button>
        <el-button
          v-if="canUpdate"
          link
          :type="row.status === 0 ? 'warning' : 'success'"
          @click.stop="toggleStatus(row)"
        >
          {{ row.status === 0 ? '停用' : '启用' }}
        </el-button>
      </template>
    </ProTable>

    <!-- 会员详情抽屉：基本信息 + 收货地址 tab（spec §4「地址列表（按会员）」；
        地址只读，维护随 C 端自助）。 -->
    <el-drawer v-model="detailVisible" :title="detail?.nickname || detail?.mobile || '会员详情'" size="760px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="ID">{{ detail.id }}</el-descriptions-item>
            <el-descriptions-item label="手机号">{{ detail.mobile }}</el-descriptions-item>
            <el-descriptions-item label="昵称">{{ detail.nickname || '-' }}</el-descriptions-item>
            <el-descriptions-item label="等级">{{ levelName(detail.levelId) }}</el-descriptions-item>
            <el-descriptions-item label="积分">{{ detail.pointBalance }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="detail.status === 0 ? 'success' : 'info'" size="small">
                {{ detail.status === 0 ? '启用' : '停用' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="头像">{{ detail.avatarUrl || '-' }}</el-descriptions-item>
            <el-descriptions-item label="备注">{{ detail.remark || '-' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>
        <el-tab-pane label="收货地址" name="addresses">
          <el-table :data="detail.addresses || []" border stripe>
            <el-table-column label="收货人" prop="receiverName" min-width="90" />
            <el-table-column label="手机号" prop="receiverMobile" min-width="120" />
            <el-table-column label="地区" min-width="150">
              <template #default="{ row }">
                {{ [row.province, row.city, row.district].filter(Boolean).join(' / ') }}
              </template>
            </el-table-column>
            <el-table-column label="详细地址" prop="detail" min-width="180" show-overflow-tooltip />
            <el-table-column label="默认" width="70">
              <template #default="{ row }">
                <el-tag v-if="row.isDefault === 1" type="success" size="small">默认</el-tag>
                <span v-else>-</span>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-if="!(detail.addresses || []).length" description="暂无收货地址" :image-size="80" />
        </el-tab-pane>
      </el-tabs>
    </el-drawer>

    <!-- 积分调整弹窗（issue #175，spec §4）：point 可正可负（0 拒绝，后端 400），
        余额扣成负数后端 409；余额只经后端唯一入账入口变动，前端不做本地
        余额推算，仅展示调整前余额。 -->
    <el-dialog v-model="adjustVisible" title="调整积分" width="480px">
      <el-descriptions v-if="adjustTarget" :column="1" border size="small" class="adjust-summary">
        <el-descriptions-item label="会员">
          {{ adjustTarget.nickname || adjustTarget.mobile }}（{{ adjustTarget.mobile }}）
        </el-descriptions-item>
        <el-descriptions-item label="当前积分">{{ adjustTarget.pointBalance }}</el-descriptions-item>
      </el-descriptions>
      <el-form label-width="90px">
        <el-form-item label="变动积分" required>
          <el-input-number
            v-model="adjustForm.point"
            :precision="0"
            :step="1"
            :max="99999999"
            :min="-99999999"
            style="width: 100%"
            placeholder="正数入账、负数扣减，不能为 0"
          />
        </el-form-item>
        <el-form-item label="备注">
          <el-input
            v-model="adjustForm.remark"
            type="textarea"
            :rows="3"
            maxlength="500"
            show-word-limit
            placeholder="调整原因（选填）"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="adjustVisible = false">取消</el-button>
        <el-button type="primary" :loading="adjustSubmitting" @click="submitAdjust">确定</el-button>
      </template>
    </el-dialog>

    <!-- 积分流水抽屉（issue #175，照 pay 钱包流水抽屉先例）：正入负出着色、
        balance_after 结存快照列；流水 append-only，无写按钮。 -->
    <el-drawer v-model="flowVisible" :title="flowTitle" size="860px">
      <el-descriptions :column="2" border size="small" class="flow-summary">
        <el-descriptions-item label="会员">
          {{ flowMember ? flowMember.nickname || flowMember.mobile : '-' }}
        </el-descriptions-item>
        <el-descriptions-item label="当前积分">{{ flowMember?.pointBalance ?? '-' }}</el-descriptions-item>
      </el-descriptions>
      <el-table v-loading="flowLoading" :data="flows" size="small" border>
        <el-table-column label="业务类型" width="110">
          <template #default="{ row }">
            <el-tag :type="bizTagType(row.bizType)" size="small">{{ bizLabel(row.bizType) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="bizId" label="业务单据" width="90" />
        <el-table-column label="变动积分" width="110" align="right">
          <template #default="{ row }">
            <span :class="['point', pointSign(row.point)]">{{ row.point }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="balanceAfter" label="结存快照" width="110" align="right" />
        <el-table-column prop="remark" label="备注" min-width="150" show-overflow-tooltip />
        <el-table-column label="时间" min-width="170">
          <template #default="{ row }">{{ formatTime(row.createTime) }}</template>
        </el-table-column>
      </el-table>
      <el-pagination
        v-model:current-page="flowQuery.pageNo"
        v-model:page-size="flowQuery.pageSize"
        :total="flowTotal"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @size-change="loadFlows"
        @current-change="loadFlows"
      />
    </el-drawer>

    <!-- 积分规则弹窗（issue #175，mall:point:*）：租户级单行配置
        point_per_yuan（1 元实付 = N 积分，支付成功联动消费；≥0，0 = 不入账）。
        行缺失后端惰性建默认行。 -->
    <el-dialog v-model="configVisible" title="积分规则" width="460px">
      <el-form label-width="120px">
        <el-form-item label="积分兑换比例" required>
          <el-input-number
            v-model="configForm.pointPerYuan"
            :precision="0"
            :min="0"
            :max="999999"
            style="width: 100%"
          />
          <div class="form-tip">1 元实付 = N 积分（0 = 支付不入账）</div>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="configForm.remark" type="textarea" :rows="2" maxlength="500" show-word-limit />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="configVisible = false">取消</el-button>
        <el-button v-if="canPointUpdate" type="primary" :loading="configSubmitting" @click="submitPointConfig">
          保存
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageMembers,
  getMember,
  createMember,
  updateMember,
  updateMemberStatus,
  listLevelOptions,
  adjustMemberPoint,
  pagePointRecords,
  getPointConfig,
  updatePointConfig,
} from '@/api/mall'

// 会员管理页（issue #174/#175，spec §4/§6）：ProTable 列表 + 详情抽屉（基本
// 信息 / 收货地址）+ 积分调整弹窗 + 积分流水抽屉 + 积分规则弹窗。mobile 租户
// 内唯一（后端 409）；密码选填（编辑留空 = 不改密码）；停启用走独立端点
//（mall:member:update），无删除（业务身份禁删，生命周期由停启用覆盖）。
// 积分调整挂 mall:member:update、流水挂 mall:member:query、配置读写挂
// mall:point:query/update（后端唯一入账入口保证余额与流水快照一致）。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('mall:member:query'))
const canUpdate = computed(() => userStore.hasPerm('mall:member:update'))
const canPointQuery = computed(() => userStore.hasPerm('mall:point:query'))
const canPointUpdate = computed(() => userStore.hasPerm('mall:point:update'))

const tableRef = ref<InstanceType<typeof ProTable> | null>(null)

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'mobile', label: '手机号', minWidth: 130 },
  { prop: 'nickname', label: '昵称', minWidth: 130 },
  { prop: 'levelId', label: '等级', width: 110, slot: true },
  { prop: 'pointBalance', label: '积分', width: 100, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 130 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'mobile', label: '手机号' },
  { prop: 'nickname', label: '昵称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'mobile', label: '手机号', required: true, placeholder: '租户内唯一，11 位大陆手机号' },
  { prop: 'password', label: '密码', type: 'password', placeholder: '选填；编辑留空 = 不改密码（6-64 位）' },
  { prop: 'nickname', label: '昵称', placeholder: '缺省取手机号' },
  { prop: 'levelId', label: '等级', slot: true },
  { prop: 'avatarUrl', label: '头像 URL' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const api = { page: pageMembers, create: createMember, update: updateMember }

// ---------- 等级下拉（启用等级清单，登录 + mall:member:query） ----------

const levelOptions = ref<any[]>([])
const levelName = (id: number) => {
  const hit = levelOptions.value.find((opt) => opt.id === id)
  return hit ? hit.name : id ? `等级 ${id}` : '-'
}

onMounted(async () => {
  try {
    levelOptions.value = (await listLevelOptions()) || []
  } catch {
    levelOptions.value = []
  }
})

// ---------- 停启用 ----------

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await updateMemberStatus(row.id, next)
  ElMessage.success(next === 0 ? '已启用' : '已停用')
}

// ---------- 详情抽屉 ----------

const detail = ref<any>(null)
const detailVisible = ref(false)
const detailTab = ref('info')

async function openDetail(row: any) {
  const data = await getMember(row.id)
  detail.value = data
  detailVisible.value = true
}

// ---------- 积分调整弹窗（issue #175） ----------

const adjustVisible = ref(false)
const adjustSubmitting = ref(false)
const adjustTarget = ref<any>(null)
const adjustForm = ref({ point: 1, remark: '' })

function openAdjust(row: any) {
  adjustTarget.value = row
  adjustForm.value = { point: 1, remark: '' }
  adjustVisible.value = true
}

async function submitAdjust() {
  if (!adjustTarget.value) return
  if (!adjustForm.value.point) {
    ElMessage.warning('变动积分不能为 0')
    return
  }
  adjustSubmitting.value = true
  try {
    await adjustMemberPoint({
      userId: adjustTarget.value.id,
      point: adjustForm.value.point,
      remark: adjustForm.value.remark || undefined,
    })
    ElMessage.success(adjustForm.value.point > 0 ? '积分已入账' : '积分已扣减')
    adjustVisible.value = false
    tableRef.value?.reload()
  } finally {
    adjustSubmitting.value = false
  }
}

// ---------- 积分流水抽屉（issue #175） ----------

// biz_type 展示（与后端 model.MemberPointBizTypes 对齐）。
const bizLabels: Record<string, string> = {
  order_pay: '支付入账',
  order_cancel: '取消回扣',
  admin_adjust: '手工调整',
}
function bizLabel(biz: string) {
  return bizLabels[biz] ?? biz
}
function bizTagType(biz: string) {
  if (biz === 'order_cancel') return 'warning'
  if (biz === 'admin_adjust') return 'primary'
  return 'success'
}
function pointSign(point: number) {
  return point < 0 ? 'neg' : 'pos'
}

const flowMember = ref<any>(null)
const flowVisible = ref(false)
const flowLoading = ref(false)
const flows = ref<any[]>([])
const flowTotal = ref(0)
const flowQuery = ref({ pageNo: 1, pageSize: 10 })

const flowTitle = computed(() => (flowMember.value ? `积分流水：${flowMember.value.nickname || flowMember.value.mobile}` : '积分流水'))

function openFlows(row: any) {
  flowMember.value = row
  flowQuery.value = { pageNo: 1, pageSize: 10 }
  flows.value = []
  flowTotal.value = 0
  flowVisible.value = true
  loadFlows()
}

async function loadFlows() {
  if (!flowMember.value) return
  flowLoading.value = true
  try {
    const data = await pagePointRecords(flowMember.value.id, {
      pageNo: flowQuery.value.pageNo,
      pageSize: flowQuery.value.pageSize,
    })
    flows.value = data.list || []
    flowTotal.value = data.total || 0
  } finally {
    flowLoading.value = false
  }
}

// ---------- 积分规则弹窗（issue #175，mall:point:*） ----------

const configVisible = ref(false)
const configSubmitting = ref(false)
const configForm = ref({ pointPerYuan: 1, remark: '' })

async function openPointConfig() {
  configVisible.value = true
  const data = await getPointConfig()
  configForm.value = { pointPerYuan: data?.pointPerYuan ?? 1, remark: data?.remark || '' }
}

async function submitPointConfig() {
  configSubmitting.value = true
  try {
    await updatePointConfig({
      pointPerYuan: configForm.value.pointPerYuan,
      remark: configForm.value.remark || undefined,
    })
    ElMessage.success('积分规则已保存')
    configVisible.value = false
  } finally {
    configSubmitting.value = false
  }
}
</script>

<style scoped>
.point-balance {
  font-weight: 600;
}

.adjust-summary,
.flow-summary {
  margin-bottom: 12px;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}

.point.pos {
  color: var(--el-color-success);
}

.point.neg {
  color: var(--el-color-danger);
}

.form-tip {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}
</style>
