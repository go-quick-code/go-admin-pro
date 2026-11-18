<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      perm-prefix="mall:property"
      title="商品属性"
      dialog-width="480px"
    >
      <template #actions="{ row }">
        <el-button v-if="canQuery" link type="primary" @click.stop="openValues(row)">属性值</el-button>
      </template>
    </ProTable>

    <!-- 属性值抽屉：属性名子资源 CRUD（同属性下值唯一，后端 409）。 -->
    <el-drawer v-model="valuesVisible" :title="`属性值：${current?.name || ''}`" size="520px">
      <div style="margin-bottom: 12px">
        <el-input
          v-if="canCreate"
          v-model="newValue"
          placeholder="新属性值，如 红色（属性内唯一）"
          style="width: 65%"
          @keyup.enter="addValue"
        />
        <el-button v-if="canCreate" type="primary" style="margin-left: 8px" @click="addValue">新增</el-button>
      </div>
      <el-table :data="values" border stripe v-loading="valuesLoading">
        <el-table-column label="ID" prop="id" width="70" />
        <el-table-column label="属性值" prop="name" min-width="120" />
        <el-table-column label="操作" width="130" v-if="canUpdate || canDelete">
          <template #default="{ row }">
            <el-button v-if="canUpdate" link type="primary" @click="openEditValue(row)">修改</el-button>
            <el-button v-if="canDelete" link type="danger" @click="removeValue(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!values.length && !valuesLoading" description="暂无属性值" :image-size="80" />
    </el-drawer>

    <!-- 属性值改名弹窗 -->
    <el-dialog v-model="editVisible" title="修改属性值" width="420px" append-to-body>
      <el-input v-model="editName" placeholder="属性值名称（属性内唯一）" @keyup.enter="saveValue" />
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" @click="saveValue">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime } from '@/utils/format'
import { useUserStore } from '@/stores/user'
import {
  pageProperties,
  createProperty,
  updateProperty,
  deleteProperty,
  listPropertyValues,
  createPropertyValue,
  updatePropertyValue,
  deletePropertyValue,
} from '@/api/mall'

// 商品属性页（issue #176，spec §3.2/§4）：属性名 ProTable + 属性值抽屉管理。
// 属性名 name 租户内唯一（后端 409）；属性值 (propertyId, name) 唯一（后端
// 409）；删除属性名级联删值（后端同事务）。SPU/SKU 的多规格 JSON 引用随
// T4 落地，与本页主数据解耦。

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('mall:property:query'))
const canCreate = computed(() => userStore.hasPerm('mall:property:create'))
const canUpdate = computed(() => userStore.hasPerm('mall:property:update'))
const canDelete = computed(() => userStore.hasPerm('mall:property:delete'))

const tableRef = ref<InstanceType<typeof ProTable> | null>(null)

const columns = [
  { prop: 'name', label: '属性名', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [{ prop: 'name', label: '属性名' }]

const formItems = [{ prop: 'name', label: '属性名', required: true, placeholder: '租户内唯一，如 颜色' }]

const api = { page: pageProperties, create: createProperty, update: updateProperty, remove: deleteProperty }

// ---------- 属性值抽屉 ----------

const valuesVisible = ref(false)
const valuesLoading = ref(false)
const values = ref<any[]>([])
const current = ref<any>(null)
const newValue = ref('')
const editVisible = ref(false)
const editName = ref('')
const editing = ref<any>(null)

async function openValues(row: any) {
  current.value = row
  valuesVisible.value = true
  await loadValues()
}

async function loadValues() {
  if (!current.value) return
  valuesLoading.value = true
  try {
    values.value = (await listPropertyValues(current.value.id)) || []
  } finally {
    valuesLoading.value = false
  }
}

async function addValue() {
  const name = newValue.value.trim()
  if (!name) {
    ElMessage.warning('请输入属性值名称')
    return
  }
  await createPropertyValue({ propertyId: current.value.id, name })
  ElMessage.success('已新增')
  newValue.value = ''
  await loadValues()
}

function openEditValue(row: any) {
  editing.value = row
  editName.value = row.name
  editVisible.value = true
}

async function saveValue() {
  const name = editName.value.trim()
  if (!name) {
    ElMessage.warning('请输入属性值名称')
    return
  }
  await updatePropertyValue({ id: editing.value.id, name })
  ElMessage.success('已保存')
  editVisible.value = false
  await loadValues()
}

async function removeValue(row: any) {
  await deletePropertyValue(row.id)
  ElMessage.success('已删除')
  await loadValues()
}
</script>
