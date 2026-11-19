<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      :before-submit="normalizeSubmit"
      perm-prefix="mall:spu"
      title="商品"
      dialog-width="960px"
      :action-width="210"
    >
      <template #cell-picUrl="{ row }">
        <el-image
          v-if="row.picUrl"
          :src="row.picUrl"
          :preview-src-list="[row.picUrl]"
          preview-teleported
          fit="cover"
          style="width: 48px; height: 48px; border-radius: 4px"
        />
        <span v-else>-</span>
      </template>
      <template #cell-categoryName="{ row }">{{ categoryName(row.categoryId) }}</template>
      <template #cell-brandName="{ row }">{{ brandName(row.brandId) }}</template>
      <template #cell-specType="{ row }">
        <el-tag :type="row.specType === 1 ? 'warning' : 'info'" size="small">
          {{ row.specType === 1 ? '多规格' : '单规格' }}
        </el-tag>
      </template>
      <template #cell-marketPrice="{ row }">{{ row.marketPrice ?? '0.00' }}</template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
          {{ row.status === 1 ? '上架' : '下架' }}
        </el-tag>
      </template>
      <template #cell-createTime="{ row }">{{ formatTime(row.createTime) }}</template>
      <template #actions="{ row }">
        <el-button
          v-if="canUpdate"
          link
          :type="row.status === 1 ? 'info' : 'success'"
          @click.stop="toggleStatus(row)"
        >
          {{ row.status === 1 ? '下架' : '上架' }}
        </el-button>
      </template>

      <!-- 表单：分类（树选，须叶子）/品牌（可空） -->
      <template #form-categoryId="{ form }">
        <el-tree-select
          v-model="form.categoryId"
          :data="categoryTree"
          :props="{ label: 'name', value: 'id', children: 'children' }"
          check-strictly
          clearable
          style="width: 100%"
          placeholder="请选择分类（商品只能挂叶子分类）"
        />
      </template>
      <template #form-brandId="{ form }">
        <el-select v-model="form.brandId" clearable filterable style="width: 100%" placeholder="可不选">
          <el-option v-for="b in brands" :key="b.id" :label="b.name" :value="b.id" />
        </el-select>
      </template>
      <!-- 规格类型切换：切换单/多规格时重置 SKU 草稿（更新时为 SKU 集全量替换语义） -->
      <template #form-specType="{ form }">
        <el-radio-group v-model="form.specType" @change="onSpecTypeChange(form)">
          <el-radio :value="0">单规格</el-radio>
          <el-radio :value="1">多规格</el-radio>
        </el-radio-group>
      </template>
      <template #form-marketPrice="{ form }">
        <el-input v-model="form.marketPrice" clearable placeholder="0.00（元，选填）" />
      </template>
      <template #form-sliderPicUrls="{ form }">
        <el-input
          :model-value="joinLines(form.sliderPicUrls)"
          type="textarea"
          :rows="3"
          placeholder="轮播图 URL，每行一个（选填）"
          @update:model-value="form.sliderPicUrls = splitLines($event)"
        />
      </template>
      <!-- SKU 编辑器：单规格一行（价格/库存/图）；多规格行内引用属性值下拉 -->
      <template #form-skus="{ form }">
        <template v-if="form.specType === 0">
          <el-table :data="ensureSkus(form)" border size="small" class="line-table">
            <el-table-column label="售价（元）" min-width="130">
              <template #default="{ row }">
                <el-input v-model="row.price" clearable placeholder="0.00" />
              </template>
            </el-table-column>
            <el-table-column label="划线价（元）" min-width="130">
              <template #default="{ row }">
                <el-input v-model="row.marketPrice" clearable placeholder="0.00（选填）" />
              </template>
            </el-table-column>
            <el-table-column label="规格图 URL" min-width="180">
              <template #default="{ row }">
                <el-input v-model="row.picUrl" clearable placeholder="选填，缺省用主图" />
              </template>
            </el-table-column>
            <el-table-column label="库存" width="150">
              <template #default="{ row }">
                <el-input-number
                  v-model="row.stock"
                  :min="0"
                  :step="1"
                  :precision="0"
                  controls-position="right"
                  style="width: 100%"
                />
              </template>
            </el-table-column>
          </el-table>
        </template>
        <template v-else>
          <el-table :data="ensureSkus(form)" border size="small" class="line-table">
            <el-table-column label="规格（属性 → 值）" min-width="260">
              <template #default="{ row }">
                <div v-for="(p, pIdx) in row.properties" :key="pIdx" class="prop-row">
                  <el-select
                    v-model="p.propertyId"
                    filterable
                    placeholder="属性"
                    style="width: 130px"
                    @change="onPropertyChange(p)"
                  >
                    <el-option v-for="prop in properties" :key="prop.id" :label="prop.name" :value="prop.id" />
                  </el-select>
                  <el-select v-model="p.valueId" filterable placeholder="属性值" style="width: 130px">
                    <el-option
                      v-for="v in valueOptions(p)"
                      :key="String(v.id)"
                      :label="v.name"
                      :value="v.id"
                    />
                  </el-select>
                  <el-button link type="danger" @click="row.properties.splice(pIdx, 1)">删除</el-button>
                </div>
                <el-button size="small" @click="row.properties.push({ propertyId: undefined, valueId: undefined })">
                  添加规格项
                </el-button>
              </template>
            </el-table-column>
            <el-table-column label="售价（元）" min-width="120">
              <template #default="{ row }">
                <el-input v-model="row.price" clearable placeholder="0.00" />
              </template>
            </el-table-column>
            <el-table-column label="划线价（元）" min-width="120">
              <template #default="{ row }">
                <el-input v-model="row.marketPrice" clearable placeholder="0.00" />
              </template>
            </el-table-column>
            <el-table-column label="规格图 URL" min-width="150">
              <template #default="{ row }">
                <el-input v-model="row.picUrl" clearable placeholder="选填" />
              </template>
            </el-table-column>
            <el-table-column label="库存" width="140">
              <template #default="{ row }">
                <el-input-number
                  v-model="row.stock"
                  :min="0"
                  :step="1"
                  :precision="0"
                  controls-position="right"
                  style="width: 100%"
                />
              </template>
            </el-table-column>
            <el-table-column label="主图" width="70">
              <template #default="{ row }">
                <el-radio
                  :model-value="!!row.first"
                  :value="true"
                  @update:model-value="markFirst(form, row)"
                >
                  <span />
                </el-radio>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="70">
              <template #default="{ $index }">
                <el-button link type="danger" @click="form.skus.splice($index, 1)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div class="line-footer">
            <el-button size="small" @click="addSku(form)">添加 SKU</el-button>
            <span class="line-tip">规格名称以属性值主数据为准（服务端写定快照）；主图 SKU 用于商品展示</span>
          </div>
        </template>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime } from '@/utils/format'
