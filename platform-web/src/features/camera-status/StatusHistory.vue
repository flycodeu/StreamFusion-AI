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
  history.value = null
  error.value = null
  try {
    const result = await getStatusHistory(
      props.cameraId,
      {
        streamProfileId: props.profileId,
        layer: props.layer,
        days: days.value,
        page: page.value,
        size: 20,
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
      <div class="sample-counts">
        <ElTag type="success">通过 {{ history.accessible }}</ElTag>
        <ElTag type="danger">异常 {{ history.failed }}</ElTag
        ><ElTag type="info">无确定结果 {{ history.unknown }}</ElTag>
      </div>
      <p v-if="history.truncated" role="status">仅统计最近 1000 次留存检查</p>
      <ElTable :data="history.items" row-key="checkId" size="small" empty-text="此范围暂无留存检查">
        <ElTableColumn label="检查时间" min-width="155"
          ><template #default="{ row }">{{
            formatDateTime(row.requestedAt)
          }}</template></ElTableColumn
        >
        <ElTableColumn label="结果" min-width="130"
          ><template #default="{ row }">{{
            row.observation
              ? reasonLabel(row.observation.reasonCode)
              : executionLabels[row.executionState as keyof typeof executionLabels]
          }}</template></ElTableColumn
        >
        <ElTableColumn label="耗时" width="85"
          ><template #default="{ row }">{{
            row.durationMs == null ? '—' : `${row.durationMs} ms`
          }}</template></ElTableColumn
        >
        <ElTableColumn label="诊断" min-width="120"
          ><template #default="{ row }"
            ><details>
              <summary>查看</summary>
              <div>{{ reasonLabel(row.reasonCode) }}</div>
              <div>请求 {{ row.traceId }}</div>
            </details></template
          ></ElTableColumn
        >
      </ElTable>
      <ElPagination
        v-if="history.total > 20"
        v-model:current-page="page"
        :page-size="20"
        :total="history.total"
        layout="prev, pager, next"
        :disabled="loading"
        @current-change="load"
      />
    </template>
  </section>
</template>

<style scoped>
.status-history {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--el-border-color-lighter);
}
header {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
}
h4 {
  margin: 0 auto 0 0;
}
.el-select {
  width: 115px;
}
.sample-counts {
  display: flex;
  gap: 8px;
  margin: 12px 0;
  flex-wrap: wrap;
}
details {
  overflow-wrap: anywhere;
}
summary {
  cursor: pointer;
}
.el-pagination {
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
