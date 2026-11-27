<script setup lang="ts">
import { ref } from 'vue'
import { ElButton, ElDialog } from 'element-plus'
import DiagnosticLink from '../diagnostics/DiagnosticLink.vue'
import { formatDateTime } from '../../utils/dateTime'
import type { PreviewFailure } from './failure'
import { streamLabel } from '../camera/streamLabels'

defineProps<{ failure: PreviewFailure }>()
const emit = defineEmits<{ close: [] }>()
const open = ref(true)
</script>

<template>
  <ElDialog
    v-model="open"
    class="management-dialog"
    title="预览排查信息"
    width="min(620px, calc(100vw - 32px))"
    align-center
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
          {{ failure.profileName ? streamLabel({ label: failure.profileName }) : '—'
          }}<span v-if="failure.streamProfileId"> · {{ failure.streamProfileId }}</span>
        </dd>
      </div>
      <div v-if="failure.leaseId">
        <dt>预览租约</dt>
        <dd>{{ failure.leaseId }}</dd>
      </div>
      <div v-if="failure.traceId" class="field-wide">
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
  margin: 0 0 14px;
  padding: 10px 14px;
  border: 1px solid var(--sf-danger-border);
  border-radius: var(--sf-radius-md);
  background: var(--sf-danger-soft);
  color: var(--el-color-danger-dark-2);
  font-size: var(--sf-fs-sub);
  font-weight: 500;
  line-height: 1.5;
}
.failure-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 16px;
  margin: 0 0 14px;
  padding: 12px 14px;
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-md);
  background: var(--sf-surface-subtle);
}
.failure-fields > div {
  display: grid;
  gap: 3px;
  min-width: 0;
}
.failure-fields > div.field-wide {
  grid-column: 1 / -1;
}
.failure-fields dt {
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-caption);
  font-weight: 500;
}
.failure-fields dd {
  margin: 0;
  color: var(--sf-text-primary);
  font-size: var(--sf-fs-sub);
  font-weight: 500;
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
}
.failure-note {
  margin: 10px 0 0;
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-caption);
  line-height: 1.5;
}
.failure-json {
  margin-top: 14px;
  font-size: var(--sf-fs-sub);
}
.failure-json summary {
  width: fit-content;
  cursor: pointer;
  color: var(--sf-primary);
  font-weight: 500;
}
.failure-json pre {
  max-height: 220px;
  overflow: auto;
  margin: 8px 0 0;
  padding: 10px 12px;
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-sm);
  background: var(--sf-surface-subtle);
  font-family: var(--sf-font-mono);
  font-size: var(--sf-fs-caption);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
@media (max-width: 540px) {
  .failure-fields {
    grid-template-columns: 1fr;
  }
}
</style>
