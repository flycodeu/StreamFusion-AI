<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElButton, ElOption, ElSelect } from 'element-plus'
import type { StatusLayer } from '../../api/camera-status/types'
import { layerKey } from '../../api/camera-status/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { streamLabel } from '../camera/form'
import { formatDateTime } from '../../utils/dateTime'
import { executionLabels, isTerminal, reasonLabel } from './model'
import StatusFact from './StatusFact.vue'
import StatusHistory from './StatusHistory.vue'
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
  initialLayer?: StatusLayer
}>()
const profileId = ref<string | null>(null)
const layer = ref<StatusLayer>(props.initialLayer ?? 'MEDIA')
const historyOpen = ref(false)
const cameraId = computed(() => props.camera?.cameraId ?? null)
const active = computed(() => props.active)
watch(
  [cameraId, () => props.initialProfileId],
  () => {
    profileId.value = props.initialProfileId ?? null
    layer.value = props.initialLayer ?? 'MEDIA'
  },
  { immediate: true, flush: 'sync' },
)
watch(
  () => props.active,
  (active) => {
    if (active) {
      if (props.initialProfileId) profileId.value = props.initialProfileId
      layer.value = props.initialLayer ?? 'MEDIA'
    }
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
  selectedProfile,
  selected,
  check,
  pending,
  pendingHere,
  reading,
  posting,
  readPaused,
  error,
  refresh,
  submit,
} = useStatusChecks(cameraId, profileId, active, layer)
const availableLayers = computed<StatusLayer[]>(() => {
  const upstream = selectedProfile.value?.upstream
  return upstream && upstream.blockingReasonCode !== 'STATUS_LAYER_NOT_APPLICABLE'
    ? ['UPSTREAM']
    : ['MEDIA', 'CONTROL']
})
watch(availableLayers, (values) => {
  if (!values.includes(layer.value)) layer.value = values[0]!
})
watch(
  () => props.initialLayer,
  (value) => {
    if (value) layer.value = value
  },
)
function canCheck(target: StatusLayer) {
  if (!props.active || posting.value || !profileId.value) return false
  if (pending.value) return pendingHere.value && pending.value.input.layer === target
  const state = selectedProfile.value?.[layerKey(target)]
  const latest = layer.value === target ? check.value : state?.latestCheck
  return !!state?.canCheck && (!latest || isTerminal(latest))
}
async function inspect(target: StatusLayer) {
  if (!canCheck(target)) return
  layer.value = target
  await submit()
}
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
  <section v-if="camera" class="camera-status-panel" aria-label="相机状态检查">
    <div class="status-heading">
      <h3>状态检查</h3>
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
    </div>
    <div class="check-buttons">
      <ElButton
        v-for="target in availableLayers"
        :key="target"
        :type="target === 'MEDIA' ? 'primary' : 'default'"
        :loading="posting && pending?.input.layer === target"
        :disabled="!canCheck(target)"
        @click="inspect(target)"
      >
        {{
          pending?.uncertain && pending.input.layer === target
            ? '重试原检查'
            : target === 'UPSTREAM'
              ? '查询平台状态'
              : target === 'MEDIA'
                ? '检查视频'
                : '检查连接'
        }}
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
      <div class="layer-switcher" role="group" aria-label="检查结果层级">
        <ElButton
          v-for="target in availableLayers"
          :key="target"
          link
          :type="layer === target ? 'primary' : 'default'"
          :aria-pressed="layer === target"
          @click="layer = target"
          >{{
            target === 'UPSTREAM' ? '平台状态' : target === 'MEDIA' ? '视频收帧' : '连接检查'
          }}</ElButton
        >
      </div>
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
    <ElButton v-if="profileId" link type="primary" @click="historyOpen = !historyOpen">{{
      historyOpen ? '收起检测记录' : '检测记录'
    }}</ElButton>
    <StatusHistory
      v-if="historyOpen && profileId"
      :camera-id="camera.cameraId"
      :profile-id="profileId"
      :layer="layer"
      :active="active"
    />
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
.check-buttons,
.layer-switcher {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}
.check-buttons .el-button,
.layer-switcher .el-button {
  margin-left: 0;
}
.layer-switcher {
  padding-top: 4px;
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
