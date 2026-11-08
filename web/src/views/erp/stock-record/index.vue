<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :show-actions="false"
      perm-prefix="erp:stock-record"
      title="出入库流水"
    >
      <template #toolbar>
        <!-- 时间区间（业务时区）：ProTable 搜索项仅支持输入/下拉，这里并入查询参数。 -->
        <el-date-picker
          v-model="timeRange"
          type="daterange"
          value-format="YYYY-MM-DD"
          range-separator="至"
          start-placeholder="入账开始"
          end-placeholder="入账结束"
          style="width: 260px"
          @change="tableRef?.reload?.()"
        />
        <ExcelActions
          title="出入库流水"
          export-perm="erp:stock-record:export"
          :export-action="exportStockRecords"
          :export-params="recordExportParams"
        />
      </template>
      <template #cell-bizType="{ row }">
        <el-tag :type="bizTagType(row.bizType)" size="small">{{ bizLabel(row.bizType) }}</el-tag>
      </template>
      <template #cell-count="{ row }">
        <span :class="['qty', milliSign(row.count)]">{{ row.count }}</span>
      </template>
      <template #cell-totalCount="{ row }">
        <span class="qty">{{ row.totalCount }}</span>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime } from '@/utils/format'
import { pageStockRecords, exportStockRecords, pageProducts, pageWarehouses } from '@/api/erp'

// 出入库流水页（issue #90，spec §4.3/§5）：append-only 流水，只读查询；
// 按 biz_type/仓库/产品/时间筛选。count 正入负出、totalCount 为变动后结存快照。
const tableRef = ref()
const timeRange = ref<[string, string] | null>(null)

const warehouseList = ref<any[]>([])
const productList = ref<any[]>([])

const warehouseOptions = computed(() =>
  warehouseList.value.map((w) => ({ label: w.name, value: w.id })),
)
const productOptions = computed(() =>
  productList.value.map((p) => ({ label: p.name, value: p.id })),
)

// biz_type 展示（与后端 stockRecordBizNames 对齐；_CANCEL 对为反审核）。
const bizLabels: Record<string, string> = {
  stock_in: '其它入库',
  stock_in_CANCEL: '其它入库（反审核）',
  stock_out: '其它出库',
  stock_out_CANCEL: '其它出库（反审核）',
  stock_move: '库存调拨',
  stock_move_CANCEL: '库存调拨（反审核）',
  stock_check: '盘点调整',
  stock_check_CANCEL: '盘点调整（反审核）',
}
function bizLabel(biz: string) {
  return bizLabels[biz] ?? biz
}
function bizTagType(biz: string) {
  if (biz.endsWith('_CANCEL')) return 'warning'
  if (biz === 'stock_out') return 'danger'
  return 'success'
}
function milliSign(count: string) {
  return count?.startsWith('-') ? 'neg' : 'pos'
}

const searchItems = computed(() => [
  {
    prop: 'bizType',
    label: '业务类型',
    type: 'select',
    options: Object.entries(bizLabels).map(([value, label]) => ({ label, value })),
  },
  { prop: 'warehouseId', label: '仓库', type: 'select', options: warehouseOptions.value },
  { prop: 'productId', label: '产品', type: 'select', options: productOptions.value },
])

const columns = [
  { prop: 'bizType', label: '业务类型', width: 130, slot: true },
  { prop: 'bizNo', label: '单据编号', minWidth: 150 },
  { prop: 'productName', label: '产品名称', minWidth: 140 },
  { prop: 'warehouseName', label: '仓库', minWidth: 110 },
  { prop: 'count', label: '变动数量', width: 120, slot: true },
  { prop: 'totalCount', label: '结存数量', width: 120, slot: true },
  { prop: 'createTime', label: '入账时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

// 分页/导出并入时间区间（end 为纯日期时后端含当日）。
function withTimeRange(params: any) {
  return {
    ...params,
    startTime: timeRange.value?.[0],
    endTime: timeRange.value?.[1],
  }
}
const pageApi = {
  page: (params: any) => pageStockRecords(withTimeRange(params)),
}
function recordExportParams() {
  const q = tableRef.value?.query || {}
  return withTimeRange({ bizType: q.bizType, warehouseId: q.warehouseId, productId: q.productId })
}

onMounted(async () => {
  warehouseList.value = ((await pageWarehouses({ pageNo: 1, pageSize: 200 })) as any)?.list || []
  productList.value = ((await pageProducts({ pageNo: 1, pageSize: 200 })) as any)?.list || []
})
</script>

<style scoped>
.qty {
  font-variant-numeric: tabular-nums;
}
.qty.neg {
  color: var(--el-color-danger);
}
.qty.pos {
  color: var(--el-color-success);
}
</style>
