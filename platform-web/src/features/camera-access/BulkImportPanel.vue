<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDescriptions,
  ElDescriptionsItem,
  ElPagination,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import * as api from '../../api/camera-access/api'
import type {
  AccessJob,
  BulkImportImpact,
  BulkImportInput,
  ImportItem,
} from '../../api/camera-access/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { isRequestRejected, useCreateRequest } from '../camera/form'
import { formatDateTime } from '../../utils/dateTime'
import { methodLabels } from './form'

const props = withDefaults(
  defineProps<{ initialJob: AccessJob; groupId?: string | null; closeLabel?: string }>(),
  { closeLabel: '完成' },
)
const emit = defineEmits<{
  close: []
  back: []
  job: [job: AccessJob]
  retry: [job: AccessJob]
  starting: []
}>()
const captureScope = usePageScope(),
  job = ref(props.initialJob),
  impact = ref<BulkImportImpact | null>(null),
  error = ref<unknown>(null)
const busy = ref(false),
  refreshing = ref(false),
  paused = ref(false),
  failuresOpen = ref(false),
  failurePage = ref(1),
  failureTotal = ref(0),
  failures = ref<ImportItem[]>([]),
  failuresLoading = ref(false)
const confirmed = shallowRef<BulkImportInput | null>(null)
const creation = useCreateRequest<BulkImportInput & { confirmation: string }>()
const started = computed(() => job.value.kind === 'BULK_IMPORT')
const running = computed(() => started.value && ['QUEUED', 'RUNNING'].includes(job.value.status))
const limitReached = computed(() => job.value.diagnostic?.reasonCode === 'IMPORT_LIMIT_REACHED')
const canRetry = computed(
  () =>
    started.value &&
    !running.value &&
    job.value.status !== 'SUCCEEDED' &&
    !limitReached.value &&
    !!job.value.sourceId &&
    !!job.value.sourceVersion,
)
const statusLabels: Record<AccessJob['status'], string> = {
  QUEUED: '等待导入',
  RUNNING: '正在导入',
  SUCCEEDED: '导入完成',
  PARTIAL: '部分完成',
  FAILED: '导入中断',
  CANCELLED: '已取消',
  EXPIRED: '已到期',
}
let timer: ReturnType<typeof globalThis.setTimeout> | undefined,
  sequence = 0,
  failureSequence = 0,
  reads = 0
