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
import { computed, reactive, ref } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { updateProfile, changePassword } from '@/api/auth'

const userStore = useUserStore()
const userInfo = computed(() => userStore.userInfo)

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

.form-narrow {
  max-width: 480px;
  padding-top: 12px;
}
</style>
