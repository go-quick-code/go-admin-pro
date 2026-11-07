<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :form-items="formItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="erp:product-unit"
      title="产品单位"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #toolbar>
        <!-- Excel 导出（issue #88，spec §4.11：ERP 只导出不导入），复用当前筛选。 -->
        <ExcelActions
          title="产品单位"
          export-perm="erp:product-unit:export"
          :export-action="exportProductUnits"
          :export-params="unitExportParams"
        />
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageProductUnits,
  getProductUnit,
  createProductUnit,
  updateProductUnit,
  deleteProductUnit,
  exportProductUnits,
} from '@/api/erp'

// 产品单位页（issue #88，spec §3.1：仅名称字典，无换算率；多单位换算明确不做）。
// 被产品引用时后端 409 拒绝删除，ProTable 直接展示错误提示。
const tableRef = ref()

const unitStatusOptions = statusOptions

const searchItems = [
  { prop: 'name', label: '单位名称' },
  { prop: 'status', label: '状态', type: 'select', options: unitStatusOptions },
]

const columns = [
  { prop: 'name', label: '单位名称', minWidth: 160 },
  { prop: 'status', label: '状态', width: 100, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const formItems = [
  { prop: 'name', label: '单位名称', required: true },
  { prop: 'status', label: '状态', type: 'radio', options: unitStatusOptions },
]

const defaultForm = { name: '', status: 0 }

// 单位导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function unitExportParams() {
  const q = tableRef.value?.query || {}
  return { name: q.name, status: q.status }
}

const api = {
  page: pageProductUnits,
  detail: getProductUnit,
  create: createProductUnit,
  update: updateProductUnit,
  remove: deleteProductUnit,
}
</script>
