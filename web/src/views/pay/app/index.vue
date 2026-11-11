<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      perm-prefix="pay:app"
      title="支付应用"
      dialog-width="680px"
      @row-click="openDetail"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openDetail(row)">详情</el-button>
      </template>
    </ProTable>

    <!-- 应用详情抽屉：基本信息 + 渠道绑定 tab（spec §6：详情含渠道绑定 tab +
        凭证只读脱敏展示——凭证是平台级渠道配置，本页只选渠道码，不涉凭证）。 -->
    <el-drawer v-model="detailVisible" :title="detail?.name || '应用详情'" size="760px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="ID">{{ detail.id }}</el-descriptions-item>
            <el-descriptions-item label="应用标识">{{ detail.appKey }}</el-descriptions-item>
            <el-descriptions-item label="应用名称">{{ detail.name }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="detail.status === 0 ? 'success' : 'info'" size="small">
                {{ detail.status === 0 ? '启用' : '停用' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="支付回调地址">{{ detail.orderNotifyUrl || '-' }}</el-descriptions-item>
            <el-descriptions-item label="退款回调地址">{{ detail.refundNotifyUrl || '-' }}</el-descriptions-item>
            <el-descriptions-item label="转账回调地址">{{ detail.transferNotifyUrl || '-' }}</el-descriptions-item>
            <el-descriptions-item label="备注">{{ detail.remark || '-' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
            <el-descriptions-item label="更新时间">{{ formatTime(detail.updateTime) }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>
        <el-tab-pane label="渠道绑定" name="channels">
          <el-alert
            type="info"
            :closable="false"
            show-icon
            title="应用可用渠道 = 勾选绑定码；停用渠道不可勾选。绑定保存为全量替换。"
            class="binding-tip"
          />
          <el-checkbox-group v-model="selectedCodes">
            <div v-for="opt in channelOptions" :key="opt.code" class="channel-option">
              <el-checkbox :value="opt.code" :disabled="opt.status !== 0">
                {{ opt.name }}（{{ opt.code }}）
              </el-checkbox>
              <el-tag :type="opt.status === 0 ? 'success' : 'info'" size="small">
                {{ opt.status === 0 ? '启用' : '停用' }}
              </el-tag>
            </div>
          </el-checkbox-group>
          <div v-if="canUpdate" class="binding-actions">
            <el-button type="primary" :loading="savingBindings" @click="saveBindings">保存绑定</el-button>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageApps,
  getApp,
  createApp,
  updateApp,
  deleteApp,
  updateAppChannels,
  listTenantChannels,
} from '@/api/pay'

// 支付应用页（issue #131，spec §6）：ProTable 列表 + 详情抽屉（基本信息 /
// 渠道绑定两个 tab）。绑定全量替换：勾选启用渠道码后整体提交（spec §5）；
// 未实现/停用渠道由后端校验（非法码 409、停用 409），前端对停用渠道禁勾。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('pay:app:query'))
const canUpdate = computed(() => userStore.hasPerm('pay:app:update'))

const columns = [
  { prop: 'id', label: 'ID', width: 80 },
  { prop: 'appKey', label: '应用标识', minWidth: 140 },
  { prop: 'name', label: '应用名称', minWidth: 160 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'appKey', label: '应用标识' },
  { prop: 'name', label: '应用名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'appKey', label: '应用标识', required: true, placeholder: '租户内唯一，如 demo-app' },
  { prop: 'name', label: '应用名称', required: true },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'orderNotifyUrl', label: '支付回调地址', placeholder: 'http(s):// 开头，支付成功后通知该地址' },
  { prop: 'refundNotifyUrl', label: '退款回调地址', placeholder: 'http(s):// 开头' },
  { prop: 'transferNotifyUrl', label: '转账回调地址', placeholder: 'http(s):// 开头' },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const api = { page: pageApps, create: createApp, update: updateApp, remove: deleteApp }

// ---------- 详情抽屉 ----------

const detail = ref<any>(null)
const detailVisible = ref(false)
const detailTab = ref('info')
const channelOptions = ref<any[]>([])
const selectedCodes = ref<string[]>([])
const savingBindings = ref(false)

async function openDetail(row: any) {
  const data = await getApp(row.id)
  detail.value = data
  selectedCodes.value = data.channelCodes || []
  detailVisible.value = true
  // 渠道可选项（14 常量、无凭证）：停用渠道禁勾。
  try {
    channelOptions.value = (await listTenantChannels()) || []
  } catch {
    channelOptions.value = []
  }
}

async function saveBindings() {
  if (!detail.value) return
  savingBindings.value = true
  try {
    await updateAppChannels(detail.value.id, selectedCodes.value)
    ElMessage.success('渠道绑定已保存')
    const data = await getApp(detail.value.id)
    detail.value = data
    selectedCodes.value = data.channelCodes || []
  } finally {
    savingBindings.value = false
  }
}
</script>

<style scoped>
.binding-tip {
  margin-bottom: 12px;
}

.channel-option {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 2px 0;
}

.binding-actions {
  margin-top: 16px;
}
</style>
