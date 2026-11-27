<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import {
  ElButton,
  ElDatePicker,
  ElDialog,
  ElEmpty,
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
import {
  getDiagnostics,
  type DiagnosticEvent,
  type DiagnosticQuery,
} from '../../api/diagnostics/api'
import { eventReason, eventResult } from '../../features/diagnostics/presentation'
import { normalizedTraceId } from '../../features/audit/presentation'
import RequestError from '../../components/feedback/RequestError.vue'
import CaptureDialog from '../../features/diagnostics/CaptureDialog.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import CursorPagination from '../../components/table/CursorPagination.vue'
import FilterBar from '../../components/ui/FilterBar.vue'
import { sessionState } from '../../session/state'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'

defineOptions({ name: 'MonitorDiagnostics' })
const props = defineProps<{
  context?: { traceId?: string; planId?: string; checkId?: string; time?: string }
}>()
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
const pageSize = ref(50)
const page = ref(1)
const cursors = ref<string[]>([''])
const nextCursor = ref<string | null>(null)
const cursorExpired = ref(false)
const droppedEvents = ref(0)
const total = ref<number | null>(null)
const totalExact = ref(false)
let activeQuery: DiagnosticQuery = {}
let sequence = 0
const asEvent = (value: unknown) => value as DiagnosticEvent

async function load() {
  ++sequence
  loading.value = false
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
  activeQuery = {
    ...filters,
    traceId: traceId ? normalizedTraceId(traceId)! : '',
    startTime: range.value[0]!.toISOString(),
    endTime: range.value[1]!.toISOString(),
    size: pageSize.value,
  }
  cursors.value = ['']
  page.value = 1
  rows.value = []
  total.value = null
  totalExact.value = false
  nextCursor.value = null
  cursorExpired.value = false
  truncated.value = unavailable.value = false
  await fetchPage(1, '')
}
async function fetchPage(target: number, cursor: string) {
  const current = ++sequence
  const inScope = captureScope()
  loading.value = true
  error.value = null
  selected.value = null
  drawer.value = false
  try {
    const result = await getDiagnostics({ ...activeQuery, cursor })
    if (current !== sequence || !inScope()) return
    rows.value = result.items
    truncated.value = result.truncated
    unavailable.value = result.unavailable
    nextCursor.value = result.nextCursor ?? null
    cursorExpired.value = result.cursorExpired ?? false
    droppedEvents.value = result.droppedEvents ?? 0
    total.value = result.total ?? null
    totalExact.value = result.totalExact ?? false
    page.value = target
    cursors.value[target - 1] = cursor
    lastRead.value = formatDateTime(new Date())
  } catch (failure) {
    if (current === sequence && inScope()) error.value = failure
  } finally {
    if (current === sequence && inScope()) loading.value = false
  }
}
function nextPage() {
  if (nextCursor.value && !loading.value) void fetchPage(page.value + 1, nextCursor.value)
}
function previousPage() {
  if (page.value > 1 && !loading.value)
    void fetchPage(page.value - 1, cursors.value[page.value - 2]!)
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
  () => props.context ?? route.query,
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
  <section class="diagnostics-page" :class="{ 'content-page': !context }">
    <CaptureDialog
      v-if="captureOpen && sessionState.me?.isSuperAdmin"
      @close="captureOpen = false"
    />
    <FilterBar :loading="loading" @search="load" @reset="reset">
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
        ><ElInput
          v-model="filters.traceId"
          class="search-field"
          placeholder="粘贴请求标识"
          clearable
      /></ElFormItem>
      <ElFormItem label="巡检任务编号"
        ><ElInput v-model="filters.planId" class="status-field" placeholder="任务编号" clearable
      /></ElFormItem>
      <ElFormItem label="检查编号"
        ><ElInput v-model="filters.checkId" class="status-field" placeholder="检查编号" clearable
      /></ElFormItem>
      <ElFormItem label="日志类型"
        ><ElSelect v-model="filters.kind" class="status-field" clearable placeholder="全部类型"
          ><ElOption label="接口请求" value="REQUEST" /><ElOption
            label="相机检查"
            value="CAMERA_CHECK" /></ElSelect
      ></ElFormItem>
      <ElFormItem label="日志级别"
        ><ElSelect v-model="filters.level" class="status-field" clearable placeholder="全部级别"
          ><ElOption label="正常" value="INFO" /><ElOption label="警告" value="WARN" /><ElOption
            label="错误"
            value="ERROR" /></ElSelect
      ></ElFormItem>
    </FilterBar>
    <p v-if="validation" role="alert" class="query-warning">{{ validation }}</p>
    <RequestError :error="error" :diagnostic-link="false" />
    <TablePanel>
      <template #actions>
        <ElButton v-if="sessionState.me?.isSuperAdmin" @click="captureOpen = true"
          >临时详细记录</ElButton
        >
        <span v-if="truncated" class="query-warning" role="status"
          >已达到扫描预算，可继续下一页或缩小查询范围。</span
        >
        <span v-if="unavailable" class="query-warning" role="status"
          >部分日志不可读取或尚未生成，当前结果可能不完整。</span
        >
        <span v-if="cursorExpired" class="query-warning" role="status"
          >日志已轮转清理或截断，请重新查询。</span
        >
        <span v-if="droppedEvents" class="query-warning" role="status"
          >本进程累计丢弃 {{ droppedEvents }} 条诊断日志（队列满），结果可能不完整。</span
        >
      </template>
      <template #tools
        ><span v-if="lastRead" class="last-read">更新于 {{ lastRead }}</span
        ><ElButton :loading="loading" @click="load">刷新</ElButton></template
      >
      <ElTable
        v-loading="loading"
        :data="rows"
        :max-height="context ? 420 : undefined"
        border
        @row-dblclick="show"
      >
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
        <ElTableColumn
          label="操作"
          width="80"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions compact
              ><ElButton link type="primary" @click="show(asEvent(row))"
                >详情</ElButton
              ></TableActions
            ></template
          ></ElTableColumn
        >
        <template #empty
          ><ElEmpty description="没有找到日志，请检查时间范围和筛选条件" :image-size="64"
        /></template>
      </ElTable>
      <template #footer>
        <CursorPagination
          v-model:page-size="pageSize"
          :page="page"
          :page-sizes="[20, 50, 100, 200]"
          :has-prev="page > 1 && !cursorExpired"
          :has-next="Boolean(nextCursor) && !cursorExpired"
          :disabled="loading"
          :summary="
            total !== null
              ? totalExact
                ? `共 ${total} 条`
                : `已匹配至少 ${total} 条`
              : '总数量暂不可用'
          "
          :page-summary="`第 ${page} 页 · 本页 ${rows.length} 条`"
          @prev="previousPage"
          @next="nextPage"
          @size-change="load"
        />
      </template>
    </TablePanel>
    <ElDialog
      v-model="drawer"
      title="诊断详情"
      width="min(820px, calc(100vw - 32px))"
      class="management-dialog"
      align-center
      append-to-body
      destroy-on-close
    >
      <template v-if="selected">
        <div class="detail-heading">
          <ElTag
            :type="
              selected.level === 'ERROR'
                ? 'danger'
                : selected.level === 'INFO'
                  ? 'success'
                  : 'warning'
            "
            >{{ eventResult(selected) }}</ElTag
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
        <details class="exception-detail">
          <summary>查看完整诊断 JSON</summary>
          <pre tabindex="0" aria-label="完整诊断 JSON">{{ JSON.stringify(selected, null, 2) }}</pre>
        </details>
        <details v-if="selected.exceptionType" open class="exception-detail">
          <summary>技术堆栈</summary>
          <pre
            >{{ selected.exceptionType
            }}{{
              selected.rootExceptionType && selected.rootExceptionType !== selected.exceptionType
                ? `\n根因：${selected.rootExceptionType}`
                : ''
            }}{{ '\n' }}{{ selected.stack.join('\n') }}</pre>
        </details>
        <details v-if="selected.request" open class="exception-detail">
          <summary>请求参数（已脱敏）</summary>
          <pre>{{ JSON.stringify(selected.request, null, 2) }}</pre>
        </details>
        <details v-if="selected.response" open class="exception-detail">
          <summary>返回内容（已脱敏）</summary>
          <pre>{{ JSON.stringify(selected.response, null, 2) }}</pre>
        </details>
        <p v-if="!selected.request && !selected.response" class="last-read">
          未采集请求参数或返回内容。
        </p>
      </template>
    </ElDialog>
  </section>
</template>

<style scoped>
.diagnostics-page {
  min-width: 0;
}
.last-read {
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-caption);
  font-variant-numeric: tabular-nums;
}
.event-title {
  font-size: var(--sf-fs-sub);
  color: var(--sf-text-primary);
  overflow-wrap: anywhere;
}
.event-meta {
  font-family: var(--sf-font-mono);
  font-size: var(--sf-fs-caption);
  line-height: 1.6;
  color: var(--sf-text-secondary);
}
.query-warning {
  color: var(--sf-warning);
  font-size: var(--sf-fs-sub);
  margin: 10px 0;
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
  color: var(--el-color-primary);
}
.exception-detail pre {
  max-height: 480px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  padding: 14px;
  background: var(--el-fill-color-light);
  line-height: 1.7;
  font-size: 12px;
}
@media (max-width: 560px) {
  .search-form :deep(.el-form-item),
  .search-form :deep(.el-date-editor) {
    width: 100%;
    min-width: 0;
    margin-right: 0;
  }
  .search-form :deep(.el-form-item__content) {
    min-width: 0;
  }
  .search-form :deep(.el-input),
  .search-form :deep(.el-select) {
    width: 100%;
  }
  .event-detail > div {
    grid-template-columns: 90px minmax(0, 1fr);
  }
}
</style>
