<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElButton, ElDrawer, ElMessageBox, ElPagination, ElTag, vLoading } from 'element-plus'
import * as api from '../../api/camera/api'
import type { Camera, CameraDeviceGroup } from '../../api/camera/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { connectionLabel, lifecycleLabels, streamLabel } from './form'
import DetailDialog from './DetailDialog.vue'
import PlacementDialog from './PlacementDialog.vue'
import DeviceMoveDialog from './DeviceMoveDialog.vue'
import DeviceEditor from './DeviceEditor.vue'

const props = defineProps<{ device: CameraDeviceGroup; canManage: boolean }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const channels = ref<Camera[]>([]),
  selectedId = ref<string | null>(null),
  camera = ref<Camera | null>(null),
  page = ref(1),
  total = ref(0),
  loading = ref(false),
  detailLoading = ref(false),
  deleting = ref(false),
  error = ref<unknown>(null),
  detailError = ref<unknown>(null)
const editingId = ref<string | null>(null)
const placement = ref<{ cameraId: string; action: 'enable' | 'disable' } | null>(null)
const moving = ref(false)
const editingDevice = ref(false),
  editedName = ref<string | null>(null)
const deviceName = computed(() => editedName.value ?? props.device.name)
watch(
  () => props.device.name,
  () => {
    editedName.value = null
  },
)
async function deviceSaved(name: string) {
  editedName.value = name
  editingDevice.value = false
  await changed()
}
const selected = computed(() => channels.value.find((item) => item.cameraId === selectedId.value))
const busy = computed(() => loading.value || detailLoading.value || deleting.value)
const captureScope = usePageScope()
let listSequence = 0,
  detailSequence = 0

