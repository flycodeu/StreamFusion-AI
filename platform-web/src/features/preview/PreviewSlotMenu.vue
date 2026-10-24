<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ElOption, ElSelect } from 'element-plus'
import type { PreviewChannel, PreviewProfile } from '../../api/preview/types'
import { streamLabel } from '../camera/streamLabels'
import type { PreviewMenuAction } from './model'

const props = defineProps<{
  position: { x: number; y: number }
  channels: PreviewChannel[]
  profiles: PreviewProfile[]
  cameraId: string
  profileId?: string
  playing: boolean
  failed: boolean
}>()
const emit = defineEmits<{
  close: []
  action: [action: PreviewMenuAction]
  channel: [cameraId: string]
  profile: [profileId: string]
}>()
const root = ref<globalThis.HTMLElement | null>(null)
const position = ref(props.position)
const profileOptions = computed(() => {
  const profiles = props.profiles.map((profile) => ({ ...profile, usageHint: profile.usage }))
  return profiles.map((profile) => ({ ...profile, displayLabel: streamLabel(profile, profiles) }))
})
function focusable(): globalThis.HTMLElement[] {
  return Array.from(
    root.value?.querySelectorAll<globalThis.HTMLElement>(
      'button:not(:disabled), input:not(:disabled), [role="combobox"]',
    ) ?? [],
  ).filter(
    (element) => element.getAttribute('tabindex') !== '-1' && element.getClientRects().length > 0,
  )
}
watch(
  () => props.position,
  async (value) => {
    position.value = value
    await nextTick()
    if (!root.value) return
    const box = root.value.getBoundingClientRect()
    position.value = {
      x: Math.max(8, Math.min(value.x, globalThis.innerWidth - box.width - 8)),
      y: Math.max(8, Math.min(value.y, globalThis.innerHeight - box.height - 8)),
    }
    focusable()[0]?.focus()
  },
  { immediate: true, flush: 'post' },
)
function keydown(event: globalThis.KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    emit('close')
  } else if (event.key === 'Tab') {
    const elements = focusable()
    const first = elements[0],
      last = elements[elements.length - 1]
    if (event.shiftKey && globalThis.document.activeElement === first) {
      event.preventDefault()
      last?.focus()
    } else if (!event.shiftKey && globalThis.document.activeElement === last) {
      event.preventDefault()
      first?.focus()
    }
  }
}
</script>

<template>
  <button
    class="context-backdrop"
    type="button"
    tabindex="-1"
    aria-label="关闭画面操作"
    @click="emit('close')"
    @contextmenu.prevent="emit('close')"
  />
  <div
    id="preview-slot-menu"
    ref="root"
    class="preview-context"
    :style="{ left: `${position.x}px`, top: `${position.y}px` }"
    role="dialog"
    aria-label="画面操作"
    @keydown="keydown"
  >
    <div v-if="channels.length > 1" class="context-section">
      <span>通道</span>
      <ElSelect
        :model-value="cameraId"
        size="small"
        aria-label="切换通道"
        @change="emit('channel', $event)"
      >
        <ElOption
          v-for="channel in channels"
          :key="channel.cameraId"
          :value="channel.cameraId"
          :label="channel.name"
          :disabled="channel.lifecycle !== 'ENABLED'"
        />
      </ElSelect>
    </div>
    <div v-if="profiles.length" class="context-section">
      <span>码流</span>
      <ElSelect
        :model-value="profileId"
        size="small"
        aria-label="切换码流"
        @change="emit('profile', $event)"
      >
        <ElOption
          v-for="profile in profileOptions"
          :key="profile.streamProfileId"
          :value="profile.streamProfileId"
          :label="profile.displayLabel"
        />
      </ElSelect>
    </div>
    <button type="button" :disabled="!playing" @click="emit('action', 'snapshot')">抓图</button>
    <button type="button" @click="emit('action', 'maximize')">大屏</button>
    <button type="button" @click="emit('action', 'reconnect')">重新连接</button>
    <button v-if="failed" type="button" @click="emit('action', 'diagnostics')">排查信息</button>
    <button type="button" @click="emit('action', 'close')">关闭预览</button>
  </div>
</template>

<style scoped>
.context-backdrop {
  position: fixed;
  inset: 0;
  z-index: 29;
  border: 0;
  background: transparent;
  cursor: default;
}
.preview-context {
  position: fixed;
  z-index: 30;
  width: 232px;
  max-width: calc(100vw - 16px);
  max-height: calc(100dvh - 16px);
  overflow: auto;
  padding: 8px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #ffffff;
  box-shadow:
    0 12px 28px -6px rgba(15, 23, 42, 0.16),
    0 4px 10px -2px rgba(15, 23, 42, 0.08);
}
.preview-context > button {
  display: block;
  width: 100%;
  padding: 8px 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: #0f172a;
  text-align: left;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 120ms ease;
}
.preview-context > button:hover,
.preview-context > button:focus-visible {
  background: #f1f5f9;
}
.preview-context > button:focus-visible {
  outline: 2px solid var(--preview-accent);
  outline-offset: -2px;
}
.preview-context > button:disabled {
  opacity: 0.4;
  cursor: default;
}
.context-section {
  display: grid;
  gap: 5px;
  padding: 6px 8px 8px;
  margin-bottom: 4px;
  border-bottom: 1px solid #f1f5f9;
}
.context-section > span {
  color: #64748b;
  font-size: 11px;
  font-weight: 600;
}
</style>
