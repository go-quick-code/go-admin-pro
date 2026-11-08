<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :show-actions="false"
      perm-prefix="erp:stock"
      title="产品库存"
    >
      <template #toolbar>
        <!-- 库存只读页（spec §5：仓库×产品余额），无增删改；导出复用当前筛选。 -->
        <ExcelActions
          title="产品库存"
          export-perm="erp:stock:export"
          :export-action="exportStocks"
          :export-params="stockExportParams"
        />
        <el-button v-if="canQuery" type="primary" plain @click="countVisible = true">库存合计</el-button>
      </template>
      <template #cell-count="{ row }">
        <span class="qty">{{ row.count }}</span>
      </template>
    </ProTable>

    <!-- 产品全仓合计（spec §5：get-count；带仓库查单仓余额，不带查全仓）。 -->
    <el-dialog v-model="countVisible" title="库存合计" width="420px" append-to-body destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="产品" required>
          <el-select v-model="countQuery.productId" filterable style="width: 100%" placeholder="请选择产品">
            <el-option v-for="p in productList" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="仓库">
          <el-select v-model="countQuery.warehouseId" clearable style="width: 100%" placeholder="不选则全仓合计">
            <el-option v-for="w in warehouseList" :key="w.id" :label="w.name" :value="w.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="合计数量">
          <span class="qty count-result">{{ countResult }}</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button type="primary" :loading="countLoading" @click="loadCount">查询</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import { formatTime } from '@/utils/format'
import { useUserStore } from '@/stores/user'
import { pageStocks, getStockCount, exportStocks, pageProducts, pageWarehouses } from '@/api/erp'

// 产品库存页（issue #90，spec §5）：仓库×产品余额分页；库存行无 owner 语义
//（仓库不做独立权限，spec §4.10），租户内全量可见。count 为毫单位定点字符串。
const tableRef = ref()
const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('erp:stock:query'))

const warehouseList = ref<any[]>([])
const productList = ref<any[]>([])

const warehouseOptions = computed(() =>
  warehouseList.value.map((w) => ({ label: w.name, value: w.id })),
)
const productOptions = computed(() =>
  productList.value.map((p) => ({ label: p.name, value: p.id })),
)

const searchItems = computed(() => [
  { prop: 'warehouseId', label: '仓库', type: 'select', options: warehouseOptions.value },
  { prop: 'productId', label: '产品', type: 'select', options: productOptions.value },
])

const columns = [
  { prop: 'productName', label: '产品名称', minWidth: 160 },
  { prop: 'warehouseName', label: '仓库', minWidth: 130 },
  { prop: 'count', label: '库存数量', width: 130, slot: true },
  { prop: 'updateTime', label: '更新时间', minWidth: 170, formatter: (row: any) => formatTime(row.updateTime) },
]

// 导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function stockExportParams() {
  const q = tableRef.value?.query || {}
  return { warehouseId: q.warehouseId, productId: q.productId }
}

const pageApi = { page: pageStocks }

// ---------- 库存合计 ----------
const countVisible = ref(false)
const countLoading = ref(false)
const countQuery = ref<{ productId?: number; warehouseId?: number }>({})
const countResult = ref('—')

async function loadCount() {
  if (!countQuery.value.productId) {
    ElMessage.warning('请选择产品')
    return
  }
  countLoading.value = true
  try {
    const row = (await getStockCount({
      productId: countQuery.value.productId,
      warehouseId: countQuery.value.warehouseId,
    })) as any
    countResult.value = row?.count ?? '—'
  } finally {
    countLoading.value = false
  }
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
.count-result {
  font-size: 16px;
  font-weight: 600;
}
</style>
