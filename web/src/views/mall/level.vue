<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      perm-prefix="mall:level"
      title="会员等级"
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
import { pageLevels, createLevel, updateLevel, deleteLevel } from '@/api/mall'

// 会员等级页（issue #174，spec §4/§6）：ProTable 标准 CRUD。name/level 租户内
// 唯一（后端撞键 409）；被会员引用禁删（后端 409）；等级纯手工维护
//（MVP 无经验值体系，experience 为纯维护阈值列）。列表按 level 升序
//（后端固定排序）。

const columns = [
  { prop: 'level', label: '等级值', width: 100 },
  { prop: 'name', label: '等级名称', minWidth: 140 },
  { prop: 'experience', label: '经验阈值', width: 110 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '等级名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '等级名称', required: true, placeholder: '租户内唯一，如 普通会员' },
  { prop: 'level', label: '等级值', type: 'number', required: true, placeholder: '租户内唯一，≥1，小值低级' },
  { prop: 'experience', label: '经验阈值', type: 'number', placeholder: '纯维护列（MVP 无经验值体系）' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const api = { page: pageLevels, create: createLevel, update: updateLevel, remove: deleteLevel }
</script>
