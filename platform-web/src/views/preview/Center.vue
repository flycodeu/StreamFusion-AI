<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElButton, ElEmpty, ElInput, ElMessage, ElOption, ElSelect, vLoading } from 'element-plus'
import { Search, Refresh, VideoPlay, Close, Grid, Folder } from '@element-plus/icons-vue'
import { getPreviewCatalog } from '../../api/preview/api'
import { getGroupTree } from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'
import type { PreviewChannel, PreviewStats } from '../../api/preview/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { groupCatalog, usageLabels } from '../../features/preview/model'
import {
  deviceLayout,
  getPreviewLayout,
  layoutCount,
  MAX_PREVIEW_SLOTS,
  type PreviewLayout,
} from '../../features/preview/layouts'
import PreviewCameraTree from '../../features/preview/PreviewCameraTree.vue'
import PreviewLayoutPicker from '../../features/preview/PreviewLayoutPicker.vue'
import { usePreviewSlots } from '../../features/preview/usePreviewSlots'

defineOptions({ name: 'PreviewCenter' })
const route = useRoute(),
  router = useRouter(),
  captureScope = usePageScope()
const channels = ref<PreviewChannel[]>([]),
  groups = ref<CameraGroup[]>([]),
  loading = ref(false),
  error = ref<unknown>(null),
  search = ref('')
const layout = ref<PreviewLayout>('16'),
  showStats = ref(false),
  catalogOpen = ref(false),
  page = ref(1),
  total = ref(0)
const selectedDevice = computed(() =>
  typeof route.query.device === 'string' && /^[dc][1-9][0-9]{0,18}$/.test(route.query.device)
    ? route.query.device
    : undefined,
)
const deviceMode = computed(() => route.query.mode === 'device')
const grouped = computed(() => groupCatalog(channels.value))
const deviceTitle = computed(
  () => channels.value[0]?.deviceName || channels.value[0]?.name || '相机预览',
)
const activeSlot = ref(0),
  context = ref<{ x: number; y: number; slot: number } | null>(null)
