<template>
  <div class="app-container">
    <el-tabs v-model="activeTab">
      <el-tab-pane label="邮件账号" name="channel">
        <ProTable
          :columns="channelColumns"
          :form-items="channelFormItems"
          :search-items="channelSearchItems"
          :api="channelApi"
          :default-form="channelDefaultForm"
          :before-submit="beforeChannelSubmit"
          :extra-query="{ channelType: 'mail' }"
          perm-prefix="message:channel"
          title="邮件账号"
          dialog-width="680px"
          @loaded="flattenChannelRows"
        >
          <template #cell-status="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
              {{ row.status === 0 ? '启用' : '停用' }}
            </el-tag>
          </template>
          <template #cell-server="{ row }">{{ serverText(row) }}</template>
          <template #cell-from="{ row }">{{ fromText(row) }}</template>
          <template #actions="{ row }">
            <el-button v-if="canTest" link type="primary" @click="openTest(row)">测试发送</el-button>
          </template>
        </ProTable>
      </el-tab-pane>

      <el-tab-pane label="邮件模板" name="template">
        <ProTable
          :columns="templateColumns"
          :form-items="templateFormItems"
          :search-items="templateSearchItems"
          :api="templateApi"
          :default-form="templateDefaultForm"
          :before-submit="beforeTemplateSubmit"
          :extra-query="{ channelType: 'mail' }"
          perm-prefix="message:template"
          title="邮件模板"
          dialog-width="760px"
        >
          <template #cell-status="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
              {{ row.status === 0 ? '启用' : '停用' }}
            </el-tag>
          </template>
          <template #cell-params="{ row }">
            <el-tag v-for="item in row.params || []" :key="item" size="small" effect="plain" class="param-tag">
              {{ item }}
            </el-tag>
            <span v-if="!row.params || !row.params.length">-</span>
          </template>
        </ProTable>
      </el-tab-pane>
    </el-tabs>

    <!-- 渠道测试发送弹窗（issue #73）：向收件人投递一条真实邮件并写发送日志 -->
    <el-dialog v-model="testVisible" title="测试发送" width="520px" destroy-on-close append-to-body>
      <el-descriptions :column="1" border size="small" class="test-channel">
        <el-descriptions-item label="渠道">{{ testForm.name }}（{{ testForm.code }}）</el-descriptions-item>
      </el-descriptions>
      <el-form label-width="90px">
        <el-form-item label="收件人" required>
          <el-input v-model="testForm.recipient" clearable placeholder="user@example.com" />
        </el-form-item>
        <el-form-item label="邮件模板" required>
          <el-select v-model="testForm.templateCode" filterable style="width: 100%" placeholder="请选择启用的邮件模板">
            <el-option
              v-for="tpl in templateOptions"
              :key="tpl.code"
              :label="`${tpl.name}（${tpl.code}）`"
              :value="tpl.code"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="模板参数">
          <el-input
            v-model="testForm.paramsText"
            type="textarea"
            :rows="3"
            placeholder='JSON 对象，如 {"name":"张三"}；模板声明了参数时必填'
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="testVisible = false">取消</el-button>
        <el-button type="primary" :loading="testing" @click="handleTest">发送</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { useUserStore } from '@/stores/user'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageChannels,
  createChannel,
  updateChannel,
  deleteChannel,
  testChannel,
  pageTemplates,
  createTemplate,
  updateTemplate,
  deleteTemplate,
} from '@/api/message'

// 邮件管理（issue #73，spec §6）：账号（渠道）/ 模板两个 tab。
// smtp provider 的账号表单为结构化字段（host/port/username/password/from/
// fromName/tls），提交时序列化进 config JSON，编辑时从列表行的 config 展开；
// 密码显示 ******，提交 ****** 表示保留原值（spec §4.3.6/§4.11）。
// 「测试发送」走 POST /message/channel/:id/test（仅平台租户），成功后可跳转发送日志。
// 页面与 tab 均固定 channelType=mail，提交时强制回填。

const router = useRouter()
const userStore = useUserStore()
const activeTab = ref('channel')

const canTest = computed(() => userStore.hasPerm('message:channel:update'))

