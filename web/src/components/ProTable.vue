<template>
  <div class="pro-table">
    <el-form v-if="searchItems.length" :inline="true" :model="query" class="search-form">
      <el-form-item v-for="item in searchItems" :key="item.prop" :label="item.label">
        <el-select
          v-if="item.type === 'select'"
          v-model="query[item.prop]"
          clearable
          :placeholder="`请选择${item.label}`"
          style="width: 160px"
        >
          <el-option
            v-for="opt in item.options || []"
            :key="String(opt.value)"
            :label="opt.label"
            :value="opt.value"
          />
        </el-select>
        <el-input
          v-else
          v-model="query[item.prop]"
          clearable
          :placeholder="`请输入${item.label}`"
          style="width: 180px"
          @keyup.enter="handleSearch"
        />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="handleSearch">查询</el-button>
        <el-button @click="handleReset">重置</el-button>
      </el-form-item>
    </el-form>

    <div v-if="showToolbar" class="toolbar">
      <el-button v-if="api.create && canCreate" type="primary" @click="openCreate">新增</el-button>
      <slot name="toolbar" />
    </div>

    <el-table
      v-loading="loading"
      :data="list"
      border
      stripe
      highlight-current-row
      :row-key="rowKey"
      :tree-props="{ children: 'children' }"
      :default-expand-all="defaultExpandAll"
      @row-click="handleRowClick"
    >
      <el-table-column
        v-for="col in columns"
        :key="col.prop"
        :prop="col.prop"
        :label="col.label"
        :width="col.width"
        :min-width="col.minWidth || 120"
        show-overflow-tooltip
      >
        <template #default="scope">
          <slot v-if="col.slot" :name="`cell-${col.prop}`" v-bind="scope" />
          <span v-else>{{ displayCell(col, scope.row) }}</span>
        </template>
      </el-table-column>

      <el-table-column v-if="showActions" label="操作" :width="actionWidth" fixed="right">
        <template #default="scope">
          <slot name="actions" v-bind="scope" />
          <el-button v-if="api.update && canUpdate" link type="primary" @click="openEdit(scope.row)">
            编辑
          </el-button>
          <el-button v-if="api.remove && canDelete" link type="danger" @click="handleDelete(scope.row)">
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      v-if="api.page"
      v-model:current-page="query.pageNo"
      v-model:page-size="query.pageSize"
      :total="total"
      :page-sizes="[10, 20, 50, 100]"
      layout="total, sizes, prev, pager, next"
      class="pager"
      @size-change="load"
      @current-change="load"
    />

    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      :width="dialogWidth"
      destroy-on-close
      append-to-body
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item
          v-for="item in visibleFormItems"
          :key="item.prop"
          :label="item.label"
          :prop="item.prop"
        >
          <slot v-if="item.slot" :name="`form-${item.prop}`" :form="form" />
          <el-input
            v-else-if="!item.type || item.type === 'input'"
            v-model="form[item.prop]"
            clearable
            :placeholder="`请输入${item.label}`"
          />
          <el-input
            v-else-if="item.type === 'password'"
            v-model="form[item.prop]"
            type="password"
            show-password
            :placeholder="`请输入${item.label}`"
          />
          <el-input
            v-else-if="item.type === 'textarea'"
            v-model="form[item.prop]"
            type="textarea"
            :rows="3"
            :placeholder="`请输入${item.label}`"
          />
          <el-input-number
            v-else-if="item.type === 'number'"
            v-model="form[item.prop]"
            :min="0"
            style="width: 100%"
          />
          <el-select
            v-else-if="item.type === 'select'"
            v-model="form[item.prop]"
            clearable
            style="width: 100%"
            :placeholder="`请选择${item.label}`"
          >
            <el-option
              v-for="opt in item.options || []"
              :key="String(opt.value)"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
          <el-select
            v-else-if="item.type === 'multiple'"
            v-model="form[item.prop]"
            multiple
            clearable
            style="width: 100%"
            :placeholder="`请选择${item.label}`"
          >
            <el-option
              v-for="opt in item.options || []"
              :key="String(opt.value)"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
          <el-switch v-else-if="item.type === 'switch'" v-model="form[item.prop]" />
          <el-radio-group v-else-if="item.type === 'radio'" v-model="form[item.prop]">
            <el-radio v-for="opt in item.options || []" :key="String(opt.value)" :value="opt.value">
              {{ opt.label }}
            </el-radio>
          </el-radio-group>
          <el-date-picker
            v-else-if="item.type === 'datetime'"
            v-model="form[item.prop]"
            type="datetime"
            value-format="YYYY-MM-DD HH:mm:ss"
            style="width: 100%"
          />
          <el-tree-select
            v-else-if="item.type === 'tree-select'"
            v-model="form[item.prop]"
            :data="item.treeData || []"
            :props="item.treeProps || { label: 'name', value: 'id', children: 'children' }"
            check-strictly
            clearable
            style="width: 100%"
            :placeholder="`请选择${item.label}`"
          />
          <span v-else>{{ form[item.prop] }}</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance } from 'element-plus'
import { useUserStore } from '@/stores/user'

interface CrudApi {
  page?: (params: any) => Promise<any>
  list?: (params?: any) => Promise<any>
  detail?: (id: any) => Promise<any>
  create?: (data: any) => Promise<any>
  update?: (data: any) => Promise<any>
  remove?: (id: any) => Promise<any>
}

