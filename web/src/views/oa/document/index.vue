<template>
  <div class="app-container">
    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <span class="title">文档目录</span>
          <el-tag size="small" effect="plain">{{ flatCount }} 个可见目录</el-tag>
        </div>
        <div class="toolbar-right">
          <el-input v-model="keyword" placeholder="搜索目录名" clearable size="small" class="keyword" />
          <el-button v-if="canCreate" type="primary" @click="openCreate(0)">新建顶层目录</el-button>
          <el-button @click="load">刷新</el-button>
        </div>
      </div>

      <div class="body">
        <div v-loading="loading" class="tree-pane">
          <el-empty v-if="!visibleTree.length" description="暂无可见目录" />
          <el-tree
            v-else
            :data="visibleTree"
            :props="{ label: 'name', children: 'children' }"
            node-key="id"
            default-expand-all
            highlight-current
            :expand-on-click-node="false"
            @node-click="selectFolder"
          >
            <template #default="{ data }">
              <div class="tree-node">
                <span class="node-name">{{ data.name }}</span>
                <el-tag v-if="data.aclConfigured" size="small" type="success" effect="plain">显式</el-tag>
                <el-tag v-else size="small" type="info" effect="plain">继承</el-tag>
                <span class="node-actions" @click.stop>
                  <el-button
                    v-if="canCreate && canManageFolder(data)"
                    link
                    type="primary"
                    size="small"
                    @click="openCreate(data.id)"
                  >
                    子目录
                  </el-button>
                  <el-button v-if="canManageFolder(data)" link type="primary" size="small" @click="openRename(data)">
                    改名
                  </el-button>
                  <el-button v-if="canManageFolder(data)" link type="primary" size="small" @click="openACL(data)">
                    权限
                  </el-button>
                  <el-button v-if="canManageFolder(data)" link type="danger" size="small" @click="removeFolder(data)">
                    删除
                  </el-button>
                </span>
              </div>
            </template>
          </el-tree>
        </div>

        <div class="detail-pane">
          <el-empty v-if="!selected" description="选择左侧目录查看详情" />
          <template v-else>
            <h3 class="detail-title">{{ selected.name }}</h3>
            <el-descriptions :column="2" border>
              <el-descriptions-item label="目录 ID">{{ selected.id }}</el-descriptions-item>
              <el-descriptions-item label="上级目录">{{ selected.parentId || '（顶层）' }}</el-descriptions-item>
              <el-descriptions-item label="Owner">{{ selected.ownerId }}</el-descriptions-item>
              <el-descriptions-item label="排序">{{ selected.sort }}</el-descriptions-item>
              <el-descriptions-item label="ACL 配置">
                <el-tag :type="selected.aclConfigured ? 'success' : 'info'" size="small">
                  {{ selected.aclConfigured ? '显式 ACL' : '继承父目录' }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="创建时间">{{ selected.createTime }}</el-descriptions-item>
            </el-descriptions>

            <div class="acl-summary">
              <h4>可见性</h4>
              <p v-if="aclLoading" class="muted">正在加载 ACL…</p>
              <p v-else-if="!canManageFolder(selected)" class="muted">需要目录管理权才能查看 ACL 明细。</p>
              <p v-else-if="!aclInfo" class="muted">ACL 明细加载失败。</p>
              <template v-else>
                <p class="muted">
                  {{ aclSourceText }}
                </p>
                <el-table :data="aclInfo?.effectiveEntries || []" size="small" border empty-text="链上无显式 ACL（仅 owner/管理员可见）">
                  <el-table-column label="主体类型" width="120">
                    <template #default="{ row }">{{ targetTypeLabel(row.targetType) }}</template>
                  </el-table-column>
                  <el-table-column prop="targetId" label="主体 ID" width="100" />
                  <el-table-column prop="targetName" label="名称" min-width="160" />
                </el-table>
                <el-button v-if="canManageFolder(selected)" type="primary" size="small" class="acl-edit" @click="openACL(selected)">
                  设置 ACL
                </el-button>
              </template>
            </div>
          </template>
        </div>
      </div>
    </el-card>

    <!-- 创建/改名目录 -->
    <el-dialog v-model="formVisible" :title="form.id ? '目录改名' : '新建目录'" width="620px" :close-on-click-modal="false">
      <el-form :model="form" label-width="90px">
        <el-form-item label="上级目录">
          <el-tree-select
            v-model="form.parentId"
            :data="tree"
            :props="{ label: 'name', value: 'id', children: 'children' }"
            check-strictly
            clearable
            :disabled="form.id > 0"
            placeholder="不选则为顶层目录"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="目录名称" required>
          <el-input v-model="form.name" maxlength="100" show-word-limit placeholder="1–100 字" />
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sort" :min="0" :max="999999" />
        </el-form-item>
        <el-form-item v-if="!form.id" label="访问 ACL">
          <AclEditor v-model="form.acl" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveForm">保存</el-button>
      </template>
    </el-dialog>

    <!-- 设置 ACL -->
    <el-dialog v-model="aclVisible" title="设置目录 ACL" width="720px" :close-on-click-modal="false">
      <p class="muted">
        目录：{{ aclTarget?.name }}；当前{{ aclInfo?.explicit ? '使用本目录显式 ACL' : aclSourceText }}。
      </p>
      <AclEditor v-model="aclEntries" />
      <template #footer>
        <el-button @click="aclVisible = false">取消</el-button>
        <el-button type="primary" :loading="aclSaving" @click="saveACL">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import AclEditor from './AclEditor.vue'
import {
  createDocumentFolder,
  deleteDocumentFolder,
  getDocumentFolderACL,
  getDocumentFolderTree,
  setDocumentFolderACL,
  updateDocumentFolder,
} from '@/api/oa'
import type { DocumentACLPayload } from '@/api/oa'

// 文档目录页（issue #58，spec §3.8/§4.12）：
//   - 目录树只展示当前用户有读权限的目录（ACL 由后端过滤），父目录不可见时
//     子目录作为根节点出现；
//   - 创建目录挂 oa:document:create；顶层目录必须至少一条 ACL；
//   - 改名/删除/ACL 设置要求目录管理权（owner 或 oa:document:manage）；
//   - 有子目录或文件的目录删除返回 409，错误消息由请求拦截器提示；
//   - 文件上传/下载由 #59 落地，本页暂不展示文件列表。

interface DocumentFolderRow {
  id: number
  parentId: number
  name: string
  ownerId: number
  sort: number
  aclConfigured: boolean
  createTime: string
  updateTime: string
  children?: DocumentFolderRow[]
}

interface DocumentACLItem {
  targetType: number
  targetId: number
  targetName: string
}

interface DocumentFolderACLInfo {
  folderId: number
  ownerId: number
  explicit: boolean
  sourceFolderId: number
  sourceFolderName: string
  entries: DocumentACLItem[]
  effectiveEntries: DocumentACLItem[]
}

const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('oa:document:create'))
const canManageAll = computed(() => userStore.hasPerm('oa:document:manage'))
const currentUserId = computed(() => Number(userStore.userInfo?.id || 0))

