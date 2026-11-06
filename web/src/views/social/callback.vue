<template>
  <div class="social-callback">
    <el-card class="callback-card">
      <template v-if="phase === 'pending'">
        <el-icon class="spin"><Loading /></el-icon>
        <p class="callback-text">正在完成社交登录，请稍候…</p>
      </template>

      <template v-else-if="phase === 'success'">
        <el-icon class="ok" color="#67c23a"><CircleCheckFilled /></el-icon>
        <p class="callback-text">{{ successText }}</p>
        <p class="callback-sub">页面即将自动跳转…</p>
      </template>

      <template v-else>
        <el-icon class="fail" color="#f56c6c"><CircleCloseFilled /></el-icon>
        <p class="callback-text">{{ errorMsg }}</p>
        <template v-if="isNotBound">
          <p class="callback-sub">请先用账号密码登录，再到「个人中心 → 社交账号」完成绑定。</p>
          <div class="callback-actions">
            <el-button type="primary" @click="goLogin">去登录</el-button>
          </div>
        </template>
        <template v-else>
          <div class="callback-actions">
            <el-button v-if="userStore.token" @click="router.replace('/profile')">返回个人中心</el-button>
            <el-button type="primary" @click="goLogin">重新登录</el-button>
          </div>
        </template>
      </template>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { CircleCheckFilled, CircleCloseFilled, Loading } from '@element-plus/icons-vue'
import { useUserStore } from '@/stores/user'
import { setToken } from '@/utils/auth'
import { socialCallback } from '@/api/system'
import { clearSocialFlow, recallSocialProvider, recallSocialRedirect } from '@/utils/social'

// 社交登录回调页（issue #77，公开路由）：三方把浏览器重定向回本页
// （redirect_uri 指向前端路由 /social/callback），页面读取 code/state/provider
// 后调 GET /system/social/callback：
//   - mode=login：与账号密码登录同构，写入 JWT 进入首页（验收标准）；
//   - mode=bind：绑定完成，回到个人中心；
//   - 未绑定（业务码 1901）：提示「先登录后绑定」并引导（验收标准）。
const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const phase = ref<'pending' | 'success' | 'error'>('pending')
const isNotBound = ref(false)
const successText = ref('')
const errorMsg = ref('')

function goLogin() {
  userStore.reset()
  router.replace('/login')
}

onMounted(async () => {
  const code = (route.query.code as string) || ''
  const state = (route.query.state as string) || ''
  // provider 优先取 query（部分平台可配置回传），缺省用发起时留底的值。
  const provider = (route.query.provider as string) || recallSocialProvider()
  const redirect = recallSocialRedirect()
  clearSocialFlow()

  if (!code || !state || !provider) {
    phase.value = 'error'
    errorMsg.value = '回调参数缺失或已过期，请重新发起社交登录'
    return
  }

  try {
    const data = await socialCallback({ provider, code, state })
    if (data.mode === 'login') {
      // 与账号密码登录同构：写 token 后由路由守卫拉取用户信息与动态菜单。
      setToken(data.accessToken)
      userStore.token = data.accessToken
      successText.value = '登录成功，正在进入系统…'
      phase.value = 'success'
      ElMessage.success('登录成功')
      await router.replace(redirect || '/')
    } else {
      successText.value = '绑定成功'
      phase.value = 'success'
      ElMessage.success('绑定成功')
      await router.replace('/profile?tab=social')
    }
  } catch (err: any) {
    // 业务错误码：HTTP 错误体在 err.response.data，200 信封错误在 responseBody。
    const bizCode = err?.response?.data?.code ?? err?.responseBody?.code
    const msg = err?.response?.data?.msg || err?.responseBody?.msg || err?.message || '社交登录失败，请稍后重试'
    phase.value = 'error'
    errorMsg.value = msg
    if (bizCode === 1901) {
      isNotBound.value = true
    }
  }
})
</script>

<style scoped>
.social-callback {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f2f4f7;
}

.callback-card {
  width: 360px;
  text-align: center;
  padding: 16px 8px;
}

.callback-card :deep(.el-icon) {
  font-size: 44px;
}

.callback-card .spin {
  animation: social-spin 1s linear infinite;
  color: #409eff;
}

@keyframes social-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

.callback-text {
  margin: 14px 0 6px;
  font-size: 15px;
  color: #303133;
  word-break: break-all;
}

.callback-sub {
  margin: 0 0 6px;
  font-size: 13px;
  color: #909399;
}

.callback-actions {
  margin-top: 14px;
  display: flex;
  justify-content: center;
  gap: 8px;
}
</style>
