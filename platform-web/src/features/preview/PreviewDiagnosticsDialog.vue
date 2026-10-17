<script setup lang="ts">
import { ref } from 'vue'
import { ElButton, ElDialog } from 'element-plus'
import DiagnosticLink from '../diagnostics/DiagnosticLink.vue'
import { formatDateTime } from '../../utils/dateTime'
import type { PreviewFailure } from './failure'

defineProps<{ failure: PreviewFailure }>()
const emit = defineEmits<{ close: [] }>()
const open = ref(true)
</script>

<template>
  <ElDialog
    v-model="open"
    title="预览排查信息"
    width="min(600px, calc(100vw - 32px))"
    append-to-body
    destroy-on-close
    @closed="emit('close')"
  >
    <p class="failure-message">{{ failure.message }}</p>
    <dl class="failure-fields">
      <div>
        <dt>错误码</dt>
        <dd>{{ failure.code }}</dd>
      </div>
      <div v-if="failure.requestCode">
        <dt>请求错误码</dt>
        <dd>{{ failure.requestCode }}</dd>
      </div>
      <div>
        <dt>发生时间</dt>
        <dd>{{ formatDateTime(failure.timestamp) }}</dd>
      </div>
      <div>
        <dt>通道</dt>
        <dd>
          {{ failure.cameraName || '—'
          }}<span v-if="failure.cameraId"> · {{ failure.cameraId }}</span>
        </dd>
      </div>
      <div>
        <dt>码流</dt>
        <dd>
          {{ failure.profileName || '—'
          }}<span v-if="failure.streamProfileId"> · {{ failure.streamProfileId }}</span>
        </dd>
      </div>
      <div v-if="failure.leaseId">
        <dt>预览租约</dt>
        <dd>{{ failure.leaseId }}</dd>
      </div>
      <div v-if="failure.traceId">
        <dt>请求标识</dt>
        <dd>{{ failure.traceId }}</dd>
      </div>
    </dl>
    <DiagnosticLink :trace-id="failure.traceId" :time="failure.timestamp" />
    <p v-if="!failure.traceId" class="failure-note">
      本次未取得请求标识，可按发生时间、通道和租约定位；返回画面后可重连或切换码流。
    </p>
    <details class="failure-json">
      <summary>查看 JSON</summary>
      <pre>{{ JSON.stringify(failure, null, 2) }}</pre>
    </details>
    <template #footer><ElButton @click="open = false">关闭</ElButton></template>
  </ElDialog>
</template>

<style scoped>
.failure-message {
  margin: 0 0 18px;
  color: var(--el-text-color-primary);
  line-height: 1.6;
}
.failure-fields {
  display: grid;
  gap: 10px;
  margin: 0 0 16px;
}
.failure-fields > div {
  display: grid;
  grid-template-columns: 6em minmax(0, 1fr);
  gap: 12px;
}
.failure-fields dt,
.failure-note {
  color: var(--el-text-color-secondary);
}
.failure-fields dd {
  margin: 0;
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
}
.failure-note {
  font-size: 13px;
  line-height: 1.6;
}
.failure-json {
  margin-top: 18px;
  font-size: 13px;
}
.failure-json summary {
  width: fit-content;
  cursor: pointer;
  color: var(--el-color-primary);
}
.failure-json pre {
  max-height: 280px;
  overflow: auto;
  padding: 12px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
  background: var(--el-fill-color-light);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
@media (max-width: 420px) {
  .failure-fields > div {
    grid-template-columns: 5em minmax(0, 1fr);
    gap: 8px;
  }
}
</style>
