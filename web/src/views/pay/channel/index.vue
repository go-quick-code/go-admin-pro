<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :before-submit="beforeSubmit"
      perm-prefix="pay:channel"
      title="支付渠道"
      dialog-width="680px"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #cell-config="{ row }">{{ configSummary(row) }}</template>
      <template #actions="{ row }">
        <el-button
          v-if="canUpdate"
          link
          :type="row.status === 0 ? 'warning' : 'success'"
          @click="toggleStatus(row)"
        >
          {{ row.status === 0 ? '停用' : '启用' }}
        </el-button>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, statusOptions } from '@/utils/format'
import { pageChannels, updateChannel } from '@/api/pay'

// 渠道配置页（issue #131，spec §6：平台级）：14 行种子常驻，禁删只启停
//（无新增/删除入口，spec §4.10）。凭证三段式：查询只见 ******，编辑弹窗
// 提交 ****** 表示保留原值（spec §3.1）。仅平台租户可管理（后端 requirePlatform
// 收口，非平台租户即使持码也 403）。

const userStore = useUserStore()
const canUpdate = computed(() => userStore.hasPerm('pay:channel:update'))
const tableRef = ref<InstanceType<typeof ProTable> | null>(null)

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'code', label: '渠道编码', minWidth: 130 },
  { prop: 'name', label: '渠道名称', minWidth: 150 },
  { prop: 'feeRate', label: '手续费率', width: 100 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'config', label: '凭证', minWidth: 180, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'updateTime', label: '更新时间', minWidth: 170, formatter: (row: any) => formatTime(row.updateTime) },
]

const searchItems = [
  { prop: 'code', label: '渠道编码' },
  { prop: 'name', label: '渠道名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '渠道名称', visibleOn: 'update' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  {
    prop: 'config',
    label: '渠道凭证',
    type: 'textarea',
    placeholder: 'JSON 对象；密钥字段查询时显示 ******，提交 ****** 表示保留原值',
  },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const api = { page: pageChannels, update: (data: any) => updateChannel(data.id, data) }

// configSummary 是列表凭证列的摘要：{} 或含密钥占位的压缩 JSON。
function configSummary(row: any): string {
  const cfg = row.config || '{}'
  return cfg.length > 40 ? `${cfg.slice(0, 40)}…` : cfg
}

// beforeSubmit 收窄提交面：渠道码/名称/费率是种子常量，只允许启停 + 凭证合并 + 备注。
function beforeSubmit(payload: any, mode: 'create' | 'update') {
  if (mode === 'update') {
    const config = typeof payload.config === 'string' ? payload.config.trim() : ''
    return {
      status: payload.status,
      config: config || undefined,
      remark: payload.remark,
    }
  }
  return payload
}

async function toggleStatus(row: any) {
  const target = row.status === 0 ? 1 : 0
  const action = target === 0 ? '启用' : '停用'
  await ElMessageBox.confirm(`确认${action}渠道「${row.name}」吗？停用后新支付不可选用该渠道。`, '提示', {
    type: 'warning',
  })
  await updateChannel(row.id, { status: target })
  ElMessage.success(`${action}成功`)
  tableRef.value?.reload()
}
</script>
