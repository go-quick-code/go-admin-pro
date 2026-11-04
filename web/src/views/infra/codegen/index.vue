<template>
  <div class="app-container">
    <el-card shadow="never">
      <template #header>
        <div class="header">
          <span class="title">代码生成</span>
          <div class="actions">
            <el-input
              v-model="keyword"
              placeholder="搜索表名 / 注释"
              clearable
              style="width: 240px"
              @keyup.enter="loadTables"
            />
            <el-button type="primary" @click="loadTables">查询</el-button>
            <el-button @click="loadTables">刷新</el-button>
          </div>
        </div>
      </template>

      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="notice"
        title="读取数据库表结构，生成 Go 的 model/dto/service/api 与 Vue 页面代码。系统表（system_ / infra_）已自动排除；下载 zip 后按包内 README 接入。"
      />

      <el-table v-loading="loading" :data="tables" border stripe>
        <el-table-column prop="name" label="表名" min-width="180" />
        <el-table-column prop="comment" label="表注释" min-width="220" />
        <el-table-column prop="createTime" label="创建时间" min-width="170" />
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openGenerate(row)">生成代码</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty
        v-if="!loading && !tables.length"
        description="暂无业务表，可在数据库中建表后点击刷新"
        :image-size="90"
      />
    </el-card>

    <el-dialog v-model="dialogVisible" title="生成代码" width="900px" destroy-on-close>
      <div class="dialog-body">
        <el-form :model="form" label-width="90px" class="gen-form">
          <el-form-item label="表名">
            <el-input v-model="form.tableName" disabled />
          </el-form-item>
          <el-form-item label="表注释">
            <el-input v-model="form.tableComment" />
          </el-form-item>
          <el-form-item label="模块名" required>
            <el-input v-model="form.moduleName" placeholder="小写字母开头，如 biz" />
          </el-form-item>
          <el-form-item label="业务名" required>
            <el-input v-model="form.businessName" placeholder="如 product" />
          </el-form-item>
          <el-form-item label="类名" required>
            <el-input v-model="form.className" placeholder="如 Product" />
          </el-form-item>
          <el-form-item label="作者">
            <el-input v-model="form.author" />
          </el-form-item>
        </el-form>

        <div class="columns-panel">
          <div class="columns-title">字段预览（{{ columns.length }}）</div>
          <el-table :data="columns" size="small" border height="360">
            <el-table-column prop="name" label="字段" min-width="110" />
            <el-table-column prop="dataType" label="类型" width="90" />
            <el-table-column prop="comment" label="注释" min-width="120" />
            <el-table-column label="主键" width="60">
              <template #default="{ row }">{{ row.primaryKey ? '是' : '' }}</template>
            </el-table-column>
          </el-table>
        </div>
      </div>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="generating" @click="handleGenerate">生成并下载</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { listCodegenTables, getCodegenTable, generateCode } from '@/api/codegen'

const loading = ref(false)
const generating = ref(false)
const keyword = ref('')
const tables = ref<any[]>([])
const columns = ref<any[]>([])
const dialogVisible = ref(false)

const form = reactive({
  tableName: '',
  tableComment: '',
  moduleName: 'biz',
  businessName: '',
  className: '',
  author: '',
})

function toCamel(input: string) {
  return input
    .split(/[_-]/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join('')
}

function toLowerCamel(input: string) {
  const value = toCamel(input)
  return value ? value.charAt(0).toLowerCase() + value.slice(1) : value
}

function deriveBusiness(tableName: string) {
  const parts = tableName.split('_').filter(Boolean)
  if (parts.length > 1 && ['biz', 't', 'app'].includes(parts[0])) {
    parts.shift()
  }
  return toLowerCamel(parts.join('_'))
}

async function loadTables() {
  loading.value = true
  try {
    tables.value = await listCodegenTables(keyword.value)
  } finally {
    loading.value = false
  }
}

async function openGenerate(row: any) {
  const data = await getCodegenTable(row.name)
  columns.value = data.columns || []
  form.tableName = row.name
  form.tableComment = row.comment || ''
  form.businessName = deriveBusiness(row.name)
  form.className = toCamel(form.businessName)
  dialogVisible.value = true
}

async function handleGenerate() {
  if (!form.moduleName || !form.businessName || !form.className) {
    ElMessage.warning('请填写模块名、业务名与类名')
    return
  }
  generating.value = true
  try {
    const response: any = await generateCode({ ...form })
    const blob = response.data instanceof Blob ? response.data : new Blob([response.data])
    const disposition = response.headers?.['content-disposition'] || ''
    const match = /filename="?([^";]+)"?/.exec(disposition)
    const filename = match ? match[1] : `${form.moduleName}-${form.businessName}.zip`
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
    ElMessage.success('代码已生成，下载已开始')
  } finally {
    generating.value = false
  }
}

onMounted(loadTables)
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
  gap: 10px;
}

.notice {
  margin-bottom: 16px;
}

.dialog-body {
  display: flex;
  gap: 24px;
}

.gen-form {
  flex: 0 0 320px;
}

.columns-panel {
  flex: 1;
  min-width: 0;
}

.columns-title {
  font-weight: 600;
  margin-bottom: 8px;
}
</style>
