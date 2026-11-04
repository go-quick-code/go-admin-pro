<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="infra:config"
      title="参数配置"
      dialog-width="640px"
    >
      <template #cell-visible="{ row }">
        <el-tag :type="row.visible ? 'success' : 'info'" size="small">
          {{ row.visible ? '公开' : '隐藏' }}
        </el-tag>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import ProTable from '@/components/ProTable.vue'
import { formatTime } from '@/utils/format'
import { pageConfigs, createConfig, updateConfig, deleteConfig } from '@/api/infra'

const typeOptions = [
  { label: '字符串', value: 'string' },
  { label: '数字', value: 'number' },
  { label: '布尔', value: 'boolean' },
  { label: 'JSON', value: 'json' },
]

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'category', label: '分类', width: 110 },
  { prop: 'name', label: '参数名称', minWidth: 140 },
  { prop: 'key', label: '参数键名', minWidth: 190 },
  { prop: 'value', label: '参数值', minWidth: 160 },
  { prop: 'type', label: '类型', width: 100 },
  { prop: 'visible', label: '可见性', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '参数名称' },
  { prop: 'key', label: '参数键名' },
  { prop: 'category', label: '分类' },
]

const formItems = [
  { prop: 'category', label: '分类', placeholder: '默认 default' },
  { prop: 'name', label: '参数名称', required: true },
  { prop: 'key', label: '参数键名', required: true, placeholder: '如 system.name' },
  { prop: 'value', label: '参数值', type: 'textarea' },
  { prop: 'type', label: '类型', type: 'select', options: typeOptions },
  { prop: 'visible', label: '对外可见', type: 'switch' },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = { category: 'default', type: 'string', visible: true }

const api = {
  page: pageConfigs,
  create: createConfig,
  update: updateConfig,
  remove: deleteConfig,
}
</script>
