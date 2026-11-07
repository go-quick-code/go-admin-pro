<template>
  <div class="doc-items-editor">
    <div class="section-head">
      <span>明细</span>
      <el-button size="small" type="primary" plain :disabled="!editable" @click="addItem">
        添加明细
      </el-button>
    </div>
    <el-table :data="items" border size="small" empty-text="请至少添加一条明细">
      <el-table-column label="#" width="48">
        <template #default="{ $index }">{{ $index + 1 }}</template>
      </el-table-column>
      <el-table-column label="产品" min-width="180">
        <template #default="{ row }">
          <el-select
            v-model="row.productId"
            filterable
            :disabled="!editable"
            placeholder="请选择产品"
            style="width: 100%"
          >
            <el-option v-for="p in products" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="数量" width="130">
        <template #default="{ row }">
          <el-input v-model="row.count" :disabled="!editable" placeholder="如 1.500" inputmode="decimal" />
        </template>
      </el-table-column>
      <el-table-column label="单价（元）" width="130">
        <template #default="{ row }">
          <el-input v-model="row.productPrice" :disabled="!editable" placeholder="0.00" inputmode="decimal" />
        </template>
      </el-table-column>
      <el-table-column label="行金额（元）" width="120">
        <template #default="{ row }">{{ rowAmount(row) }}</template>
      </el-table-column>
      <el-table-column v-if="editable" label="操作" width="70">
        <template #default="{ $index }">
          <el-button link type="danger" @click="removeItem($index)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script lang="ts">
// DocItemRow 明细行数据（数量/单价为十进制字符串，qty/Money 定点契约）。
export interface DocItemRow {
  productId?: number
  count: string
  productPrice: string
}
</script>

<script setup lang="ts">
// ErpDocItemsEditor 是 ERP 单据明细行编辑器（issue #90，spec §6「明细行编辑器
// 做成 ERP 共用组件」的首个实现）：产品选择、数量/单价十进制字符串输入、行金额
// 联动预览（服务端按 单价×数量 权威计算，此处仅展示）。#91+ 的采购/销售单据
// 复用或在此基础上扩展。
import { computed } from 'vue'

const props = defineProps<{
  modelValue: DocItemRow[]
  products: any[]
  editable?: boolean
}>()

const emit = defineEmits<{ (e: 'update:modelValue', rows: DocItemRow[]): void }>()

const items = computed({
  get: () => props.modelValue,
  set: (rows) => emit('update:modelValue', rows),
})

function addItem() {
  emit('update:modelValue', [...props.modelValue, { productId: undefined, count: '', productPrice: '' }])
}

function removeItem(index: number) {
  const rows = [...props.modelValue]
  rows.splice(index, 1)
  emit('update:modelValue', rows)
}

// 行金额预览：单价 × 数量，四舍五入到分。仅展示用（不回传服务端，
// 服务端以 Money.MulQty 权威计算）。
function rowAmount(row: DocItemRow): string {
  const price = Number(row.productPrice)
  const count = Number(row.count)
  if (!isFinite(price) || !isFinite(count)) return '—'
  return (Math.round(Math.abs(price * count) * 100) / 100).toFixed(2)
}
</script>

<style scoped>
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 4px 0 8px;
  font-weight: 600;
}
</style>