const {
  slots,
  setVideoRef,
  setTileRef,
  assign,
  play,
  stop,
  switchProfile,
  sampleStats,
  fullscreen,
  snapshot,
  firstFrame,
  trim,
} = usePreviewSlots()
const slotCount = computed(() => layoutCount(layout.value))
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
function chooseLayout(value: PreviewLayout) {
  layout.value = value
  context.value = null
  const count = layoutCount(value)
  activeSlot.value = Math.min(activeSlot.value, count - 1)
  void trim(value)
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
      chooseLayout(selectedLayout)
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
  activeSlot.value = index
  if (!slots[index].channel) return
  context.value = {
    x: Math.max(8, Math.min(event.clientX, globalThis.innerWidth - 232)),
    y: Math.max(8, Math.min(event.clientY, globalThis.innerHeight - 375)),
    slot: index,
  }
}
async function menuAction(action: 'snapshot' | 'fullscreen' | 'reconnect' | 'close') {
  const index = context.value?.slot
  context.value = null
  if (index == null) return
  if (action === 'snapshot') {
    if (!(await snapshot(index))) ElMessage.info('当前还没有可抓取的画面')
  }
  if (action === 'fullscreen')
    await fullscreen(index).catch(() => ElMessage.info('当前浏览器无法进入全屏'))
  if (action === 'reconnect') void play(index)
  if (action === 'close') void stop(index)
}
function menuChannel(cameraId: string) {
  const index = context.value?.slot,
    channel = channels.value.find((channel) => channel.cameraId === cameraId)
  context.value = null
  if (index != null && channel) void assign(index, channel, slots[index].usage)
}
function menuProfile(profileId: string) {
  const index = context.value?.slot
  context.value = null
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
    context.value = null
    catalogOpen.value = false
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
  () => {
    for (let index = 0; index < MAX_PREVIEW_SLOTS; index++) void stop(index)
    layout.value = deviceMode.value ? '1' : '16'
    void load()
  },
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
        <span>{{ grouped.reduce((count, group) => count + group.devices.length, 0) }} 台相机</span
        ><span>{{ total }} 个通道</span>
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
      <div v-if="deviceMode && !loading && !channels.length" class="device-empty">
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
            :ref="(element) => setTileRef(index, element)"
            class="preview-tile"
            :class="{
              occupied: slot.channel,
              playing: slot.state === 'PLAYING',
              selected: activeSlot === index,
              lead: Boolean(layoutDefinition.leadSpan) && index === 0,
            }"
            tabindex="0"
            :aria-label="
              slot.channel
                ? `${slot.channel.name}，${usageLabels[slot.profile?.usage || slot.usage]}`
                : `画面 ${index + 1}`
            "
            @click="activeSlot = index"
            @keydown.enter="activeSlot = index"
            @contextmenu="openContext($event, index)"
          >
            <video
              v-if="slot.channel"
              :ref="(element) => setVideoRef(index, element)"
              autoplay
              muted
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
            <div v-if="slot.channel && slot.state !== 'PLAYING'" class="tile-state">
              <VideoPlay v-if="slot.state !== 'ERROR'" class="state-icon" /><span>{{
                slot.error || '正在连接…'
              }}</span
              ><ElButton
                v-if="slot.state === 'ERROR'"
                size="small"
                text
                type="primary"
                @click.stop="play(index)"
                >重连</ElButton
              >
            </div>
            <div v-if="!slot.channel" class="tile-empty">
              <VideoPlay /><span>{{ index + 1 }}</span>
            </div>
            <button
              v-if="slot.channel"
              class="tile-close"
              type="button"
              aria-label="关闭画面"
              @click.stop="stop(index)"
            >
              <Close />
            </button>
          </article>
        </div>
      </div>
      <footer class="stage-controls" aria-label="预览控制">
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
          <PreviewLayoutPicker :model-value="layout" @update:model-value="chooseLayout" />
        </div>
      </footer>
    </main>
    <template v-if="context">
      <button
        class="context-backdrop"
        type="button"
        aria-label="关闭菜单"
        @click="context = null"
        @contextmenu.prevent="context = null"
      />
      <div
        class="preview-context"
        :style="{ left: `${context.x}px`, top: `${context.y}px` }"
        role="menu"
      >
        <div v-if="contextChannels.length > 1" class="context-section">
          <span>通道</span
          ><ElSelect
            :model-value="contextSlot?.channel?.cameraId"
            size="small"
            aria-label="切换通道"
            @change="menuChannel"
            ><ElOption
              v-for="channel in contextChannels"
              :key="channel.cameraId"
              :value="channel.cameraId"
              :label="channel.name"
              :disabled="channel.lifecycle !== 'ENABLED'"
          /></ElSelect>
        </div>
        <div v-if="contextProfiles.length" class="context-section">
          <span>码流</span
          ><ElSelect
            :model-value="contextSlot?.profile?.streamProfileId"
            size="small"
            aria-label="切换码流"
            @change="menuProfile"
            ><ElOption
              v-for="profile in contextProfiles"
              :key="profile.streamProfileId"
              :value="profile.streamProfileId"
              :label="`${usageLabels[profile.usage]} · ${profile.label}`"
          /></ElSelect>
        </div>
        <button
          role="menuitem"
          type="button"
          :disabled="contextSlot?.state !== 'PLAYING'"
          @click="menuAction('snapshot')"
        >
          抓图</button
        ><button role="menuitem" type="button" @click="menuAction('fullscreen')">全屏</button
        ><button role="menuitem" type="button" @click="menuAction('reconnect')">重新连接</button
        ><button role="menuitem" type="button" @click="menuAction('close')">关闭预览</button>
      </div>
    </template>
  </div>
</template>

<style scoped src="../../features/preview/preview-layout.css" />
