<template>
  <div class="app-container">
    <el-tabs v-model="activeTab">
      <!-- 客户端是平台级资源，仅平台租户展示（后端另有 403 收口） -->
      <el-tab-pane v-if="isPlatformTenant" label="社交客户端" name="clients">
        <ProTable
          ref="clientTableRef"
          :columns="clientColumns"
          :form-items="clientFormItems"
          :search-items="clientSearchItems"
          :api="clientApi"
          :default-form="clientDefaultForm"
          perm-prefix="system:social"
          title="客户端"
          dialog-width="680px"
        >
          <template #toolbar>
            <span class="tip">配置第三方 OAuth2 客户端；密钥查询脱敏，提交 ****** 表示保留原值</span>
          </template>
          <template #cell-status="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'info'">
              {{ row.status === 0 ? '启用' : '停用' }}
            </el-tag>
          </template>
        </ProTable>
      </el-tab-pane>

      <el-tab-pane label="绑定用户" name="users">
        <ProTable
          ref="userTableRef"
          :columns="userColumns"
          :search-items="userSearchItems"
          :api="userApi"
          perm-prefix="system:social-user"
          title="绑定"
        >
          <template #toolbar>
            <span class="tip">租户内社交账号绑定关系；解绑后同一三方账号可重新绑定</span>
          </template>
          <template #actions="{ row }">
            <el-button v-if="canUnbind" link type="danger" @click="unbind(row)">解绑</el-button>
          </template>
        </ProTable>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import { useUserStore } from '@/stores/user'
import {
  pageSocialClients,
  createSocialClient,
  updateSocialClient,
  deleteSocialClient,
  pageSocialUsers,
  deleteSocialUser,
} from '@/api/system'

const userStore = useUserStore()
const activeTab = ref('clients')

// 平台租户（ID=1）才可管理客户端；userInfo 由 /system/auth/user-info 返回。
const isPlatformTenant = computed(() => (userStore.userInfo?.tenantId ?? 1) === 1)
const canUnbind = computed(() => userStore.hasPerm('system:social-user:delete'))

const providerOptions = [
  { label: '微信开放平台', value: 'wechat_open' },
  { label: '钉钉', value: 'dingtalk' },
  { label: '企业微信', value: 'wecom' },
]

const clientColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'code', label: '编码', minWidth: 120 },
  { prop: 'name', label: '名称', minWidth: 120 },
  { prop: 'provider', label: '社交平台', width: 120 },
  { prop: 'clientId', label: '客户端 ID', minWidth: 140 },
  { prop: 'redirectUri', label: '回调地址', minWidth: 180 },
  { prop: 'status', label: '状态', width: 80, slot: true },
  { prop: 'createTime', label: '创建时间', width: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const clientSearchItems = [
  { prop: 'code', label: '编码' },
  { prop: 'name', label: '名称' },
  { prop: 'provider', label: '社交平台', type: 'select', options: providerOptions },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const clientFormItems = [
  { prop: 'code', label: '客户端编码', required: true },
  { prop: 'name', label: '客户端名称', required: true },
  { prop: 'provider', label: '社交平台', type: 'select', options: providerOptions, required: true },
  { prop: 'clientId', label: '客户端 ID', required: true },
  { prop: 'clientSecret', label: '客户端密钥', type: 'password', requiredOn: 'create' },
  { prop: 'authorizeUrl', label: '授权端点' },
  { prop: 'tokenUrl', label: 'Token 端点' },
  { prop: 'userinfoUrl', label: '用户信息端点' },
  { prop: 'scopes', label: '授权 Scope' },
  { prop: 'redirectUri', label: '回调地址', required: true },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const clientDefaultForm = { status: 0 }

const clientApi = {
  page: pageSocialClients,
  create: createSocialClient,
  update: updateSocialClient,
  remove: deleteSocialClient,
}

const userColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'username', label: '本地用户', minWidth: 120 },
  { prop: 'provider', label: '社交平台', width: 120 },
  { prop: 'openid', label: 'OpenID', minWidth: 160 },
  { prop: 'unionid', label: 'UnionID', minWidth: 140 },
  { prop: 'nickname', label: '三方昵称', minWidth: 120 },
  { prop: 'lastLoginAt', label: '最近登录', width: 170, formatter: (row: any) => formatTime(row.lastLoginAt) },
  { prop: 'createTime', label: '绑定时间', width: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const userSearchItems = [
  { prop: 'provider', label: '社交平台', type: 'select', options: providerOptions },
]

const userApi = { page: pageSocialUsers }

const userTableRef = ref<InstanceType<typeof ProTable>>()

async function unbind(row: any) {
  await ElMessageBox.confirm(
    `确认解绑用户「${row.username || row.userId}」的 ${row.provider} 账号吗？`,
    '提示',
    { type: 'warning' },
  )
  await deleteSocialUser(row.id)
  ElMessage.success('已解绑')
  userTableRef.value?.reload()
}
</script>

<style scoped>
.tip {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