import {
  pageProductSPUs,
  getProductSPU,
  createProductSPU,
  updateProductSPU,
  updateProductSPUStatus,
  deleteProductSPU,
  treeProductCategories,
  pageBrands,
  pageProperties,
  listPropertyValues,
} from '@/api/mall'
import { useUserStore } from '@/stores/user'

// 商品 SPU/SKU 页（issue #177，spec §3.2/§4）：ProTable 列表 + 表单弹窗
//（单/多规格切换编辑器，规格行引用属性值下拉；照 crm 合同页行项目形状）。
// 金额一律十进制字符串（Money 口径）；SKU 规格名称快照由后端按属性主数据
// 反查写定（前端只传 propertyId/valueId）；上下架是 T5 起的购买边界。

const userStore = useUserStore()
const canUpdate = computed(() => userStore.hasPerm('mall:spu:update'))

const tableRef = ref()
const categoryTree = ref<any[]>([])
const categoryFlat = ref<{ id: number; label: string }[]>([])
const brands = ref<any[]>([])
const properties = ref<any[]>([])
const valueCache = ref<Record<number, any[]>>({})
const brandOptions = computed(() => brands.value.map((b) => ({ label: b.name, value: b.id })))

function categoryName(id: number) {
  return categoryFlat.value.find((c) => c.id === id)?.label || '-'
}
function brandName(id: number) {
  if (!id) return '-'
  return brands.value.find((b) => b.id === id)?.name || '-'
}

