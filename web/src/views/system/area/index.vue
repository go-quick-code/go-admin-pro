<template>
  <div class="app-container">
    <el-card shadow="never">
      <template #header>
        <div class="header">
          <span class="title">地区管理</span>
          <div class="actions">
            <el-input
              v-model="keyword"
              placeholder="按名称 / 区划代码过滤"
              clearable
              style="width: 220px"
            />
            <el-button type="primary" @click="loadTree">刷新</el-button>
            <el-button v-if="userStore.hasPerm('system:area:import')" type="success" @click="openImport">
              导入
            </el-button>
          </div>
        </div>
      </template>

      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="notice"
        title="行政区划为平台级参考数据：省市两级随初始化种子写入，区县由平台租户经「导入」按 code upsert 补齐（JSON 数组，单次 ≤ 5000 条，父节点须已存在或同批出现）。"
      />

      <el-tree
        v-loading="loading"
        :data="filteredTree"
        :props="treeProps"
        node-key="code"
        default-expand-all
        empty-text="暂无地区数据"
      >
        <template #default="{ data }">
          <span class="area-node">
            <span class="area-name">{{ data.name }}</span>
            <el-tag size="small" :type="levelTagType(data.level)" class="area-code">
              {{ levelLabel(data.level) }} · {{ data.code }}
            </el-tag>
            <el-tag v-if="data.status === 1" size="small" type="danger">已停用</el-tag>
          </span>
        </template>
      </el-tree>
    </el-card>

    <el-dialog v-model="dialogVisible" title="导入地区数据" width="720px" destroy-on-close>
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        class="notice"
        title="按 code upsert：重复导入不会产生重复行；父节点必须在库中已存在或与本批同批出现；层级 1 省 / 2 市 / 3 区县。"
      />
      <el-upload
        :auto-upload="false"
        :show-file-list="false"
        accept=".json,application/json"
        :on-change="handleFileChange"
        class="upload"
      >
        <el-button>选择 JSON 文件</el-button>
      </el-upload>
      <el-input
        v-model="importText"
        type="textarea"
        :rows="12"
        placeholder='[{"code":"130102","parentCode":"130100","name":"长安区","level":3}]'
      />
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="importing" @click="handleImport">导入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
// 地区管理（issue #82，spec §6）：只读地区树 + 平台导入入口。
// 树/子级接口对所有登录用户开放（spec §4.8.1），导入按钮仅对持
// system:area:import 权限码（平台租户）可见，后端二次校验。
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { UploadFile } from 'element-plus'
import { listAreaTree, importAreas } from '@/api/system'
import { useUserStore } from '@/stores/user'

const userStore = useUserStore()

const loading = ref(false)
const tree = ref<any[]>([])
const keyword = ref('')
const treeProps = { label: 'name', children: 'children' }

const dialogVisible = ref(false)
const importing = ref(false)
const importText = ref('')

// 过滤在树上做：命中名称或 code 的节点保留（子节点命中时父链保留）。
const filteredTree = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return tree.value
  const filterNodes = (nodes: any[]): any[] =>
    nodes
      .map((n) => {
        const kids = filterNodes(n.children || [])
        if (n.name.toLowerCase().includes(kw) || String(n.code).toLowerCase().includes(kw)) {
          return { ...n, children: kids }
        }
        return kids.length ? { ...n, children: kids } : null
      })
      .filter(Boolean)
  return filterNodes(tree.value)
})

function filterNode() {
  return true
}

async function loadTree() {
  loading.value = true
  try {
    tree.value = (await listAreaTree()) || []
  } finally {
    loading.value = false
  }
}

function levelLabel(level: number) {
  return { 1: '省', 2: '市', 3: '区县' }[level] || `L${level}`
}

function levelTagType(level: number) {
  return ({ 1: 'danger', 2: 'primary', 3: 'info' } as Record<number, any>)[level] || 'info'
}

function openImport() {
  importText.value = ''
  dialogVisible.value = true
}

// 支持选择本地 JSON 文件填充文本框，也支持直接粘贴，路径统一走文本解析。
async function handleFileChange(file: UploadFile) {
  const raw = file.raw
  if (!raw) return
  importText.value = await raw.text()
}

async function handleImport() {
  let items: any[]
  try {
    items = JSON.parse(importText.value)
  } catch {
    ElMessage.error('不是合法的 JSON')
    return
  }
  if (!Array.isArray(items)) {
    ElMessage.error('导入内容应为 JSON 数组')
    return
  }
  importing.value = true
  try {
    const data = await importAreas(items)
    ElMessage.success(`导入成功，共写入 ${data?.count ?? 0} 条`)
    dialogVisible.value = false
    await loadTree()
  } finally {
    importing.value = false
  }
}

onMounted(loadTree)
</script>

<style scoped>
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.title {
  font-weight: 600;
}
.actions {
  display: flex;
  gap: 8px;
}
.notice {
  margin-bottom: 12px;
}
.upload {
  margin: 8px 0;
}
.area-node {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.area-name {
  font-size: 14px;
}
.area-code {
  font-family: monospace;
}
</style>
