<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="crm:product"
      title="产品"
      :action-width="170"
    >
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { dictLabel, formatTime } from '@/utils/format'
import {
  pageProducts,
  getProduct,
  createProduct,
  updateProduct,
  deleteProduct,
} from '@/api/crm'
// 产品状态（生成自字段元信息：状态）。
const productStatusOptions = [
  { label: '下架', value: 0 },
  { label: '上架', value: 1 },
]
// TODO：单位选项接入系统字典 crm_product_unit（web 尚无字典读取封装，先留空选项）。
const productUnitOptions: { label: string; value: any }[] = []

const tableRef = ref()
const searchItems = [
  { prop: 'name', label: '产品名称' },
  { prop: 'code', label: '产品编码' },
  { prop: 'unit', label: '单位', type: 'select', options: productUnitOptions },
  { prop: 'status', label: '状态', type: 'select', options: productStatusOptions },
]

const columns = [
  { prop: 'categoryId', label: '产品分类 ID', minWidth: 120 },
  { prop: 'name', label: '产品名称', minWidth: 120 },
  { prop: 'code', label: '产品编码', minWidth: 120 },
  { prop: 'unit', label: '单位', width: 110, formatter: (row: any) => dictLabel(productUnitOptions, row.unit) },
  { prop: 'price', label: '价格（元）', minWidth: 120 },
  { prop: 'status', label: '状态', width: 90, formatter: (row: any) => dictLabel(productStatusOptions, row.status) },
  { prop: 'description', label: '描述', minWidth: 120 },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'categoryId', label: '产品分类 ID', required: true, type: 'number' },
  { prop: 'name', label: '产品名称', required: true },
  { prop: 'code', label: '产品编码', required: true },
  { prop: 'unit', label: '单位', type: 'select', options: productUnitOptions },
  { prop: 'price', label: '价格（元）', required: true },
  { prop: 'status', label: '状态', type: 'select', options: productStatusOptions },
  { prop: 'description', label: '描述', type: 'textarea' },
]

const defaultForm: Record<string, any> = {
  categoryId: 0,
  name: '',
  code: '',
  unit: '',
  price: '',
  status: 1,
  description: '',
}

const api = {
  page: pageProducts,
  detail: getProduct,
  create: createProduct,
  update: updateProduct,
  remove: deleteProduct,
}
</script>
