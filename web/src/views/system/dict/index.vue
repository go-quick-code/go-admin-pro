<template>
  <div class="app-container">
    <el-row :gutter="16">
      <el-col :xs="24" :md="10">
        <ProTable
          ref="typeTableRef"
          :columns="typeColumns"
          :form-items="typeFormItems"
          :search-items="typeSearchItems"
          :api="typeApi"
          :default-form="typeDefaultForm"
          perm-prefix="system:dict"
          title="字典类型"
          :action-width="160"
          @row-click="handleTypeClick"
          @success="handleTypeSuccess"
        >
          <template #toolbar>
            <span class="tip">点击行查看字典数据</span>
          </template>
          <template #cell-status="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
              {{ row.status === 0 ? '启用' : '停用' }}
            </el-tag>
          </template>
        </ProTable>
      </el-col>

      <el-col :xs="24" :md="14">
        <el-card shadow="never" class="data-card">
          <template #header>
            <div class="data-header">
              <span>字典数据</span>
              <el-tag v-if="selectedType" type="primary" size="small">{{ selectedType }}</el-tag>
              <span v-else class="text-muted">请选择左侧字典类型</span>
            </div>
          </template>

          <ProTable
            v-if="selectedType"
            :key="selectedType"
            :columns="dataColumns"
            :form-items="dataFormItems"
            :search-items="dataSearchItems"
            :api="dataApi"
            :default-form="dataDefaultForm"
            :extra-query="{ dictType: selectedType }"
            perm-prefix="system:dict"
            title="字典数据"
            :action-width="150"
            :before-submit="beforeDataSubmit"
          >
            <template #cell-status="{ row }">
              <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
                {{ row.status === 0 ? '启用' : '停用' }}
              </el-tag>
            </template>
            <template #cell-colorType="{ row }">
              <el-tag v-if="row.colorType" :type="row.colorType" size="small" effect="plain">
                {{ row.colorType }}
              </el-tag>
              <span v-else>-</span>
            </template>
          </ProTable>
          <el-empty v-else description="请选择左侧字典类型查看数据" :image-size="90" />
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageDictTypes,
  createDictType,
  updateDictType,
  deleteDictType,
  listDictData,
  createDictData,
  updateDictData,
  deleteDictData,
} from '@/api/system'

const selectedType = ref('')
const typeTableRef = ref()

const typeColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '字典名称', minWidth: 130 },
  { prop: 'type', label: '字典类型', minWidth: 150 },
  { prop: 'status', label: '状态', width: 80, slot: true },
  { prop: 'remark', label: '备注', minWidth: 120 },
]

const typeSearchItems = [
  { prop: 'name', label: '字典名称' },
  { prop: 'type', label: '字典类型' },
]

const typeFormItems = [
  { prop: 'name', label: '字典名称', required: true },
  { prop: 'type', label: '字典类型', required: true, placeholder: '如 sys_user_sex' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const typeDefaultForm = { status: 0 }

const typeApi = {
  page: pageDictTypes,
  create: createDictType,
  update: updateDictType,
  remove: deleteDictType,
}

const colorOptions = [
  { label: 'primary', value: 'primary' },
  { label: 'success', value: 'success' },
  { label: 'warning', value: 'warning' },
  { label: 'danger', value: 'danger' },
  { label: 'info', value: 'info' },
]

const dataColumns = [
  { prop: 'label', label: '字典标签', minWidth: 130 },
  { prop: 'value', label: '字典值', minWidth: 100 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'colorType', label: '标签颜色', width: 110, slot: true },
  { prop: 'status', label: '状态', width: 80, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const dataSearchItems = [{ prop: 'label', label: '字典标签' }]

const dataFormItems = [
  { prop: 'label', label: '字典标签', required: true },
  { prop: 'value', label: '字典值', required: true },
  { prop: 'sort', label: '排序', type: 'number' },
  { prop: 'colorType', label: '标签颜色', type: 'select', options: colorOptions },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const dataDefaultForm = { sort: 0, status: 0, colorType: 'primary' }

const dataApi = {
  list: listDictData,
  create: createDictData,
  update: updateDictData,
  remove: deleteDictData,
}

function handleTypeClick(row: any) {
  selectedType.value = row.type
}

function handleTypeSuccess() {
  typeTableRef.value?.reload()
}

function beforeDataSubmit(payload: any) {
  payload.dictType = selectedType.value
  return payload
}
</script>

<style scoped>
.data-card {
  min-height: 480px;
}

.data-header {
  display: flex;
  align-items: center;
  gap: 10px;
  font-weight: 600;
}

.tip {
  color: #909399;
  font-size: 12px;
  margin-left: 8px;
}
</style>
