<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import {
  ElButton,
  ElDatePicker,
  ElDescriptions,
  ElDescriptionsItem,
  ElDrawer,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTabPane,
  ElTabs,
  ElTag,
  vLoading,
} from 'element-plus'
import { getAuditDetail, getAuditPage } from '../../api/audit/api'
import type { AuditDetail, AuditEntry } from '../../api/audit/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import { useColumns } from '../../composables/table/useColumns'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
import IpBlockPanel from '../../features/security/IpBlockPanel.vue'
import { sessionState } from '../../session/state'
import AuditReferenceList from '../../features/audit/AuditReferenceList.vue'
import {
  auditChanges,
  auditReason,
  normalizedTraceId,
  referenceName,
  traceDiagnostic,
} from '../../features/audit/presentation'

defineOptions({ name: 'SystemAudit' })
const activeTab = ref('log')
const query = reactive({
  page: 1,
  size: 20,
  user: '',
  action: '',
  module: '',
  result: '',
  traceId: '',
})
const traceError = ref('')
const timeRange = ref<string[]>([])
const rows = ref<AuditEntry[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref<unknown>(null)
const detailError = ref<unknown>(null)
const detailLoading = ref(false)
const drawer = ref(false)
const detail = ref<AuditDetail | null>(null)
const detailId = ref('')
const captureScope = usePageScope()
const columnOptions = [
  { key: 'user', label: '操作人' },
  { key: 'module', label: '模块' },
  { key: 'action', label: '动作' },
  { key: 'target', label: '操作对象' },
  { key: 'result', label: '结果' },
  { key: 'time', label: '操作时间' },
  { key: 'trace', label: '请求标识' },
]
const columns = useColumns(
  'audit',
  ['user', 'module', 'action', 'target', 'result', 'time'],
  columnOptions.map((item) => item.key),
)
const moduleNames: Record<string, string> = {
  USER: '用户',
  ROLE: '角色',
  MENU: '菜单',
  DEPT: '部门',
  IP_BLOCK: 'IP封禁',
}
const resultNames: Record<string, string> = { SUCCESS: '成功', FAILURE: '失败', DENIED: '拒绝' }
const actionNames: Record<string, string> = {
  BOOTSTRAP: '初始化管理员',
  LOGIN: '登录',
  LOGOUT: '退出登录',
  PROFILE_UPDATE: '修改个人信息',
  PASSWORD_CHANGE: '修改密码',
  USER_CREATE: '新建用户',
  USER_UPDATE: '修改用户',
  USER_ENABLE: '启用用户',
  USER_DISABLE: '停用用户',
  USER_RESET_PASSWORD: '重置用户密码',
  USER_DELETE: '删除用户',
  USER_ROLE_UPDATE: '调整用户角色',
  USER_DEPARTMENTS_UPDATE: '调整用户部门',
  ROLE_CREATE: '新建角色',
  ROLE_UPDATE: '修改角色',
  ROLE_ENABLE: '启用角色',
  ROLE_DISABLE: '停用角色',
  ROLE_MENU_UPDATE: '调整角色菜单',
  ROLE_DELETE: '删除角色',
  MENU_CREATE: '新建菜单',
  MENU_UPDATE: '修改菜单',
  MENU_DELETE: '删除菜单',
  DEPT_CREATE: '新建部门',
  DEPT_UPDATE: '修改部门',
  DEPT_DELETE: '删除部门',
  IP_BLOCK_CREATE: '封禁IP',
  IP_BLOCK_RELEASE: '解除IP封禁',
}
const changes = computed(() => auditChanges(detail.value))
let sequence = 0
let detailSequence = 0

function actor(row: AuditEntry): string {
  return referenceName(
    row.actor,
    row.nickname || row.username || (row.actorId ? `账号ID ${row.actorId}` : '未登录'),
  )
}
function target(row: AuditEntry): string {
  return referenceName(row.target, row.targetId ? `ID ${row.targetId}` : '—')
}
const asEntry = (value: unknown): AuditEntry => value as AuditEntry
function utc(value?: string): string | undefined {
  return value ? new Date(`${value.replace(' ', 'T')}+08:00`).toISOString() : undefined
}

async function load(): Promise<void> {
  traceError.value =
    query.traceId && !normalizedTraceId(query.traceId) ? '请输入完整的32位请求标识' : ''
  if (traceError.value) return
  const current = ++sequence
  const inScope = captureScope()
  loading.value = true
  error.value = null
  try {
    const result = await getAuditPage({
      page: query.page,
      size: query.size,
      user: query.user.trim() || undefined,
      action: query.action.trim().toUpperCase() || undefined,
      module: query.module || undefined,
      result: query.result || undefined,
      traceId: normalizedTraceId(query.traceId) || undefined,
      startTime: utc(timeRange.value?.[0]),
      endTime: utc(timeRange.value?.[1]),
    })
    if (current !== sequence || !inScope()) return
    rows.value = result.items
    total.value = result.total
  } catch (cause) {
    if (current === sequence && inScope()) error.value = cause
  } finally {
    if (current === sequence && inScope()) loading.value = false
  }
}
function search(): void {
  query.page = 1
  void load()
}
function reset(): void {
  Object.assign(query, { user: '', action: '', module: '', result: '', traceId: '' })
  traceError.value = ''
  timeRange.value = []
  search()
}
function relatedOperations(traceId: string): void {
  const normalized = normalizedTraceId(traceId)
  if (!normalized) return
  Object.assign(query, {
    page: 1,
    user: '',
    action: '',
    module: '',
    result: '',
    traceId: normalized,
  })
  timeRange.value = []
  drawer.value = false
  void load()
}
async function copyDiagnostics(): Promise<void> {
  const value = detail.value
  if (!value) return
  const inScope = captureScope()
  const marker = value.record.traceId ? traceDiagnostic(value.record.traceId) : null
  const text = [
    `时间：${formatDateTime(value.record.createdAt)}`,
    `操作人：${actor(value.record)}`,
    `操作：${actionNames[value.record.action] || value.record.action}`,
    `对象：${target(value.record)}`,
    `结果：${resultNames[value.record.result] || value.record.result}`,
    `原因：${auditReason(value.record.reasonCode)}`,
    `记录ID：${value.record.id}`,
    marker ? `后端日志检索：${marker}` : '该记录无请求标识',
  ].join('\n')
  try {
    await globalThis.navigator.clipboard.writeText(text)
    if (inScope()) ElMessage.success('排查信息已复制')
  } catch {
    if (inScope()) ElMessage.warning('浏览器未允许复制，可手动选择请求标识')
  }
}
async function showDetail(id: string): Promise<void> {
  const current = ++detailSequence
  const inScope = captureScope()
  detailId.value = id
  drawer.value = true
  detail.value = null
  detailError.value = null
  detailLoading.value = true
  try {
    const value = await getAuditDetail(id)
    if (current === detailSequence && inScope()) detail.value = value
  } catch (cause) {
    if (current === detailSequence && inScope()) detailError.value = cause
  } finally {
    if (current === detailSequence && inScope()) detailLoading.value = false
  }
}
onMounted(load)
watch(drawer, (open) => {
  if (open) return
  detailSequence++
  detailLoading.value = false
  detailId.value = ''
})
watch(activeTab, (value) => {
  drawer.value = false
  detailSequence++
  if (value === 'log') void load()
})
watch(
  () => sessionState.me?.isSuperAdmin,
  (allowed) => {
    if (!allowed) activeTab.value = 'log'
  },
)
</script>

<template>
  <div class="content-page">
    <ElTabs v-if="sessionState.me?.isSuperAdmin" v-model="activeTab">
      <ElTabPane label="操作记录" name="log" />
      <ElTabPane label="IP封禁" name="ip" />
    </ElTabs>
    <IpBlockPanel v-if="activeTab === 'ip' && sessionState.me?.isSuperAdmin" />
    <template v-else>
      <ElForm class="search-form" inline @submit.prevent="search">
        <ElFormItem label="操作人"
          ><ElInput
            v-model="query.user"
            class="search-field"
            placeholder="当前账号/昵称或ID"
            clearable
        /></ElFormItem>
        <ElFormItem label="动作"
          ><ElSelect
            v-model="query.action"
            class="search-field"
            placeholder="选择或输入动作"
            filterable
            allow-create
            default-first-option
            clearable
            ><ElOption
              v-for="(name, key) in actionNames"
              :key="key"
              :label="name"
              :value="key" /></ElSelect
        ></ElFormItem>
        <ElFormItem label="模块"
          ><ElSelect v-model="query.module" class="status-field" placeholder="全部模块" clearable
            ><ElOption
              v-for="(name, key) in moduleNames"
              :key="key"
              :label="name"
              :value="key" /></ElSelect
        ></ElFormItem>
        <ElFormItem label="结果"
          ><ElSelect v-model="query.result" class="status-field" placeholder="全部结果" clearable
            ><ElOption
              v-for="(name, key) in resultNames"
              :key="key"
              :label="name"
              :value="key" /></ElSelect
        ></ElFormItem>
        <ElFormItem label="操作时间"
          ><ElDatePicker
            v-model="timeRange"
            type="datetimerange"
            value-format="YYYY-MM-DD HH:mm:ss"
            start-placeholder="开始时间"
            end-placeholder="结束时间"
        /></ElFormItem>
        <ElFormItem label="请求标识" :error="traceError"
          ><ElInput
            v-model="query.traceId"
            class="search-field"
            placeholder="完整请求标识"
            maxlength="32"
            clearable
        /></ElFormItem>
        <ElFormItem
          ><ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton
          ><ElButton @click="reset">重置</ElButton></ElFormItem
        >
      </ElForm>
      <RequestError :error="error" />
      <TablePanel>
        <template #tools
          ><ElButton :loading="loading" @click="load">刷新</ElButton
          ><ColumnPicker v-model="columns" :options="columnOptions"
        /></template>
        <ElTable v-loading="loading" :data="rows" row-key="id" border empty-text="暂无操作记录">
          <ElTableColumn
            v-if="columns.includes('user')"
            label="操作人"
            min-width="150"
            show-overflow-tooltip
            ><template #default="{ row }">{{ actor(asEntry(row)) }}</template></ElTableColumn
          >
          <ElTableColumn v-if="columns.includes('module')" label="模块" min-width="95"
            ><template #default="{ row }">{{
              moduleNames[row.module] || row.module
            }}</template></ElTableColumn
          >
          <ElTableColumn
            v-if="columns.includes('action')"
            label="动作"
            min-width="150"
            show-overflow-tooltip
            ><template #default="{ row }">{{
              actionNames[row.action] || row.action
            }}</template></ElTableColumn
          >
          <ElTableColumn
            v-if="columns.includes('target')"
            label="操作对象"
            min-width="200"
            show-overflow-tooltip
            ><template #default="{ row }">{{ target(asEntry(row)) }}</template></ElTableColumn
          >
          <ElTableColumn v-if="columns.includes('result')" label="结果" width="90"
            ><template #default="{ row }"
              ><ElTag
                :type="
                  row.result === 'SUCCESS'
                    ? 'success'
                    : row.result === 'DENIED'
                      ? 'warning'
                      : 'danger'
                "
                effect="plain"
                >{{ resultNames[row.result] || row.result }}</ElTag
              ></template
            ></ElTableColumn
          >
          <ElTableColumn v-if="columns.includes('time')" label="操作时间" min-width="175"
            ><template #default="{ row }">{{
              formatDateTime(row.createdAt)
            }}</template></ElTableColumn
          >
          <ElTableColumn
            v-if="columns.includes('trace')"
            prop="traceId"
            label="请求标识"
            min-width="220"
            show-overflow-tooltip
          />
          <ElTableColumn
            label="操作"
            min-width="100"
            fixed="right"
            class-name="table-actions-column"
            :resizable="false"
            ><template #default="{ row }"
              ><TableActions
                ><ElButton plain type="primary" @click="showDetail(row.id)"
                  >详情</ElButton
                ></TableActions
              ></template
            ></ElTableColumn
          >
        </ElTable>
        <template #footer
          ><ElPagination
            v-model:current-page="query.page"
            v-model:page-size="query.size"
            :total="total"
            :page-sizes="[20, 50, 100]"
            layout="total, sizes, prev, pager, next"
            @change="load"
        /></template>
      </TablePanel>
      <ElDrawer v-model="drawer" title="操作详情" size="760px" destroy-on-close>
        <div v-loading="detailLoading">
          <RequestError :error="detailError" />
          <ElButton
            v-if="detailError && detailId"
            :loading="detailLoading"
            @click="showDetail(detailId)"
            >重新加载详情</ElButton
          >
          <template v-if="detail">
            <ElDescriptions :column="1" border>
              <ElDescriptionsItem label="记录ID">{{ detail.record.id }}</ElDescriptionsItem>
              <ElDescriptionsItem label="操作人">{{ actor(detail.record) }}</ElDescriptionsItem>
              <ElDescriptionsItem label="操作者ID">{{
                detail.record.actorId || '—'
              }}</ElDescriptionsItem>
              <ElDescriptionsItem label="模块">{{
                moduleNames[detail.record.module] || detail.record.module
              }}</ElDescriptionsItem>
              <ElDescriptionsItem label="动作">{{
                actionNames[detail.record.action] || detail.record.action
              }}</ElDescriptionsItem>
              <ElDescriptionsItem label="操作对象"
                ><AuditReferenceList
                  v-if="detail.record.target"
                  :references="[detail.record.target]"
                /><span v-else>{{ target(detail.record) }}</span></ElDescriptionsItem
              >
              <ElDescriptionsItem label="结果">{{
                resultNames[detail.record.result] || detail.record.result
              }}</ElDescriptionsItem>
              <ElDescriptionsItem label="原因">{{
                auditReason(detail.record.reasonCode)
              }}</ElDescriptionsItem>
              <ElDescriptionsItem label="来源IP">{{ detail.sourceIp || '—' }}</ElDescriptionsItem>
              <ElDescriptionsItem label="操作时间">{{
                formatDateTime(detail.record.createdAt)
              }}</ElDescriptionsItem>
              <ElDescriptionsItem label="请求定位"
                ><div class="trace-tools">
                  <code>{{ detail.record.traceId || '—' }}</code>
                  <div class="trace-buttons">
                    <ElButton plain @click="copyDiagnostics">复制排查信息</ElButton
                    ><ElButton
                      v-if="detail.record.traceId"
                      plain
                      type="primary"
                      @click="relatedOperations(detail.record.traceId)"
                      >查看关联操作</ElButton
                    >
                  </div>
                </div></ElDescriptionsItem
              >
            </ElDescriptions>
            <h3 class="detail-section-title">变更内容</h3>
            <ElTable v-if="changes.length" :data="changes" border
              ><ElTableColumn prop="name" label="字段" width="150" /><ElTableColumn label="内容"
                ><template #default="{ row }"
                  ><AuditReferenceList
                    v-if="row.references.length"
                    :references="row.references"
                  /><span v-else class="change-content">{{ row.value }}</span></template
                ></ElTableColumn
              ></ElTable
            >
            <ElEmpty v-else description="无变更摘要" :image-size="60" />
          </template>
        </div>
      </ElDrawer>
    </template>
  </div>
</template>

<style scoped>
.detail-section-title {
  margin: 24px 0 12px;
  font-size: 15px;
  font-weight: 600;
}
.trace-tools code {
  display: block;
  overflow-wrap: anywhere;
  user-select: all;
}
.trace-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 10px;
}
.trace-buttons .el-button + .el-button {
  margin-left: 0;
}
.change-content {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
</style>