const tree = ref<DocumentFolderRow[]>([])
const loading = ref(false)
const keyword = ref('')
const selected = ref<DocumentFolderRow | null>(null)
const aclInfo = ref<DocumentFolderACLInfo | null>(null)
const aclLoading = ref(false)

const formVisible = ref(false)
const saving = ref(false)
const form = ref<{ id: number; parentId: number; name: string; sort: number; acl: DocumentACLPayload[] }>({
  id: 0,
  parentId: 0,
  name: '',
  sort: 0,
  acl: [],
})

const aclVisible = ref(false)
const aclSaving = ref(false)
const aclTarget = ref<DocumentFolderRow | null>(null)
const aclEntries = ref<DocumentACLPayload[]>([])

const visibleTree = computed(() => filterFolders(tree.value, keyword.value.trim()))
const flatCount = computed(() => countFolders(tree.value))
const aclSourceText = computed(() => {
  if (!aclInfo.value) return ''
  if (aclInfo.value.sourceFolderId === 0) return '链上无显式 ACL：仅目录 owner 与管理员可见。'
  if (aclInfo.value.explicit) return '使用本目录显式 ACL（覆盖祖先）。'
  return `继承最近祖先目录「${aclInfo.value.sourceFolderName || aclInfo.value.sourceFolderId}」的 ACL。`
})

function filterFolders(nodes: DocumentFolderRow[], kw: string): DocumentFolderRow[] {
  if (!kw) return nodes
  const out: DocumentFolderRow[] = []
  for (const node of nodes) {
    const children = filterFolders(node.children || [], kw)
    if (node.name.includes(kw) || children.length > 0) {
      out.push({ ...node, children })
    }
  }
  return out
}

function countFolders(nodes: DocumentFolderRow[]): number {
  return nodes.reduce((sum, node) => sum + 1 + countFolders(node.children || []), 0)
}

function findFolder(nodes: DocumentFolderRow[], id: number): DocumentFolderRow | null {
  for (const node of nodes) {
    if (node.id === id) return node
    const found = findFolder(node.children || [], id)
    if (found) return found
  }
  return null
}

