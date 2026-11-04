<template>
  <div class="not-found">
    <div class="code">404</div>
    <p>抱歉，你访问的页面不存在</p>
    <el-button type="primary" @click="router.push('/')">返回首页</el-button>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()
</script>

<style scoped>
.not-found {
  height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  background: #f2f4f7;
}

.code {
  font-size: 96px;
  font-weight: 800;
  color: #409eff;
  line-height: 1;
}

p {
  color: #909399;
  margin: 0 0 8px;
}
</style>