function stopPolling() {
  if (timer) globalThis.clearTimeout(timer)
  timer = undefined
}
function accept(value: AccessJob) {
  job.value = value
  emit('job', value)
}
function schedule() {
  stopPolling()
  if (running.value && ++reads < 600) timer = globalThis.setTimeout(() => void refresh(), 3000)
  else if (running.value) paused.value = true
}
async function preview() {
  if (busy.value) return
  const active = captureScope()
  busy.value = true
  error.value = null
  try {
    const input = { version: job.value.version, groupId: props.groupId ?? null }
    const value = await api.previewBulkImport(job.value.jobId, input)
    if (active()) {
      impact.value = value
      confirmed.value = input
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) busy.value = false
  }
}
async function start() {
  if (busy.value || !impact.value || !confirmed.value) return
  const active = captureScope()
  busy.value = true
  error.value = null
  emit('starting')
  try {
    const value = await api.startBulkImport(
      job.value.jobId,
      creation.capture({ ...confirmed.value, confirmation: impact.value.confirmation }),
    )
    if (!active()) return
    accept(value)
    creation.reset()
    impact.value = null
    confirmed.value = null
    schedule()
  } catch (cause) {
    if (active()) {
      error.value = cause
      if (isRequestRejected(cause)) {
        creation.reset()
        impact.value = null
        confirmed.value = null
      }
    }
  } finally {
    if (active()) busy.value = false
  }
}
async function refresh() {
  if (refreshing.value || busy.value) return
  const active = captureScope(),
    current = ++sequence
  refreshing.value = true
  stopPolling()
  try {
    const value = await api.getAccessJob(job.value.jobId)
    if (!active() || current !== sequence) return
    accept(value)
    paused.value = false
    schedule()
  } catch (cause) {
    if (active() && current === sequence) {
      error.value = cause
      paused.value = true
    }
  } finally {
    if (active() && current === sequence) refreshing.value = false
  }
}
async function cancel() {
  if (busy.value || !running.value) return
  const active = captureScope()
  sequence++
  stopPolling()
  refreshing.value = false
  busy.value = true
  error.value = null
  try {
    const value = await api.cancelAccessJob(job.value.jobId)
    if (active()) {
      accept(value)
      schedule()
    }
  } catch (cause) {
    if (active()) {
      error.value = cause
      paused.value = true
    }
  } finally {
    if (active()) busy.value = false
  }
}
async function loadFailures() {
  const active = captureScope(),
    current = ++failureSequence
  failuresLoading.value = true
  failuresOpen.value = true
  try {
    const value = await api.getImportItems(job.value.jobId, {
      page: failurePage.value,
      size: 20,
      status: 'FAILED',
    })
    if (active() && current === failureSequence) {
      failures.value = value.items
      failureTotal.value = value.total
    }
  } catch (cause) {
    if (active() && current === failureSequence) error.value = cause
  } finally {
    if (active() && current === failureSequence) failuresLoading.value = false
  }
}
onMounted(() => {
  if (started.value) void refresh()
  else void preview()
})
onBeforeUnmount(() => {
  sequence++
  failureSequence++
  stopPolling()
  creation.reset()
})
</script>
<template>
  <section class="bulk-import-panel">
    <RequestError :error="error" />
    <template v-if="!started">
      <h3>整批导入平台目录</h3>
      <ElDescriptions v-if="impact" :column="2" border>
        <ElDescriptionsItem label="范围"
          >全部目录，共 {{ impact.declaredTotal ?? '待统计' }} 个资源</ElDescriptionsItem
        >
        <ElDescriptionsItem label="目标分组">{{ impact.groupPath || '待归档' }}</ElDescriptionsItem>
        <ElDescriptionsItem label="分组授权"
          >{{ impact.affectedUserCount }} 个账户</ElDescriptionsItem
        >
        <ElDescriptionsItem label="本次上限">{{ impact.maxItems }} 个资源</ElDescriptionsItem>
      </ElDescriptions>
      <p class="panel-note">已有相机保留原分组。完成后可在相机列表查看。</p>
      <ElAlert
        v-if="creation.pending.value"
        type="warning"
        :closable="false"
        title="启动结果未确认，请重试。"
      />
      <div class="panel-actions">
        <ElButton :disabled="busy || !!creation.pending.value" @click="emit('back')"
          >返回选择</ElButton
        >
        <ElButton v-if="!impact" :loading="busy" @click="preview">重新检查</ElButton>
        <ElButton v-else type="primary" :loading="busy" @click="start">{{
          creation.pending.value ? '重试原启动请求' : '确认整批导入'
        }}</ElButton>
      </div>
    </template>
    <template v-else>
      <div class="progress-heading">
        <h3>{{ statusLabels[job.status] }}</h3>
        <ElTag>{{ methodLabels[job.method] ?? job.method }}</ElTag>
      </div>
      <template v-if="job.bulk">
        <p class="progress-count">
          已处理 <strong>{{ job.bulk.processedCount }}</strong
          ><span v-if="job.bulk.total != null"> / {{ job.bulk.total }}</span> 个资源 · 第
          {{ job.bulk.pageNumber }} 页
        </p>
        <progress
          v-if="job.bulk.total && running"
          :value="job.bulk.processedCount"
          :max="job.bulk.total"
          aria-label="导入进度"
        />
        <dl class="import-counts">
          <div>
            <dt>新增</dt>
            <dd>{{ job.bulk.createdCount }}</dd>
          </div>
          <div>
            <dt>已有</dt>
            <dd>{{ job.bulk.existingCount }}</dd>
          </div>
          <div>
            <dt>失败</dt>
            <dd>{{ job.bulk.failedCount }}</dd>
          </div>
          <div>
            <dt>目录重复</dt>
            <dd>{{ job.bulk.duplicateCount }}</dd>
          </div>
        </dl>
      </template>
      <ElAlert
        v-if="job.diagnostic && job.diagnostic.reasonCode !== 'CANCELLED_BY_USER'"
        type="warning"
        :closable="false"
        :title="job.diagnostic.actionHint"
      />
      <p v-if="running" class="panel-note">关闭窗口后继续导入，可从“导入任务”查看。</p>
      <p v-else-if="limitReached" class="panel-note">
        已达到本次整批导入上限，已成功的相机已保留。重新执行仍从首页开始，不会续传剩余目录。
      </p>
      <p v-else-if="job.status !== 'SUCCEEDED'" class="panel-note">
        已成功的相机已保留。重试会重新读取目录并匹配已有相机。
      </p>
      <p v-if="paused" class="panel-note">自动查询已暂停，请刷新状态。</p>
      <details class="panel-note">
        <summary>任务信息</summary>
        <p>{{ job.jobId }}</p>
        <p>执行期限 {{ formatDateTime(job.expiresAt) }}</p>
        <p v-if="job.diagnostic">
          {{ job.diagnostic.reasonCode }} · {{ job.diagnostic.originTraceId }}
        </p>
      </details>
      <ElButton
        v-if="job.bulk?.failedCount"
        link
        type="primary"
        :loading="failuresLoading"
        @click="loadFailures"
        >失败明细（{{ job.bulk.failedCount }}）</ElButton
      >
      <template v-if="failuresOpen">
        <ElTable :data="failures" row-key="id" border
          ><ElTableColumn label="相机" min-width="170"
            ><template #default="{ row }">{{
              row.name || row.externalKey
            }}</template></ElTableColumn
          ><ElTableColumn prop="pageNumber" label="目录页" width="90" /><ElTableColumn
            prop="reasonCode"
            label="原因"
            min-width="160"
        /></ElTable>
        <ElPagination
          v-model:current-page="failurePage"
          :page-size="20"
          :total="failureTotal"
          layout="total, prev, pager, next"
          @current-change="loadFailures"
        />
      </template>
      <div class="panel-actions">
        <ElButton :loading="refreshing" :disabled="busy" @click="refresh">刷新状态</ElButton
        ><ElButton v-if="running" :loading="busy" @click="cancel">取消导入</ElButton
        ><ElButton v-if="canRetry" :disabled="busy" @click="emit('retry', job)"
          >重新读取并重试</ElButton
        ><ElButton type="primary" :disabled="busy" @click="emit('close')">{{
          closeLabel
        }}</ElButton>
      </div>
    </template>
  </section>
</template>
<style scoped>
.bulk-import-panel h3 {
  margin: 0 0 18px;
  font-size: 18px;
}
.progress-heading {
  display: flex;
  align-items: center;
  gap: 12px;
}
.progress-heading h3 {
  margin: 0;
}
.progress-count {
  font-size: 16px;
}
.progress-count strong {
  font-size: 24px;
  font-variant-numeric: tabular-nums;
}
.bulk-import-panel progress {
  width: 100%;
  height: 8px;
  accent-color: var(--el-color-primary);
}
.import-counts {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin: 20px 0;
}
.import-counts > div {
  padding: 16px;
  background: var(--el-fill-color-light);
  border-radius: 4px;
}
.import-counts dt {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.import-counts dd {
  margin: 5px 0 0;
  font-size: 24px;
  font-variant-numeric: tabular-nums;
}
.panel-note {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}
.panel-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 24px;
}
.panel-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.bulk-import-panel :deep(.el-pagination) {
  margin-top: 14px;
}
</style>
