<template>
  <div class="app-container">
    <ProTable
      ref="tableRef"
      :columns="columns"
      :search-items="searchItems"
      :api="pageApi"
      :action-width="150"
      perm-prefix="erp:finance-account"
      title="结算账户"
    >
      <template #toolbar>
        <el-button v-if="canCreate" type="primary" @click="openCreate">新增</el-button>
      </template>
      <template #cell-status="{ row }">
        <el-tag :type="row.status === 0 ? 'success' : 'info'" size="small">{{ row.statusName }}</el-tag>
      </template>
      <template #cell-defaultStatus="{ row }">
        <el-tag v-if="row.defaultStatus === 1" type="warning" size="small">默认</el-tag>
        <span v-else>—</span>
      </template>
      <template #actions="{ row }">
        <el-button v-if="canUpdate" link type="primary" @click.stop="openEdit(row.id)">编辑</el-button>
        <el-button v-if="canDelete" link type="danger" @click.stop="doDelete(row)">删除</el-button>
      </template>
    </ProTable>

    <!-- 新增/编辑：无余额字段（余额 = 已审核收付款净额，服务端实时汇总）；置默认
         会取消原默认账户（唯一互斥）。 -->
    <el-dialog
      v-model="formVisible"
      :title="formTitle"
      width="560px"
      append-to-body
      destroy-on-close
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="92px">
        <el-form-item label="账户名称" prop="name">
          <el-input v-model="form.name" maxlength="64" placeholder="如 工商银行基本户" />
        </el-form-item>
        <el-form-item label="账户编号" prop="no">
          <el-input v-model="form.no" maxlength="32" placeholder="可选（展示用）" />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-select v-model="form.status" style="width: 100%">
            <el-option label="启用" :value="0" />
            <el-option label="停用" :value="1" />
          </el-select>
        </el-form-item>
        <el-form-item label="默认账户" prop="defaultStatus">
          <el-switch v-model="form.defaultStatus" :active-value="1" :inactive-value="0" active-text="默认" />
        </el-form-item>
        <el-form-item label="排序" prop="sort">
          <el-input-number v-model="form.sort" :min="0" :max="999999" style="width: 100%" />
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="form.remark" type="textarea" :rows="2" maxlength="500" placeholder="可选" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ProTable from '@/components/ProTable.vue'
import { formatTime } from '@/utils/format'
import { useUserStore } from '@/stores/user'
import {
  pageFinanceAccounts,
  getFinanceAccount,
  createFinanceAccount,
  updateFinanceAccount,
  deleteFinanceAccount,
} from '@/api/erp'

// 结算账户页（issue #94，spec §3.6/§4.7/§6）：无余额入参，列表 balance 列 =
// 该账户已审核收款 − 已审核付款实时净额；默认账户唯一互斥（置默认取消原默认）；
// 被收付款单引用时后端 409 拒绝删除。
const tableRef = ref()
const userStore = useUserStore()
const canCreate = computed(() => userStore.hasPerm('erp:finance-account:create'))
const canUpdate = computed(() => userStore.hasPerm('erp:finance-account:update'))
const canDelete = computed(() => userStore.hasPerm('erp:finance-account:delete'))

const statusOptions = [
  { label: '启用', value: 0 },
  { label: '停用', value: 1 },
]

const searchItems = [
  { prop: 'name', label: '账户名称' },
  { prop: 'status', label: '状态', type: 'select', options: statusOptions },
]

const columns = [
  { prop: 'name', label: '账户名称', minWidth: 160 },
  { prop: 'no', label: '账户编号', minWidth: 130 },
  { prop: 'status', label: '状态', width: 90, slot: true },
  { prop: 'defaultStatus', label: '默认', width: 80, slot: true },
  { prop: 'balance', label: '余额（元）', width: 140 },
  { prop: 'sort', label: '排序', width: 80 },
  { prop: 'remark', label: '备注', minWidth: 140 },
  { prop: 'createTime', label: '创建时间', minWidth: 160, formatter: (row: any) => formatTime(row.createTime) },
]

const pageApi = { page: pageFinanceAccounts }

// ---------- 新增/编辑 ----------
const formVisible = ref(false)
const formTitle = ref('新增结算账户')
const submitting = ref(false)
const editingID = ref<number | undefined>(undefined)
const formRef = ref()

const form = ref<{
  name: string
  no: string
  status: number
  defaultStatus: number
  sort: number
  remark: string
}>({ name: '', no: '', status: 0, defaultStatus: 0, sort: 0, remark: '' })

const rules = {
  name: [{ required: true, message: '请输入账户名称', trigger: 'blur' }],
}

function openCreate() {
  editingID.value = undefined
  form.value = { name: '', no: '', status: 0, defaultStatus: 0, sort: 0, remark: '' }
  formTitle.value = '新增结算账户'
  formVisible.value = true
}

async function openEdit(id: number) {
  const detail = (await getFinanceAccount(id)) as any
  editingID.value = detail.id
  form.value = {
    name: detail.name,
    no: detail.no || '',
    status: detail.status,
    defaultStatus: detail.defaultStatus,
    sort: detail.sort,
    remark: detail.remark || '',
  }
  formTitle.value = '编辑结算账户'
  formVisible.value = true
}

async function submit() {
  await formRef.value?.validate()
  submitting.value = true
  try {
    const payload: any = { ...form.value }
    if (editingID.value) {
      await updateFinanceAccount({ id: editingID.value, ...payload })
      ElMessage.success('已保存')
    } else {
      await createFinanceAccount(payload)
      ElMessage.success('已创建')
    }
    formVisible.value = false
    tableRef.value?.reload?.()
  } finally {
    submitting.value = false
  }
}

async function doDelete(row: any) {
  await ElMessageBox.confirm(`确认删除结算账户 ${row.name}？被收付款单引用时将被拒绝。`, '删除确认', {
    type: 'warning',
  })
  await deleteFinanceAccount(row.id)
  ElMessage.success('已删除')
  tableRef.value?.reload?.()
}
</script>
