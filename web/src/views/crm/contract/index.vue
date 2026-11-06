<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :form-items="formItems"
      :search-items="searchItems"
      :api="api"
      :default-form="defaultForm"
      :before-submit="normalizeSubmit"
      perm-prefix="crm:contract"
      title="合同"
      :action-width="220"
      dialog-width="900px"
      @row-click="openDetail"
    >
      <template #toolbar>
        <!-- Excel 导出（issue #20）：挂 crm:contract:export，复用当前列表筛选；
             后端再套数据范围谓词，导出内容与列表可见范围一致。 -->
        <ExcelActions
          title="合同"
          export-perm="crm:contract:export"
          :export-action="exportContracts"
          :export-params="contractExportParams"
        />
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="statusTagType(row.status, contractStatusTagTypes)" size="small">{{ statusLabel(row.status) }}</el-tag>
      </template>
      <template #cell-amount="{ row }">{{ row.amount ?? '0.00' }}</template>
      <template #cell-signDate="{ row }">{{ formatDate(row.signDate) }}</template>
      <template #cell-ownerUserName="{ row }">
        <span v-if="row.ownerUserName">{{ row.ownerUserName }}（{{ row.ownerDeptName || '-' }}）</span>
        <span v-else>-</span>
      </template>
      <template #actions="{ row }">
        <el-button link type="primary" @click.stop="openDetail(row)">详情</el-button>
        <el-button
          v-if="transitionsFor(row).length"
          link
          type="warning"
          @click.stop="openStatus(row)"
        >
          状态
        </el-button>
      </template>

      <!-- 新建：客户必选，来源商机可空（选了则须与客户一致）；编辑：客户/商机不可改。 -->
      <template #form-customerId="{ form }">
        <el-select
          v-model="form.customerId"
          filterable
          style="width: 100%"
          placeholder="请选择所属客户"
          @change="loadOpportunities"
        >
          <el-option v-for="c in customers" :key="c.id" :label="c.name" :value="c.id" />
        </el-select>
      </template>
      <template #form-customerName="{ form }">
        <span>{{ form.customerName || '-' }}</span>
      </template>
      <template #form-opportunityId="{ form }">
        <el-select
          v-model="form.opportunityId"
          filterable
          clearable
          style="width: 100%"
          placeholder="可不选；选择后客户须与商机一致"
        >
          <el-option v-for="o in opportunities" :key="o.id" :label="o.name" :value="o.id" />
        </el-select>
      </template>
      <template #form-opportunityName="{ form }">
        <span>{{ form.opportunityName || '-' }}</span>
      </template>
      <!-- 金额：有行项目时由后端按 SUM(行项目金额) 计算并忽略手填；已签合同金额锁定。 -->
      <template #form-amount="{ form }">
        <el-input
          v-if="!isSigned(form)"
          v-model="form.amount"
          clearable
          placeholder="0.00（元；有行项目时忽略，可留空）"
        />
        <span v-else>{{ form.amount ?? '0.00' }} 元（已签锁定）</span>
      </template>
      <template #form-signDate="{ form }">
        <el-date-picker
          v-model="form.signDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择签订日期"
        />
      </template>
      <template #form-startDate="{ form }">
        <el-date-picker
          v-model="form.startDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择生效日期"
        />
      </template>
      <template #form-endDate="{ form }">
        <el-date-picker
          v-model="form.endDate"
          type="date"
          value-format="YYYY-MM-DD"
          clearable
          style="width: 100%"
          placeholder="请选择失效日期"
        />
      </template>
      <!-- 行项目：已签合同锁定（后端拒绝修改行项目），其余状态可增删行。
           金额与合计本地即时展示；落库金额由后端按 数量×单价 计算。 -->
      <template #form-products="{ form }">
        <el-table
          v-if="!isSigned(form)"
          :data="form.products"
          border
          size="small"
          class="line-table"
        >
          <el-table-column label="产品" min-width="180">
            <template #default="{ $index }">
              <el-select
                v-model="form.products[$index].productId"
                filterable
                placeholder="请选择产品"
                style="width: 100%"
              >
                <el-option
                  v-for="p in products"
                  :key="p.id"
                  :label="`${p.name}（${p.price}）`"
                  :value="p.id"
                />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="数量" width="130">
            <template #default="{ $index }">
              <el-input-number
                v-model="form.products[$index].quantity"
                :min="1"
                :step="1"
                :precision="0"
                controls-position="right"
                style="width: 100%"
              />
            </template>
          </el-table-column>
          <el-table-column label="单价（元）" width="150">
            <template #default="{ $index }">
              <el-input v-model="form.products[$index].price" clearable placeholder="0.00" />
            </template>
          </el-table-column>
          <el-table-column label="金额（元）" width="120">
            <template #default="{ $index }">
              <span>{{ lineAmount(form.products[$index]) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="70">
            <template #default="{ $index }">
              <el-button link type="danger" @click="form.products.splice($index, 1)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <template v-else>
          <el-table v-if="form.products?.length" :data="form.products" border size="small">
            <el-table-column prop="productName" label="产品" min-width="160" />
            <el-table-column prop="quantity" label="数量" width="90" />
            <el-table-column prop="price" label="单价（元）" width="120" />
            <el-table-column prop="amount" label="金额（元）" width="120" />
          </el-table>
          <el-empty v-else description="暂无行项目" :image-size="60" />
        </template>
        <div v-if="!isSigned(form)" class="line-footer">
          <el-button size="small" @click="addLine(form)">添加行</el-button>
          <span class="line-total">合计：{{ formTotal(form.products) }}</span>
        </div>
      </template>
    </ProTable>

    <!-- 合同详情抽屉：基本信息 + 产品行项目 + 跟进记录 tab（跟进可见性跟随合同负责人）。 -->
    <el-drawer v-model="detailVisible" :title="detail?.name || '合同详情'" size="900px">
      <el-tabs v-if="detail" v-model="detailTab">
        <el-tab-pane label="基本信息" name="info">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="合同编号">{{ detail.contractNo }}</el-descriptions-item>
            <el-descriptions-item label="合同名称">{{ detail.name }}</el-descriptions-item>
            <el-descriptions-item label="状态">
              <el-tag :type="statusTagType(detail.status, contractStatusTagTypes)" size="small">{{ statusLabel(detail.status) }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="所属客户">{{ detail.customerName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="来源商机">{{ detail.opportunityName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="负责人">
              {{ detail.ownerUserName || '-' }}{{ detail.ownerDeptName ? `（${detail.ownerDeptName}）` : '' }}
            </el-descriptions-item>
            <el-descriptions-item label="合同金额">{{ detail.amount ?? '0.00' }} 元</el-descriptions-item>
            <el-descriptions-item label="行项目合计">{{ detail.totalAmount ?? '0.00' }} 元</el-descriptions-item>
            <el-descriptions-item label="签订日期">{{ formatDate(detail.signDate) }}</el-descriptions-item>
            <el-descriptions-item label="生效日期">{{ formatDate(detail.startDate) }}</el-descriptions-item>
            <el-descriptions-item label="失效日期">{{ formatDate(detail.endDate) }}</el-descriptions-item>
            <el-descriptions-item v-if="detail.rejectReason" label="驳回原因">
              {{ detail.rejectReason }}
            </el-descriptions-item>
            <el-descriptions-item label="备注">{{ detail.remark || '-' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detail.createTime) }}</el-descriptions-item>
            <el-descriptions-item label="更新时间">{{ formatTime(detail.updateTime) }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>
        <el-tab-pane label="产品行项目" name="products">
          <el-table v-if="detail.products?.length" :data="detail.products" border size="small">
            <el-table-column prop="productName" label="产品" min-width="160" />
            <el-table-column prop="quantity" label="数量" width="90" />
            <el-table-column prop="price" label="单价（元）" width="120" />
            <el-table-column prop="amount" label="金额（元）" width="120" />
          </el-table>
          <el-empty v-else description="暂无行项目" :image-size="60" />
          <div v-if="detail.products?.length" class="line-footer">
            <span class="line-total">合计：{{ detail.totalAmount ?? '0.00' }} 元</span>
          </div>
        </el-tab-pane>
        <!-- 审批记录（issue #47）：合同详情展示 bpm 时间线与当前待办入口；
             审批动作统一在待办中心完成，合同页不再内嵌审批按钮。 -->
        <el-tab-pane label="审批记录" name="approval">
          <template v-if="detail.approval">
            <div class="approval-head">
              <span>
                流程状态：
                <el-tag :type="instanceStatusTag(detail.approval.instance.status)" size="small">
                  {{ instanceStatusText(detail.approval.instance.status) }}
                </el-tag>
              </span>
              <el-button
                v-if="pendingApprovalTasks.length"
                type="primary"
                size="small"
                @click="goTaskCenter"
              >
                前往待办中心
              </el-button>
            </div>
            <div v-if="pendingApprovalTasks.length" class="approval-section">
              <h4 class="approval-title">当前待办</h4>
              <el-table :data="pendingApprovalTasks" border size="small">
                <el-table-column prop="nodeName" label="节点" min-width="140" />
                <el-table-column label="办理人" min-width="140">
                  <template #default="{ row }">
                    {{ row.assigneeName || `用户#${row.assigneeId}` }}
                  </template>
                </el-table-column>
                <el-table-column label="到达时间" min-width="170">
                  <template #default="{ row }">{{ formatTime(row.createTime) }}</template>
                </el-table-column>
              </el-table>
            </div>
            <div class="approval-section">
              <h4 class="approval-title">审批时间线</h4>
              <el-timeline v-if="detail.approval.records.length">
                <el-timeline-item
                  v-for="rec in detail.approval.records"
                  :key="rec.id"
                  :timestamp="formatTime(rec.createTime)"
                  :type="recordTag(rec.action) === 'success' ? 'success' : recordTag(rec.action) === 'danger' ? 'danger' : 'primary'"
                >
                  <div>
                    <el-tag :type="recordTag(rec.action)" size="small">{{ recordText(rec.action) }}</el-tag>
                    <span class="timeline-operator">{{ rec.operatorName || `用户#${rec.operatorId}` }}</span>
                  </div>
                  <div class="timeline-node">{{ rec.nodeName || '（实例级动作）' }}</div>
                  <div v-if="rec.comment" class="timeline-comment">{{ rec.comment }}</div>
                </el-timeline-item>
              </el-timeline>
              <el-empty v-else description="暂无审批记录（等待首个审批动作）" :image-size="60" />
            </div>
          </template>
          <el-empty v-else description="尚未发起审批" :image-size="60" />
        </el-tab-pane>
        <el-tab-pane label="跟进记录" name="follow-ups">
          <ProTable
            :columns="followUpColumns"
            :form-items="followUpFormItems"
            :search-items="followUpSearchItems"
            :api="followUpApi"
            :extra-query="followUpExtraQuery"
            :default-form="followUpDefaultForm"
            :before-submit="injectContractId"
            perm-prefix="crm:follow-up"
            title="跟进记录"
          >
            <template #cell-type="{ row }">
              <el-tag size="small" :type="followUpTypeTag(row.type)">{{ followUpTypeLabel(row.type) }}</el-tag>
            </template>
            <template #cell-nextFollowTime="{ row }">
              <span>{{ row.nextFollowTime ? formatTime(row.nextFollowTime) : '-' }}</span>
            </template>
          </ProTable>
        </el-tab-pane>
        <!-- 附件（issue #17）：复用 infra_file，权限跟随合同。
             无 crm:contract:update 时隐藏上传与删除；作废合同不再接受新材料。 -->
        <el-tab-pane label="附件" name="files">
          <div class="file-toolbar">
            <el-upload
              v-if="canUploadFile"
              :show-file-list="false"
              :http-request="handleFileUpload"
              :accept="fileAccept"
            >
              <el-button type="primary">上传附件</el-button>
            </el-upload>
            <span class="file-tip">支持图片、文档、压缩包等，单文件不超过 20MB</span>
          </div>
          <el-table v-if="contractFiles.length" :data="contractFiles" border size="small">
            <el-table-column label="文件名" min-width="220">
              <template #default="{ row }">
                <el-button link type="primary" class="file-link" @click="downloadFile(row)">
                  {{ row.name }}
                </el-button>
              </template>
            </el-table-column>
            <el-table-column label="大小" width="110">
              <template #default="{ row }">{{ formatSize(row.size) }}</template>
            </el-table-column>
            <el-table-column label="上传时间" min-width="170">
              <template #default="{ row }">{{ formatTime(row.createTime) }}</template>
            </el-table-column>
            <el-table-column v-if="canUpdateContract" label="操作" width="90">
              <template #default="{ row }">
                <el-button link type="danger" @click="removeContractFile(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-else description="暂无附件" :image-size="60" />
        </el-tab-pane>
      </el-tabs>
      <template #footer>
        <el-button @click="detailVisible = false">关闭</el-button>
        <el-button
          v-if="detail && transitionsFor(detail).length"
          type="primary"
          @click="openStatus(detail)"
        >
          状态流转
        </el-button>
      </template>
    </el-drawer>

    <!-- 状态流转对话框：草稿→待审（提交审批）、任意非作废→作废（终态）。
         审批通过/驳回统一在 bpm 待办中心办理（issue #47），此处不再提供审批按钮。 -->
    <el-dialog v-model="statusVisible" title="合同状态流转" width="460px" destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="合同">
          <span>{{ statusRow?.name }}</span>
        </el-form-item>
        <el-form-item label="当前状态">
          <el-tag :type="statusTagType(statusRow?.status, contractStatusTagTypes)" size="small">
            {{ statusLabel(statusRow?.status) }}
          </el-tag>
        </el-form-item>
        <el-form-item label="目标状态">
          <el-radio-group v-model="statusTo">
            <el-radio v-for="opt in transitionOptions" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-alert
          v-if="statusTo === 2"
          type="info"
          :closable="false"
          title="提交后生成 bpm 待办，审批通过/驳回请在「流程中心 → 待办任务」办理"
        />
        <el-alert
          v-if="statusTo === 4"
          type="warning"
          :closable="false"
          title="作废后合同不可再流转、不可编辑（终态）；待审合同的审批流程将自动终止"
        />
      </el-form>
      <template #footer>
        <el-button @click="statusVisible = false">取消</el-button>
        <el-button type="primary" :disabled="!statusTo" @click="confirmStatus">
          确定
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'
import ProTable from '@/components/ProTable.vue'
import ExcelActions from '@/components/ExcelActions.vue'
import {
  formatTime,
  formatSize,
  formatDate,
  dictLabel,
  statusTagType,
  type StatusTagType,
} from '@/utils/format'
import { saveBlobAs } from '@/utils/download'
import {
  pageContracts,
  getContract,
  createContract,
  updateContract,
  deleteContract,
  changeContractStatus,
  listContractFiles,
  uploadContractFile,
  deleteContractFile,
  downloadContractFile,
  exportContracts,
  pageCustomers,
  pageOpportunities,
  pageProducts,
  pageFollowUps,
  getFollowUp,
  createFollowUp,
  updateFollowUp,
  deleteFollowUp,
} from '@/api/crm'
import { useUserStore } from '@/stores/user'

// 合同状态对齐规格（docs/crm-mvp-spec.md §4.4 / oa-mvp-spec §4.5）：
// 1 草稿 → 2 待审（提交，生成 bpm 待办）→ 3 已签（审批通过）/ 1 草稿（驳回）；
// 4 作废（终态）。审批结果由 bpm 回调回写，页面只展示状态与时间线。
const statusOptions = [
  { label: '草稿', value: 1 },
  { label: '待审', value: 2 },
  { label: '已签', value: 3 },
  { label: '作废', value: 4 },
]

const contractStatusTagTypes: Record<number, StatusTagType> = {
  2: 'warning',
  3: 'success',
  4: 'danger',
}

// 跟进类型对齐芋道字典语义（issue #12 同款）
const followUpTypeOptions = [
  { label: '电话', value: 1 },
  { label: '拜访', value: 2 },
  { label: '微信', value: 3 },
  { label: '邮件', value: 4 },
  { label: '其他', value: 5 },
]

function statusLabel(status?: number) {
  return dictLabel(statusOptions, status)
}

function isSigned(form: any) {
  return form?.status === 3
}

function followUpTypeLabel(type: number) {
  return dictLabel(followUpTypeOptions, type)
}

function followUpTypeTag(type: number) {
  if (type === 1) return 'primary'
  if (type === 2) return 'success'
  return 'info'
}

// ---------- 行项目本地金额计算（仅展示；落库金额由后端 数量×单价 计算） ----------
function parseMoney(text: any): number {
  if (typeof text !== 'string' || text.trim() === '') return 0
  const n = Number(text)
  return Number.isFinite(n) ? n : 0
}

function lineAmount(line: any): string {
  if (!line?.quantity || !line?.price) return '0.00'
  return (line.quantity * parseMoney(line.price)).toFixed(2)
}

function formTotal(lines: any[]): string {
  if (!lines?.length) return '0.00'
  return lines.reduce((sum, l) => sum + parseMoney(lineAmount(l)), 0).toFixed(2)
}

function addLine(form: any) {
  if (!Array.isArray(form.products)) form.products = []
  form.products.push({ productId: undefined, quantity: 1, price: '0.00' })
}

// ---------- 状态流转选项：按当前状态 + 权限码过滤 ----------
// 用户动作只有提交审批与作废（issue #47）：审批通过与驳回是 bpm 待办动作，
// 不在合同页内嵌；提交后待办由流程中心「待办任务」统一办理。
const userStore = useUserStore()

function transitionsFor(row: any) {
  const options: { label: string; value: number; perm: string }[] = []
  if (row?.status === 1) {
    options.push({ label: '提交审批', value: 2, perm: 'crm:contract:update' })
    options.push({ label: '作废', value: 4, perm: 'crm:contract:update' })
  } else if (row?.status === 2) {
    options.push({ label: '作废', value: 4, perm: 'crm:contract:update' })
  } else if (row?.status === 3) {
    options.push({ label: '作废', value: 4, perm: 'crm:contract:update' })
  }
  return options.filter((opt) => userStore.hasPerm(opt.perm))
}

const tableRef = ref()
const customers = ref<any[]>([])
const products = ref<any[]>([])
const opportunities = ref<any[]>([])

// 合同导出复用当前列表筛选（空值由 ExcelActions 剔除）。
function contractExportParams() {
  const q = tableRef.value?.query || {}
  return {
    name: q.name,
    status: q.status,
    customerId: q.customerId,
  }
}

const searchItems = [
  { prop: 'name', label: '合同名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const columns = [
  { prop: 'contractNo', label: '合同编号', width: 150 },
  { prop: 'name', label: '合同名称', minWidth: 160 },
  { prop: 'customerName', label: '所属客户', minWidth: 140 },
  { prop: 'amount', label: '合同金额（元）', width: 130, slot: true },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'signDate', label: '签订日期', width: 120, slot: true },
  { prop: 'ownerUserName', label: '负责人', minWidth: 150, slot: true },
  {
    prop: 'createTime',
    label: '创建时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const formItems = [
  { prop: 'customerId', label: '所属客户', slot: true, requiredOn: 'create', visibleOn: 'create' },
  { prop: 'customerName', label: '所属客户', slot: true, visibleOn: 'update' },
  { prop: 'opportunityId', label: '来源商机', slot: true, visibleOn: 'create' },
  { prop: 'opportunityName', label: '来源商机', slot: true, visibleOn: 'update' },
  { prop: 'name', label: '合同名称', required: true },
  { prop: 'amount', label: '合同金额', slot: true },
  { prop: 'signDate', label: '签订日期', slot: true },
  { prop: 'startDate', label: '生效日期', slot: true },
  { prop: 'endDate', label: '失效日期', slot: true },
  { prop: 'remark', label: '备注', type: 'textarea' },
  { prop: 'products', label: '产品行项目', slot: true },
]

const defaultForm = { products: [] }

const api = {
  page: pageContracts,
  detail: getContract,
  create: createContract,
  update: updateContract,
  remove: deleteContract,
}

// 提交前规整：金额空串省略（后端 money 只收十进制字符串或省略字段）；
// 已签合同行项目与金额锁定（后端拒绝 products、忽略 amount），编辑只提交元数据；
// 行项目裁剪为最小结构并兜底数量/单价（编辑回显带出的展示字段一并剔除）。
function normalizeSubmit(payload: any) {
  const out: any = { ...payload }
  if (out.amount === '' || out.amount == null) delete out.amount
  if (out.status === 3) {
    delete out.products
    delete out.amount
  } else if (Array.isArray(out.products)) {
    out.products = out.products
      .filter((p: any) => p.productId)
      .map((p: any) => ({
        productId: p.productId,
        quantity: p.quantity && p.quantity > 0 ? p.quantity : 1,
        price: p.price === '' || p.price == null ? '0.00' : p.price,
      }))
  }
  return out
}

// 来源商机下拉：随所选客户加载（空客户无候选；后端仍会校验商机与客户一致）。
async function loadOpportunities(customerId?: number) {
  if (!customerId) {
    opportunities.value = []
    return
  }
  const page = await pageOpportunities({ customerId, pageNo: 1, pageSize: 200 })
  opportunities.value = page.list || []
}

const detail = ref<any>(null)
const detailVisible = ref(false)
const detailTab = ref('info')
const contractFiles = ref<any[]>([])
const fileAccept =
  '.jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.txt,.md,.csv,.json,.zip,.rar,.7z,.mp4,.mp3'
const canUpdateContract = computed(() => userStore.hasPerm('crm:contract:update'))
const canUploadFile = computed(() => canUpdateContract.value && detail.value?.status !== 4)

async function openDetail(row: any) {
  detail.value = await getContract(row.id)
  detailTab.value = 'info'
  detailVisible.value = true
  await loadContractFiles(row.id)
}

// ---------- 附件 tab（issue #17）：权限跟随合同，上传/删除挂 crm:contract:update ----------
async function loadContractFiles(contractId: number) {
  contractFiles.value = (await listContractFiles(contractId)) || []
}

// 附件下载走鉴权端点（后端返回的 url 已含 /admin-api 前缀）：
// 带 Authorization 取 blob，按 Content-Disposition 的原文件名保存。
async function downloadFile(row: any) {
  const response = await downloadContractFile(row.url)
  saveBlobAs(response, row.name)
}

async function handleFileUpload(options: any) {
  if (!detail.value) return
  await uploadContractFile(detail.value.id, options.file)
  ElMessage.success('附件上传成功')
  await loadContractFiles(detail.value.id)
}

async function removeContractFile(row: any) {
  if (!detail.value) return
  const confirmed = await ElMessageBox.confirm(
    `确认删除附件「${row.name}」？删除后不可恢复。`,
    '删除附件',
    { type: 'warning' },
  ).catch(() => false)
  if (!confirmed) return
  await deleteContractFile(detail.value.id, row.id)
  ElMessage.success('附件已删除')
  await loadContractFiles(detail.value.id)
}

// ---------- 审批记录 tab（issue #47）：bpm 时间线与当前待办入口 ----------
const pendingApprovalTasks = computed(() =>
  ((detail.value?.approval?.tasks || []) as any[]).filter((task) => task.status === 0),
)

function instanceStatusText(status: number): string {
  return ['运行中', '通过', '驳回', '撤回'][status] ?? `未知(${status})`
}
function instanceStatusTag(status: number): string {
  if (status === 1) return 'success'
  if (status === 2 || status === 3) return 'danger'
  return 'primary'
}
function recordText(action: number): string {
  return ['通过', '驳回', '撤回', '转办'][action - 1] ?? `未知(${action})`
}
function recordTag(action: number): string {
  if (action === 1) return 'success'
  if (action === 2) return 'danger'
  return 'warning'
}

const router = useRouter()
const route = useRoute()
function goTaskCenter() {
  router.push({ path: '/bpm/task', query: { tab: 'todo' } })
}

// ---------- 状态流转（用户唯一状态入口 PUT /crm/contract/status） ----------
const statusVisible = ref(false)
const statusRow = ref<any>(null)
const statusTo = ref<number>()
const transitionOptions = computed(() => transitionsFor(statusRow.value))

function openStatus(row: any) {
  statusRow.value = row
  statusTo.value = undefined
  statusVisible.value = true
}

async function confirmStatus() {
  if (!statusTo.value || !statusRow.value) return
  await changeContractStatus(statusRow.value.id, statusTo.value)
  ElMessage.success(`已变更为「${statusLabel(statusTo.value)}」`)
  statusVisible.value = false
  tableRef.value?.reload()
  if (detailVisible.value && detail.value?.id === statusRow.value.id) {
    detail.value = await getContract(statusRow.value.id)
  }
}

// ---------- 跟进记录 tab：列表范围锁定当前合同，可见性跟随合同负责人 ----------
const followUpColumns = [
  { prop: 'type', label: '类型', width: 90, slot: true },
  { prop: 'content', label: '内容', minWidth: 220 },
  { prop: 'nextFollowTime', label: '下次跟进时间', minWidth: 170, slot: true },
  {
    prop: 'createTime',
    label: '跟进时间',
    minWidth: 170,
    formatter: (row: any) => formatTime(row.createTime),
  },
]

const followUpSearchItems = [
  { prop: 'type', label: '类型', type: 'select', options: followUpTypeOptions },
]

const followUpFormItems = [
  { prop: 'type', label: '类型', type: 'select', options: followUpTypeOptions },
  { prop: 'content', label: '内容', required: true, type: 'textarea' },
  { prop: 'nextFollowTime', label: '下次跟进时间', type: 'datetime' },
]

const followUpDefaultForm = { type: 5 }

const followUpExtraQuery = computed(() => (detail.value ? { contractId: detail.value.id } : {}))

// 新建跟进时注入所属合同（customerId/opportunityId/contractId 三选一）。
function injectContractId(payload: any, mode: 'create' | 'update') {
  if (mode === 'create' && detail.value) {
    return { ...payload, contractId: detail.value.id }
  }
  return payload
}

const followUpApi = {
  page: pageFollowUps,
  detail: getFollowUp,
  create: createFollowUp,
  update: updateFollowUp,
  remove: deleteFollowUp,
}

onMounted(async () => {
  // 客户与产品下拉：租户内全量（v1 规模可控，取前 200 条）。
  const [custPage, prodPage] = await Promise.all([
    pageCustomers({ pageNo: 1, pageSize: 200 }),
    pageProducts({ pageNo: 1, pageSize: 200 }),
  ])
  customers.value = custPage.list || []
  products.value = prodPage.list || []
  // 待办中心「查看业务」跳转：/crm/contract?id=<合同ID> 直接打开详情抽屉。
  const queryId = typeof route.query.id === 'string' ? Number(route.query.id) : 0
  if (queryId > 0) {
    await openDetail({ id: queryId })
  }
})
</script>

<style scoped>
.line-table {
  width: 100%;
}
.line-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
}
.line-total {
  font-weight: 600;
  color: var(--el-color-danger);
}
.file-toolbar {
  display: flex;
  align-items: center;
  margin-bottom: 10px;
}
.file-tip {
  color: #909399;
  font-size: 12px;
  margin-left: 10px;
}
.file-link {
  color: #409eff;
}
.approval-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}
.approval-section {
  margin-bottom: 18px;
}
.approval-title {
  margin: 0 0 8px;
}
.timeline-operator {
  margin-left: 8px;
  font-weight: 600;
}
.timeline-node {
  color: #909399;
  font-size: 12px;
  margin-top: 2px;
}
.timeline-comment {
  margin-top: 4px;
  padding: 6px 8px;
  background: #f5f7fa;
  border-radius: 4px;
  font-size: 13px;
}
</style>
