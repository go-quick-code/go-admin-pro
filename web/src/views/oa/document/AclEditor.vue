<template>
  <div class="acl-editor">
    <div v-for="(row, index) in rows" :key="index" class="acl-row">
      <el-select v-model="row.targetType" class="type-select" @change="onTypeChange(row)">
        <el-option v-for="opt in targetTypeOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
      </el-select>

      <span v-if="row.targetType === 0" class="all-hint">租户内全部用户</span>

      <el-tree-select
        v-else-if="row.targetType === 1"
        v-model="row.targetId"
        :data="deptTree"
        :props="{ label: 'name', value: 'id', children: 'children' }"
        check-strictly
        clearable
        class="target-select"
        placeholder="选择部门（含子部门）"
        @change="emitChange"
      />

      <el-select
        v-else-if="row.targetType === 2"
        v-model="row.targetId"
        clearable
        filterable
        class="target-select"
        placeholder="选择角色"
        @change="emitChange"
      >
        <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
      </el-select>

      <el-select
        v-else
        v-model="row.targetId"
        clearable
        filterable
        class="target-select"
        placeholder="选择用户"
        @change="emitChange"
      >
        <el-option v-for="user in users" :key="user.id" :label="user.nickname" :value="user.id" />
      </el-select>

      <el-button link type="danger" @click="removeRow(index)">移除</el-button>
    </div>

    <div class="acl-footer">
      <el-button link type="primary" :disabled="rows.length >= 200" @click="addRow">添加 ACL 条目</el-button>
      <span class="hint">留空表示继承父目录；顶层目录必须至少一条</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { listSimpleRoles, listSimpleUsers, treeDepts } from '@/api/system'
import type { DocumentACLPayload } from '@/api/oa'

// AclEditor 是目录 ACL 的行编辑器（issue #58）：四类主体（全员/部门含子树/
// 角色/用户）逐行编辑；顶层目录创建时父组件负责校验非空，子目录留空表示
// 继承父目录。选项接口不可用时降级为空数组（后端仍会按 ID 校验）。
const props = defineProps<{ modelValue: DocumentACLPayload[] }>()
const emit = defineEmits<{ (e: 'update:modelValue', value: DocumentACLPayload[]): void }>()

const targetTypeOptions = [
  { value: 0, label: '全员' },
  { value: 1, label: '部门' },
  { value: 2, label: '角色' },
  { value: 3, label: '用户' },
]

const deptTree = ref<any[]>([])
const roles = ref<any[]>([])
const users = ref<any[]>([])
const rows = ref<DocumentACLPayload[]>([])

watch(
  () => props.modelValue,
  (value) => {
    rows.value = (value || []).map((item) => ({ targetType: item.targetType, targetId: item.targetId }))
  },
  { immediate: true, deep: true },
)

onMounted(async () => {
  try {
    deptTree.value = (await treeDepts()) || []
  } catch {
    deptTree.value = []
  }
  try {
    roles.value = (await listSimpleRoles()) || []
  } catch {
    roles.value = []
  }
  try {
    users.value = (await listSimpleUsers()) || []
  } catch {
    users.value = []
  }
})

function emitChange() {
  emit(
    'update:modelValue',
    rows.value.map((item) => ({ targetType: item.targetType, targetId: item.targetId })),
  )
}

function onTypeChange(row: DocumentACLPayload) {
  // 切换主体类型时清空目标：全员固定 0，其余等待重新选择。
  row.targetId = 0
  emitChange()
}

function addRow() {
  rows.value.push({ targetType: 0, targetId: 0 })
  emitChange()
}

function removeRow(index: number) {
  rows.value.splice(index, 1)
  emitChange()
}
</script>

<style scoped>
.acl-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.type-select {
  width: 110px;
  flex: none;
}
.target-select {
  width: 260px;
}
.all-hint {
  width: 260px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.acl-footer {
  display: flex;
  align-items: center;
  gap: 12px;
}
.hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