// 搜索项用 computed 包装：分类/品牌下拉随主数据加载就绪（ref 嵌套在对象里
// 不会被模板解包，直接传 ref 会导致 v-for 迭代 Ref 本体）。
const searchItems = computed(() => [
  { prop: 'name', label: '商品名称' },
  { prop: 'categoryId', label: '分类', type: 'select', options: categoryFlat.value },
  { prop: 'brandId', label: '品牌', type: 'select', options: brandOptions.value },
  {
    prop: 'status',
    label: '状态',
    type: 'select',
    options: [
      { label: '下架', value: 0 },
      { label: '上架', value: 1 },
    ],
  },
])

const columns = [
  { prop: 'picUrl', label: '主图', width: 90, slot: true },
  { prop: 'name', label: '商品名称', minWidth: 180 },
  { prop: 'categoryName', label: '分类', minWidth: 130, slot: true },
  { prop: 'brandName', label: '品牌', minWidth: 110, slot: true },
  { prop: 'specType', label: '规格', width: 90, slot: true },
  { prop: 'marketPrice', label: '市场价（元）', width: 120, slot: true },
  { prop: 'status', label: '状态', width: 80, slot: true },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, slot: true },
]

const formItems = [
  { prop: 'categoryId', label: '分类', slot: true, required: true },
  { prop: 'brandId', label: '品牌', slot: true },
  { prop: 'name', label: '商品名称', required: true },
  { prop: 'specType', label: '规格类型', slot: true },
  { prop: 'marketPrice', label: '市场价', slot: true },
  { prop: 'picUrl', label: '主图 URL' },
  { prop: 'sliderPicUrls', label: '轮播图', slot: true },
  { prop: 'detail', label: '图文详情', type: 'textarea' },
  { prop: 'skus', label: '规格 / SKU', slot: true },
]

const defaultForm = { specType: 0 }

const api = {
  page: pageProductSPUs,
  detail: getProductSPU,
  create: createProductSPU,
  update: updateProductSPU,
  remove: deleteProductSPU,
}

// ---------- SKU 草稿 ----------
function emptySku() {
  return { properties: [] as any[], price: '0.00', marketPrice: '', picUrl: '', stock: 0, first: false }
}

// 懒初始化（模板绑定调用，幂等）：create 打开时 skus 未定义，给一行空 SKU。
function ensureSkus(form: any) {
  if (!Array.isArray(form.skus) || form.skus.length === 0) {
    form.skus = [emptySku()]
    if (form.specType === 0) form.skus[0].first = true
  }
  return form.skus
}

function addSku(form: any) {
  ensureSkus(form)
  form.skus.push(emptySku())
}

function onSpecTypeChange(form: any) {
  form.skus = [emptySku()]
  if (form.specType === 0) form.skus[0].first = true
}

function markFirst(form: any, row: any) {
  for (const sku of form.skus || []) sku.first = sku === row
  row.first = true
}

// ---------- 规格下拉：属性清单全量 + 属性值按需懒加载（缓存） ----------
async function ensureValues(propertyId: number) {
  if (!propertyId || valueCache.value[propertyId]) return
  valueCache.value[propertyId] = (await listPropertyValues(propertyId)) || []
}

function onPropertyChange(p: any) {
  p.valueId = undefined
  if (p.propertyId) void ensureValues(p.propertyId)
}

