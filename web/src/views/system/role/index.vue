<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="system:role"
      title="角色"
      dialog-width="640px"
    >
      <template #cell-dataScope="{ row }">
        <el-tag size="small" effect="plain">{{ dataScopeLabel(row.dataScope) }}</el-tag>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #cell-type="{ row }">
        <el-tag :type="row.type === 1 ? 'warning' : 'info'" size="small">
          {{ row.type === 1 ? '内置' : '自定义' }}
        </el-tag>
      </template>
      <template #form-deptIds="{ form }">
        <el-tree-select
          v-if="form.dataScope === 2"
          v-model="form.deptIds"
          :data="deptTree"
          :props="{ label: 'name', value: 'id', children: 'children' }"
          multiple
          check-strictly
          show-checkbox
          clearable
          default-expand-all
          style="width: 100%"
          placeholder="请选择可见部门（精确集合，不含子部门）"
        />
        <span v-else class="dept-hint">仅「自定义部门」范围需要选择部门</span>
      </template>
      <template #form-menuIds="{ form }">
        <div class="tree-wrap">
          <div class="tree-toolbar">
            <el-button link type="primary" @click="checkAllMenus(form)">全选</el-button>
            <el-button link type="primary" @click="clearAllMenus(form)">清空</el-button>
            <span class="text-muted tip">勾选父级不会自动勾选子级</span>
          </div>
          <el-tree
            ref="menuTreeRef"
            :data="menuTree"
            :props="treeProps"
            node-key="id"
            show-checkbox
            check-strictly
            default-expand-all
            :default-checked-keys="form.menuIds || []"
            class="menu-tree"
            @check="() => syncMenuIds(form)"
          />
        </div>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { dataScopeOptions, formatTime, statusOptions } from '@/utils/format'
import { pageRoles, getRole, createRole, updateRole, deleteRole, listMenus, treeDepts } from '@/api/system'

const menuTree = ref<any[]>([])
const menuTreeRef = ref()
const treeProps = { label: 'name', children: 'children' }
const deptTree = ref<any[]>([])

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'name', label: '角色名称', minWidth: 130 },
  { prop: 'code', label: '角色编码', minWidth: 130 },
  { prop: 'dataScope', label: '数据权限', minWidth: 130, slot: true },
  { prop: 'type', label: '类型', width: 90, slot: true },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'name', label: '角色名称' },
  { prop: 'code', label: '角色编码' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'name', label: '角色名称', required: true },
  { prop: 'code', label: '角色编码', required: true },
  { prop: 'sort', label: '排序', type: 'number' },
  { prop: 'dataScope', label: '数据权限', type: 'select', options: dataScopeOptions, required: true },
  { prop: 'deptIds', label: '自定义部门', slot: true },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
  { prop: 'menuIds', label: '菜单权限', slot: true },
]

const defaultForm = { sort: 0, dataScope: 1, status: 0, menuIds: [], deptIds: [] }

const api = {
  page: pageRoles,
  detail: getRole,
  create: createRole,
  update: updateRole,
  remove: deleteRole,
}

function dataScopeLabel(scope: number) {
  return dataScopeOptions.find((item) => item.value === scope)?.label || '-'
}

function syncMenuIds(form: any) {
  if (!menuTreeRef.value) return
  form.menuIds = menuTreeRef.value.getCheckedKeys()
}

function collectIds(nodes: any[]): number[] {
  const ids: number[] = []
  for (const node of nodes) {
    ids.push(node.id)
    if (node.children?.length) ids.push(...collectIds(node.children))
  }
  return ids
}

const allMenuIds = computed(() => collectIds(menuTree.value))

function checkAllMenus(form: any) {
  menuTreeRef.value?.setCheckedKeys(allMenuIds.value)
  syncMenuIds(form)
}

function clearAllMenus(form: any) {
  menuTreeRef.value?.setCheckedKeys([])
  form.menuIds = []
}

onMounted(async () => {
  menuTree.value = await listMenus()
  deptTree.value = await treeDepts()
})
</script>

<style scoped>
.menu-tree {
  width: 100%;
  max-height: 280px;
  overflow: auto;
  border: 1px solid #ebeef5;
  border-radius: 4px;
  padding: 8px;
}

.tree-toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 6px;
}

.tip {
  font-size: 12px;
  margin-left: 8px;
}

.dept-hint {
  font-size: 12px;
  color: #909399;
}
</style>
