<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElButton, ElEmpty, ElInput, ElMessage, vLoading } from 'element-plus'
import {
  Search,
  Refresh,
  VideoPlay,
  Close,
  Grid,
  Folder,
  MoreFilled,
  MuteNotification,
  Headset,
} from '@element-plus/icons-vue'
import { getPreviewCatalog } from '../../api/preview/api'
import { getGroupTree } from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'
import type { PreviewChannel, PreviewStats } from '../../api/preview/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { usageLabels } from '../../features/preview/model'
import type { PreviewMenuAction } from '../../features/preview/model'
import type { PreviewFailure } from '../../features/preview/failure'
import {
  deviceLayout,
  getPreviewLayout,
  layoutCount,
  MAX_PREVIEW_SLOTS,
  type PreviewLayout,
} from '../../features/preview/layouts'
import PreviewCameraTree from '../../features/preview/PreviewCameraTree.vue'
import PreviewLayoutPicker from '../../features/preview/PreviewLayoutPicker.vue'
import PreviewCapacity from '../../features/preview/PreviewCapacity.vue'
import PreviewSlotMenu from '../../features/preview/PreviewSlotMenu.vue'
import PreviewDiagnosticsDialog from '../../features/preview/PreviewDiagnosticsDialog.vue'
import { usePreviewSlots } from '../../features/preview/usePreviewSlots'
import PreviewSavedViews from '../../features/preview/PreviewSavedViews.vue'
import { captureView, resolveSavedView, type SavedView } from '../../features/preview/saved-views'
import { sessionState } from '../../session/state'

defineOptions({ name: 'PreviewCenter' })
const route = useRoute(),
  router = useRouter(),
  captureScope = usePageScope()
const channels = ref<PreviewChannel[]>([]),
  groups = ref<CameraGroup[]>([]),
  loading = ref(false),
  error = ref<unknown>(null),
  search = ref('')
const layout = ref<PreviewLayout>('4'),
  showStats = ref(false),
  catalogOpen = ref(false),
  maximizedSlot = ref<number | null>(null),
  page = ref(1),
  total = ref(0)
const selectedDevice = computed(() =>
  typeof route.query.device === 'string' && /^[dc][1-9][0-9]{0,18}$/.test(route.query.device)
    ? route.query.device
    : undefined,
)
const deviceMode = computed(() => route.query.mode === 'device')
const loadedDeviceCount = computed(
  () => new Set(channels.value.map((channel) => channel.deviceGroupKey)).size,
)
const deviceTitle = computed(
  () => channels.value[0]?.deviceName || channels.value[0]?.name || '相机预览',
)
const activeSlot = ref(0),
  context = ref<{ x: number; y: number; slot: number } | null>(null)
const diagnosticFailure = ref<PreviewFailure | null>(null)
let menuTrigger: globalThis.HTMLElement | null = null,
  menuTile: globalThis.HTMLElement | null = null
const {
  slots,
  setVideoRef,
  assign,
  play,
  stop,
  switchProfile,
  sampleStats,
  snapshot,
  firstFrame,
  trim,
  muteAll,
  toggleAudio,
  unavailable,
  clearIdentity,
  slotRevision,
} = usePreviewSlots()
const openingView = ref(false)
const retryingSlots = ref(new Set<number>())
const retryRequests = new Map<number, globalThis.AbortController>()
let viewSequence = 0,
  viewAbort: globalThis.AbortController | null = null
