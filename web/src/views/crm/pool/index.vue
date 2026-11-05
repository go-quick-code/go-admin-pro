<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="api"
      title="公海"
      :show-toolbar="true"
      :action-width="100"
    >
      <template #toolbar>
        <!-- 公海租户配置（issue #14）：回收天数/认领上限按租户生效，
            编辑权限 crm:pool-config:update（菜单种子 4050）。 -->
        <el-button
          v-if="userStore.hasPerm('crm:pool-config:update')"
          type="primary"
          plain
          @click="openConfig"
        >
          公海配置
        </el-button>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status, customerStatusTagTypes)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-poolEnterTime="{ row }">
        {{ formatTime(row.poolEnterTime) }}
      </template>
      <template #actions="{ row }">
        <el-button
          v-if="userStore.hasPerm('crm:customer:claim')"
          link
          type="primary"
          @click.stop="handleClaim(row)"
        >
          认领
        </el-button>
      </template>
    </ProTable>

    <!-- 公海配置对话框：编辑当前租户的自动回收天数与认领上限。
        认领上限即时生效；回收天数在下一次自动回收任务执行时生效。 -->
    <el-dialog v-model="configVisible" title="公海配置" width="440px" destroy-on-close>
      <el-form label-width="100px">
        <el-form-item label="回收天数">
          <el-input-number
            v-model="configForm.recycleDays"
            :min="1"
            :max="3650"
            :step="1"
            step-strictly
            style="width: 180px"
          />
          <span class="config-tip">超过该天数未跟进的客户由定时任务自动回收（默认 30）</span>
        </el-form-item>
        <el-form-item label="认领上限">
          <el-input-number
            v-model="configForm.claimLimit"
            :min="1"
            :max="100000"
            :step="1"
            step-strictly
            style="width: 180px"
          />
          <span class="config-tip">每人名下客户数达到上限后不可再认领（默认 50）</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="configVisible = false">取消</el-button>
        <el-button type="primary" :loading="configSaving" @click="saveConfig">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime, dictLabel, statusTagType, type StatusTagType } from '@/utils/format'
import {
  pagePoolCustomers,
  claimCustomer,
  getPoolConfig,
  updatePoolConfig,
} from '@/api/crm'
import { useUserStore } from '@/stores/user'

// 公海（issue #13，docs/crm-mvp-spec.md §4.2）：无负责人客户池，
// 列表不受数据范围过滤；认领受租户配置的认领上限约束（默认 50），先到先得。
// 超期未跟进的客户由定时任务按租户配置的回收天数自动回收（issue #14，默认 30）。
// 客户状态对齐规格：1 潜在 / 2 活跃 / 3 流失。
const customerStatusOptions = [
  { label: '潜在', value: 1 },
  { label: '活跃', value: 2 },
  { label: '流失', value: 3 },
]

const customerStatusTagTypes: Record<number, StatusTagType> = {
  2: 'success',
  3: 'danger',
}

function statusLabel(status: number) {
  return dictLabel(customerStatusOptions, status)
}

const tableRef = ref()
const userStore = useUserStore()

const searchItems = computed(() => [
  { prop: 'name', label: '客户名称' },
  { prop: 'status', label: '状态', type: 'select', options: customerStatusOptions },
])

const columns = [
  { prop: 'customerNo', label: '客户编号', width: 150 },
  { prop: 'name', label: '客户名称', minWidth: 160 },
  { prop: 'industry', label: '行业', width: 110 },
  { prop: 'level', label: '级别', width: 90 },
  { prop: 'source', label: '来源', width: 110 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'releaseReason', label: '释放原因', minWidth: 130 },
  {
    prop: 'poolEnterTime',
    label: '进入公海时间',
    minWidth: 170,
    slot: true,
  },
]

// 公海页只读列表：不提供新增/编辑/删除，仅认领与租户配置。
const api = { page: pagePoolCustomers }

async function handleClaim(row: any) {
  const confirmed = await ElMessageBox.confirm(
    `认领「${row.name}」到自己名下？认领后该客户将按数据权限对你的角色可见。`,
    '认领客户',
    { type: 'warning' },
  ).catch(() => false)
  if (!confirmed) return
  await claimCustomer(row.id)
  ElMessage.success('认领成功')
  tableRef.value?.reload()
}

// ---------- 公海租户配置（issue #14） ----------
const configVisible = ref(false)
const configSaving = ref(false)
const configForm = reactive({ recycleDays: 30, claimLimit: 50 })

async function openConfig() {
  // request 拦截器已解包信封：直接拿到 { recycleDays, claimLimit }
  // （配置行缺失时后端返回默认值 30/50）。
  const values: any = await getPoolConfig()
  configForm.recycleDays = values.recycleDays ?? 30
  configForm.claimLimit = values.claimLimit ?? 50
  configVisible.value = true
}

async function saveConfig() {
  configSaving.value = true
  try {
    await updatePoolConfig({ ...configForm })
    ElMessage.success('公海配置已保存')
    configVisible.value = false
  } finally {
    configSaving.value = false
  }
}
</script>

<style scoped>
.config-tip {
  margin-left: 12px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
