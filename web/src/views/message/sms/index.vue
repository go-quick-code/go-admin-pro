<template>
  <div class="app-container">
    <el-tabs v-model="activeTab">
      <el-tab-pane label="短信渠道" name="channel">
        <ProTable
          :columns="channelColumns"
          :form-items="channelFormItems"
          :search-items="channelSearchItems"
          :api="channelApi"
          :default-form="channelDefaultForm"
          :before-submit="beforeChannelSubmit"
          :extra-query="{ channelType: 'sms' }"
          perm-prefix="message:channel"
          title="短信渠道"
          dialog-width="680px"
          @loaded="flattenChannelRows"
        >
          <template #cell-status="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
              {{ row.status === 0 ? '启用' : '停用' }}
            </el-tag>
          </template>
          <template #cell-sign="{ row }">{{ signText(row) }}</template>
          <template #cell-endpoint="{ row }">{{ endpointText(row) }}</template>
          <template #actions="{ row }">
            <el-button v-if="canTest" link type="primary" @click="openTest(row)">测试发送</el-button>
          </template>
        </ProTable>
      </el-tab-pane>

      <el-tab-pane label="短信模板" name="template">
        <ProTable
          :columns="templateColumns"
          :form-items="templateFormItems"
          :search-items="templateSearchItems"
          :api="templateApi"
          :default-form="templateDefaultForm"
          :before-submit="beforeTemplateSubmit"
          :extra-query="{ channelType: 'sms' }"
          perm-prefix="message:template"
          title="短信模板"
          dialog-width="720px"
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

    <!-- 渠道测试发送弹窗（issue #74）：向手机号投递一条真实短信并写发送日志 -->
    <el-dialog v-model="testVisible" title="测试发送" width="520px" destroy-on-close append-to-body>
      <el-descriptions :column="1" border size="small" class="test-channel">
        <el-descriptions-item label="渠道">{{ testForm.name }}（{{ testForm.code }}）</el-descriptions-item>
      </el-descriptions>
      <el-form label-width="90px">
        <el-form-item label="手机号" required>
          <el-input v-model="testForm.recipient" clearable placeholder="13800001234" />
        </el-form-item>
        <el-form-item label="短信模板" required>
          <el-select v-model="testForm.templateCode" filterable style="width: 100%" placeholder="请选择启用的短信模板">
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

// 短信管理（issue #74，spec §4.3）：渠道 / 模板两个 tab。
// aliyun_sms provider 的渠道表单为结构化字段（accessKeyId/accessKeySecret/
// signName/regionId/endpoint），提交时序列化进 config JSON，编辑时从列表行的
// config 展开；accessKeySecret 显示 ******，提交 ****** 表示保留原值
// （spec §4.3.6/§4.11）。log provider 只用于开发调试，无配置项。
// aliyun_sms 的模板编码直接填阿里云控制台的模板编码（SMS_ 开头）。
// 「测试发送」走 POST /message/channel/:id/test（仅平台租户），成功后可跳转发送日志。
// 页面与 tab 均固定 channelType=sms，提交时强制回填。

const router = useRouter()
const userStore = useUserStore()
const activeTab = ref('channel')

const canTest = computed(() => userStore.hasPerm('message:channel:update'))

const providerOptions = [
  { label: 'aliyun_sms（阿里云短信）', value: 'aliyun_sms' },
  { label: 'log（开发日志，不真正发送）', value: 'log' },
]

const channelColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'code', label: '渠道编码', minWidth: 130 },
  { prop: 'name', label: '渠道名称', minWidth: 140 },
  { prop: 'provider', label: 'Provider', width: 120 },
  { prop: 'sign', label: '短信签名', minWidth: 130, slot: true },
  { prop: 'endpoint', label: '接入点', minWidth: 200, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const channelSearchItems = [
  { prop: 'code', label: '渠道编码' },
  { prop: 'name', label: '渠道名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const channelFormItems = [
  { prop: 'code', label: '渠道编码', required: true, placeholder: '如 sms-aliyun' },
  { prop: 'name', label: '渠道名称', required: true },
  { prop: 'provider', label: 'Provider', type: 'select', options: providerOptions, required: true },
  { prop: 'accessKeyId', label: 'AccessKey ID', placeholder: '阿里云 AccessKey ID（仅 aliyun_sms 渠道使用）' },
  { prop: 'accessKeySecret', label: 'AccessKey Secret', type: 'password', placeholder: '查询时显示 ******；提交 ****** 表示保留原值' },
  { prop: 'signName', label: '短信签名', placeholder: '已报备的签名，如 平台通知' },
  { prop: 'regionId', label: 'Region ID', placeholder: '缺省 cn-hangzhou' },
  { prop: 'endpoint', label: '接入点', placeholder: '缺省 https://dysmsapi.aliyuncs.com' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const channelDefaultForm = {
  channelType: 'sms',
  provider: 'aliyun_sms',
  status: 0,
  regionId: 'cn-hangzhou',
  endpoint: 'https://dysmsapi.aliyuncs.com',
}

const channelApi = {
  page: pageChannels,
  create: createChannel,
  update: updateChannel,
  remove: deleteChannel,
}

// flattenChannelRows 把列表行的 config JSON 展开为结构化字段：
// ProTable 的编辑弹窗直接取行数据，展开后表单即可回显（密钥为 ******）。
function flattenChannelRows(rows: any[]) {
  for (const row of rows || []) {
    const cfg = parseConfig(row)
    row.accessKeyId = cfg.accessKeyId ?? ''
    row.accessKeySecret = cfg.accessKeySecret ?? ''
    row.signName = cfg.signName ?? ''
    row.regionId = cfg.regionId ?? 'cn-hangzhou'
    row.endpoint = cfg.endpoint ?? 'https://dysmsapi.aliyuncs.com'
  }
}

// signText/endpointText 是列表展示：log 渠道没有阿里云配置，显示 -。
function parseConfig(row: any): Record<string, any> {
  try {
    return JSON.parse(row.config || '{}') || {}
  } catch {
    return {}
  }
}

function signText(row: any): string {
  if (row.provider !== 'aliyun_sms') return '-'
  return parseConfig(row).signName || '-'
}

function endpointText(row: any): string {
  if (row.provider !== 'aliyun_sms') return '-'
  return parseConfig(row).endpoint || 'https://dysmsapi.aliyuncs.com'
}

function beforeChannelSubmit(payload: any) {
  payload.channelType = 'sms'
  if (payload.provider === 'aliyun_sms') {
    const config: Record<string, any> = {}
    if (payload.accessKeyId) config.accessKeyId = String(payload.accessKeyId).trim()
    if (payload.accessKeySecret) config.accessKeySecret = payload.accessKeySecret
    if (payload.signName) config.signName = String(payload.signName).trim()
    if (payload.regionId) config.regionId = String(payload.regionId).trim()
    if (payload.endpoint) config.endpoint = String(payload.endpoint).trim()
    payload.config = JSON.stringify(config)
  } else {
    payload.config = '{}'
  }
  // 结构化字段只存在于表单，提交前移除避免后端 DTO 收到未知字段。
  for (const key of ['accessKeyId', 'accessKeySecret', 'signName', 'regionId', 'endpoint']) {
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
  // 拉取启用中的短信模板供选择（含模板参数提示）。
  try {
    const data = await pageTemplates({ pageNo: 1, pageSize: 100, channelType: 'sms', status: 0 })
    templateOptions.value = data.list || []
  } catch {
    templateOptions.value = []
  }
}

async function handleTest() {
  const recipient = (testForm.recipient || '').trim()
  if (!recipient) {
    ElMessage.warning('请输入接收手机号')
    return
  }
  if (!testForm.templateCode) {
    ElMessage.warning('请选择短信模板')
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
  { prop: 'code', label: '模板编码', minWidth: 150 },
  { prop: 'name', label: '模板名称', minWidth: 140 },
  { prop: 'content', label: '模板内容', minWidth: 200 },
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
  { prop: 'code', label: '模板编码', required: true, placeholder: '如 SMS_154950909（aliyun_sms 用阿里云模板编码）' },
  { prop: 'name', label: '模板名称', required: true },
  {
    prop: 'content',
    label: '模板内容',
    type: 'textarea',
    required: true,
    placeholder: '仅支持 {{name}} 占位，如：您好 {{name}}，欢迎使用。',
  },
  { prop: 'params', label: '参数声明', placeholder: '逗号分隔，如 name,mobile' },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const templateDefaultForm = { channelType: 'sms', status: 0, params: '' }

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
  payload.channelType = 'sms'
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
