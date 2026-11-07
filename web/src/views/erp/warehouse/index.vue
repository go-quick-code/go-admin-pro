<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="erp:warehouse"
      title="仓库"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #toolbar>
        <!-- Excel 导出（issue #89，spec §4.11：ERP 只导出不导入），复用当前筛选。 -->
        <ExcelActions
          title="仓库"
          export-perm="erp:warehouse:export"
          :export-action="exportWarehouses"
          :export-params="warehouseExportParams"
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
  pageWarehouses,
  getWarehouse,
  createWarehouse,
  updateWarehouse,
  deleteWarehouse,
  exportWarehouses,
} from '@/api/erp'

// 仓库页（issue #89，spec §3.1/§6）：address/principal 纯展示（负责人不做独立
// 权限，spec §4.10）；owner = 创建人，列表按数据范围过滤；status 0 启用/1 停用。
const tableRef = ref()

const warehouseStatusOptions = statusOptions

const searchItems = [
  { prop: 'name', label: '仓库名称' },
  { prop: 'status', label: '状态', type: 'select', options: warehouseStatusOptions },
]

const columns = [
  { prop: 'name', label: '仓库名称', minWidth: 160 },
  { prop: 'address', label: '仓库地址', minWidth: 220 },
  { prop: 'principal', label: '负责人', width: 110 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 100, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const formItems = [
  { prop: 'name', label: '仓库名称', required: true },
  { prop: 'address', label: '仓库地址' },
  { prop: 'principal', label: '负责人' },
  { prop: 'sort', label: '排序', type: 'number' },
  { prop: 'status', label: '状态', type: 'radio', options: warehouseStatusOptions },
]

const defaultForm = { name: '', address: '', principal: '', sort: 0, status: 0 }

// 仓库导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function warehouseExportParams() {
  const q = tableRef.value?.query || {}
  return { name: q.name, status: q.status }
}

const api = {
  page: pageWarehouses,
  detail: getWarehouse,
  create: createWarehouse,
  update: updateWarehouse,
  remove: deleteWarehouse,
}
</script>
