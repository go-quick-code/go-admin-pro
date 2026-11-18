<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      perm-prefix="mall:brand"
      title="商品品牌"
      dialog-width="560px"
    >
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { formatTime, statusOptions } from '@/utils/format'
import { pageBrands, createBrand, updateBrand, deleteBrand } from '@/api/mall'

// 商品品牌页（issue #176，spec §3.2/§4）：ProTable 标准 CRUD（照会员等级页
// 形状）。name 租户内唯一（后端撞键 409）；picUrl 品牌图（MVP 直填 URL，
// 上传组件随后续票）；SPU 引用守卫随 T4 落地。

const columns = [
  { prop: 'name', label: '品牌名称', minWidth: 140 },
  { prop: 'picUrl', label: '品牌图片', minWidth: 160, formatter: (row: any) => row.picUrl || '-' },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '品牌名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '品牌名称', required: true, placeholder: '租户内唯一' },
  { prop: 'picUrl', label: '品牌图片 URL' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const api = { page: pageBrands, create: createBrand, update: updateBrand, remove: deleteBrand }
</script>
