<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { CopyDocument } from '@element-plus/icons-vue'
import {
  ElButton,
  ElDatePicker,
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
import DiagnosticLink from '../../features/diagnostics/DiagnosticLink.vue'
import { moduleNames, actionNames } from '../../features/audit/labels'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import { useColumns } from '../../composables/table/useColumns'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
import IpBlockPanel from '../../features/security/IpBlockPanel.vue'
import SecurityAlertPanel from '../../features/security/SecurityAlertPanel.vue'
import { sessionState } from '../../session/state'
import AuditReferenceList from '../../features/audit/AuditReferenceList.vue'
import RetentionDialog from '../../features/audit/RetentionDialog.vue'
import ExportDialog from '../../features/audit/ExportDialog.vue'
import {
  auditChanges,
  auditReason,
  normalizedTraceId,
  referenceName,
  traceDiagnostic,
} from '../../features/audit/presentation'

defineOptions({ name: 'SystemAudit' })
const activeTab = ref('log')
const retentionOpen = ref(false)
const exportOpen = ref(false)
const exportFilter = computed(() => ({
  ...query,
  startTime: utc(timeRange.value?.[0]),
  endTime: utc(timeRange.value?.[1]),
}))
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
const resultNames: Record<string, string> = { SUCCESS: '成功', FAILURE: '失败', DENIED: '拒绝' }
const changes = computed(() => auditChanges(detail.value))
const failureReason = computed(() => {
  const record = detail.value?.record
  if (!record || record.result === 'SUCCESS') return ''
  const reason = auditReason(record.reasonCode).trim()
  return reason === '—' ? '' : reason
})
const detailIdentifiers = computed(() => [
  { key: 'record', label: '记录 ID', value: detail.value?.record.id },
  { key: 'actor', label: '操作者 ID', value: detail.value?.record.actorId },
  { key: 'trace', label: '请求定位', value: detail.value?.record.traceId },
])
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
async function copyIdentifier(value: string | null | undefined, label: string): Promise<void> {
  if (!value) return
  const inScope = captureScope()
  try {
    await globalThis.navigator.clipboard.writeText(value)
    if (inScope()) ElMessage.success(`${label}已复制`)
  } catch {
    if (inScope()) ElMessage.warning('浏览器未允许复制，可手动选择对应标识')
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
      <ElTabPane label="安全告警" name="security" />
    </ElTabs>
    <IpBlockPanel v-if="activeTab === 'ip' && sessionState.me?.isSuperAdmin" />
    <SecurityAlertPanel v-else-if="activeTab === 'security' && sessionState.me?.isSuperAdmin" />
    <template v-else>
      <RetentionDialog
        v-if="retentionOpen && sessionState.me?.isSuperAdmin"
        @close="retentionOpen = false"
      />
      <ExportDialog v-if="exportOpen" :filter="exportFilter" @close="exportOpen = false" />
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
        <ElFormItem label="操作时间（UTC+8）"
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
      <TablePanel :initial-loading="loading && !rows.length">
        <template #actions>
          <ElButton @click="exportOpen = true">导出与记录</ElButton>
          <ElButton v-if="sessionState.me?.isSuperAdmin" @click="retentionOpen = true"
            >保留策略</ElButton
          >
        </template>
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
            layout="total, sizes, prev, pager, next, jumper"
            @change="load"
        /></template>
      </TablePanel>
      <ElDrawer v-model="drawer" title="操作详情" size="min(760px, 100vw)" destroy-on-close>
        <div v-loading="detailLoading" class="audit-detail">
          <RequestError :error="detailError" />
          <ElButton
            v-if="detailError && detailId"
            :loading="detailLoading"
            @click="showDetail(detailId)"
            >重新加载详情</ElButton
          >
          <template v-if="detail">
            <section class="audit-summary" aria-label="操作摘要">
              <div class="audit-summary-heading">
                <span class="audit-module">{{
                  moduleNames[detail.record.module] || detail.record.module
                }}</span>
                <h3>{{ actionNames[detail.record.action] || detail.record.action }}</h3>
                <span
                  class="audit-result"
                  :class="{
                    'is-success': detail.record.result === 'SUCCESS',
                    'is-failure': ['FAILURE', 'DENIED'].includes(detail.record.result),
                  }"
                  >{{ resultNames[detail.record.result] || detail.record.result }}</span
                >
              </div>
              <p v-if="failureReason" class="audit-summary-reason">
                <span>原因</span>{{ failureReason }}
              </p>
              <ul class="audit-summary-meta">
                <li>操作人：{{ actor(detail.record) }}</li>
                <li>来源 IP：{{ detail.sourceIp || '—' }}</li>
                <li>时间：{{ formatDateTime(detail.record.createdAt) }}</li>
              </ul>
            </section>

            <section aria-label="操作对象">
              <dl class="audit-object-info">
                <div>
                  <dt>操作对象</dt>
                  <dd class="audit-object-name" :title="target(detail.record)">
                    {{ target(detail.record) }}
                  </dd>
                </div>
                <div>
                  <dt>对象快照 ID</dt>
                  <dd class="audit-identifier">
                    <code>{{ detail.record.targetId || '—' }}</code>
                    <button
                      v-if="detail.record.targetId"
                      type="button"
                      class="audit-copy"
                      title="复制对象快照 ID"
                      aria-label="复制对象快照 ID"
                      @click="copyIdentifier(detail.record.targetId, '对象快照 ID')"
                    >
                      <CopyDocument aria-hidden="true" />
                    </button>
                  </dd>
                </div>
              </dl>
            </section>

            <section aria-labelledby="audit-changes-title">
              <h3 id="audit-changes-title" class="detail-section-title">变更内容</h3>
              <div v-if="changes.length" class="audit-changes">
                <ElTable :data="changes">
                  <ElTableColumn prop="name" label="字段" width="140" />
                  <ElTableColumn label="内容" min-width="180">
                    <template #default="{ row }">
                      <AuditReferenceList
                        v-if="row.references.length"
                        :references="row.references"
                      />
                      <span v-else class="change-content">{{ row.value }}</span>
                    </template>
                  </ElTableColumn>
                </ElTable>
              </div>
              <ElEmpty
                v-else
                class="audit-changes-empty"
                description="无变更摘要"
                :image-size="48"
              />
            </section>

            <details class="audit-technical">
              <summary>
                <span
                  >技术排查<span class="audit-technical-hint"
                    >记录 ID · 请求定位 · 排查 JSON</span
                  ></span
                >
              </summary>
              <div class="audit-technical-content">
                <dl class="audit-technical-fields">
                  <div
                    v-for="item in detailIdentifiers"
                    :key="item.key"
                    class="audit-technical-field"
                    :class="{ 'is-wide': item.key === 'trace' }"
                  >
                    <dt>{{ item.label }}</dt>
                    <dd class="audit-identifier">
                      <code>{{ item.value || '—' }}</code>
                      <button
                        v-if="item.value"
                        type="button"
                        class="audit-copy"
                        :title="`复制${item.label}`"
                        :aria-label="`复制${item.label}`"
                        @click="copyIdentifier(item.value, item.label)"
                      >
                        <CopyDocument aria-hidden="true" />
                      </button>
                    </dd>
                  </div>
                </dl>
                <details class="diagnostic-json">
                  <summary>查看排查 JSON</summary>
                  <pre tabindex="0" aria-label="审计排查 JSON">{{
                    JSON.stringify(detail, null, 2)
                  }}</pre>
                </details>
              </div>
            </details>
          </template>
        </div>
        <template v-if="detail" #footer>
          <div class="audit-detail-actions">
            <DiagnosticLink
              :trace-id="detail.record.traceId"
              :time="detail.record.createdAt"
              :plan-id="
                detail.record.module === 'CAMERA_STATUS_PLAN' ? detail.record.targetId : null
              "
              button-style="secondary"
            />
            <ElButton size="small" @click="copyDiagnostics">复制排查信息</ElButton>
            <ElButton
              v-if="detail.record.traceId"
              size="small"
              @click="relatedOperations(detail.record.traceId)"
              >查看关联操作</ElButton
            >
          </div>
        </template>
      </ElDrawer>
    </template>
  </div>
</template>

<style scoped>
.audit-detail {
  min-height: 120px;
  color: #334155;
  font-size: 14px;
  line-height: 1.6;
}
.audit-summary {
  padding: 16px;
  margin-bottom: 20px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
}
.audit-summary-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 10px;
}
.audit-summary-heading h3 {
  flex: 1;
  min-width: 120px;
  margin: 0;
  color: #0f172a;
  font-size: 16px;
  font-weight: 600;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.audit-module {
  padding: 2px 7px;
  border: 1px solid #e2e8f0;
  border-radius: 4px;
  background: #fff;
  color: #475569;
  font-size: 12px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.audit-result {
  flex-shrink: 0;
  padding: 2px 10px;
  margin-left: auto;
  border: 1px solid #e2e8f0;
  border-radius: 99px;
  background: #f1f5f9;
  color: #475569;
  font-size: 12px;
  font-weight: 500;
}
.audit-result.is-success {
  background: #ecfdf5;
  border-color: #a7f3d0;
  color: #059669;
}
.audit-result.is-failure {
  background: #fef2f2;
  border-color: #fecaca;
  color: #b91c1c;
}
.audit-summary-reason {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin: 12px 0 0;
  padding: 8px 10px;
  border-radius: 6px;
  background: #fef2f2;
  color: #b91c1c;
  font-size: 13px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.audit-summary-reason > span {
  flex-shrink: 0;
  font-weight: 600;
}
.audit-summary-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 0;
  padding: 0;
  margin: 12px 0 0;
  list-style: none;
  color: #64748b;
  font-size: 13px;
}
.audit-summary-meta li {
  display: flex;
  align-items: baseline;
  overflow-wrap: anywhere;
}
.audit-summary-meta li + li::before {
  content: '';
  height: 10px;
  margin: 0 10px;
  border-left: 1px solid #cbd5e1;
}
.audit-object-info,
.audit-technical-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
  margin: 0;
}
.audit-object-info > div {
  min-width: 0;
}
.audit-object-info {
  padding-bottom: 16px;
  border-bottom: 1px dashed #e2e8f0;
}
.audit-object-info dt {
  margin-bottom: 4px;
  color: #64748b;
  font-size: 13px;
}
.audit-object-info dd {
  margin: 0;
  color: #1e293b;
  line-height: 24px;
  overflow-wrap: anywhere;
}
.audit-object-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.audit-identifier {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  min-width: 0;
}
.audit-identifier code {
  min-width: 0;
  color: #64748b;
  font:
    12px/24px Consolas,
    monospace;
  overflow-wrap: anywhere;
  user-select: all;
}
.audit-copy {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 4px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: #94a3b8;
  cursor: pointer;
  transition:
    background 0.15s ease,
    color 0.15s ease;
}
.audit-copy svg {
  width: 14px;
  height: 14px;
}
.audit-copy:hover {
  background: #e2e8f0;
  color: #334155;
}
.audit-copy:focus-visible,
.audit-technical summary:focus-visible {
  outline: 2px solid #2563eb;
  outline-offset: 3px;
}
.detail-section-title {
  margin: 20px 0 10px;
  color: #1e293b;
  font-size: 14px;
  font-weight: 600;
}
.audit-changes {
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
}
.audit-changes :deep(.el-table) {
  --el-table-border-color: #f1f5f9;
  --el-table-header-bg-color: #f8fafc;
  --el-table-header-text-color: #475569;
  --el-table-row-hover-bg-color: #f8fafc;
  font-size: 13px;
}
.audit-changes :deep(.el-table__inner-wrapper::before) {
  display: none;
}
.audit-changes :deep(.el-table .cell) {
  padding: 0 14px;
}
.audit-changes-empty {
  padding: 16px 0;
}
.change-content {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.audit-technical {
  margin-top: 20px;
  border-radius: 8px;
  background: #f8fafc;
  font-size: 13px;
}
.audit-technical > summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  color: #475569;
  font-weight: 500;
  cursor: pointer;
  list-style: none;
}
.audit-technical-hint {
  margin-left: 8px;
  color: #94a3b8;
  font-size: 12px;
  font-weight: 400;
}
.audit-technical > summary::-webkit-details-marker {
  display: none;
}
.audit-technical > summary::after {
  content: '';
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  margin-right: 3px;
  border-right: 1px solid #94a3b8;
  border-bottom: 1px solid #94a3b8;
  transform: rotate(45deg);
}
.audit-technical[open] > summary::after {
  transform: rotate(225deg);
}
.audit-technical-content {
  padding: 0 14px 14px;
}
.audit-technical-fields {
  gap: 12px 20px;
}
.audit-technical-field {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
  align-items: baseline;
  min-width: 0;
}
.audit-technical-field.is-wide {
  grid-column: 1 / -1;
}
.audit-technical-field dt {
  color: #64748b;
}
.audit-technical-field dd {
  margin: 0;
}
.diagnostic-json {
  margin-top: 14px;
}
.diagnostic-json summary {
  width: fit-content;
  cursor: pointer;
  color: #475569;
}
.diagnostic-json pre {
  max-height: 240px;
  margin: 10px 0 0;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  padding: 12px;
  border-radius: 6px;
  background: #0f172a;
  color: #e2e8f0;
  scrollbar-width: thin;
  scrollbar-color: #475569 #0f172a;
  font:
    12px/1.7 Consolas,
    monospace;
}
.audit-detail-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid #e2e8f0;
}
.audit-detail-actions :deep(.el-button) {
  height: 28px;
  margin: 0;
  padding: 0 12px;
  border-color: #dbe2ea;
  border-radius: 6px;
  background: #fff;
  color: #334155;
  font-size: 12px;
}
.audit-detail-actions :deep(.el-button:hover) {
  border-color: #cbd5e1;
  background: #f8fafc;
  color: #0f172a;
}
@media (max-width: 640px) {
  .audit-object-info,
  .audit-technical-fields {
    grid-template-columns: minmax(0, 1fr);
  }
  .audit-summary-meta {
    display: grid;
    gap: 4px;
  }
  .audit-summary-meta li + li::before {
    display: none;
  }
}
</style>
