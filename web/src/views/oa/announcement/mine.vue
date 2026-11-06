<template>
  <div class="app-container">
    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <span class="title">我的公告</span>
          <el-input
            v-model="keyword"
            clearable
            placeholder="标题关键字"
            style="width: 220px"
            @clear="loadAnnouncements"
            @keyup.enter="loadAnnouncements"
          />
          <el-button :icon="Search" circle @click="loadAnnouncements" />
        </div>
      </div>

      <el-table v-loading="loading" :data="rows" row-key="id" @row-click="openDetail">
        <el-table-column label="标题" min-width="320">
          <template #default="{ row }">
            <el-tag v-if="row.pinned" type="danger" size="small" effect="plain" class="pin-tag">置顶</el-tag>
            <span class="link-title">{{ row.title }}</span>
          </template>
        </el-table-column>
        <el-table-column label="发布时间" width="180">
          <template #default="{ row }">{{ formatTime(row.publishedAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click.stop="openDetail(row)">查看</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!loading && !rows.length" description="暂无公告" :image-size="80" />
      <div class="pager">
        <el-pagination
          v-model:current-page="pageNo"
          :page-size="pageSize"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="loadAnnouncements"
        />
      </div>
    </el-card>

    <el-dialog v-model="detail.visible" :title="detail.title" width="820px" append-to-body>
      <div class="detail-meta">
        <span>发布时间：{{ formatTime(detail.publishedAt) }}</span>
        <el-tag v-if="detail.status === 2" type="warning" size="small">已撤回</el-tag>
        <el-tag v-if="detail.pinned" type="danger" size="small" effect="plain">置顶</el-tag>
      </div>
      <div class="detail-content" v-html="detail.content" />
      <div v-if="files.length" class="detail-files">
        <div class="files-title">附件</div>
        <el-button v-for="f in files" :key="f.id" link type="primary" @click="downloadAttachment(f)">
          {{ f.name }}
        </el-button>
      </div>
      <template #footer>
        <el-button @click="detail.visible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { saveBlobAs } from '@/utils/download'
import { formatTime } from '@/utils/format'
import { downloadAnnouncementFile, getAnnouncement, listAnnouncementFiles, pageMyAnnouncements } from '@/api/oa'

// 我的公告（issue #48，菜单 532）：登录即可；只展示已发布且命中受众的公告，
// 置顶在前。打开详情走 GET /oa/announcement/:id（#49 将在该接口写已读记录），
// 已撤回公告仍可从站内通知跳转查看「已撤回」。
interface AnnouncementRow {
  id: number
  title: string
  pinned: boolean
  publishedAt: string | null
}
interface Attachment {
  id: number
  name: string
  url: string
  size: number
}

const route = useRoute()
const loading = ref(false)
const rows = ref<AnnouncementRow[]>([])
const total = ref(0)
const pageNo = ref(1)
const pageSize = 10
const keyword = ref('')

const detail = ref<any>({ visible: false, title: '', content: '', status: 1, pinned: false, publishedAt: null })
const files = ref<Attachment[]>([])

async function loadAnnouncements() {
  loading.value = true
  try {
    const data = await pageMyAnnouncements({
      pageNo: pageNo.value,
      pageSize,
      keyword: keyword.value || undefined,
    })
    rows.value = data.list || []
    total.value = data.total || 0
  } finally {
    loading.value = false
  }
}

async function openDetail(row: AnnouncementRow) {
  await showDetail(row.id)
}

async function showDetail(id: number) {
  try {
    const data = await getAnnouncement(id)
    detail.value = { ...data, visible: true }
    files.value = (await listAnnouncementFiles(id)) || []
  } catch {
    ElMessage.error('公告不存在或已不可见')
  }
}

async function downloadAttachment(row: Attachment) {
  const response = await downloadAnnouncementFile(row.url)
  saveBlobAs(response, row.name)
}

onMounted(async () => {
  await loadAnnouncements()
  // 站内通知跳转：/oa/announcement-mine?id=<公告 ID>
  const id = Number(route.query.id)
  if (id > 0) await showDetail(id)
})
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}
.toolbar-left {
  display: flex;
  gap: 8px;
  align-items: center;
}
.title {
  font-weight: 600;
}
.pin-tag {
  margin-right: 6px;
}
.link-title {
  cursor: pointer;
}
.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
.detail-meta {
  display: flex;
  gap: 12px;
  align-items: center;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  margin-bottom: 10px;
}
.detail-content {
  border-top: 1px solid var(--el-border-color-lighter);
  padding: 14px 2px;
  line-height: 1.75;
  min-height: 100px;
}
.detail-files {
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: 10px;
}
.files-title {
  font-weight: 600;
  margin-bottom: 6px;
}
</style>
