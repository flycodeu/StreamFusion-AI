<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { ElButton, ElOption, ElPagination, ElSelect } from 'element-plus'
import { getCamera, getDeviceChannels } from '../../api/camera/api'
import { getCameraStatus } from '../../api/camera-status/api'
import type { Camera, CameraProfile } from '../../api/camera/types'
import type { PlanTarget, TargetSelection } from '../../api/camera-status/planTargets'
import { sessionState } from '../../session/state'
import { usePageScope } from '../../composables/usePageScope'
import RequestError from '../../components/feedback/RequestError.vue'
import { streamLabel } from '../camera/streamLabels'
import { reasonLabel } from './model'

const props = defineProps<{ targets: PlanTarget[]; disabled: boolean }>()
const emit = defineEmits<{ change: [selection: TargetSelection] }>()
const page = ref(1)
const visible = computed(() => props.targets.slice((page.value - 1) * 10, page.value * 10))
const editing = shallowRef<PlanTarget | null>(null)
const channels = ref<Camera[]>([])
const channelId = ref<string | null>(null)
const profileId = ref<string | null>(null)
const profiles = ref<CameraProfile[]>([])
const channelPage = ref(0)
const channelTotal = ref(0)
const loading = ref(false)
const profileLoading = ref(false)
const channelError = shallowRef<unknown>(null)
const profileError = shallowRef<unknown>(null)
const error = computed(() => channelError.value || profileError.value)
const capture = usePageScope()
let revision = 0
let profileRevision = 0
let controller: globalThis.AbortController | undefined

watch(
  () => props.targets.length,
  () => {
    page.value = Math.min(page.value, Math.max(1, Math.ceil(props.targets.length / 10)))
  },
)
watch(() => sessionState.epoch, cancel, { flush: 'sync' })
watch(
  () => props.targets,
  () => {
    if (
      editing.value &&
      !props.targets.some((target) => target.groupKey === editing.value?.groupKey)
    )
      cancel()
  },
)
function cancel() {
  ++revision
  ++profileRevision
  controller?.abort()
  editing.value = null
  loading.value = false
  profileLoading.value = false
  channelError.value = null
  profileError.value = null
}
async function edit(target: PlanTarget) {
  if (props.disabled) return
  cancel()
  editing.value = target
  channels.value = []
  profiles.value = []
  channelId.value = target.cameraId ?? null
  profileId.value = target.streamProfileId ?? null
  channelPage.value = 0
  channelTotal.value = 0
  await Promise.all([loadChannels(), loadProfiles()])
}
async function loadChannels() {
  const target = editing.value
  if (!target || loading.value) return
  const current = revision,
    active = capture()
  loading.value = true
  channelError.value = null
  try {
    const result = await getDeviceChannels(target.groupKey, {
      page: channelPage.value + 1,
      size: 50,
      lifecycle: 'ENABLED',
    })
    if (current !== revision || !active()) return
    channels.value = [
      ...new Map(
        [...channels.value, ...result.items].map((camera) => [camera.cameraId, camera]),
      ).values(),
    ]
    channelPage.value = result.page
    channelTotal.value = result.total
  } catch (cause) {
    if (current === revision && active()) channelError.value = cause
  } finally {
    if (current === revision && active()) loading.value = false
  }
}
async function loadProfiles() {
  const id = channelId.value
  if (!editing.value || !id) return
  const wanted =
    profileId.value ??
    (id === editing.value.cameraId ? (editing.value.streamProfileId ?? null) : null)
  const current = ++profileRevision,
    active = capture()
  controller?.abort()
  const request = new globalThis.AbortController()
  controller = request
  profiles.value = []
  profileId.value = null
  profileLoading.value = true
  profileError.value = null
  try {
    const [camera, status] = await Promise.all([
      getCamera(id, request.signal),
      getCameraStatus(id, request.signal),
    ])
    if (request.signal.aborted || current !== profileRevision || !active()) return
    channels.value = [
      ...new Map([...channels.value, camera].map((item) => [item.cameraId, item])).values(),
    ]
    const byId = new Map(
      camera.profiles
        .filter((profile) => profile.enabled)
        .map((profile) => [profile.streamProfileId, profile]),
    )
    profiles.value = status.profiles.flatMap((profile) => {
      const detail = byId.get(profile.streamProfileId)
      return detail ? [detail] : []
    })
    profileId.value = profiles.value.some((profile) => profile.streamProfileId === wanted)
      ? wanted
      : (profiles.value[0]?.streamProfileId ?? null)
  } catch (cause) {
    if (!request.signal.aborted && current === profileRevision && active())
      profileError.value = cause
  } finally {
    if (!request.signal.aborted && current === profileRevision && active())
      profileLoading.value = false
  }
}
function channelChanged() {
  profileId.value = null
  void loadProfiles()
}
function save() {
  if (
    props.disabled ||
    loading.value ||
    profileLoading.value ||
    error.value ||
    !editing.value ||
    !channelId.value ||
    !profileId.value
  )
    return
  emit('change', {
    groupKey: editing.value.groupKey,
    cameraId: channelId.value,
    streamProfileId: profileId.value,
  })
  cancel()
}
function profileLabel(profile: CameraProfile) {
  return `${streamLabel(profile, profiles.value)}${profile.bitrateKbps ? ` · ${profile.bitrateKbps} kbps` : ''}`
}
onScopeDispose(cancel)
</script>