const props = withDefaults(
  defineProps<{
    columns: any[]
    formItems?: any[]
    searchItems?: any[]
    api: CrudApi
    permPrefix?: string
    extraQuery?: Record<string, any>
    defaultForm?: Record<string, any>
    idKey?: string
    rowKey?: string
    dialogWidth?: string
    title?: string
    defaultExpandAll?: boolean
    showToolbar?: boolean
    showActions?: boolean
    actionWidth?: number | string
    beforeSubmit?: (payload: any, mode: 'create' | 'update') => any
  }>(),
  {
    formItems: () => [],
    searchItems: () => [],
    permPrefix: '',
    extraQuery: () => ({}),
    defaultForm: () => ({}),
    idKey: 'id',
    rowKey: 'id',
    dialogWidth: '620px',
    title: '',
    defaultExpandAll: false,
    showToolbar: true,
    showActions: true,
    actionWidth: 170,
  },
)

const emit = defineEmits<{ success: []; rowClick: [row: any] }>()

const userStore = useUserStore()
const list = ref<any[]>([])
const total = ref(0)
const loading = ref(false)
const submitting = ref(false)
const dialogVisible = ref(false)
const dialogTitle = ref('新增')
const mode = ref<'create' | 'update'>('create')
const formRef = ref<FormInstance>()
const form = reactive<any>({})
const rules = ref<Record<string, any>>({})

const query = reactive<any>({ pageNo: 1, pageSize: 10 })

const canCreate = computed(() => !props.permPrefix || userStore.hasPerm(`${props.permPrefix}:create`))
const canUpdate = computed(() => !props.permPrefix || userStore.hasPerm(`${props.permPrefix}:update`))
const canDelete = computed(() => !props.permPrefix || userStore.hasPerm(`${props.permPrefix}:delete`))

const visibleFormItems = computed(() =>
  props.formItems.filter((item: any) => {
    if (!item.visibleOn) return true
    return item.visibleOn === mode.value
  }),
)

function buildRules(currentMode: 'create' | 'update') {
  const result: Record<string, any> = {}
  for (const item of props.formItems) {
    if (item.visibleOn && item.visibleOn !== currentMode) continue
    let required = !!item.required
    if (item.requiredOn) required = item.requiredOn === currentMode
    if (!required) continue
    result[item.prop] = [
      {
        required: true,
        message:
          item.type === 'select' || item.type === 'multiple' || item.type === 'tree-select'
            ? `请选择${item.label}`
            : `请输入${item.label}`,
        trigger: item.type === 'select' || item.type === 'tree-select' ? 'change' : 'blur',
      },
    ]
  }
  return result
}

function defaultValues() {
  const data: Record<string, any> = {}
  for (const item of props.formItems) {
    if (item.slot) data[item.prop] = undefined
    else if (item.type === 'multiple') data[item.prop] = []
    else if (item.type === 'switch') data[item.prop] = true
    else if (item.type === 'number') data[item.prop] = 0
    else if (item.type === 'radio' || item.type === 'select' || item.type === 'tree-select')
      data[item.prop] = undefined
    else data[item.prop] = ''
  }
  return { ...data, ...props.defaultForm }
}

function handleRowClick(row: any) {
  emit('rowClick', row)
}

function displayCell(col: any, row: any) {
  if (typeof col.formatter === 'function') return col.formatter(row)
  const value = row[col.prop]
  if (value === null || value === undefined || value === '') return '-'
  return value
}

function requestParams() {
  return { ...query, ...props.extraQuery }
}

async function load() {
  loading.value = true
  try {
    if (props.api.page) {
      const data = await props.api.page(requestParams())
      list.value = data.list || []
      total.value = data.total || 0
    } else if (props.api.list) {
      const data = await props.api.list(requestParams())
      list.value = Array.isArray(data) ? data : data.list || []
      total.value = list.value.length
    }
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  query.pageNo = 1
  load()
}

function handleReset() {
  for (const item of props.searchItems) {
    query[item.prop] = item.type === 'select' ? undefined : ''
  }
  query.pageNo = 1
  load()
}

function openCreate() {
  mode.value = 'create'
  dialogTitle.value = `新增${props.title}`
  Object.keys(form).forEach((key) => delete form[key])
  Object.assign(form, defaultValues())
  rules.value = buildRules('create')
  dialogVisible.value = true
}

async function openEdit(row: any) {
  mode.value = 'update'
  dialogTitle.value = `修改${props.title}`
  let data = row
  if (props.api.detail) {
    data = await props.api.detail(row[props.idKey])
  }
  Object.keys(form).forEach((key) => delete form[key])
  Object.assign(form, defaultValues(), data)
  rules.value = buildRules('update')
  dialogVisible.value = true
}

async function handleSubmit() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  let payload: any = { ...form }
  if (props.beforeSubmit) {
    const replaced = await props.beforeSubmit(payload, mode.value)
    if (replaced) payload = replaced
  }

  submitting.value = true
  try {
    if (mode.value === 'create') {
      await props.api.create?.(payload)
      ElMessage.success('新增成功')
    } else {
      await props.api.update?.(payload)
      ElMessage.success('修改成功')
    }
    dialogVisible.value = false
    emit('success')
    load()
  } finally {
    submitting.value = false
  }
}

async function handleDelete(row: any) {
  await ElMessageBox.confirm(`确认删除该${props.title || '记录'}吗？`, '提示', { type: 'warning' })
  await props.api.remove?.(row[props.idKey])
  ElMessage.success('删除成功')
  load()
}

watch(
  () => props.extraQuery,
  () => {
    query.pageNo = 1
    load()
  },
  { deep: true },
)

onMounted(load)

// query/openEdit 暴露给父页面：query 供 Excel 导出复用当前列表筛选（issue #20）；
// openEdit 供自定义操作列按状态打开编辑弹窗（issue #44：仅草稿可编辑，
// 发布/停用行的编辑入口由父页面按状态机隐藏）。
defineExpose({ reload: load, openCreate, openEdit, query })
</script>

<style scoped>
.pro-table {
  min-width: 0;
}
</style>
