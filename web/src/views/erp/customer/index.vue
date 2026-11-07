<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      :before-submit="beforeSubmit"
      perm-prefix="erp:customer"
      title="客户"
      :action-width="160"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #toolbar>
        <!-- Excel 导出（issue #89，spec §4.11：ERP 只导出不导入），复用当前筛选。 -->
        <ExcelActions
          title="客户"
          export-perm="erp:customer:export"
          :export-action="exportCustomers"
          :export-params="customerExportParams"
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
  pageCustomers,
  getCustomer,
  createCustomer,
  updateCustomer,
  deleteCustomer,
  exportCustomers,
} from '@/api/erp'

// ERP 客户页（issue #89，spec §3.1/§6）：与 CRM 客户完全独立（不同表、不同
// 路由 /erp/customer vs /crm/customer，ADR 0015）。税率以十进制字符串提交
//（两位小数 0–999.99），未填写保持 NULL（后端 money 定点，不经过 float64）。
const tableRef = ref()

const customerStatusOptions = statusOptions

const searchItems = [
  { prop: 'name', label: '客户名称' },
  { prop: 'mobile', label: '手机号码' },
  { prop: 'status', label: '状态', type: 'select', options: customerStatusOptions },
]

const columns = [
  { prop: 'name', label: '客户名称', minWidth: 160 },
  { prop: 'contact', label: '联系人', width: 100 },
  { prop: 'mobile', label: '手机号码', width: 130 },
  { prop: 'telephone', label: '联系电话', width: 130 },
  { prop: 'email', label: '电子邮箱', minWidth: 160 },
  { prop: 'taxNo', label: '税号', minWidth: 170 },
  { prop: 'taxPercent', label: '税率(%)', width: 90, formatter: (row: any) => row.taxPercent ?? '-' },
  { prop: 'bankName', label: '开户行', minWidth: 150 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const formItems = [
  { prop: 'name', label: '客户名称', required: true },
  { prop: 'contact', label: '联系人' },
  { prop: 'mobile', label: '手机号码' },
  { prop: 'telephone', label: '联系电话' },
  { prop: 'email', label: '电子邮箱' },
  { prop: 'fax', label: '传真' },
  { prop: 'taxNo', label: '税号' },
  { prop: 'taxPercent', label: '税率（%）' },
  { prop: 'bankName', label: '开户行' },
  { prop: 'bankAccount', label: '开户账号' },
  { prop: 'bankAddress', label: '开户行地址' },
  { prop: 'sort', label: '排序', type: 'number' },
  { prop: 'status', label: '状态', type: 'radio', options: customerStatusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm: Record<string, any> = {
  name: '',
  contact: '',
  mobile: '',
  telephone: '',
  email: '',
  fax: '',
  taxNo: '',
  taxPercent: '',
  bankName: '',
  bankAccount: '',
  bankAddress: '',
  sort: 0,
  status: 0,
  remark: '',
}

// 税率未填写时不提交空串（后端 money 定点只接受十进制字符串或 null，
// 空串会 400）；删除后未提交即保持/清空 NULL。
function beforeSubmit(payload: Record<string, any>) {
  if (payload.taxPercent === '' || payload.taxPercent == null) {
    delete payload.taxPercent
  }
  return payload
}

// 客户导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function customerExportParams() {
  const q = tableRef.value?.query || {}
  return { name: q.name, mobile: q.mobile, status: q.status }
}

const api = {
  page: pageCustomers,
  detail: getCustomer,
  create: createCustomer,
  update: updateCustomer,
  remove: deleteCustomer,
}
</script>
