<template>
  <div class="app-container">
    <el-row :gutter="16">
      <el-col :md="7" :xs="24">
        <el-card shadow="never" class="profile-card">
          <el-avatar :size="72" class="profile-avatar">
            {{ (userInfo?.nickname || userInfo?.username || 'U').slice(0, 1) }}
          </el-avatar>
          <div class="profile-name">{{ userInfo?.nickname || userInfo?.username }}</div>
          <div class="profile-account">@{{ userInfo?.username }}</div>
          <div class="profile-tags">
            <el-tag v-for="role in userStore.roles" :key="role" size="small" type="primary" effect="plain">
              {{ role }}
            </el-tag>
          </div>
          <el-divider />
          <div class="info-row"><span>所属部门</span><b>{{ userInfo?.deptName || '-' }}</b></div>
          <div class="info-row"><span>手机号</span><b>{{ userInfo?.mobile || '-' }}</b></div>
          <div class="info-row"><span>邮箱</span><b>{{ userInfo?.email || '-' }}</b></div>
          <div class="info-row"><span>租户</span><b>{{ userInfo?.tenantId }}</b></div>

          <!-- 当前租户配额（issue #71）：套餐限额与用量一目了然。 -->
          <el-divider />
          <div class="quota-title">
            租户配额
            <el-tag v-if="quota?.unlimited" type="success" size="small" effect="plain">不限</el-tag>
          </div>
          <template v-if="quota">
            <div class="quota-row">
              <span>用户数</span>
              <b>
                {{ quota.used.users }}
                <span v-if="!quota.unlimited && quota.limit.maxUsers > 0" class="quota-limit">/ {{ quota.limit.maxUsers }}</span>
              </b>
            </div>
            <div class="quota-row">
              <span>存储</span>
              <b>
                {{ formatSize(quota.used.storageBytes) }}
                <span v-if="!quota.unlimited && quota.limit.maxStorageMb > 0" class="quota-limit">/ {{ quota.limit.maxStorageMb }} MB</span>
              </b>
            </div>
            <div class="quota-row">
              <span>本月短信</span>
              <b>
                {{ quota.used.smsMonthly }}
                <span v-if="!quota.unlimited && quota.limit.maxSmsMonthly > 0" class="quota-limit">/ {{ quota.limit.maxSmsMonthly }}</span>
              </b>
            </div>
            <div class="quota-row">
              <span>本月邮件</span>
              <b>
                {{ quota.used.mailMonthly }}
                <span v-if="!quota.unlimited && quota.limit.maxMailMonthly > 0" class="quota-limit">/ {{ quota.limit.maxMailMonthly }}</span>
              </b>
            </div>
          </template>
        </el-card>
      </el-col>

      <el-col :md="17" :xs="24">
        <el-card shadow="never">
          <el-tabs v-model="activeTab">
            <el-tab-pane label="基本资料" name="basic">
              <el-form :model="profileForm" label-width="90px" class="form-narrow">
                <el-form-item label="昵称">
                  <el-input v-model="profileForm.nickname" />
                </el-form-item>
                <el-form-item label="手机号">
                  <el-input v-model="profileForm.mobile" />
                </el-form-item>
                <el-form-item label="邮箱">
                  <el-input v-model="profileForm.email" />
                </el-form-item>
                <el-form-item label="性别">
                  <el-radio-group v-model="profileForm.gender">
                    <el-radio :value="0">未知</el-radio>
                    <el-radio :value="1">男</el-radio>
                    <el-radio :value="2">女</el-radio>
                  </el-radio-group>
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" :loading="saving" @click="saveProfile">保存</el-button>
                </el-form-item>
              </el-form>
            </el-tab-pane>

            <el-tab-pane label="修改密码" name="password">
              <el-form ref="pwdFormRef" :model="pwdForm" :rules="pwdRules" label-width="90px" class="form-narrow">
                <el-form-item label="原密码" prop="oldPassword">
                  <el-input v-model="pwdForm.oldPassword" type="password" show-password />
                </el-form-item>
                <el-form-item label="新密码" prop="newPassword">
                  <el-input v-model="pwdForm.newPassword" type="password" show-password />
                </el-form-item>
                <el-form-item label="确认密码" prop="confirm">
                  <el-input v-model="pwdForm.confirm" type="password" show-password />
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" :loading="saving" @click="savePassword">修改密码</el-button>
                </el-form-item>
              </el-form>
            </el-tab-pane>
          </el-tabs>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { updateProfile, changePassword } from '@/api/auth'
import { getCurrentTenantQuota } from '@/api/system'
import { formatSize } from '@/utils/format'

const userStore = useUserStore()
const userInfo = computed(() => userStore.userInfo)

// 当前租户配额（issue #71）：平台租户/未绑定套餐时 unlimited=true。
const quota = ref<any>(null)
onMounted(async () => {
  quota.value = await getCurrentTenantQuota()
})

const activeTab = ref('basic')
const saving = ref(false)
const pwdFormRef = ref<FormInstance>()

const profileForm = reactive({
  nickname: userInfo.value?.nickname || '',
  mobile: userInfo.value?.mobile || '',
  email: userInfo.value?.email || '',
  gender: userInfo.value?.gender ?? 0,
  avatar: userInfo.value?.avatar || '',
})

const pwdForm = reactive({ oldPassword: '', newPassword: '', confirm: '' })

const pwdRules: FormRules = {
  oldPassword: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  newPassword: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 6, max: 32, message: '密码长度为 6-32 位', trigger: 'blur' },
  ],
  confirm: [
    { required: true, message: '请再次输入新密码', trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        if (value !== pwdForm.newPassword) callback(new Error('两次输入的密码不一致'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
}

async function saveProfile() {
  saving.value = true
  try {
    await updateProfile(profileForm)
    await userStore.fetchUserInfo()
    ElMessage.success('资料已更新')
  } finally {
    saving.value = false
  }
}

async function savePassword() {
  if (!pwdFormRef.value) return
  const valid = await pwdFormRef.value.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    await changePassword({ oldPassword: pwdForm.oldPassword, newPassword: pwdForm.newPassword })
    ElMessage.success('密码修改成功，请重新登录')
    await userStore.logout()
    window.location.href = '/login'
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.profile-card {
  text-align: center;
}

.profile-avatar {
  background: linear-gradient(135deg, #409eff, #7c6cf5);
  color: #fff;
  font-size: 30px;
  margin: 8px auto 12px;
}

.profile-name {
  font-size: 18px;
  font-weight: 600;
}

.profile-account {
  color: #909399;
  margin-top: 4px;
  font-size: 13px;
}

.profile-tags {
  margin-top: 14px;
  display: flex;
  justify-content: center;
  gap: 8px;
  flex-wrap: wrap;
}

.info-row {
  display: flex;
  justify-content: space-between;
  padding: 9px 4px;
  color: #909399;
  font-size: 13px;
}

.info-row b {
  color: #303133;
  font-weight: 500;
}

.quota-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
  color: #909399;
  margin-bottom: 4px;
}

.quota-row {
  display: flex;
  justify-content: space-between;
  padding: 7px 4px;
  color: #909399;
  font-size: 13px;
}

.quota-row b {
  color: #303133;
  font-weight: 500;
}

.quota-limit {
  color: #c0c4cc;
  font-weight: 400;
}

.form-narrow {
  max-width: 480px;
  padding-top: 12px;
}
</style>
