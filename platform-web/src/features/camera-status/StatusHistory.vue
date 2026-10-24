<script setup lang="ts">
import { ref, watch, onScopeDispose } from 'vue'
import {
  ElButton,
  ElSelect,
  ElOption,
  ElPagination,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import { getStatusHistory } from '../../api/camera-status/api'
import type { StatusLayer } from '../../api/camera-status/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
import { reasonLabel, executionLabels } from './model'

const props = defineProps<{
  cameraId: string
  profileId: string
  layer: StatusLayer
  active: boolean
}>()
const days = ref(7),
  page = ref(1),
  size = ref(10),
  loading = ref(false),
  error = ref<unknown>(null)
const history = ref<Awaited<ReturnType<typeof getStatusHistory>> | null>(null)
const capture = usePageScope()
let sequence = 0,
  controller: globalThis.AbortController | undefined
async function load() {
  controller?.abort()
  const current = ++sequence,
    active = capture()
  if (!props.active) {
    loading.value = false
    return
  }
  controller = new globalThis.AbortController()
  loading.value = true
  error.value = null
  try {
    const result = await getStatusHistory(
      props.cameraId,
      {
        streamProfileId: props.profileId,
        layer: props.layer,
        days: days.value,
        page: page.value,
        size: size.value,
      },
      controller.signal,
    )
    if (active() && current === sequence) history.value = result
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
function changeSize(nextSize: number) {
  size.value = nextSize
  page.value = 1
  void load()
}
watch(
  () => [props.cameraId, props.profileId, props.layer, props.active, days.value],
  () => {
    history.value = null
    page.value = 1
    void load()
  },
  { immediate: true },
)
onScopeDispose(() => {
  sequence++
  controller?.abort()
})
</script>

<template>
  <section class="status-history" aria-label="检测记录">
    <header>
      <h4>检测记录</h4>
      <ElSelect v-model="days" aria-label="记录时间范围">
        <ElOption
          v-for="day in [1, 7, 30]"
          :key="day"
          :value="day"
          :label="`近 ${day} 天`"
        /> </ElSelect
      ><ElButton :loading="loading" @click="load">刷新记录</ElButton>
    </header>
    <RequestError :error="error" />
    <template v-if="history">
      <div class="history-summary-bar">
        <div class="sample-counts">
          <ElTag type="success" size="small">通过 {{ history.accessible }}</ElTag>
          <ElTag type="danger" size="small">异常 {{ history.failed }}</ElTag>
          <ElTag type="info" size="small">无确定结果 {{ history.unknown }}</ElTag>
        </div>
        <span v-if="history.truncated" class="truncated-note" role="status"
          >仅统计最近 1000 次留存检查</span
        >
      </div>
      <ElTable
        :data="history.items"
        row-key="checkId"
        size="small"
        max-height="220"
        class="history-table"
        empty-text="此范围暂无留存检查"
      >
        <ElTableColumn label="检查时间" width="152"
          ><template #default="{ row }">{{
            formatDateTime(row.requestedAt)
          }}</template></ElTableColumn
        >
        <ElTableColumn label="结果" min-width="135" show-overflow-tooltip
          ><template #default="{ row }">{{
            row.observation
              ? reasonLabel(row.observation.reasonCode)
              : executionLabels[row.executionState as keyof typeof executionLabels]
          }}</template></ElTableColumn
        >
        <ElTableColumn label="耗时" width="82"
          ><template #default="{ row }">{{
            row.durationMs == null ? '—' : `${row.durationMs} ms`
          }}</template></ElTableColumn
        >
        <ElTableColumn label="诊断" min-width="140"
          ><template #default="{ row }"
            ><details class="diag-details">
              <summary>查看</summary>
              <div class="diag-body">
                <div>{{ reasonLabel(row.reasonCode) || '—' }}</div>
                <div class="diag-trace">请求 {{ row.traceId }}</div>
              </div>
            </details></template
          ></ElTableColumn
        >
      </ElTable>
      <div class="history-pagination">
        <ElPagination
          v-model:current-page="page"
          v-model:page-size="size"
          :page-sizes="[5, 10, 20, 50]"
          :total="history.total"
          size="small"
          layout="total, sizes, prev, pager, next"
          :disabled="loading"
          @current-change="load"
          @size-change="changeSize"
        />
      </div>
    </template>
  </section>
</template>

<style scoped>
.status-history {
  margin-top: 12px;
  padding: 14px 16px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #f8fafc;
}
header {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
h4 {
  margin: 0 auto 0 0;
  color: #0f172a;
  font-size: 14px;
  font-weight: 600;
}
.el-select {
  width: 115px;
}
.history-summary-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  margin: 10px 0;
}
.sample-counts {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.truncated-note {
  color: #64748b;
  font-size: 12px;
}
.history-table {
  width: 100%;
  border-radius: 6px;
  overflow: hidden;
  border: 1px solid #e2e8f0;
}
.diag-details {
  overflow-wrap: anywhere;
  word-break: break-all;
}
.diag-details summary {
  cursor: pointer;
  color: #2563eb;
  user-select: none;
}
.diag-body {
  margin-top: 4px;
  color: #475569;
  font-size: 12px;
  line-height: 1.45;
}
.diag-trace {
  color: #64748b;
  font-family: Consolas, monospace;
}
.history-pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px solid #e2e8f0;
}
.history-pagination :deep(.el-pagination) {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px 0;
}
</style>
