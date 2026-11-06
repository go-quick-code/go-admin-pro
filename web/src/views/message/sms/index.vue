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
        >
          <template #cell-status="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">
              {{ row.status === 0 ? '启用' : '停用' }}
            </el-tag>
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
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import ProTable from '@/components/ProTable.vue'
import { formatTime, statusOptions } from '@/utils/format'
import {
  pageChannels,
  createChannel,
  updateChannel,
  deleteChannel,
  pageTemplates,
  createTemplate,
  updateTemplate,
  deleteTemplate,
} from '@/api/message'

// 短信管理（issue #72，spec §6）：渠道 / 模板两个 tab。
// 渠道与模板是平台级资源，非平台租户在后端 service 层直接 403。
// 页面与 tab 均固定 channelType=sms；提交时强制回填，避免误改类型。

const activeTab = ref('channel')

const providerOptions = [
  { label: 'log（开发日志，不真正发送）', value: 'log' },
  { label: 'aliyun_sms（阿里云短信）', value: 'aliyun_sms' },
  { label: 'smtp（邮件）', value: 'smtp' },
]

const channelColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'code', label: '渠道编码', minWidth: 130 },
  { prop: 'name', label: '渠道名称', minWidth: 140 },
  { prop: 'provider', label: 'Provider', width: 130 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 160 },
  { prop: 'createTime', label: '创建时间', minWidth: 170, formatter: (row: any) => formatTime(row.createTime) },
]

const channelSearchItems = [
  { prop: 'code', label: '渠道编码' },
  { prop: 'name', label: '渠道名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const channelFormItems = [
  { prop: 'code', label: '渠道编码', required: true, placeholder: '如 sms-log' },
  { prop: 'name', label: '渠道名称', required: true },
  { prop: 'provider', label: 'Provider', type: 'select', options: providerOptions, required: true },
  {
    prop: 'config',
    label: '配置 JSON',
    type: 'textarea',
    placeholder: 'provider 配置 JSON，密钥字段查询时显示 ******；提交 ****** 表示保留原值',
  },
  { prop: 'status', label: '状态', type: 'radio', options: statusOptions },
  { prop: 'remark', label: '备注', type: 'textarea' },
]

const channelDefaultForm = { channelType: 'sms', provider: 'log', status: 0, config: '{}' }

const channelApi = {
  page: pageChannels,
  create: createChannel,
  update: updateChannel,
  remove: deleteChannel,
}

function beforeChannelSubmit(payload: any) {
  payload.channelType = 'sms'
  return payload
}

const templateColumns = [
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'code', label: '模板编码', minWidth: 130 },
  { prop: 'name', label: '模板名称', minWidth: 140 },
  { prop: 'params', label: '参数', minWidth: 140, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'remark', label: '备注', minWidth: 140 },
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
</style>
