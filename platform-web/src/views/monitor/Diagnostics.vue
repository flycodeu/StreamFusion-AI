<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElButton,
  ElDatePicker,
  ElDrawer,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElOption,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
  ElMessage,
  vLoading,
} from 'element-plus'
import { getDiagnostics, type DiagnosticEvent } from '../../api/diagnostics/api'
import { eventReason, eventResult } from '../../features/diagnostics/presentation'
import { normalizedTraceId } from '../../features/audit/presentation'
import RequestError from '../../components/feedback/RequestError.vue'
import CaptureDialog from '../../features/diagnostics/CaptureDialog.vue'
import { sessionState } from '../../session/state'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'

defineOptions({ name: 'MonitorDiagnostics' })
const route = useRoute()
const captureOpen = ref(false)
const captureScope = usePageScope()
const filters = reactive({ traceId: '', planId: '', checkId: '', kind: '', level: '' })
const range = ref<Date[]>([])
const rows = ref<DiagnosticEvent[]>([])
const loading = ref(false)
const error = ref<unknown>(null)
const validation = ref('')
const truncated = ref(false)
const unavailable = ref(false)
const selected = ref<DiagnosticEvent | null>(null)
const drawer = ref(false)
const lastRead = ref('')
let sequence = 0
const summary = computed(
  () => `${rows.value.length} 条记录${lastRead.value ? ` · 更新于 ${lastRead.value}` : ''}`,
)
const asEvent = (value: unknown) => value as DiagnosticEvent

async function load() {
  validation.value = ''
  const traceId = filters.traceId.trim()
  if (traceId && !normalizedTraceId(traceId)) validation.value = '请输入完整的32位请求标识'
  else if ([filters.planId, filters.checkId].some((id) => id && !/^[1-9][0-9]{0,18}$/.test(id)))
    validation.value = '任务和检查编号必须为正整数'
  else if (
    range.value.length !== 2 ||
    range.value[1]!.getTime() < range.value[0]!.getTime() ||
    range.value[1]!.getTime() - range.value[0]!.getTime() > 7 * 86400000
  )
    validation.value = '请选择不超过7天的时间范围'
  if (validation.value) return
  const current = ++sequence
  const inScope = captureScope()
  loading.value = true
  error.value = null
  rows.value = []
  truncated.value = unavailable.value = false
  selected.value = null
  drawer.value = false
  try {
    const result = await getDiagnostics({
      ...filters,
      traceId: traceId ? normalizedTraceId(traceId)! : '',
      startTime: range.value[0]!.toISOString(),
      endTime: range.value[1]!.toISOString(),
    })
    if (current !== sequence || !inScope()) return
    rows.value = result.items
    truncated.value = result.truncated
    unavailable.value = result.unavailable
    lastRead.value = formatDateTime(new Date())
  } catch (failure) {
    if (current === sequence && inScope()) error.value = failure
  } finally {
    if (current === sequence && inScope()) loading.value = false
  }
}
function reset() {
  Object.assign(filters, { traceId: '', planId: '', checkId: '', kind: '', level: '' })
  range.value = [new Date(Date.now() - 86400000), new Date()]
  void load()
}
function show(row: DiagnosticEvent) {
  selected.value = row
  drawer.value = true
}
function relatedPlan() {
  if (!selected.value?.planId) return
  Object.assign(filters, {
    planId: selected.value.planId,
    checkId: '',
    traceId: '',
    kind: 'CAMERA_CHECK',
    level: '',
  })
  void load()
}
async function copy() {
  if (!selected.value) return
  try {
    await globalThis.navigator.clipboard.writeText(JSON.stringify(selected.value, null, 2))
    ElMessage.success('排查信息已复制')
  } catch {
    ElMessage.warning('复制失败，请手动选择请求标识')
  }
}
watch(
  () => route.query,
  (query) => {
    Object.assign(filters, {
      traceId: typeof query.traceId === 'string' ? query.traceId : '',
      planId: typeof query.planId === 'string' ? query.planId : '',
      checkId: typeof query.checkId === 'string' ? query.checkId : '',
      kind: '',
      level: '',
    })
    const time = typeof query.time === 'string' ? Date.parse(query.time) : NaN
    range.value = Number.isFinite(time)
      ? [new Date(time - 3600000), new Date(time + 3600000)]
      : [new Date(Date.now() - 86400000), new Date()]
    if (filters.planId)
      range.value = [
        new Date(Math.max(Number.isFinite(time) ? time : 0, Date.now() - 7 * 86400000)),
        new Date(),
      ]
    void load()
  },
  { immediate: true },
)
</script>