function cancelSavedRetries() {
  for (const controller of retryRequests.values()) controller.abort()
  retryRequests.clear()
  retryingSlots.value.clear()
}
function missingChannel(cameraId: string): PreviewChannel {
  return {
    cameraId,
    name: '收藏相机暂不可用',
    deviceGroupKey: `c${cameraId}`,
    deviceName: null,
    groupId: null,
    groupPath: null,
    lifecycle: 'DISABLED',
    profiles: [],
  }
}
async function retrySlot(index: number) {
  if (openingView.value || retryingSlots.value.has(index) || retryingSlots.value.size >= 4) return
  const slot = slots[index]!
  if (slot.error?.code === 'LEASE_RELEASE_FAILED') {
    closeSlot(index)
    return
  }
  if (!slot.channel || !slot.savedProfileId) {
    await play(index)
    return
  }
  const cameraId = slot.channel.cameraId,
    profileId = slot.savedProfileId,
    active = captureScope(),
    revision = viewSequence,
    controller = new globalThis.AbortController()
  retryRequests.set(index, controller)
  retryingSlots.value.add(index)
  try {
    const generation = await stop(index, false)
    if (generation < 0 || !active() || controller.signal.aborted) return
    slot.state = 'CONNECTING'
    slot.error = null
    const [target] = await resolveSavedView(
      {
        id: 'retry',
        name: 'retry',
        layout: layout.value,
        updatedAt: new Date().toISOString(),
        slots: [{ index, cameraId, profileId }],
      },
      controller.signal,
    )
    if (
      !target ||
      !active() ||
      controller.signal.aborted ||
      revision !== viewSequence ||
      slotRevision(index) !== generation ||
      slots[index]!.channel?.cameraId !== cameraId ||
      slots[index]!.savedProfileId !== profileId
    )
      return
    if (target.channel) await assign(index, target.channel, slot.usage, profileId)
    else unavailable(index, missingChannel(cameraId), profileId, target.failure)
  } finally {
    if (retryRequests.get(index) === controller) {
      retryRequests.delete(index)
      retryingSlots.value.delete(index)
    }
  }
}
function currentView() {
  return captureView(
    layout.value,
    slots.map((slot) => ({
      channel: slot.channel,
      profile:
        slot.profile || (slot.savedProfileId ? { streamProfileId: slot.savedProfileId } : null),
    })),
  )
}
async function openView(view: SavedView) {
  if (openingView.value || !sessionState.me) return
  const revision = ++viewSequence,
    active = captureScope()
  cancelSavedRetries()
  // A device-entry catalog may still be in flight and would otherwise auto-assign
  // its channels after this explicit saved workspace has taken ownership.
  sequence++
  loading.value = false
  globalThis.clearTimeout(searchTimer)
  viewAbort?.abort()
  const controller = new globalThis.AbortController()
  viewAbort = controller
  openingView.value = true
  closeContext()
  muteAll()
  try {
    const closed = await Promise.all(slots.map((_slot, index) => stop(index)))
    if (!active() || revision !== viewSequence || controller.signal.aborted) return
    if (closed.some((value) => value < 0)) {
      ElMessage.info('原画面资源释放尚未确认，请重试关闭后再打开收藏')
      return
    }
    const targets = await resolveSavedView(view, controller.signal)
    if (!active() || revision !== viewSequence || controller.signal.aborted) return
    if (!(await chooseLayout(view.layout))) return
    maximizedSlot.value = null
    activeSlot.value = 0
    const restored = new Map(channels.value.map((channel) => [channel.cameraId, channel]))
    const starts: Promise<void>[] = []
    for (const target of targets) {
      if (!active() || revision !== viewSequence || controller.signal.aborted) return
      if (target.channel) {
        restored.set(target.cameraId, target.channel)
        starts.push(assign(target.index, target.channel, 'MAIN', target.profileId))
      } else {
        unavailable(target.index, missingChannel(target.cameraId), target.profileId, target.failure)
      }
    }
    channels.value = [...restored.values()]
    await Promise.all(starts)
    if (active() && revision === viewSequence) {
      const failed = targets.filter((target) => slots[target.index]?.state === 'ERROR').length
      if (failed) ElMessage.info(`已应用收藏，${failed} 个窗口需重新选择或重试；其余按队列连接`)
    }
  } catch {
    if (active() && revision === viewSequence && !controller.signal.aborted)
      ElMessage.info('收藏未能完整打开，请保留当前画面并重试')
  } finally {
    if (viewAbort === controller) viewAbort = null
    if (revision === viewSequence) openingView.value = false
  }
}
function soundTitle(index: number) {
  const slot = slots[index]!
  if (slot.audio === 'BLOCKED') return '浏览器阻止发声，点击重试'
  if (slot.audio !== 'AVAILABLE') return '尚未收到可播放音轨'
  return slot.muted ? '打开声音（本页仅一路有声）' : '静音'
}
const slotCount = computed(() => layoutCount(layout.value))
const workspaceOccupied = computed(() => slots.some((slot) => slot.channel || slot.player))
const layoutDefinition = computed(() => getPreviewLayout(layout.value))
const gridStyle = computed(() => ({
  '--layout-columns': layoutDefinition.value.columns,
  '--layout-rows': layoutDefinition.value.rows,
  '--layout-lead-span': layoutDefinition.value.leadSpan || 1,
  '--layout-min-height': `${Math.max(320, layoutDefinition.value.rows * 72)}px`,
  '--layout-narrow-width':
    layoutDefinition.value.columns >= 3
      ? `${Math.max(480, layoutDefinition.value.columns * 104)}px`
      : '0px',
  '--layout-narrow-height': `${Math.max(440, layoutDefinition.value.rows * 84)}px`,
}))
const contextSlot = computed(() => (context.value ? slots[context.value.slot] : null))
const contextChannels = computed(() =>
  channels.value.filter(
    (channel) => channel.deviceGroupKey === contextSlot.value?.channel?.deviceGroupKey,
  ),
)
const contextProfiles = computed(
  () =>
    contextSlot.value?.channel?.profiles.filter(
      (profile) => profile.enabled && profile.configured,
    ) || [],
)
const pendingAssignments = new Set<number>()
let sequence = 0,
  groupsLoaded = false,
  statsTimer: ReturnType<typeof globalThis.setInterval> | undefined,
  searchTimer: ReturnType<typeof globalThis.setTimeout> | undefined
