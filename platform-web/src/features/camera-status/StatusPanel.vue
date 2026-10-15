<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElButton, ElOption, ElSelect } from 'element-plus'
import RequestError from '../../components/feedback/RequestError.vue'
import { streamLabel } from '../camera/form'
import { formatDateTime } from '../../utils/dateTime'
import { executionLabels, reasonLabel } from './model'
import StatusFact from './StatusFact.vue'
import { useStatusChecks } from './useStatusChecks'

const props = defineProps<{
  camera: {
    cameraId: string
    profiles: {
      streamProfileId: string
      label: string
      usageHint: string
      classification?: { usageHint: string; origin: string }
    }[]
  } | null
  active: boolean
  initialProfileId?: string | null
}>()
const profileId = ref<string | null>(null)
const cameraId = computed(() => props.camera?.cameraId ?? null)
const active = computed(() => props.active)
watch(
  [cameraId, () => props.initialProfileId],
  () => {
    profileId.value = props.initialProfileId ?? null
  },
  { immediate: true, flush: 'sync' },
)
watch(
  () => props.active,
  (active) => {
    if (active && props.initialProfileId) profileId.value = props.initialProfileId
  },
  { flush: 'sync' },
)
watch(
  () => props.camera?.profiles,
  (profiles) => {
    if (profileId.value && !profiles?.some((item) => item.streamProfileId === profileId.value))
      profileId.value = null
  },
  { flush: 'sync' },
)
const {
  selected,
  check,
  pending,
  pendingHere,
  canSubmit,
  reading,
  posting,
  readPaused,
  error,
  refresh,
  submit,
} = useStatusChecks(cameraId, profileId, active)
const targetError = computed(() =>
  pendingHere.value ? (pending.value?.error ?? error.value) : error.value,
)
const executionReason = computed(() => {
  if (!check.value?.reasonCode) return ''
  const fact = selected.value?.observation
  if (fact?.checkId === check.value.checkId && fact.reasonCode === check.value.reasonCode) return ''
  return reasonLabel(check.value.reasonCode)
})
</script>

<template>
  <section v-if="camera" class="camera-status-panel" aria-label="连接检查">
    <div class="status-heading">
      <h3>连接检查</h3>
      <ElButton link :loading="reading" :disabled="!active || reading" @click="refresh"
        >刷新结果</ElButton
      >
    </div>
    <div class="status-actions">
      <ElSelect
        v-model="profileId"
        placeholder="选择要检查的码流"
        aria-label="检查码流"
        :disabled="posting"
      >
        <ElOption
          v-for="profile in camera.profiles"
          :key="profile.streamProfileId"
          :label="streamLabel(profile)"
          :value="profile.streamProfileId"
        />
      </ElSelect>
      <ElButton
        type="primary"
        plain
        :loading="posting && pendingHere"
        :disabled="pendingHere ? !active || posting : !canSubmit"
        @click="submit"
      >
        {{ pendingHere && pending?.uncertain ? '重试原检查' : '检查 RTSP' }}
      </ElButton>
    </div>
    <p v-if="pending?.uncertain" class="status-note" role="status">
      {{
        pendingHere
          ? '响应未确认，重试将使用原检查请求。'
          : `通道 ${pending.cameraId} 的码流 ${pending.input.streamProfileId} 有未确认请求，请返回原目标重试。`
      }}
    </p>
    <p v-else-if="selected && !selected.canCheck" class="status-note">
      {{ reasonLabel(selected.blockingReasonCode) || '当前不能执行手动检查' }}
    </p>
    <template v-if="profileId">
      <StatusFact :profile="selected" />
      <div v-if="check" class="check-execution" aria-live="polite">
        <div class="execution-heading">
          <span>最近检查</span><strong>{{ executionLabels[check.executionState] }}</strong>
        </div>
        <p v-if="executionReason" class="status-note">{{ executionReason }}</p>
        <details class="check-diagnostics">
          <summary>检查信息</summary>
          <dl>
            <div>
              <dt>检查标识</dt>
              <dd>{{ check.checkId }}</dd>
            </div>
            <div>
              <dt>请求时间</dt>
              <dd>{{ formatDateTime(check.requestedAt) }}</dd>
            </div>
            <div v-if="check.reasonCode">
              <dt>原因码</dt>
              <dd>{{ check.reasonCode }}</dd>
            </div>
            <div>
              <dt>请求标识</dt>
              <dd>{{ check.traceId }}</dd>
            </div>
          </dl>
        </details>
      </div>
    </template>
    <p v-if="readPaused" class="status-note" role="status">
      已暂停读取，请点击“刷新结果”查询进度。
    </p>
    <RequestError :error="targetError" />
    <details class="check-diagnostics">
      <summary>检查范围</summary>
      <p>RTSP DESCRIBE 验证控制连接和视频描述，不获取视频首帧或媒体质量。</p>
    </details>
  </section>
</template>

<style scoped>
.camera-status-panel {
  margin-top: var(--camera-card-section-gap, 20px);
  padding-top: var(--camera-card-section-gap, 20px);
  border-top: 1px solid var(--el-border-color-lighter);
  font-size: var(--camera-card-text, 14px);
  line-height: 1.6;
}
.status-heading,
.execution-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
h3 {
  margin: 0;
  font-size: inherit;
}
.status-actions {
  display: flex;
  gap: 8px;
  margin: 12px 0 16px;
}
.status-actions .el-select {
  flex: 1;
  min-width: 0;
}
.status-actions .el-button {
  flex-shrink: 0;
  margin: 0;
}
.status-note {
  margin: 8px 0;
  color: var(--text-secondary);
  font-size: var(--camera-card-meta, 13px);
  overflow-wrap: anywhere;
}
.check-execution {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--el-border-color-extra-light);
}
.execution-heading > span {
  color: var(--text-secondary);
}
.execution-heading > strong {
  font-size: var(--camera-card-meta, 13px);
  font-weight: 500;
}
.check-diagnostics {
  margin-top: 8px;
  color: var(--text-secondary);
  font-size: var(--camera-card-meta, 13px);
}
summary {
  cursor: pointer;
  width: fit-content;
}
dl {
  display: grid;
  gap: 4px;
  margin: 8px 0 0;
}
dl > div {
  display: grid;
  grid-template-columns: 5em minmax(0, 1fr);
  gap: 12px;
}
dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
  user-select: text;
}
@media (max-width: 420px) {
  .status-actions {
    flex-wrap: wrap;
  }
  .status-actions .el-select {
    flex-basis: 100%;
  }
}
</style>