<template>
  <section class="target-settings" aria-label="每台相机检测码流">
    <div class="target-table">
      <div class="target-row target-header">
        <span>相机</span><span>检测通道 / 码流</span><span />
      </div>
      <div v-for="target in visible" :key="target.groupKey" class="target-row">
        <strong :title="target.name">{{ target.name }}</strong>
        <span v-if="target.videoSupported === false" class="target-description">平台在线状态</span>
        <span
          v-else
          class="target-description"
          :title="target.reasonCode ? reasonLabel(target.reasonCode) : undefined"
        >
          {{ target.channelName || '自动选择通道' }} /
          {{ target.profileName ? streamLabel({ label: target.profileName }) : '低负载码流' }}
        </span>
        <ElButton
          v-if="target.videoSupported !== false"
          link
          :disabled="disabled"
          :aria-label="`调整 ${target.name} 检测码流`"
          @click="edit(target)"
          >调整</ElButton
        >
      </div>
    </div>
    <ElPagination
      v-if="targets.length > 10"
      class="target-pagination"
      :current-page="page"
      :page-size="10"
      :total="targets.length"
      layout="total, prev, pager, next"
      @update:current-page="page = $event"
    />
    <div v-if="editing" class="target-editor" :aria-label="`设置 ${editing.name} 检测码流`">
      <header>
        <strong>{{ editing.name }}</strong
        ><ElButton link :disabled="disabled" @click="cancel">取消调整</ElButton>
      </header>
      <RequestError :error="error" />
      <div class="target-controls">
        <ElSelect
          v-model="channelId"
          filterable
          :disabled="disabled"
          :loading="loading"
          aria-label="巡检检测通道"
          placeholder="选择通道"
          @change="channelChanged"
        >
          <ElOption
            v-for="camera in channels"
            :key="camera.cameraId"
            :value="camera.cameraId"
            :label="camera.name"
          />
        </ElSelect>
        <ElSelect
          v-model="profileId"
          :disabled="disabled || profileLoading"
          :loading="profileLoading"
          aria-label="巡检检测码流"
          placeholder="选择码流"
        >
          <ElOption
            v-for="profile in profiles"
            :key="profile.streamProfileId"
            :value="profile.streamProfileId"
            :label="profileLabel(profile)"
          />
        </ElSelect>
        <ElButton
          :disabled="disabled || loading || profileLoading || !!error || !profileId"
          @click="save"
          >应用</ElButton
        >
      </div>
      <div class="target-load-actions">
        <ElButton
          v-if="channelError || channelPage * 50 < channelTotal || (!channelPage && !loading)"
          link
          :loading="loading"
          :disabled="disabled"
          @click="loadChannels"
          >{{ channelError ? '重新加载通道' : '加载更多通道' }}</ElButton
        >
        <ElButton v-if="profileError && channelId" link :disabled="disabled" @click="loadProfiles"
          >重新加载码流</ElButton
        >
      </div>
    </div>
  </section>
</template>

<style scoped>
.target-settings {
  margin-top: 16px;
}
.target-table {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  overflow: hidden;
}
.target-row {
  display: grid;
  grid-template-columns: minmax(140px, 1fr) minmax(180px, 1.4fr) 50px;
  align-items: center;
  gap: 16px;
  padding: 10px 16px;
  border-bottom: 1px solid #f1f5f9;
  font-size: 13px;
}
.target-row:last-child {
  border-bottom: 0;
}
.target-row strong,
.target-description {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.target-row strong {
  color: #334155;
  font-weight: 500;
}
.target-description {
  color: #64748b;
}
.target-header {
  background: #f8fafc;
  color: #64748b;
  font-size: 12px;
}
.target-pagination {
  justify-content: flex-end;
  margin-top: 12px;
}
.target-editor {
  margin-top: 12px;
  padding: 14px 16px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}
.target-editor header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
  color: #334155;
  font-size: 13px;
}
.target-controls {
  display: flex;
  align-items: center;
  gap: 10px;
}
.target-controls :deep(.el-select) {
  flex: 1;
  min-width: 0;
}
.target-load-actions {
  display: flex;
  gap: 12px;
}
@media (max-width: 680px) {
  .target-row {
    grid-template-columns: minmax(80px, 1fr) minmax(100px, 1.4fr) 40px;
    padding: 10px;
    gap: 8px;
  }
  .target-controls {
    flex-wrap: wrap;
  }
  .target-controls :deep(.el-select) {
    flex-basis: 100%;
  }
}
</style>