// 属性值下拉候选：优先缓存清单；编辑回显时缓存未就绪则回落规格快照里的
// 当前值（快照带 valueName），保证显示不退化为裸 ID。
function valueOptions(p: any) {
  const cached = p.propertyId ? valueCache.value[p.propertyId] : undefined
  if (cached?.length) return cached
  if (p.valueId && (p as any).valueName) return [{ id: p.valueId, name: (p as any).valueName }]
  return []
}

// ---------- 提交前规整（照 crm 合同页 normalizeSubmit 形状） ----------
function joinLines(urls: any): string {
  return Array.isArray(urls) ? urls.filter(Boolean).join('\n') : ''
}
function splitLines(text: string): string[] {
  return String(text || '')
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
}

function normalizeSubmit(payload: any, mode: 'create' | 'update') {
  const out: any = { ...payload }
  // 上下架只走独立端点（PUT :id/status）；表单不携带状态位。
  delete out.status
  if (out.marketPrice === '' || out.marketPrice == null) delete out.marketPrice
  if (out.brandId === '' || out.brandId == null) out.brandId = 0
  const specType = Number(out.specType) === 1 ? 1 : 0
  out.specType = specType
  // SKU 集全量替换：裁剪为最小结构（规格只留 ID 引用；快照名称服务端写定）。
  const skus = Array.isArray(out.skus) ? out.skus : []
  if (specType === 0) {
    const single = skus[0] || emptySku()
    out.skus = [
      {
        price: single.price || '0.00',
        ...(single.marketPrice ? { marketPrice: single.marketPrice } : {}),
        picUrl: single.picUrl || '',
        stock: Number(single.stock) || 0,
      },
    ]
  } else {
    out.skus = skus
      .filter((s: any) => Array.isArray(s.properties) && s.properties.length > 0)
      .map((s: any) => ({
        properties: s.properties
          .filter((p: any) => p.propertyId && p.valueId)
          .map((p: any) => ({ propertyId: p.propertyId, valueId: p.valueId })),
        price: s.price || '0.00',
        ...(s.marketPrice ? { marketPrice: s.marketPrice } : {}),
        picUrl: s.picUrl || '',
        stock: Number(s.stock) || 0,
        first: !!s.first,
      }))
  }
  if (mode === 'update') out.id = payload.id
  return out
}

// ---------- 上下架（PUT :id/status，独立端点） ----------
async function toggleStatus(row: any) {
  const to = row.status === 1 ? 0 : 1
  const action = to === 1 ? '上架' : '下架'
  const confirmed = await ElMessageBox.confirm(`确认${action}商品「${row.name}」？`, `${action}商品`, {
    type: 'warning',
  }).catch(() => false)
  if (!confirmed) return
  await updateProductSPUStatus(row.id, to)
  ElMessage.success(`已${action}`)
  tableRef.value?.reload()
}

onMounted(async () => {
  // 分类树（叶子校验在后端 400 提示）与品牌/属性清单：租户内全量（MVP
  // 规模可控，取前 200 条）；属性值按需懒加载（ensureValues）。
  const [tree, brandPage, propPage] = await Promise.all([
    treeProductCategories(),
    pageBrands({ pageNo: 1, pageSize: 200 }),
    pageProperties({ pageNo: 1, pageSize: 200 }),
  ])
  categoryTree.value = tree || []
  const flat: { id: number; label: string }[] = []
  for (const root of categoryTree.value) {
    flat.push({ id: root.id, label: root.name })
    for (const child of root.children || []) {
      flat.push({ id: child.id, label: `${root.name} / ${child.name}` })
    }
  }
  categoryFlat.value = flat
  brands.value = brandPage.list || []
  properties.value = propPage.list || []
})
</script>

<style scoped>
.line-table {
  width: 100%;
}
.prop-row {
  display: flex;
  gap: 6px;
  align-items: center;
  margin-bottom: 6px;
}
.line-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
}
.line-tip {
  color: #909399;
  font-size: 12px;
}
</style>