const providerOptions = [
  { label: 'smtp（SMTP 邮件）', value: 'smtp' },
  { label: 'log（开发日志，不真正发送）', value: 'log' },
]

const tlsOptions = [
  { label: 'STARTTLS（推荐）', value: 'starttls' },
  { label: '无加密（明文，仅内网调试）', value: 'none' },
]

const channelColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'code', label: '账号编码', minWidth: 130 },
  { prop: 'name', label: '账号名称', minWidth: 140 },
  { prop: 'provider', label: 'Provider', width: 110 },
  { prop: 'server', label: 'SMTP 服务器', minWidth: 160, slot: true },
  { prop: 'from', label: '发件人', minWidth: 170, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const channelSearchItems = [
  { prop: 'code', label: '账号编码' },
  { prop: 'name', label: '账号名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const channelFormItems = [
  { prop: 'code', label: '账号编码', required: true, placeholder: '如 mail-smtp' },
  { prop: 'name', label: '账号名称', required: true },
  { prop: 'provider', label: 'Provider', type: 'select', options: providerOptions, required: true },
  { prop: 'smtpHost', label: 'SMTP 主机', placeholder: '如 smtp.example.com（仅 smtp 渠道使用）' },
  { prop: 'smtpPort', label: 'SMTP 端口', type: 'number' },
  { prop: 'smtpUsername', label: '用户名', placeholder: '匿名发送可留空' },
  { prop: 'smtpPassword', label: '密码/授权码', type: 'password', placeholder: '查询时显示 ******；提交 ****** 表示保留原值' },
  { prop: 'smtpFrom', label: '发件人地址', placeholder: 'noreply@example.com' },
  { prop: 'smtpFromName', label: '发件人名称', placeholder: '如 平台通知中心' },
  { prop: 'smtpTLS', label: '加密方式', type: 'select', options: tlsOptions },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const channelDefaultForm = { channelType: 'mail', provider: 'smtp', status: 0, smtpPort: 25, smtpTLS: 'starttls' }

const channelApi = {
  page: pageChannels,
  create: createChannel,
  update: updateChannel,
  remove: deleteChannel,
}

// flattenChannelRows 把列表行的 config JSON 展开为结构化字段：
// ProTable 的编辑弹窗直接取行数据，展开后表单即可回显（密码为 ******）。
function flattenChannelRows(rows: any[]) {
  for (const row of rows || []) {
    let cfg: Record<string, any> = {}
    try {
      cfg = JSON.parse(row.config || '{}') || {}
    } catch {
      cfg = {}
    }
    row.smtpHost = cfg.host ?? ''
    row.smtpPort = cfg.port ?? 25
    row.smtpUsername = cfg.username ?? ''
    row.smtpPassword = cfg.password ?? ''
    row.smtpFrom = cfg.from ?? ''
    row.smtpFromName = cfg.fromName ?? ''
    row.smtpTLS = cfg.tls ?? 'starttls'
  }
}

// serverText/fromText 是列表展示：log 渠道没有 SMTP 配置，显示 -。
function parseConfig(row: any): Record<string, any> {
  try {
    return JSON.parse(row.config || '{}') || {}
  } catch {
    return {}
  }
}

function serverText(row: any): string {
  if (row.provider !== 'smtp') return '-'
  const cfg = parseConfig(row)
  return cfg.host ? `${cfg.host}:${cfg.port ?? 25}` : '-'
}

function fromText(row: any): string {
  if (row.provider !== 'smtp') return '-'
  const cfg = parseConfig(row)
  return cfg.from || '-'
}

function beforeChannelSubmit(payload: any) {
  payload.channelType = 'mail'
  if (payload.provider === 'smtp') {
    const config: Record<string, any> = {}
    if (payload.smtpHost) config.host = String(payload.smtpHost).trim()
    if (Number(payload.smtpPort)) config.port = Number(payload.smtpPort)
    if (payload.smtpUsername) config.username = payload.smtpUsername
    if (payload.smtpPassword) config.password = payload.smtpPassword
    if (payload.smtpFrom) config.from = String(payload.smtpFrom).trim()
    if (payload.smtpFromName) config.fromName = payload.smtpFromName
    config.tls = payload.smtpTLS || 'starttls'
    payload.config = JSON.stringify(config)
  } else {
    payload.config = '{}'
  }
  // 结构化字段只存在于表单，提交前移除避免后端 DTO 收到未知字段。
  for (const key of ['smtpHost', 'smtpPort', 'smtpUsername', 'smtpPassword', 'smtpFrom', 'smtpFromName', 'smtpTLS']) {
    delete payload[key]
  }
  return payload
}

// ---------- 测试发送 ----------

const testVisible = ref(false)
const testing = ref(false)
const templateOptions = ref<any[]>([])
const testForm = reactive({
  id: 0,
  code: '',
  name: '',
  recipient: '',
  templateCode: '',
  paramsText: '',
})

async function openTest(row: any) {
  testForm.id = row.id
  testForm.code = row.code
  testForm.name = row.name
  testForm.recipient = ''
  testForm.templateCode = ''
  testForm.paramsText = ''
  testVisible.value = true
  // 拉取启用中的邮件模板供选择（含模板参数提示）。
  try {
    const data = await pageTemplates({ pageNo: 1, pageSize: 100, channelType: 'mail', status: 0 })
    templateOptions.value = data.list || []
  } catch {
    templateOptions.value = []
  }
}

async function handleTest() {
  const recipient = (testForm.recipient || '').trim()
  if (!recipient) {
    ElMessage.warning('请输入收件人邮箱')
    return
  }
  if (!testForm.templateCode) {
    ElMessage.warning('请选择邮件模板')
    return
  }
  let params: Record<string, string> = {}
  const text = (testForm.paramsText || '').trim()
  if (text) {
    try {
      params = JSON.parse(text)
    } catch {
      ElMessage.warning('模板参数必须是合法的 JSON 对象')
      return
    }
  }
  testing.value = true
  try {
    await testChannel(testForm.id, { recipient, templateCode: testForm.templateCode, params })
    testVisible.value = false
    // 结果（成功/失败原因）都写入了发送日志，引导前往日志页核对。
    try {
      await ElMessageBox.confirm('测试发送已提交并写入发送日志，是否前往日志页查看结果？', '测试发送成功', {
        confirmButtonText: '查看日志',
        cancelButtonText: '留在本页',
        type: 'success',
      })
      router.push('/message/log')
    } catch {
      // 留在本页
    }
  } catch {
    // 失败提示（502 中文文案）已由请求拦截器弹出，弹窗保持打开便于修改重试
  } finally {
    testing.value = false
  }
}

// ---------- 模板 ----------

const templateColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'code', label: '模板编码', minWidth: 130 },
  { prop: 'name', label: '模板名称', minWidth: 140 },
  { prop: 'title', label: '邮件主题', minWidth: 160 },
  { prop: 'params', label: '参数', minWidth: 140, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const templateSearchItems = [
  { prop: 'code', label: '模板编码' },
  { prop: 'name', label: '模板名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const templateFormItems = [
  { prop: 'code', label: '模板编码', required: true, placeholder: '如 welcome' },
  { prop: 'name', label: '模板名称', required: true },
  { prop: 'title', label: '邮件主题', placeholder: '支持 {{name}} 占位' },
  {
    prop: 'content',
    label: '模板内容',
    type: 'textarea',
    required: true,
    placeholder: 'HTML 或纯文本（含典型标签按 HTML 发送），仅支持 {{name}} 占位',
  },
  { prop: 'params', label: '参数声明', placeholder: '逗号分隔，如 name,mobile' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const templateDefaultForm = { channelType: 'mail', status: 0, params: '' }

const templateApi = {
  page: pageTemplates,
  create: createTemplate,
  update: updateTemplate,
  remove: deleteTemplate,
}

function normalizeParams(value: any): string[] {
  if (Array.isArray(value)) return value.map((item) => String(item)).filter(Boolean)
  return String(value || '')
    .split(/[,，\s]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function beforeTemplateSubmit(payload: any) {
  payload.channelType = 'mail'
  payload.params = normalizeParams(payload.params)
  return payload
}
</script>

<style scoped>
.param-tag {
  margin-right: 4px;
}

.test-channel {
  margin-bottom: 14px;
}
</style>