async function selectChannel(cameraId: string) {
  const active = captureScope(),
    current = ++detailSequence
  selectedId.value = cameraId
  camera.value = null
  detailError.value = null
  detailLoading.value = true
  try {
    const result = await api.getCamera(cameraId)
    if (active() && current === detailSequence) camera.value = result
  } catch (cause) {
    if (active() && current === detailSequence) detailError.value = cause
  } finally {
    if (active() && current === detailSequence) detailLoading.value = false
  }
}
async function load() {
  const active = captureScope(),
    current = ++listSequence
  ++detailSequence
  camera.value = null
  detailError.value = null
  detailLoading.value = false
  loading.value = true
  error.value = null
  try {
    // The device card shows all authorized channels, independent of the list's search filters.
    const result = await api.getDeviceChannels(props.device.groupKey, {
      page: page.value,
      size: 20,
    })
    if (!active() || current !== listSequence) return
    const lastPage = Math.max(1, Math.ceil(result.total / 20))
    if (page.value > lastPage) {
      page.value = lastPage
      await load()
      return
    }
    channels.value = result.items
    total.value = result.total
    const next = result.items.find((item) => item.cameraId === selectedId.value) ?? result.items[0]
    selectedId.value = next?.cameraId ?? null
    if (next) await selectChannel(next.cameraId)
  } catch (cause) {
    if (active() && current === listSequence) error.value = cause
  } finally {
    if (active() && current === listSequence) loading.value = false
  }
}
async function changed() {
  await load()
  emit('changed')
}
async function placed() {
  placement.value = null
  moving.value = false
  await changed()
}
function close() {
  if (
    !deleting.value &&
    !editingId.value &&
    !placement.value &&
    !moving.value &&
    !editingDevice.value
  )
    emit('close')
}
async function remove() {
  if (busy.value || !camera.value || !props.canManage) return
  const target = camera.value,
    active = captureScope()
  deleting.value = true
  try {
    await ElMessageBox.confirm(
      `删除通道“${target.name}”及其码流档案？存在授权或业务引用时不能删除。`,
      '确认删除通道',
      { type: 'warning' },
    )
    if (!active()) return
    await api.deleteCamera(target)
    if (active()) await changed()
  } catch (cause) {
    if (active() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    deleting.value = false
  }
}
onMounted(load)
</script>

<template>
  <ElDrawer
    :model-value="!editingId && !placement && !moving && !editingDevice"
    title="相机详情"
    size="var(--camera-card-width)"
    class="camera-device-card"
    append-to-body
    :before-close="close"
    :close-on-click-modal="!deleting"
    :close-on-press-escape="!deleting"
    :show-close="!deleting"
  >
    <header class="device-summary">
      <div class="device-symbol" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
          <rect x="3" y="6" width="12" height="12" rx="3" />
          <path d="m15 10 6-3v10l-6-3" />
        </svg>
      </div>
      <div>
        <h2>{{ deviceName }}</h2>
        <p>
          {{ [device.manufacturer, device.model].filter(Boolean).join(' · ') || '设备资料未获取' }}
        </p>
      </div>
    </header>
    <ElTag v-if="!device.identified" type="info" size="small">待关联设备</ElTag>
    <dl class="device-facts">
      <div>
        <dt>接入方式</dt>
        <dd>{{ connectionLabel(device.sourceType, device.connectionCategory) }}</dd>
      </div>
      <div>
        <dt>接入来源</dt>
        <dd>{{ device.sourceDisplayName }}</dd>
      </div>
    </dl>
    <div v-if="canManage" class="device-actions">
      <ElButton :disabled="busy" @click="editingDevice = true">编辑相机</ElButton>
      <ElButton :disabled="busy" @click="moving = true">移动分组</ElButton>
    </div>
    <RequestError :error="error" />
    <section v-loading="loading" class="channel-section" aria-label="相机通道">
      <div class="section-heading">
        <h3>通道</h3>
        <span>{{ total }} 个可访问通道</span>
      </div>
      <div class="channel-switcher" role="group" aria-label="选择通道">
        <ElButton
          v-for="item in channels"
          :key="item.cameraId"
          :type="selectedId === item.cameraId ? 'primary' : 'default'"
          :plain="selectedId !== item.cameraId"
          :aria-pressed="selectedId === item.cameraId"
          :disabled="loading || deleting"
          @click="selectChannel(item.cameraId)"
          >{{ item.name }}</ElButton
        >
      </div>
      <ElPagination
        v-if="total > 20"
        v-model:current-page="page"
        :disabled="busy"
        :total="total"
        :page-size="20"
        :pager-count="5"
        layout="prev, pager, next"
        @current-change="load"
      />
      <p v-if="!loading && !channels.length && !error" class="empty-note">暂无可访问通道</p>
      <div v-if="selected" v-loading="detailLoading" class="channel-detail">
        <div class="channel-caption">
          <span>{{ selected.groupPath || '未设置视频分组' }}</span>
          <ElTag size="small" :type="selected.lifecycle === 'ENABLED' ? 'success' : 'info'">{{
            lifecycleLabels[selected.lifecycle]
          }}</ElTag>
        </div>
        <RequestError :error="detailError" />
        <ElButton v-if="detailError" link type="primary" @click="selectChannel(selected.cameraId)"
          >重试读取通道</ElButton
        >
        <template v-if="camera">
          <div class="section-heading streams-heading">
            <h3>码流</h3>
            <span>{{ camera.profiles.length }} 个</span>
          </div>
          <div class="stream-summaries" aria-label="当前通道码流">
            <article
              v-for="profile in camera.profiles"
              :key="profile.streamProfileId"
              class="stream-summary"
            >
              <div class="stream-info">
                <div class="stream-title">
                  <strong :title="profile.label">{{ streamLabel(profile) }}</strong
                  ><span v-if="!profile.enabled" class="stream-state">已停用</span
                  ><span
                    v-else-if="profile.streamProfileId === camera.defaultPreviewProfileId"
                    class="stream-default"
                    >默认</span
                  >
                </div>
                <p class="stream-parameters">
                  {{ profile.videoCodec || '编码未获取'
                  }}<template v-if="profile.width && profile.height">
                    · {{ profile.width }} × {{ profile.height }}</template
                  ><template v-if="profile.frameRate != null">
                    · {{ profile.frameRate }} fps</template
                  >
                </p>
              </div>
              <span class="stream-bitrate">{{
                profile.bitrateKbps == null ? '码率未获取' : `${profile.bitrateKbps} kbps`
              }}</span>
            </article>
          </div>
          <p v-if="!camera.profiles.length" class="empty-note">尚无码流档案</p>
          <div class="channel-actions">
            <ElButton type="primary" plain :disabled="busy" @click="editingId = camera.cameraId"
              >编辑通道</ElButton
            >
            <template v-if="canManage">
              <ElButton
                v-if="camera.lifecycle !== 'PENDING_ASSIGNMENT'"
                :disabled="busy"
                @click="
                  placement = {
                    cameraId: camera.cameraId,
                    action: camera.lifecycle === 'ENABLED' ? 'disable' : 'enable',
                  }
                "
                >{{ camera.lifecycle === 'ENABLED' ? '停用' : '启用' }}</ElButton
              >
              <ElButton link type="danger" :disabled="busy" @click="remove">删除通道</ElButton>
            </template>
          </div>
        </template>
      </div>
    </section>
    <template #footer
      ><ElButton link :loading="loading" :disabled="busy" @click="load">刷新资料</ElButton
      ><ElButton :disabled="deleting" @click="close">关闭</ElButton></template
    >
  </ElDrawer>
  <DetailDialog
    v-if="editingId"
    :key="editingId"
    :camera-id="editingId"
    :device-name="deviceName"
    :can-manage="canManage"
    @close="editingId = null"
    @changed="changed"
  />
  <PlacementDialog
    v-if="placement"
    :camera-id="placement.cameraId"
    :action="placement.action"
    @close="placement = null"
    @saved="placed"
  />
  <DeviceEditor
    v-if="editingDevice"
    :device="device"
    @close="editingDevice = false"
    @saved="deviceSaved"
  />
  <DeviceMoveDialog
    v-if="moving"
    :device="{ ...device, name: deviceName }"
    @close="moving = false"
    @saved="placed"
  />
</template>

<style>
.camera-device-card.el-drawer.rtl {
  --camera-card-gap: clamp(0.75rem, 1.25vw, 2.5rem);
  --camera-card-width: min(calc(100vw - 2 * var(--camera-card-gap)), clamp(32.5rem, 32vw, 50rem));
  --camera-card-padding: clamp(1.25rem, 1.3vw, 2rem);
  --camera-card-section-gap: clamp(1rem, 0.95vw, 1.5rem);
  --camera-card-row-gap: clamp(0.75rem, 0.7vw, 1.125rem);
  --camera-card-text: clamp(0.875rem, 0.65rem + 0.25vw, 1rem);
  --camera-card-meta: clamp(0.8125rem, 0.65rem + 0.2vw, 0.9375rem);
  top: var(--camera-card-gap);
  right: var(--camera-card-gap);
  bottom: auto;
  height: auto;
  max-height: calc(100dvh - 2 * var(--camera-card-gap));
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 12px;
  box-shadow: var(--el-box-shadow);
  font-size: var(--camera-card-text);
}
.camera-device-card > .el-drawer__header {
  flex-shrink: 0;
  gap: 16px;
  margin: 0;
  padding: var(--camera-card-section-gap) var(--camera-card-padding);
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.camera-device-card .el-drawer__title {
  font-size: clamp(1rem, 0.8rem + 0.25vw, 1.125rem);
}
.camera-device-card > .el-drawer__body {
  padding: var(--camera-card-padding);
  min-height: 0;
  overflow: auto;
}
.camera-device-card > .el-drawer__footer {
  display: flex;
  flex-shrink: 0;
  justify-content: space-between;
  gap: 12px;
  padding: var(--camera-card-row-gap) var(--camera-card-padding);
  border-top: 1px solid var(--el-border-color-lighter);
}
.camera-device-card .el-button {
  height: auto;
  min-height: 36px;
  padding: 8px 14px;
  font-size: var(--camera-card-text);
  line-height: 1.4;
}
.camera-device-card .el-tag {
  flex-shrink: 0;
  height: auto;
  padding: 2px 6px;
  font-size: var(--camera-card-meta);
  line-height: 1.4;
}
@media (max-width: 680px) {
  .camera-device-card.el-drawer.rtl {
    --camera-card-padding: 1rem;
  }
}
@media (pointer: coarse) {
  .camera-device-card .el-button,
  .camera-device-card .el-drawer__close-btn {
    min-height: 44px;
    min-width: 44px;
  }
}
</style>
<style scoped>
.device-summary {
  display: flex;
  align-items: center;
  gap: var(--camera-card-section-gap);
}
.device-summary > div:last-child {
  min-width: 0;
}
.device-symbol {
  display: grid;
  place-items: center;
  width: clamp(46px, 2.5vw, 64px);
  height: clamp(46px, 2.5vw, 64px);
  flex-shrink: 0;
  border-radius: 12px;
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.device-symbol svg {
  width: 55%;
  height: 55%;
}
.device-summary h2 {
  margin: 0 0 8px;
  font-size: clamp(1.125rem, 0.85rem + 0.4vw, 1.5rem);
  line-height: 1.4;
  overflow-wrap: anywhere;
  color: var(--text-primary);
}
.device-summary p,
.stream-parameters {
  margin: 0;
  color: var(--text-secondary);
  font-size: var(--camera-card-meta);
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.device-facts {
  margin: var(--camera-card-section-gap) 0;
  display: grid;
  gap: 12px;
}
.device-facts > div {
  display: grid;
  grid-template-columns: 5em minmax(0, 1fr);
  gap: 12px;
  font-size: var(--camera-card-text);
  line-height: 1.6;
}
.device-facts dt {
  color: var(--text-secondary);
}
.device-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.channel-section {
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: var(--camera-card-section-gap);
  min-height: 110px;
}
.device-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: var(--camera-card-section-gap);
}
.device-actions .el-button {
  margin-left: 0;
}
.section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: var(--camera-card-section-gap);
}
.section-heading h3 {
  font-size: var(--camera-card-text);
  margin: 0;
}
.section-heading > span,
.channel-caption {
  color: var(--text-secondary);
  font-size: var(--camera-card-meta);
}
.channel-switcher {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.channel-switcher .el-button {
  margin: 0;
  max-width: 100%;
  white-space: normal;
  overflow-wrap: anywhere;
}
.channel-section :deep(.el-pagination) {
  margin-top: 12px;
  justify-content: center;
}
.channel-detail {
  min-height: 90px;
}
.channel-caption {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: var(--camera-card-row-gap) 0;
  line-height: 1.6;
}
.channel-caption > span:first-child {
  min-width: 0;
  overflow-wrap: anywhere;
}
.streams-heading {
  margin: 4px 0 0;
}
.stream-summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: var(--camera-card-row-gap) 0;
  border-bottom: 1px solid var(--el-border-color-extra-light);
}
.stream-info {
  min-width: 0;
}
.stream-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 3px;
}
.stream-title strong {
  font-size: var(--camera-card-text);
  overflow-wrap: anywhere;
}
.stream-default {
  font-size: var(--camera-card-meta);
  color: var(--el-color-primary);
}
.stream-state {
  font-size: var(--camera-card-meta);
  color: var(--text-secondary);
}
.stream-bitrate {
  flex-shrink: 0;
  color: var(--text-secondary);
  font-size: var(--camera-card-meta);
  font-variant-numeric: tabular-nums;
}
.channel-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: var(--camera-card-section-gap);
}
.channel-actions .el-button {
  margin: 0;
}
.channel-actions .el-button.is-link {
  margin-left: auto;
}
.empty-note {
  color: var(--text-secondary);
  font-size: var(--camera-card-text);
  line-height: 1.7;
  margin: 16px 0;
}
</style>