<template>
  <section class="diagnostics-page">
    <div v-if="sessionState.me?.isSuperAdmin" class="capture-actions">
      <ElButton @click="captureOpen = true">临时详细记录</ElButton>
    </div>
    <CaptureDialog
      v-if="captureOpen && sessionState.me?.isSuperAdmin"
      @close="captureOpen = false"
    />
    <ElForm class="diagnostic-filters" label-position="top" @submit.prevent="load">
      <ElFormItem label="时间范围" class="time-filter"
        ><ElDatePicker
          v-model="range"
          type="datetimerange"
          format="YYYY-MM-DD HH:mm"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
          :clearable="false"
      /></ElFormItem>
      <ElFormItem label="请求标识"
        ><ElInput v-model="filters.traceId" placeholder="粘贴请求标识" clearable
      /></ElFormItem>
      <ElFormItem label="巡检任务编号"
        ><ElInput v-model="filters.planId" placeholder="任务编号" clearable
      /></ElFormItem>
      <ElFormItem label="检查编号"
        ><ElInput v-model="filters.checkId" placeholder="检查编号" clearable
      /></ElFormItem>
      <ElFormItem label="日志类型"
        ><ElSelect v-model="filters.kind" clearable placeholder="全部类型"
          ><ElOption label="接口请求" value="REQUEST" /><ElOption
            label="相机检查"
            value="CAMERA_CHECK" /></ElSelect
      ></ElFormItem>
      <ElFormItem label="日志级别"
        ><ElSelect v-model="filters.level" clearable placeholder="全部级别"
          ><ElOption label="正常" value="INFO" /><ElOption label="警告" value="WARN" /><ElOption
            label="错误"
            value="ERROR" /></ElSelect
      ></ElFormItem>
      <div class="filter-actions">
        <ElButton type="primary" native-type="submit" :loading="loading">查询日志</ElButton
        ><ElButton @click="reset">重置</ElButton>
      </div>
    </ElForm>
    <p v-if="validation" role="alert" class="query-warning">{{ validation }}</p>
    <RequestError :error="error" />
    <div class="log-toolbar">
      <span>{{ summary }}</span>
    </div>
    <p v-if="truncated" class="query-warning" role="status">
      已达到本次查询上限，请缩小时间范围或补充请求、任务编号。
    </p>
    <p v-if="unavailable" class="query-warning" role="status">
      部分日志不可读取或尚未生成，当前结果可能不完整。
    </p>
    <div v-loading="loading" class="log-table">
      <ElTable :data="rows" stripe @row-dblclick="show">
        <ElTableColumn label="发生时间" width="180"
          ><template #default="{ row }">{{
            formatDateTime(asEvent(row).occurredAt)
          }}</template></ElTableColumn
        >
        <ElTableColumn label="请求 / 检查" min-width="240"
          ><template #default="{ row }"
            ><div class="event-title">
              {{
                row.kind === 'REQUEST' ? `${row.method} ${row.route}` : `相机检查 · ${row.cameraId}`
              }}
            </div>
            <div class="event-meta">
              {{ row.planId ? `任务 ${row.planId}` : row.traceId }}
            </div></template
          ></ElTableColumn
        >
        <ElTableColumn label="结果" width="125"
          ><template #default="{ row }"
            ><ElTag
              :type="
                row.level === 'ERROR' ? 'danger' : row.level === 'WARN' ? 'warning' : 'success'
              "
              effect="plain"
              >{{ eventResult(asEvent(row)) }}</ElTag
            ></template
          ></ElTableColumn
        >
        <ElTableColumn label="原因" min-width="190" show-overflow-tooltip
          ><template #default="{ row }">{{ eventReason(asEvent(row)) }}</template></ElTableColumn
        >
        <ElTableColumn label="耗时" width="95"
          ><template #default="{ row }">{{
            row.durationMs == null ? '—' : `${row.durationMs} ms`
          }}</template></ElTableColumn
        >
        <ElTableColumn label="操作" width="80" fixed="right"
          ><template #default="{ row }"
            ><ElButton link type="primary" @click="show(asEvent(row))">详情</ElButton></template
          ></ElTableColumn
        >
        <template #empty
          ><ElEmpty description="没有找到日志，请检查时间范围和筛选条件" :image-size="72"
        /></template>
      </ElTable>
    </div>
    <ElDrawer v-model="drawer" title="诊断详情" size="min(760px, 100%)" destroy-on-close>
      <template v-if="selected">
        <div class="detail-heading">
          <ElTag :type="selected.level === 'INFO' ? 'success' : 'warning'">{{
            eventResult(selected)
          }}</ElTag
          ><span>{{ formatDateTime(selected.occurredAt) }}</span>
        </div>
        <dl class="event-detail">
          <div>
            <dt>请求标识</dt>
            <dd>{{ selected.traceId || '—' }}</dd>
          </div>
          <div v-if="selected.route">
            <dt>接口</dt>
            <dd>{{ selected.method }} {{ selected.route }}</dd>
          </div>
          <div v-if="selected.status">
            <dt>HTTP 状态</dt>
            <dd>{{ selected.status }}</dd>
          </div>
          <div v-if="selected.planId">
            <dt>巡检任务</dt>
            <dd>{{ selected.planId }}</dd>
          </div>
          <div v-if="selected.checkId">
            <dt>检查编号</dt>
            <dd>{{ selected.checkId }}</dd>
          </div>
          <div v-if="selected.cameraId">
            <dt>通道 / 码流编号</dt>
            <dd>{{ selected.cameraId }} / {{ selected.profileId }}</dd>
          </div>
          <div>
            <dt>原因</dt>
            <dd>{{ eventReason(selected) }}</dd>
          </div>
          <div v-if="selected.errorCode || selected.reasonCode">
            <dt>诊断代码</dt>
            <dd>{{ [selected.errorCode, selected.reasonCode].filter(Boolean).join(' / ') }}</dd>
          </div>
          <div>
            <dt>耗时</dt>
            <dd>{{ selected.durationMs == null ? '—' : `${selected.durationMs} ms` }}</dd>
          </div>
        </dl>
        <div class="detail-actions">
          <ElButton type="primary" plain @click="copy">复制排查信息</ElButton
          ><ElButton v-if="selected.planId" @click="relatedPlan">查看同任务检查</ElButton>
        </div>
        <details v-if="selected.exceptionType" class="exception-detail">
          <summary>技术堆栈</summary>
          <pre
            >{{ selected.exceptionType
            }}{{
              selected.rootExceptionType && selected.rootExceptionType !== selected.exceptionType
                ? `\n根因：${selected.rootExceptionType}`
                : ''
            }}{{ '\n' }}{{ selected.stack.join('\n') }}</pre>
        </details>
        <details v-if="selected.request" class="exception-detail">
          <summary>请求参数（已脱敏）</summary>
          <pre>{{ JSON.stringify(selected.request, null, 2) }}</pre>
        </details>
        <details v-if="selected.response" class="exception-detail">
          <summary>返回内容（已脱敏）</summary>
          <pre>{{ JSON.stringify(selected.response, null, 2) }}</pre>
        </details>
      </template>
    </ElDrawer>
  </section>
