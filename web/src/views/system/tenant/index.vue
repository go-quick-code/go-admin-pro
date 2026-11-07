<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="system:tenant"
      title="租户"
      dialog-width="640px"
    >
      <template #toolbar>
        <span class="tip">新建租户会自动初始化管理员账号 admin / 123456</span>
      </template>
      <template #cell-status="{ row }">
        <el-switch :model-value="row.status === 0" @change="toggleStatus(row)" />
      </template>
      <template #cell-expireTime="{ row }">
        {{ row.expireTime ? formatTime(row.expireTime) : '永不过期' }}
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click="openQuota(row)">配额</el-button>
      </template>
    </ProTable>

    <!-- 租户配额（issue #71）：展示套餐限额与当前用量，不限项显示「不限」。 -->
    <el-dialog v-model="quotaVisible" :title="`配额 - ${quotaTenantName}`" width="520px" destroy-on-close append-to-body>
      <el-descriptions v-if="quota" :column="1" border>
        <el-descriptions-item label="套餐">
          <el-tag v-if="quota.unlimited" type="success" size="small">不限（平台租户 / 未绑定套餐）</el-tag>
          <span v-else>{{ quotaPackageName }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="用户数">
          {{ quotaText(quota.used.users, quota.limit.maxUsers) }}
          <el-progress
            v-if="!quota.unlimited && quota.limit.maxUsers > 0"
            :percentage="percent(quota.used.users, quota.limit.maxUsers)"
            :status="percent(quota.used.users, quota.limit.maxUsers) >= 100 ? 'exception' : undefined"
            :stroke-width="8"
          />
        </el-descriptions-item>
        <el-descriptions-item label="存储">
          {{ quota.unlimited ? '不限' : `${formatSize(quota.used.storageBytes)} / ${quota.limit.maxStorageMb} MB` }}
          <el-progress
            v-if="!quota.unlimited && quota.limit.maxStorageMb > 0"
            :percentage="percent(quota.used.storageBytes, quota.limit.maxStorageMb * 1024 * 1024)"
            :status="percent(quota.used.storageBytes, quota.limit.maxStorageMb * 1024 * 1024) >= 100 ? 'exception' : undefined"
            :stroke-width="8"
          />
        </el-descriptions-item>
        <el-descriptions-item label="本月短信">
          {{ quotaText(quota.used.smsMonthly, quota.limit.maxSmsMonthly) }}
        </el-descriptions-item>
        <el-descriptions-item label="本月邮件">
          {{ quotaText(quota.used.mailMonthly, quota.limit.maxMailMonthly) }}
        </el-descriptions-item>
      </el-descriptions>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatSize, formatTime, statusOptions } from '@/utils/format'
import {
  pageTenants,
  createTenant,
  updateTenant,
  updateTenantStatus,
  deleteTenant,
  listSimpleTenantPackages,
  getTenantQuota,
} from '@/api/system'

const packages = ref<any[]>([])

const packageOptions = computed(() =>
  packages.value.map((pkg) => ({ label: pkg.name, value: pkg.id })),
)

function packageName(packageId: number) {
  if (!packageId) return '未绑定'
  return packages.value.find((pkg) => pkg.id === packageId)?.name || `#${packageId}`
}

// ---------- 租户配额弹窗（issue #71） ----------
const quotaVisible = ref(false)
const quotaTenantName = ref('')
const quotaPackageName = ref('')
const quota = ref<any>(null)

async function openQuota(row: any) {
  quotaTenantName.value = row.name
  quotaPackageName.value = packageName(row.packageId)
  quota.value = await getTenantQuota(row.id)
  quotaVisible.value = true
}

// quotaText 渲染「已用 / 上限」；上限为 0（不限）只显示已用。
function quotaText(used: number, limit: number) {
  if (!quota.value?.unlimited && limit > 0) return `${used} / ${limit}`
  return String(used)
}

function percent(used: number, limit: number) {
  if (limit <= 0) return 0
  return Math.min(100, Math.round((used / limit) * 100))
}

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '租户名称', minWidth: 140 },
  { prop: 'contactName', label: '联系人', minWidth: 110 },
  { prop: 'contactMobile', label: '联系电话', minWidth: 130 },
  { prop: 'packageId', label: '套餐', minWidth: 120, formatter: (row: any) => packageName(row.packageId) },
  { prop: 'accountCount', label: '账号额度', width: 100 },
  { prop: 'expireTime', label: '过期时间', minWidth: 170, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '租户名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = computed(() => [
  { prop: 'name', label: '租户名称', required: true },
  { prop: 'contactName', label: '联系人' },
  { prop: 'contactMobile', label: '联系电话' },
  { prop: 'packageId', label: '租户套餐', type: 'select', options: packageOptions.value },
  { prop: 'domain', label: '绑定域名', placeholder: '可选，如 demo.example.com' },
  { prop: 'accountCount', label: '账号额度', type: 'number' },
  { prop: 'expireTime', label: '过期时间', type: 'datetime' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
])

const defaultForm = { status: 0, accountCount: 10, packageId: 0 }

const api = {
  page: pageTenants,
  create: createTenant,
  update: updateTenant,
  remove: deleteTenant,
}

onMounted(async () => {
  packages.value = (await listSimpleTenantPackages()) || []
})

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await updateTenantStatus({ id: row.id, status: next })
  row.status = next
  ElMessage.success(next === 0 ? '租户已启用' : '租户已停用')
}
</script>

<style scoped>
.tip {
  color: #909399;
  font-size: 12px;
  margin-left: 8px;
}
</style>
