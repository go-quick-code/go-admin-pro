<template>
  <div class="login-page">
    <div class="brand-panel">
      <div class="brand-inner">
        <div class="brand-mark">G</div>
        <h1>go-admin-pro</h1>
        <p class="brand-desc">Go + Gin + GORM + Vue3 的企业级后台管理系统</p>
        <ul class="feature-list">
          <li>模块化单体架构，分层清晰</li>
          <li>多租户数据隔离 + RBAC 权限</li>
          <li>部门数据权限与菜单级权限控制</li>
          <li>内置代码生成器，一键生成 Go / Vue 代码</li>
        </ul>
      </div>
    </div>

    <div class="form-panel">
      <div class="login-card">
        <h2>登录</h2>
        <p class="subtitle">欢迎回来，请登录你的账号</p>

        <el-form ref="formRef" :model="form" :rules="rules" size="large" @keyup.enter="handleLogin">
          <el-form-item prop="tenantId">
            <el-input-number
              v-model="form.tenantId"
              :min="1"
              :controls="false"
              placeholder="租户 ID"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item prop="username">
            <el-input v-model="form.username" placeholder="用户名" :prefix-icon="User" clearable />
          </el-form-item>
          <el-form-item prop="password">
            <el-input
              v-model="form.password"
              type="password"
              placeholder="密码"
              :prefix-icon="Lock"
              show-password
            />
          </el-form-item>
          <el-form-item v-if="captcha.enabled" prop="captchaCode">
            <div class="captcha-row">
              <el-input v-model="form.captchaCode" placeholder="验证码" :prefix-icon="Key" />
              <img v-if="captcha.image" :src="captchaSrc" class="captcha-img" @click="loadCaptcha" />
            </div>
          </el-form-item>
          <el-button type="primary" :loading="loading" class="submit" @click="handleLogin">
            登 录
          </el-button>
        </el-form>

        <div class="tips">
          <span>超级管理员：admin / 123456</span>
          <span>普通角色：demo / 123456</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { User, Lock, Key } from '@element-plus/icons-vue'
import { useUserStore } from '@/stores/user'
import { getCaptcha } from '@/api/auth'

const router = useRouter()
const route = useRoute()
const store = useUserStore()

const formRef = ref<FormInstance>()
const loading = ref(false)
const captcha = reactive({ enabled: false, key: '', image: '' })
const form = reactive({
  tenantId: 1,
  username: 'admin',
  password: '',
  captchaCode: '',
})

const rules = computed<FormRules>(() => ({
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
  captchaCode: captcha.enabled
    ? [{ required: true, message: '请输入验证码', trigger: 'blur' }]
    : [],
}))

const captchaSrc = computed(() =>
  captcha.image.startsWith('data:') ? captcha.image : `data:image/png;base64,${captcha.image}`,
)

async function loadCaptcha() {
  try {
    const data = await getCaptcha()
    captcha.enabled = !!data.enabled
    captcha.key = data.key || ''
    captcha.image = data.image || ''
    form.captchaCode = ''
  } catch {
    captcha.enabled = false
  }
}

async function handleLogin() {
  if (!formRef.value) return
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  try {
    await store.login({
      tenantId: form.tenantId,
      username: form.username,
      password: form.password,
      captchaKey: captcha.key,
      captchaCode: form.captchaCode,
    })
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || '/'
    await router.replace(redirect)
  } catch {
    loadCaptcha()
  } finally {
    loading.value = false
  }
}

onMounted(loadCaptcha)
</script>

<style scoped>
.login-page {
  height: 100vh;
  display: flex;
  background: #f2f4f7;
}

.brand-panel {
  flex: 1.1;
  background: linear-gradient(135deg, #1b2a4a 0%, #243b6b 45%, #3a2f6b 100%);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px;
  position: relative;
  overflow: hidden;
}

.brand-panel::before {
  content: '';
  position: absolute;
  width: 520px;
  height: 520px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(64, 158, 255, 0.35), transparent 65%);
  top: -160px;
  right: -120px;
}

.brand-inner {
  position: relative;
  max-width: 460px;
}

.brand-mark {
  width: 64px;
  height: 64px;
  border-radius: 16px;
  background: linear-gradient(135deg, #409eff, #7c6cf5);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 34px;
  font-weight: 700;
  margin-bottom: 24px;
}

.brand-inner h1 {
  margin: 0 0 12px;
  font-size: 34px;
  letter-spacing: 1px;
}

.brand-desc {
  color: rgba(255, 255, 255, 0.75);
  font-size: 16px;
  margin: 0 0 32px;
}

.feature-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.feature-list li {
  color: rgba(255, 255, 255, 0.82);
  padding: 9px 0 9px 24px;
  position: relative;
  font-size: 14px;
}

.feature-list li::before {
  content: '';
  position: absolute;
  left: 0;
  top: 16px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #409eff;
}

.form-panel {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 32px;
}

.login-card {
  width: 100%;
  max-width: 400px;
  background: #fff;
  border-radius: 16px;
  padding: 40px 36px 32px;
  box-shadow: 0 18px 50px rgba(31, 45, 61, 0.12);
}

.login-card h2 {
  margin: 0 0 6px;
  font-size: 26px;
}

.subtitle {
  color: #909399;
  margin: 0 0 28px;
  font-size: 14px;
}

.submit {
  width: 100%;
  margin-top: 4px;
}

.captcha-row {
  display: flex;
  gap: 12px;
  width: 100%;
}

.captcha-img {
  width: 120px;
  height: 40px;
  border-radius: 6px;
  cursor: pointer;
  border: 1px solid #dcdfe6;
  flex-shrink: 0;
}

.tips {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px dashed #ebeef5;
  display: flex;
  flex-direction: column;
  gap: 4px;
  color: #909399;
  font-size: 12px;
}

@media (max-width: 900px) {
  .brand-panel {
    display: none;
  }
}
</style>