function canManageFolder(folder: DocumentFolderRow | null): boolean {
  if (!folder) return false
  return canManageAll.value || folder.ownerId === currentUserId.value
}

function targetTypeLabel(type: number): string {
  switch (type) {
    case 0:
      return '全员'
    case 1:
      return '部门'
    case 2:
      return '角色'
    case 3:
      return '用户'
    default:
      return '未知'
  }
}

async function load() {
  loading.value = true
  try {
    tree.value = (await getDocumentFolderTree()) || []
    if (selected.value) {
      const still = findFolder(tree.value, selected.value.id)
      selected.value = still
      if (still) await loadAclInfo(still)
    }
  } finally {
    loading.value = false
  }
}

async function loadAclInfo(folder: DocumentFolderRow) {
  aclInfo.value = null
  if (!canManageFolder(folder)) return
  aclLoading.value = true
  try {
    aclInfo.value = (await getDocumentFolderACL(folder.id)) || null
  } catch {
    aclInfo.value = null
  } finally {
    aclLoading.value = false
  }
}

function selectFolder(folder: DocumentFolderRow) {
  selected.value = folder
  void loadAclInfo(folder)
}

function openCreate(parentId: number) {
  form.value = { id: 0, parentId: parentId > 0 ? parentId : 0, name: '', sort: 0, acl: [] }
  formVisible.value = true
}

function openRename(folder: DocumentFolderRow) {
  form.value = { id: folder.id, parentId: folder.parentId, name: folder.name, sort: folder.sort, acl: [] }
  formVisible.value = true
}

async function saveForm() {
  const name = form.value.name.trim()
  if (!name) {
    ElMessage.warning('目录名称不能为空')
    return
  }
  if (!form.value.id && form.value.parentId === 0 && form.value.acl.length === 0) {
    ElMessage.warning('顶层目录必须设置至少一条 ACL')
    return
  }
  saving.value = true
  try {
    if (form.value.id) {
      await updateDocumentFolder({ id: form.value.id, name, sort: form.value.sort })
    } else {
      await createDocumentFolder({
        parentId: form.value.parentId || 0,
        name,
        sort: form.value.sort,
        acl: form.value.acl,
      })
    }
    ElMessage.success('已保存')
    formVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function openACL(folder: DocumentFolderRow) {
  aclTarget.value = folder
  aclEntries.value = []
  try {
    aclInfo.value = (await getDocumentFolderACL(folder.id)) || null
    aclEntries.value = (aclInfo.value?.entries || []).map((item) => ({
      targetType: item.targetType,
      targetId: item.targetId,
    }))
  } catch {
    return
  }
  aclVisible.value = true
}

async function saveACL() {
  if (!aclTarget.value) return
  if (aclTarget.value.parentId === 0 && aclEntries.value.length === 0) {
    ElMessage.warning('顶层目录必须设置至少一条 ACL')
    return
  }
  aclSaving.value = true
  try {
    await setDocumentFolderACL(aclTarget.value.id, aclEntries.value)
    ElMessage.success('ACL 已更新')
    aclVisible.value = false
    await load()
  } finally {
    aclSaving.value = false
  }
}

async function removeFolder(folder: DocumentFolderRow) {
  try {
    await ElMessageBox.confirm(
      `确认删除目录「${folder.name}」？有子目录或文件的目录不能删除。`,
      '删除目录',
      { type: 'warning' },
    )
  } catch {
    return
  }
  try {
    await deleteDocumentFolder(folder.id)
    ElMessage.success('已删除')
    if (selected.value?.id === folder.id) selected.value = null
    await load()
  } catch {
    // 409/403 等业务错误已由请求拦截器提示。
  }
}

onMounted(load)
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.title {
  font-size: 16px;
  font-weight: 600;
}
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.keyword {
  width: 200px;
}
.body {
  display: flex;
  gap: 16px;
  min-height: 420px;
}
.tree-pane {
  flex: 1 1 48%;
  min-width: 0;
  border-right: 1px solid var(--el-border-color-lighter);
  padding-right: 16px;
}
.detail-pane {
  flex: 1 1 52%;
  min-width: 0;
}
.tree-node {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 30px;
  width: 100%;
}
.node-name {
  flex: none;
}
.node-actions {
  display: flex;
  gap: 4px;
  margin-left: auto;
}
.detail-title {
  margin: 0 0 12px;
}
.acl-summary {
  margin-top: 20px;
}
.acl-summary h4 {
  margin: 0 0 8px;
}
.acl-edit {
  margin-top: 12px;
}
.muted {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
</style>