function addChannel(channel: PreviewChannel) {
  if (openingView.value) return
  if (channel.lifecycle !== 'ENABLED') {
    ElMessage.info('该通道未启用')
    return
  }
  const existing = slots.findIndex(
    (slot, index) => index < slotCount.value && slot.channel?.cameraId === channel.cameraId,
  )
  if (existing >= 0) {
    activeSlot.value = existing
    catalogOpen.value = false
    return
  }
  const empty =
    !slots[activeSlot.value]?.channel && !pendingAssignments.has(activeSlot.value)
      ? activeSlot.value
      : slots.findIndex(
          (slot, index) =>
            index < slotCount.value && !slot.channel && !pendingAssignments.has(index),
        )
  const index = empty >= 0 ? empty : activeSlot.value
  activeSlot.value = index
  pendingAssignments.add(index)
  catalogOpen.value = false
  void assign(index, channel, deviceMode.value ? 'SUB' : 'MAIN').finally(() =>
    pendingAssignments.delete(index),
  )
}
function addDevice(deviceChannels: PreviewChannel[]) {
  if (openingView.value) return
  if (deviceChannels.length === 1) addChannel(deviceChannels[0])
  else {
    const visibleSlots = slots.slice(0, slotCount.value)
    const additions = deviceChannels.filter(
      (channel) =>
        channel.lifecycle === 'ENABLED' &&
        !visibleSlots.some((slot) => slot.channel?.cameraId === channel.cameraId),
    )
    const available = visibleSlots.filter(
      (slot, index) => !slot.channel && !pendingAssignments.has(index),
    ).length
    if (available < additions.length) {
      ElMessage.info('当前布局空间不足，请选择更多画面')
      return
    }
    if (!additions.length) {
      const existing = deviceChannels.find((channel) => channel.lifecycle === 'ENABLED')
      if (existing) addChannel(existing)
      else ElMessage.info('该相机没有已启用通道')
      return
    }
    for (const channel of additions) addChannel(channel)
  }
}
let layoutSequence = 0
async function chooseLayout(value: PreviewLayout): Promise<boolean> {
  const revision = ++layoutSequence,
    active = captureScope()
  closeContext()
  if (!(await trim(value)) || revision !== layoutSequence || !active()) return false
  layout.value = value
  const count = layoutCount(value)
  activeSlot.value = Math.min(activeSlot.value, count - 1)
  if (maximizedSlot.value !== null && maximizedSlot.value >= count) maximizedSlot.value = null
  return true
}
async function load(append = false) {
  const active = captureScope(),
    current = ++sequence
  loading.value = true
  error.value = null
  if (deviceMode.value && !selectedDevice.value) {
    channels.value = []
    total.value = 0
    loading.value = false
    return
  }
  try {
    const refreshGroups = !deviceMode.value && !append && (!groupsLoaded || !search.value.trim())
    const groupPromise = refreshGroups
      ? getGroupTree().catch(() => groups.value)
      : Promise.resolve(groups.value)
    const [result, groupRows] = await Promise.all([
      getPreviewCatalog({
        page: append ? page.value + 1 : 1,
        size: 100,
        name: search.value.trim() || undefined,
        device: selectedDevice.value,
      }),
      groupPromise,
    ])
    if (!active() || current !== sequence) return
    groups.value = groupRows
    if (refreshGroups) groupsLoaded = true
    channels.value = append ? [...channels.value, ...result.items] : result.items
    page.value = result.page
    total.value = result.total
    if (deviceMode.value && !append) {
      const selectedLayout = deviceLayout(result.items.length)
      if (!(await chooseLayout(selectedLayout)) || !active() || current !== sequence) return
      for (let index = 0; index < Math.min(result.items.length, MAX_PREVIEW_SLOTS); index++)
        void assign(index, result.items[index], 'SUB')
      if (selectedLayout === '64')
        ElMessage.info(
          result.total > MAX_PREVIEW_SLOTS
            ? `已切换为8 × 8，展示前${MAX_PREVIEW_SLOTS}个通道`
            : `已切换为8 × 8，展示${result.items.length}个通道`,
        )
    }
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}
function openContext(event: globalThis.MouseEvent, index: number) {
  event.preventDefault()
  if (!slots[index].channel) return
  rememberFocus(event.currentTarget)
  activeSlot.value = index
  context.value = {
    x: Math.max(8, Math.min(event.clientX, globalThis.innerWidth - 232)),
    y: Math.max(8, Math.min(event.clientY, globalThis.innerHeight - 375)),
    slot: index,
  }
}
function rememberFocus(target: globalThis.EventTarget | null) {
  menuTrigger = target instanceof globalThis.HTMLElement ? target : null
  menuTile = menuTrigger?.closest<globalThis.HTMLElement>('.preview-tile') ?? null
}
function restoreFocus() {
  void nextTick(() => (menuTrigger?.isConnected ? menuTrigger : menuTile)?.focus())
}
function closeContext(restore = true) {
  context.value = null
  if (restore) restoreFocus()
}
function openMore(event: globalThis.MouseEvent | globalThis.KeyboardEvent, index: number) {
  if (!slots[index].channel) return
  rememberFocus(event.currentTarget)
  if (!menuTrigger) return
  const box = menuTrigger.getBoundingClientRect()
  activeSlot.value = index
  context.value = { x: box.right - 224, y: box.bottom + 6, slot: index }
}
function tileKeydown(event: globalThis.KeyboardEvent, index: number) {
  if (event.target !== event.currentTarget) return
  if (event.key === 'ContextMenu' || (event.key === 'F10' && event.shiftKey)) {
    event.preventDefault()
    openMore(event, index)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    activeSlot.value = index
    toggleMaximize(index)
  }
}
function toggleMaximize(index: number) {
  if (!slots[index]?.channel) return
  maximizedSlot.value = maximizedSlot.value === index ? null : index
}
function closeSlot(index: number) {
  if (openingView.value) return
  if (maximizedSlot.value === index) maximizedSlot.value = null
  void stop(index)
}
function showDiagnostics(index: number, event?: globalThis.MouseEvent) {
  const failure = slots[index].error
  if (!failure) return
  if (event) rememberFocus(event.currentTarget)
  diagnosticFailure.value = { ...failure }
}
function closeDiagnostics() {
  diagnosticFailure.value = null
  restoreFocus()
}
async function menuAction(action: PreviewMenuAction) {
  if (openingView.value) return
  const index = context.value?.slot
  if (index == null) return
  closeContext(action !== 'diagnostics')
  if (action === 'diagnostics') showDiagnostics(index)
  if (action === 'snapshot') {
    if (!(await snapshot(index))) ElMessage.info('当前还没有可抓取的画面')
  }
  if (action === 'maximize') toggleMaximize(index)
  if (action === 'reconnect') void retrySlot(index)
  if (action === 'close') closeSlot(index)
}
function menuChannel(cameraId: string) {
  if (openingView.value) return
  const index = context.value?.slot,
    channel = channels.value.find((channel) => channel.cameraId === cameraId)
  closeContext()
  if (index != null && channel) void assign(index, channel, slots[index].usage)
}
function menuProfile(profileId: string) {
  if (openingView.value) return
  const index = context.value?.slot
  closeContext()
  if (index != null) void switchProfile(index, profileId)
}
function displayRate(stats: PreviewStats | null) {
  return stats?.mediaKbps == null ? '码率待采集' : `${stats.mediaKbps.toFixed(0)} kbps`
}
function rateDetails(stats: PreviewStats | null) {
  return [
    stats?.videoCodec,
    stats?.width && stats.height ? `${stats.width} × ${stats.height}` : null,
    stats?.frameRate ? `${stats.frameRate} fps` : null,
  ]
    .filter(Boolean)
    .join(' · ')
}
function dismissContext(event: globalThis.KeyboardEvent) {
  if (event.key === 'Escape') {
    if (context.value) closeContext()
    catalogOpen.value = false
    maximizedSlot.value = null
  }
}
watch(search, () => {
  globalThis.clearTimeout(searchTimer)
  searchTimer = globalThis.setTimeout(() => {
    void load()
  }, 300)
})
watch(
  () => [route.query.mode, route.query.device],
  async () => {
    viewAbort?.abort()
    cancelSavedRetries()
    viewSequence++
    openingView.value = false
    muteAll()
    const revision = ++layoutSequence,
      active = captureScope()
    sequence++
    const closed = await Promise.all(slots.map((_slot, index) => stop(index)))
    if (!active() || revision !== layoutSequence) return
    if (closed.some((result) => result < 0)) {
      loading.value = false
      return
    }
    layout.value = deviceMode.value ? '1' : '4'
    maximizedSlot.value = null
    void load()
  },
)
watch(
  () => [sessionState.me?.user.id, sessionState.epoch],
  () => {
    viewAbort?.abort()
    cancelSavedRetries()
    viewSequence++
    sequence++
    openingView.value = false
    muteAll()
    closeContext(false)
    diagnosticFailure.value = null
    channels.value = []
    groups.value = []
    void clearIdentity()
  },
  { flush: 'sync' },
)
watch(showStats, (visible) => {
  if (visible) void sampleStats()
})
onMounted(() => {
  void load()
  statsTimer = globalThis.setInterval(() => {
    if (showStats.value) void sampleStats()
  }, 5000)
  globalThis.document.addEventListener('keydown', dismissContext)
})
onBeforeUnmount(() => {
  viewAbort?.abort()
  cancelSavedRetries()
  viewSequence++
  muteAll()
  sequence++
  globalThis.clearTimeout(searchTimer)
  globalThis.clearInterval(statsTimer)
  globalThis.document.removeEventListener('keydown', dismissContext)
})
</script>

<template>
  <div class="preview-page" :class="{ 'device-preview': deviceMode }">
    <button
      v-if="catalogOpen && !deviceMode"
      class="catalog-backdrop"
      type="button"
      aria-label="关闭相机列表"
      @click="catalogOpen = false"
    />
    <aside
      v-if="!deviceMode"
      class="preview-sidebar"
      :class="{ 'is-open': catalogOpen }"
      aria-label="可见相机"
    >
      <header class="preview-sidebar-heading">
        <div><h1>相机列表</h1></div>
        <ElButton
          :icon="Refresh"
          text
          circle
          :loading="loading"
          aria-label="刷新相机"
          @click="load()"
        />
      </header>
      <ElInput
        v-model="search"
        :prefix-icon="Search"
        clearable
        placeholder="搜索相机"
        class="camera-search"
      />
      <div v-loading="loading" class="camera-list">
        <PreviewCameraTree
          v-if="channels.length"
          :channels="channels"
          :groups="groups"
          :searching="Boolean(search.trim())"
          @device="addDevice"
          @channel="addChannel"
        />
        <ElEmpty v-if="!loading && !channels.length" :image-size="66" description="暂无可见相机" />
        <ElButton
          v-if="channels.length < total"
          text
          :loading="loading"
          class="load-more"
          @click="load(true)"
          >加载更多</ElButton
        >
      </div>
      <footer class="preview-sidebar-footer">
        <span>已加载 {{ loadedDeviceCount }} 台</span><span>共 {{ total }} 个通道</span>
      </footer>
    </aside>
    <main class="preview-stage">
      <header v-if="deviceMode" class="stage-toolbar">
        <div>
          <ElButton size="small" @click="router.back()">返回</ElButton>
          <h2>{{ deviceMode ? deviceTitle : '预览中心' }}</h2>
        </div>
      </header>
      <RequestError :error="error" />
      <div
        v-if="deviceMode && !loading && !channels.length && !workspaceOccupied"
        class="device-empty"
      >
        <ElEmpty description="暂无可见通道" />
      </div>
      <div v-else class="preview-canvas">
        <div
          class="preview-grid"
          :class="{
            [`layout-${layout}`]: true,
            dense: layoutDefinition.columns >= 5,
          }"
          :style="gridStyle"
        >
          <article
            v-for="(slot, index) in slots.slice(0, slotCount)"
            :key="index"
            class="preview-tile"
            :class="{
              occupied: Boolean(slot.channel || slot.player),
              playing: slot.state === 'PLAYING',
              selected: activeSlot === index,
              maximized: maximizedSlot === index,
              lead: Boolean(layoutDefinition.leadSpan) && index === 0,
            }"
            tabindex="0"
            aria-keyshortcuts="Shift+F10"
            :aria-label="
              slot.channel
                ? `${slot.channel.name}，${usageLabels[slot.profile?.usage || slot.usage]}`
                : `画面 ${index + 1}`
            "
            @click="activeSlot = index"
            @focusin="activeSlot = index"
            @keydown="tileKeydown($event, index)"
            @contextmenu="openContext($event, index)"
            @dblclick="toggleMaximize(index)"
          >
            <video
              v-if="slot.channel"
              :ref="(element) => setVideoRef(index, element)"
              autoplay
              :muted="slot.muted"
              playsinline
              @playing="firstFrame(index)"
            />
            <div v-if="slot.channel" class="tile-overlays">
              <div class="tile-caption" aria-hidden="true">
                <span class="tile-name" :title="slot.channel.name">{{ slot.channel.name }}</span>
                <span class="tile-usage">{{ usageLabels[slot.profile?.usage || slot.usage] }}</span>
              </div>
              <div v-if="showStats" class="tile-stats" title="媒体服务观测速率">
                <span>{{ displayRate(slot.stats) }}</span>
                <span v-if="rateDetails(slot.stats)" :title="rateDetails(slot.stats)">{{
                  rateDetails(slot.stats)
                }}</span>
              </div>
            </div>
            <div
              v-if="(slot.channel || slot.player) && slot.state !== 'PLAYING'"
              class="tile-state"
            >
              <VideoPlay v-if="slot.state !== 'ERROR'" class="state-icon" /><span>{{
                slot.state === 'CLOSING' ? '正在关闭画面…' : slot.error?.message || '正在连接…'
              }}</span>
              <div v-if="slot.state === 'ERROR'" class="tile-error-actions">
                <ElButton
                  size="small"
                  text
                  type="primary"
                  :disabled="openingView || retryingSlots.has(index)"
                  :loading="retryingSlots.has(index)"
                  v-if="
                    slot.profile ||
                    slot.savedProfileId ||
                    slot.error?.code === 'LEASE_RELEASE_FAILED'
                  "
                  @click.stop="retrySlot(index)"
                  @dblclick.stop
                  >{{
                    slot.error?.code === 'LEASE_RELEASE_FAILED'
                      ? '重试关闭'
                      : slot.savedProfileId
                        ? '重新检查'
                        : '重连'
                  }}</ElButton
                >
                <span v-else class="tile-reselect">请从相机列表重新选择，或重试打开收藏</span>
                <ElButton
                  v-if="slot.error"
                  size="small"
                  text
                  @click.stop="showDiagnostics(index, $event)"
                  @dblclick.stop
                  >排查信息</ElButton
                >
              </div>
            </div>
            <div v-if="!slot.channel && !slot.player" class="tile-empty">
              <VideoPlay /><span>{{ index + 1 }}</span>
            </div>
            <button
              v-if="slot.channel"
              class="tile-audio"
              type="button"
              :aria-label="soundTitle(index)"
              :title="soundTitle(index)"
              :aria-pressed="!slot.muted"
              :disabled="openingView || slot.state !== 'PLAYING' || slot.audio === 'UNKNOWN'"
              @click.stop="toggleAudio(index)"
              @dblclick.stop
            >
              <MuteNotification v-if="slot.muted" /><Headset v-else />
            </button>
            <button
              v-if="slot.channel && maximizedSlot === index"
              class="tile-fullscreen-exit"
              type="button"
              aria-label="退出大屏"
              @click.stop="toggleMaximize(index)"
              @dblclick.stop
            >
              退出大屏
            </button>
            <button
              v-if="slot.channel"
              class="tile-more"
              type="button"
              :aria-label="`${slot.channel.name} 更多操作`"
              aria-haspopup="dialog"
              aria-controls="preview-slot-menu"
              :aria-expanded="context?.slot === index"
              @click.stop="openMore($event, index)"
              @dblclick.stop
            >
              <MoreFilled />
            </button>
            <button
              v-if="slot.channel || slot.player"
              class="tile-close"
              type="button"
              :aria-label="slot.state === 'CLOSING' ? '正在关闭画面' : '关闭画面'"
              :disabled="openingView || slot.state === 'CLOSING'"
              @click.stop="closeSlot(index)"
              @dblclick.stop
            >
              <Close />
            </button>
          </article>
        </div>
      </div>
      <footer class="stage-controls" aria-label="预览控制">
        <PreviewCapacity />
        <ElButton
          v-if="!deviceMode"
          class="catalog-toggle"
          :icon="Folder"
          :aria-expanded="catalogOpen"
          @click="catalogOpen = !catalogOpen"
          >相机列表</ElButton
        >
        <div class="canvas-options">
          <ElButton
            size="small"
            :type="showStats ? 'primary' : 'default'"
            :aria-pressed="showStats"
            :icon="Grid"
            @click="showStats = !showStats"
            >码率信息</ElButton
          >
          <PreviewSavedViews :busy="openingView" :current="currentView" @open="openView" />
          <PreviewLayoutPicker
            :model-value="layout"
            @update:model-value="(value) => !openingView && chooseLayout(value)"
          />
        </div>
      </footer>
    </main>
    <PreviewSlotMenu
      v-if="context && contextSlot?.channel"
      :position="context"
      :channels="contextChannels"
      :profiles="contextProfiles"
      :camera-id="contextSlot.channel.cameraId"
      :profile-id="contextSlot.profile?.streamProfileId"
      :playing="contextSlot.state === 'PLAYING'"
      :failed="Boolean(contextSlot.error)"
      @close="closeContext"
      @action="menuAction"
      @channel="menuChannel"
      @profile="menuProfile"
    />
    <PreviewDiagnosticsDialog
      v-if="diagnosticFailure"
      :failure="diagnosticFailure"
      @close="closeDiagnostics"
    />
  </div>
</template>

<style scoped src="../../features/preview/preview-layout.css" />
