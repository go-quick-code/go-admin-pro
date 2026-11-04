<template>
  <div class="app-container">
    <ProTable
      :columns="columns"
      :form-items="formItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="system:menu"
      title="菜单"
      :default-expand-all="true"
      :action-width="190"
      @success="loadMenuOptions"
    >
      <template #toolbar>
        <span class="tip">目录 / 菜单 / 按钮三级结构，按钮用于接口权限控制</span>
      </template>
      <template #cell-name="{ row }">
        <span class="menu-name">
          <el-icon v-if="row.icon"><component :is="row.icon" /></el-icon>
          {{ row.name }}
        </span>
      </template>
      <template #cell-type="{ row }">
        <el-tag :type="menuTagType(row.type)" size="small">{{ menuTypeLabel(row.type) }}</el-tag>
      </template>
      <template #cell-visible="{ row }">
        <el-tag :type="row.visible ? 'success' : 'info'" size="small">{{ row.visible ? '显示' : '隐藏' }}</el-tag>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
      <template #form-parentId="{ form }">
        <el-tree-select
          v-model="form.parentId"
          :data="parentOptions"
          :props="{ label: 'name', value: 'id', children: 'children' }"
          check-strictly
          clearable
          style="width: 100%"
          placeholder="不选则为顶级菜单"
        />
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { menuTypeOptions, statusOptions } from '@/utils/format'
import { listMenus, getMenu, createMenu, updateMenu, deleteMenu } from '@/api/system'

const parentOptions = ref<any[]>([])

const columns = [
  { prop: 'name', label: '菜单名称', minWidth: 180, slot: true },
  { prop: 'type', label: '类型', width: 90, slot: true },
  { prop: 'permission', label: '权限标识', minWidth: 170 },
  { prop: 'path', label: '路由地址', minWidth: 120 },
  { prop: 'component', label: '组件路径', minWidth: 160 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'visible', label: '显示', width: 80, slot: true },
  { prop: 'status', label: '状态', width: 80, slot: true },
]

const formItems = [
  { prop: 'parentId', label: '上级菜单', slot: true },
  { prop: 'type', label: '类型', type: 'radio', options: menuTypeOptions, required: true },
  { prop: 'name', label: '菜单名称', required: true },
  { prop: 'icon', label: '图标', placeholder: 'Element Plus 图标名，如 User' },
  { prop: 'permission', label: '权限标识', placeholder: '如 system:user:query' },
  { prop: 'path', label: '路由地址', placeholder: '目录/菜单必填，如 /system 或 user' },
  { prop: 'component', label: '组件路径', placeholder: '如 system/user/index' },
  { prop: 'componentName', label: '组件名称', placeholder: '可选，用于 keep-alive' },
  { prop: 'sort', label: '排序', type: 'number' },
  { prop: 'visible', label: '是否显示', type: 'switch' },
  { prop: 'keepAlive', label: '是否缓存', type: 'switch' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
]

const defaultForm = {
  parentId: 0,
  type: 2,
  sort: 0,
  visible: true,
  keepAlive: true,
  status: 0,
}

const api = {
  list: listMenus,
  detail: getMenu,
  create: createMenu,
  update: updateMenu,
  remove: deleteMenu,
}

function menuTypeLabel(type: number) {
  return menuTypeOptions.find((item) => item.value === type)?.label || '-'
}

function menuTagType(type: number) {
  if (type === 1) return 'warning'
  if (type === 2) return 'primary'
  return 'info'
}

function filterButtons(nodes: any[]): any[] {
  return nodes
    .filter((node) => node.type !== 3)
    .map((node) => ({
      ...node,
      children: node.children?.length ? filterButtons(node.children) : undefined,
    }))
}

async function loadMenuOptions() {
  const data = await listMenus()
  parentOptions.value = filterButtons(data)
}

onMounted(loadMenuOptions)
</script>

<style scoped>
.menu-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.tip {
  color: #909399;
  font-size: 12px;
  margin-left: 8px;
}
</style>
