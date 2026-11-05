<template>
  <div class="app-container">
    <el-row :gutter="16">
      <el-col :xs="24" :sm="6" :md="4">
        <el-card shadow="never" class="dept-card">
          <template #header><span class="card-title">部门</span></template>
          <el-tree
            :data="deptTree"
            :props="treeProps"
            node-key="id"
            default-expand-all
            highlight-current
            @node-click="handleDeptClick"
          />
          <el-button v-if="extraQuery.deptId" link type="primary" class="clear-btn" @click="clearDept">
            清除部门筛选
          </el-button>
        </el-card>
      </el-col>

      <el-col :xs="24" :sm="18" :md="20">
        <ProTable
          ref="tableRef"
          :columns="columns"
          :form-items="formItems"
          :search-items="searchItems"
          :api="api"
          :extra-query="extraQuery"
          :default-form="defaultForm"
          perm-prefix="system:user"
          title="用户"
          :action-width="230"
        >
          <template #cell-status="{ row }">
            <el-switch :model-value="row.status === 0" @change="toggleStatus(row)" />
          </template>
          <template #actions="{ row }">
            <el-button link type="warning" @click="resetPassword(row)">重置密码</el-button>
          </template>
          <template #form-deptId="{ form }">
            <el-tree-select
              v-model="form.deptId"
              :data="deptTree"
              :props="treeProps"
              check-strictly
              clearable
              style="width: 100%"
              placeholder="请选择部门"
            />
          </template>
          <template #form-roleIds="{ form }">
            <el-select v-model="form.roleIds" multiple clearable style="width: 100%" placeholder="请选择角色">
              <el-option v-for="role in roles" :key="role.id" :label="role.name" :value="role.id" />
            </el-select>
          </template>
          <template #form-postIds="{ form }">
            <el-select v-model="form.postIds" multiple clearable style="width: 100%" placeholder="请选择岗位">
              <el-option v-for="post in posts" :key="post.id" :label="post.name" :value="post.id" />
            </el-select>
          </template>
        </ProTable>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageUsers,
  getUser,
  createUser,
  updateUser,
  deleteUser,
  updateUserStatus,
  resetUserPassword,
  treeDepts,
  listSimpleRoles,
  listSimplePosts,
} from '@/api/system'

const userStore = useUserStore()
const tableRef = ref()
const deptTree = ref<any[]>([])
const roles = ref<any[]>([])
const posts = ref<any[]>([])
const extraQuery = reactive<{ deptId?: number }>({})
const treeProps = { label: 'name', children: 'children' }

const genderOptions = [
  { label: '未知', value: 0 },
  { label: '男', value: 1 },
  { label: '女', value: 2 },
]

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'username', label: '用户名', minWidth: 120 },
  { prop: 'nickname', label: '昵称', minWidth: 120 },
  { prop: 'deptName', label: '部门', minWidth: 120 },
  { prop: 'mobile', label: '手机号', minWidth: 130 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'username', label: '用户名' },
  { prop: 'nickname', label: '昵称' },
  { prop: 'mobile', label: '手机号' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'username', label: '用户名', requiredOn: 'create', visibleOn: 'create' },
  { prop: 'password', label: '密码', type: 'password', requiredOn: 'create', visibleOn: 'create' },
  { prop: 'nickname', label: '昵称', required: true },
  { prop: 'deptId', label: '部门', slot: true },
  { prop: 'roleIds', label: '角色', slot: true },
  { prop: 'postIds', label: '岗位', slot: true },
  { prop: 'mobile', label: '手机号' },
  { prop: 'email', label: '邮箱' },
  { prop: 'gender', label: '性别', type: 'select', options: genderOptions },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = { gender: 0, status: 0, deptId: undefined, roleIds: [], postIds: [] }

const api = {
  page: pageUsers,
  detail: getUser,
  create: createUser,
  update: updateUser,
  remove: deleteUser,
}

async function toggleStatus(row: any) {
  const next = row.status === 0 ? 1 : 0
  await updateUserStatus({ id: row.id, status: next })
  row.status = next
  ElMessage.success(next === 0 ? '已启用' : '已停用')
}

async function resetPassword(row: any) {
  const result = await ElMessageBox.prompt('请输入新密码（6-32 位）', `重置「${row.username}」的密码`, {
    inputType: 'password',
    inputValidator: (value: string) => (value && value.length >= 6 ? true : '密码至少 6 位'),
  })
  await resetUserPassword({ id: row.id, password: result.value })
  ElMessage.success('密码已重置')
}

function handleDeptClick(node: any) {
  extraQuery.deptId = node.id
}

function clearDept() {
  extraQuery.deptId = undefined
}

onMounted(async () => {
  // 简表接口已按权限码收口：无权限时不发请求，下拉保持为空，
  // 避免自定义角色进入用户页时看到 403 报错（普通角色由 seed 绑定三条简表权限）。
  const [depts, roleList, postList] = await Promise.all([
    treeDepts(),
    userStore.hasPerm('system:role:simple-list') ? listSimpleRoles() : Promise.resolve([]),
    userStore.hasPerm('system:post:simple-list') ? listSimplePosts() : Promise.resolve([]),
  ])
  deptTree.value = depts
  roles.value = roleList
  posts.value = postList
})
</script>

<style scoped>
.dept-card {
  min-height: 480px;
}

.card-title {
  font-weight: 600;
}

.clear-btn {
  margin-top: 12px;
}
</style>