</template>

<style scoped>
.diagnostics-page {
  min-width: 0;
}
.capture-actions {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}
.diagnostic-filters {
  display: grid;
  grid-template-columns: minmax(340px, 2fr) repeat(3, minmax(160px, 1fr));
  gap: 0 16px;
  padding: 18px 20px 2px;
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-light);
  border-radius: 8px;
}
.diagnostic-filters :deep(.el-form-item) {
  min-width: 0;
  margin-bottom: 16px;
}
.diagnostic-filters :deep(.el-date-editor) {
  width: 100%;
  min-width: 0;
}
.filter-actions {
  display: flex;
  align-items: flex-end;
  padding-bottom: 16px;
}
.log-toolbar {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin: 18px 0 10px;
}
.log-table {
  overflow: hidden;
  border: 1px solid var(--el-border-color-light);
  border-radius: 8px;
}
.event-title {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.event-meta {
  font:
    11px/1.7 Consolas,
    monospace;
  color: var(--el-text-color-secondary);
}
.query-warning {
  color: var(--el-color-warning-dark-2);
  font-size: 13px;
  margin: 12px 0;
}
.detail-heading,
.detail-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.detail-heading {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.event-detail {
  margin: 20px 0;
}
.event-detail > div {
  display: grid;
  grid-template-columns: 120px minmax(0, 1fr);
  padding: 12px 0;
  gap: 12px;
  border-bottom: 1px solid var(--el-border-color-lighter);
  font-size: 13px;
}
.event-detail dt {
  color: var(--el-text-color-secondary);
}
.event-detail dd {
  margin: 0;
  overflow-wrap: anywhere;
  user-select: text;
}
.exception-detail {
  margin-top: 24px;
  font-size: 13px;
}
.exception-detail summary {
  cursor: pointer;
}
.exception-detail pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  padding: 14px;
  background: var(--el-fill-color-light);
  line-height: 1.7;
  font-size: 12px;
}
@media (max-width: 1100px) {
  .diagnostic-filters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .time-filter {
    grid-column: 1 / -1;
  }
}
@media (max-width: 560px) {
  .diagnostic-filters {
    grid-template-columns: minmax(0, 1fr);
    padding: 14px 12px 0;
  }
  .event-detail > div {
    grid-template-columns: 90px minmax(0, 1fr);
  }
}
</style>
