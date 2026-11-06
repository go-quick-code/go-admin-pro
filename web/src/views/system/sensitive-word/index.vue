<template>
  <div class="app-container">
    <!-- 敏感词管理（issue #81，spec §5.6/§6）：词库 CRUD + 检测文本框。
         词库为租户级资源（后端租户插件隔离），检测复用 query 权限码。 -->
    <el-card v-if="canQuery" shadow="never" class="check-card">
      <template #header>敏感词检测</template>
      <el-input
        v-model="checkText"
        type="textarea"
        :rows="3"
        maxlength="5000"
        show-word-limit
        placeholder="输入待检测文本（标题/正文/跟进内容等），点击检测查看命中词与打码结果"
      />
      <div class="check-actions">
        <el-button type="primary" :loading="checking" @click="handleCheck">检测</el-button>
      </div>
      <div v-if="checkResult" class="check-result">
        <div class="check-line">
          <span class="check-label">命中词：</span>
          <template v-if="checkResult.words.length">
            <el-tag v-for="w in checkResult.words" :key="w" type="danger" size="small" class="hit-tag">
              {{ w }}
            </el-tag>
          </template>
          <span v-else class="no-hit">无命中</span>
        </div>
        <div class="check-line">
          <span class="check-label">打码文本：</span>
          <span class="masked-text">{{ checkResult.masked }}</span>
        </div>
      </div>
    </el-card>

    <ProTable
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      perm-prefix="system:sensitive-word"
      title="敏感词"
    >
      <template #toolbar>
        <span class="tip">去空白后 1–50 字符；停用词不参与匹配；单租户上限 5000 条</span>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'">
          {{ row.status === 0 ? '启用' : '停用' }}
        </el-tag>
      </template>
    </ProTable>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import { useUserStore } from '@/stores/user'
import {
  pageSensitiveWords,
  createSensitiveWord,
  updateSensitiveWord,
  deleteSensitiveWord,
  checkSensitiveWord,
} from '@/api/system'

const userStore = useUserStore()
const canQuery = computed(() => userStore.hasPerm('system:sensitive-word:query'))

const columns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'word', label: '敏感词', minWidth: 160 },
  { prop: 'category', label: '分类', minWidth: 110 },
  { prop: 'status', label: '状态', width: 80, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', width: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const searchItems = [
  { prop: 'word', label: '敏感词' },
  { prop: 'category', label: '分类' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const formItems = [
  { prop: 'word', label: '敏感词', required: true },
  { prop: 'category', label: '分类' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const defaultForm = { status: 0 }

const api = {
  page: pageSensitiveWords,
  create: createSensitiveWord,
  update: updateSensitiveWord,
  remove: deleteSensitiveWord,
}

// ---------- 检测文本框 ----------
const checkText = ref('')
const checking = ref(false)
const checkResult = ref<{ words: string[]; masked: string } | null>(null)

async function handleCheck() {
  if (!checkText.value.trim()) return
  checking.value = true
  try {
    const res = await checkSensitiveWord(checkText.value)
    checkResult.value = { words: res?.words || [], masked: res?.masked || '' }
  } finally {
    checking.value = false
  }
}
</script>

<style scoped>
.check-card {
  margin-bottom: 12px;
}
.check-actions {
  margin-top: 8px;
}
.check-result {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.check-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.hit-tag {
  margin-right: 6px;
}
.no-hit {
  color: var(--el-color-success);
  font-size: 13px;
}
.masked-text {
  font-size: 13px;
  word-break: break-all;
}
.tip {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
